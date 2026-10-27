package screener

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type historyGroup struct {
	name string
	ids  []string
}

// Queue one operation per instrument/timeframe. Each original ID remains a
// polling alias while a running operation publishes its canonical filename.
func groupHistories(directory string, names []string) []historyGroup {
	groups := []historyGroup{}
	positions := map[string]int{}
	for _, name := range names {
		key := name
		if match := downloadFilename.FindStringSubmatch(strings.ToLower(name)); match != nil {
			key = match[1] + "/" + match[2]
		}
		index, exists := positions[key]
		if !exists {
			index = len(groups)
			positions[key] = index
			groups = append(groups, historyGroup{name: name})
		}
		groups[index].ids = append(groups[index].ids, dataFileID(directory, name))
	}
	return groups
}

func historyIDWanted(wanted map[string]bool, id string, aliases []string) bool {
	if wanted[id] {
		return true
	}
	for _, alias := range aliases {
		if wanted[alias] {
			return true
		}
	}
	return false
}

// Consolidation runs without provider access before any refresh or enrichment.
// A refused download therefore cannot leave legacy overlapping histories in
// the visible CSV list. The worker performs the same bounded ordered merge and
// indicator replay as incremental downloads; Go alone publishes/removes files.
func (m *Manager) consolidateHistory(ctx context.Context, directory, name string, legacy map[string][]EnrichmentRequest, progress func(string)) (string, error) {
	match := downloadFilename.FindStringSubmatch(strings.ToLower(name))
	if match == nil {
		return name, nil
	}
	work, err := os.MkdirTemp(directory, ".cryptodesk-consolidate-*")
	if err != nil {
		return name, err
	}
	defer os.RemoveAll(work)
	job := Job{Symbol: match[1], Timeframe: match[2], From: match[3], To: match[4], AsOf: time.Now().UnixMilli()}
	plan, originals, err := prepareDownloadPlan(job, directory, work)
	if err != nil {
		return name, err
	}
	if len(plan.Inputs) < 2 {
		return name, nil
	}
	for index := range plan.Inputs {
		for _, recipe := range legacy[plan.Inputs[index].Name] {
			plan.Inputs[index].Metadata.Recipes = addRecipe(plan.Inputs[index].Metadata.Recipes, recipe)
		}
	}
	if err = writeState(plan.workerFile(work), plan); err != nil {
		return name, err
	}
	output := filepath.Join(work, "consolidated.csv")
	input, err := json.Marshal(struct {
		Symbol    string `json:"symbol"`
		Timeframe string `json:"timeframe"`
		From      string `json:"from"`
		To        string `json:"to"`
		Output    string `json:"output"`
		PlanFile  string `json:"planFile"`
		OwnerPipe bool   `json:"ownerPipe"`
		LocalOnly bool   `json:"localOnly"`
	}{job.Symbol, job.Timeframe, job.From, job.To, output, plan.workerFile(work), true, true})
	if err != nil {
		return name, err
	}
	cmd := exec.CommandContext(ctx, m.runtime.node, m.runtime.worker)
	configureProcess(cmd)
	cmd.Dir, cmd.WaitDelay = work, 2*time.Second
	owner, err := cmd.StdinPipe()
	if err != nil {
		return name, err
	}
	defer owner.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return name, err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return name, fmt.Errorf("start history consolidation: %w", err)
	}
	if _, err = owner.Write(append(input, '\n')); err != nil {
		_ = cmd.Cancel()
		_ = cmd.Wait()
		return name, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var result workerEvent
	var done bool
	var workerError string
	for scanner.Scan() {
		var event workerEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			workerError = "History consolidation returned invalid progress"
			_ = cmd.Cancel()
			break
		}
		switch event.Type {
		case "progress":
			progress(cleanMessage(event.Message))
		case "done":
			result, done = event, event.Rows > 0
		case "error":
			workerError = cleanMessage(event.Message)
			_ = cmd.Cancel()
		default:
			workerError = "Offline history consolidation returned an unexpected event"
			_ = cmd.Cancel()
		}
	}
	if scanner.Err() != nil {
		workerError = "Read history consolidation progress: " + scanner.Err().Error()
		_ = cmd.Cancel()
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return name, ctx.Err()
	}
	if workerError != "" {
		return name, errors.New(workerError)
	}
	if err != nil {
		return name, fmt.Errorf("history consolidation failed: %w %s", err, cleanMessage(stderr.String()))
	}
	if !done {
		return name, errors.New("history consolidation did not confirm its rows")
	}
	if err := os.Chmod(output, originals[plan.Inputs[0].Input].Mode().Perm()); err != nil {
		return name, err
	}
	metadata, err := downloadedMetadata(output, result)
	if err != nil {
		return name, err
	}
	// Offline merging proves row ordering and values, not that a legacy range
	// has no missing bars. Retain its need for a full first provider refresh.
	for _, source := range plan.Inputs {
		if !source.Metadata.Verified || source.Metadata.Checksum == "" || !metadataMatches(source.Metadata, originals[source.Input]) {
			metadata.Verified, metadata.CheckedThrough = false, 0
			break
		}
	}
	for _, recipe := range metadata.Recipes {
		if _, err := m.validateEnrichment(recipe); err != nil {
			return name, err
		}
	}
	canonical, err := canonicalHistoryName(job.Symbol, job.Timeframe, job.From, job.To, plan.Inputs, metadata)
	if err != nil {
		return name, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return name, ctx.Err()
	}
	if _, err = publishHistory(directory, canonical, output, plan.Inputs, originals, metadata); err != nil {
		return name, err
	}
	m.recordHistoryRenameLocked(directory, canonical, plan.Inputs)
	return canonical, nil
}
