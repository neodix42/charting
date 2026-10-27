package screener

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type downloadSource struct {
	Input    string       `json:"input"`
	Name     string       `json:"name"`
	From     string       `json:"from"`
	To       string       `json:"to"`
	Bytes    int64        `json:"bytes"`
	Modified int64        `json:"modified"` // milliseconds, exactly representable in Node
	Metadata fileMetadata `json:"metadata"`
}
type downloadPlan struct {
	Version int              `json:"version"`
	AsOf    int64            `json:"asOf"`
	Inputs  []downloadSource `json:"inputs"`
}

func (plan downloadPlan) workerFile(directory string) string {
	if len(plan.Inputs) == 0 {
		return ""
	}
	return filepath.Join(directory, "plan.json")
}

// Freeze the source set before fetching. The owned plan survives normal close,
// forced termination and retries together with each missing range's checkpoint.
func prepareDownloadPlan(job Job, directory, work string) (downloadPlan, map[string]os.FileInfo, error) {
	var plan downloadPlan
	filename := filepath.Join(work, "plan.json")
	err := readState(filename, 8*1024*1024, &plan)
	if errors.Is(err, os.ErrNotExist) {
		plan = downloadPlan{Version: 1, AsOf: job.AsOf, Inputs: []downloadSource{}}
		if plan.AsOf == 0 {
			plan.AsOf = time.Now().UnixMilli()
		}
		names, listErr := csvNames(directory)
		if listErr != nil {
			return plan, nil, listErr
		}
		for _, name := range names {
			match := downloadFilename.FindStringSubmatch(strings.ToLower(name))
			if match == nil || match[1] != job.Symbol || match[2] != job.Timeframe {
				continue
			}
			full := filepath.Join(directory, name)
			info, statErr := os.Lstat(full)
			if statErr != nil || !info.Mode().IsRegular() {
				return plan, nil, errors.New("existing CSV is no longer a regular file")
			}
			metadata, metadataErr := readFileMetadata(directory, name)
			if metadataErr != nil {
				return plan, nil, metadataErr
			}
			if !metadataMatches(metadata, info) {
				metadata.Verified = false
				metadata.Coverage = nil
				metadata.Checksum = ""
			}
			plan.Inputs = append(plan.Inputs, downloadSource{full, name, match[3], match[4], info.Size(), info.ModTime().UnixMilli(), metadata})
		}
		sort.Slice(plan.Inputs, func(i, j int) bool {
			a, b := plan.Inputs[i], plan.Inputs[j]
			if a.From != b.From {
				return a.From < b.From
			}
			if a.To != b.To {
				return a.To > b.To
			}
			if len(a.Name) != len(b.Name) {
				return len(a.Name) < len(b.Name)
			}
			return a.Name < b.Name
		})
		if err = writeState(filename, plan); err != nil {
			return plan, nil, err
		}
	} else if err != nil {
		return plan, nil, fmt.Errorf("restore incremental download plan: %w", err)
	}
	if plan.Version != 1 || plan.AsOf <= 0 || plan.AsOf > time.Now().UnixMilli() || (job.AsOf != 0 && job.AsOf != plan.AsOf) {
		return plan, nil, errors.New("invalid saved incremental download cutoff")
	}
	originals := map[string]os.FileInfo{}
	for _, source := range plan.Inputs {
		match := downloadFilename.FindStringSubmatch(strings.ToLower(source.Name))
		if filepath.Base(source.Name) != source.Name || source.Input != filepath.Join(directory, source.Name) || match == nil || match[1] != job.Symbol || match[2] != job.Timeframe {
			return plan, nil, errors.New("saved incremental source belongs to another asset or folder")
		}
		info, statErr := os.Lstat(source.Input)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != source.Bytes || info.ModTime().UnixMilli() != source.Modified {
			return plan, nil, errors.New("existing CSV changed since the download was queued; original kept")
		}
		originals[source.Input] = info
	}
	return plan, originals, nil
}

func downloadedMetadata(partial string, event workerEvent) (fileMetadata, error) {
	digest, err := hex.DecodeString(event.Checksum)
	if err != nil || len(digest) != 32 || event.FirstTimestamp < 0 || event.LastTimestamp < event.FirstTimestamp || len(event.Recipes) > 2000 || len(event.Coverage) > 10000 {
		return fileMetadata{}, errors.New("downloader returned invalid coverage metadata")
	}
	for _, span := range event.Coverage {
		if span.From < -8640000000000000 || span.To <= span.From || span.To > 8640000000000000 {
			return fileMetadata{}, errors.New("downloader returned invalid coverage range")
		}
	}
	info, err := os.Lstat(partial)
	if err != nil || !info.Mode().IsRegular() {
		return fileMetadata{}, errors.New("downloaded CSV is not a regular file")
	}
	return fileMetadata{Recipes: event.Recipes, Verified: len(event.Coverage) == 1, Bytes: info.Size(), Modified: info.ModTime().UnixNano(), FirstTimestamp: event.FirstTimestamp, LastTimestamp: event.LastTimestamp, Checksum: event.Checksum, CheckedThrough: event.CheckedThrough, Coverage: event.Coverage}, nil
}
func writeDownloadedMetadata(directory, name, partial string, event workerEvent) error {
	metadata, err := downloadedMetadata(partial, event)
	if err != nil {
		return err
	}
	return writeFileMetadata(directory, name, metadata)
}

// Completed queue entries keep pointing to the single published history when
// Refresh or Enrichment consolidates or renames it after the download finished.
func (m *Manager) recordHistoryRenameLocked(directory, name string, sources []downloadSource) {
	if m.batchDirectory != directory {
		return
	}
	previous := map[string]bool{}
	for _, source := range sources {
		previous[source.Input] = true
	}
	changed := false
	for i := range m.jobs {
		job := &m.jobs[i]
		if job.Status == "completed" && previous[job.OutputPath] && job.OutputPath != filepath.Join(directory, name) {
			job.OutputPath = filepath.Join(directory, name)
			m.publication[job.ID] = name
			changed = true
		}
	}
	if changed {
		m.saveDownloadsLocked()
	}
}

func (m *Manager) publishExtendedDownload(ctx context.Context, index int, job Job, directory, work, partial string, plan downloadPlan, originals map[string]os.FileInfo, result workerEvent) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	for filename, original := range originals {
		if err := unchangedFile(filename, original); err != nil {
			return "", "", err
		}
	}
	target := plan.Inputs[0]
	message := cleanMessage(result.Message)
	metadataInput := partial
	if result.NoChange && len(plan.Inputs) == 1 {
		metadataInput = target.Input
	}
	metadata, err := downloadedMetadata(metadataInput, result)
	if err != nil {
		return "", "", err
	}
	if result.NoChange && result.Coverage == nil {
		// A covered request may skip replay. Retain its existing recipes and
		// query ledger rather than replacing them with an empty done event.
		metadata.Recipes, metadata.Coverage = target.Metadata.Recipes, target.Metadata.Coverage
		metadata.Verified, metadata.CheckedThrough = target.Metadata.Verified, target.Metadata.CheckedThrough
	}
	for _, recipe := range metadata.Recipes {
		if _, err := m.validateEnrichment(recipe); err != nil {
			return "", "", err
		}
	}
	name, err := canonicalHistoryName(job.Symbol, job.Timeframe, job.From, job.To, plan.Inputs, metadata)
	if err != nil {
		return "", "", err
	}
	output := filepath.Join(directory, name)
	if result.NoChange && len(plan.Inputs) == 1 && target.Name == name {
		if err := checkHistoryFamily(directory, historyPublication{Name: name, Sources: []historyOriginal{{Name: target.Name}}}); err != nil {
			return "", "", err
		}
		if result.Coverage != nil {
			if err := writeFileMetadata(directory, name, metadata); err != nil {
				return "", "", err
			}
		}
		m.jobs[index].Status, m.jobs[index].Progress, m.jobs[index].Message, m.jobs[index].OutputPath = "completed", 100, message, output
		m.saveDownloadsLocked()
		return output, message, nil
	}
	if metadataInput == target.Input {
		// Keep the same recovery inode even for a filename-only extension of
		// already queried empty coverage. The worker's empty partial is owned.
		if err := os.Remove(partial); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		if err := os.Link(target.Input, partial); err != nil {
			return "", "", err
		}
	}
	if err := os.Chmod(partial, originals[target.Input].Mode().Perm()); err != nil {
		return "", "", err
	}
	m.publication[job.ID] = name
	if !m.saveDownloadsLocked() {
		return "", "", errors.New(m.stateError)
	}
	if _, err = publishHistory(directory, name, partial, plan.Inputs, originals, metadata); err != nil {
		return "", "", err
	}
	m.jobs[index].Status, m.jobs[index].Progress, m.jobs[index].Message, m.jobs[index].OutputPath = "completed", 100, message, output
	m.saveDownloadsLocked()
	return output, message, nil
}
