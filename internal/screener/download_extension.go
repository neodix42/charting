package screener

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
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
	if result.NoChange {
		if digest, err := hex.DecodeString(result.Checksum); err != nil || len(digest) != 32 {
			return "", "", errors.New("downloader did not confirm the unchanged history fingerprint")
		}
		if result.Coverage != nil {
			if err := writeDownloadedMetadata(directory, target.Name, target.Input, result); err != nil {
				return "", "", err
			}
		}
		m.jobs[index].Status, m.jobs[index].Progress, m.jobs[index].Message, m.jobs[index].OutputPath = "completed", 100, message, target.Input
		m.saveDownloadsLocked()
		return target.Input, message, nil
	}
	if err := os.Chmod(partial, originals[target.Input].Mode().Perm()); err != nil {
		return "", "", err
	}
	metadata, err := downloadedMetadata(partial, result)
	if err != nil {
		return "", "", err
	}
	for _, recipe := range metadata.Recipes {
		if _, err := m.validateEnrichment(recipe); err != nil {
			return "", "", err
		}
	}
	backupFolder := filepath.Join(directory, ".cryptodesk-download-backups")
	if err = os.Mkdir(backupFolder, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", "", err
	}
	if info, err := os.Lstat(backupFolder); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("download backup folder must be a real directory")
	}
	backup := filepath.Join(backupFolder, job.ID+".csv")
	if err = os.Link(target.Input, backup); err != nil {
		old, backupErr := os.Lstat(backup)
		if !errors.Is(err, os.ErrExist) || backupErr != nil || !os.SameFile(originals[target.Input], old) {
			return "", "", fmt.Errorf("preserve original download: %w", err)
		}
	}
	encoded, _ := json.Marshal(struct {
		Filename string       `json:"filename"`
		Previous fileMetadata `json:"previous"`
	}{target.Name, target.Metadata})
	backupState := filepath.Join(backupFolder, job.ID+".json")
	if err = writeSyncedExclusive(backupState, encoded); err != nil {
		previous, readErr := os.ReadFile(backupState)
		if !errors.Is(err, os.ErrExist) || readErr != nil || !bytes.Equal(previous, encoded) {
			return "", "", fmt.Errorf("save original download metadata: %w", err)
		}
	}
	if folder, err := os.Open(backupFolder); err == nil {
		_ = folder.Sync()
		_ = folder.Close()
	}
	m.publication[job.ID] = target.Name
	if !m.saveDownloadsLocked() {
		return "", "", errors.New(m.stateError)
	}
	// Metadata precedes replacement; a crash invalidates its fingerprint safely.
	if err = writeFileMetadata(directory, target.Name, metadata); err != nil {
		return "", "", err
	}
	publication := filepath.Join(work, "publish.csv")
	if err = os.Remove(publication); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	if err = os.Link(partial, publication); err != nil {
		return "", "", err
	}
	if folder, err := os.Open(work); err == nil {
		_ = folder.Sync()
		_ = folder.Close()
	}
	for filename, original := range originals {
		if err := unchangedFile(filename, original); err != nil {
			return "", "", err
		}
	}
	if err = os.Rename(publication, target.Input); err != nil {
		return "", "", fmt.Errorf("publish extended download (original kept): %w", err)
	}
	if folder, err := os.Open(directory); err == nil {
		_ = folder.Sync()
		_ = folder.Close()
	}
	// Keep the private partial hard link until the completed queue commits. The
	// existing intent/inode recovery then handles a crash after atomic replacement.
	m.jobs[index].Status, m.jobs[index].Progress, m.jobs[index].Message, m.jobs[index].OutputPath = "completed", 100, message, target.Input
	m.saveDownloadsLocked()
	return target.Input, message, nil
}
