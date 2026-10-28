package screener

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type RefreshJob struct {
	fileIDAliases []string
	ID            string   `json:"id"`
	FileID        string   `json:"fileId"`
	Name          string   `json:"name"`
	Status        string   `json:"status"`
	Progress      float64  `json:"progress"`
	Rows          int64    `json:"rows"`
	AddedRows     int64    `json:"addedRows"`
	UpdatedRows   int64    `json:"updatedRows"`
	Message       string   `json:"message"`
	Columns       []string `json:"columns"`
	BackupPath    string   `json:"backupPath,omitempty"`
}

type RefreshInfo struct {
	Directory        string       `json:"directory"`
	Active           bool         `json:"active"`
	AsOf             int64        `json:"asOf,omitempty"`
	RuntimeAvailable bool         `json:"runtimeAvailable"`
	RuntimeError     string       `json:"runtimeError,omitempty"`
	Total            int          `json:"total"`
	Completed        int          `json:"completed"`
	Failed           int          `json:"failed"`
	Cancelled        int          `json:"cancelled"`
	Progress         float64      `json:"progress"`
	CurrentFile      string       `json:"currentFile"`
	CurrentProgress  float64      `json:"currentProgress"`
	Jobs             []RefreshJob `json:"jobs"`
}

func (m *Manager) refreshRuntimeError() string {
	if message := m.enrichmentRuntimeError(); message != "" {
		return message
	}
	if st, err := os.Stat(filepath.Join(filepath.Dir(m.runtime.worker), "refresh-worker.cjs")); err != nil || !st.Mode().IsRegular() {
		return "Refresh worker is missing; rebuild or reinstall the complete Screener runtime"
	}
	return ""
}

func (m *Manager) refreshInfoLocked(fileIDs []string) RefreshInfo {
	message := m.refreshRuntimeError()
	info := RefreshInfo{Directory: m.directory, Active: m.refreshActive, AsOf: m.refreshAsOf, RuntimeAvailable: message == "", RuntimeError: message, Total: len(m.refreshJobs), Jobs: []RefreshJob{}}
	wanted := map[string]bool{}
	for _, id := range fileIDs {
		wanted[id] = true
	}
	for _, job := range m.refreshJobs {
		progress := job.Progress
		switch job.Status {
		case "completed":
			info.Completed++
			progress = 100
		case "failed":
			info.Failed++
			progress = 100
		case "cancelled":
			info.Cancelled++
			progress = 100
		case "running":
			info.CurrentFile, info.CurrentProgress = job.Name, job.Progress
		}
		info.Progress += progress
		if historyIDWanted(wanted, job.FileID, job.fileIDAliases) {
			job.Columns = append([]string{}, job.Columns...)
			info.Jobs = append(info.Jobs, job)
		}
	}
	if info.Total > 0 {
		info.Progress /= float64(info.Total)
	}
	return info
}

func (m *Manager) Refresh(fileIDs []string) (RefreshInfo, error) {
	if len(fileIDs) > 200 {
		return RefreshInfo{}, errors.New("at most 200 visible files may be polled")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshInfoLocked(fileIDs), nil
}

func (m *Manager) StartRefresh() (RefreshInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	asOf := time.Now().UnixMilli()
	if m.closed {
		return m.refreshInfoLocked(nil), errors.New("screener is closed")
	}
	if m.liveActive {
		return m.refreshInfoLocked(nil), errors.New("finish the Live Alerts check before refreshing files")
	}
	if m.refreshActive {
		return m.refreshInfoLocked(nil), errors.New("a data refresh is already running")
	}
	if m.active || m.pendingLocked() {
		return m.refreshInfoLocked(nil), errors.New("finish or cancel downloads before refreshing existing files")
	}
	if m.enrichmentActive {
		return m.refreshInfoLocked(nil), errors.New("finish or cancel enrichment before refreshing files")
	}
	if m.analysisActiveLocked() {
		return m.refreshInfoLocked(nil), errors.New("finish or cancel Analysis before changing its data files")
	}
	if message := m.refreshRuntimeError(); message != "" {
		return m.refreshInfoLocked(nil), errors.New(message)
	}
	if err := recoverHistoryPublications(m.directory); err != nil {
		return m.refreshInfoLocked(nil), fmt.Errorf("recover saved history: %w", err)
	}
	names, err := csvNames(m.directory)
	if err != nil {
		return m.refreshInfoLocked(nil), err
	}
	if len(names) == 0 {
		return m.refreshInfoLocked(nil), errors.New("there are no downloaded CSV files to refresh")
	}
	groups := groupHistories(m.directory, names)
	jobs := make([]RefreshJob, len(groups))
	for i, group := range groups {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return m.refreshInfoLocked(nil), err
		}
		jobs[i] = RefreshJob{ID: hex.EncodeToString(random[:]), FileID: dataFileID(m.directory, group.name), Name: group.name, fileIDAliases: group.ids, Status: "queued", Message: "Waiting for data refresh", Columns: []string{}}
	}
	m.refreshJobs, m.refreshActive = jobs, true
	m.refreshAsOf = asOf
	ctx, cancel := context.WithCancel(context.Background())
	m.refreshCancel, m.refreshDone = cancel, make(chan struct{})
	m.wg.Add(1)
	go m.runRefresh(ctx, m.directory, asOf)
	return m.refreshInfoLocked(nil), nil
}

func (m *Manager) CancelRefresh() RefreshInfo {
	m.mu.Lock()
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
	done := m.refreshDone
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshInfoLocked(nil)
}

func (m *Manager) runRefresh(ctx context.Context, directory string, asOf int64) {
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.refreshActive, m.refreshCancel = false, nil
		close(m.refreshDone)
	}()
	ctx, cleanup, err := withProviderSession(ctx, m.profile)
	if err != nil {
		m.mu.Lock()
		for i := range m.refreshJobs {
			m.refreshJobs[i].Status, m.refreshJobs[i].Message = "failed", "Prepare Dukascopy session: "+cleanMessage(err.Error())
		}
		m.mu.Unlock()
		return
	}
	defer cleanup()
	legacy := legacyRecipes(ctx, directory)
	m.mu.Lock()
	count := len(m.refreshJobs)
	m.mu.Unlock()
	runFilePoolWithConcurrency(count, MaxConcurrency, func(index int) {
		m.mu.Lock()
		if ctx.Err() != nil {
			m.refreshJobs[index].Status, m.refreshJobs[index].Message = "cancelled", "Refresh cancelled; original file kept"
			m.mu.Unlock()
			return
		}
		m.refreshJobs[index].Status, m.refreshJobs[index].Message = "running", "Checking saved data and indicator recipes"
		job := m.refreshJobs[index]
		m.mu.Unlock()
		var err error
		if len(job.fileIDAliases) > 1 {
			job.Name, err = m.consolidateHistory(ctx, directory, job.Name, legacy, func(message string) {
				m.mu.Lock()
				m.refreshJobs[index].Message = message
				m.mu.Unlock()
			})
			if err == nil {
				job.FileID = dataFileID(directory, job.Name)
				m.mu.Lock()
				m.refreshJobs[index].Name, m.refreshJobs[index].FileID = job.Name, job.FileID
				m.mu.Unlock()
			}
		}
		if err == nil {
			err = m.refreshFile(ctx, index, directory, job, legacy[job.Name], asOf)
		}
		m.mu.Lock()
		if m.refreshJobs[index].Status != "completed" {
			if ctx.Err() != nil {
				m.refreshJobs[index].Status, m.refreshJobs[index].Message = "cancelled", "Refresh cancelled; original file kept"
			} else if err != nil {
				m.refreshJobs[index].Status, m.refreshJobs[index].Message = "failed", cleanMessage(err.Error())
			} else {
				m.refreshJobs[index].Status, m.refreshJobs[index].Message = "failed", "Refresh did not publish its CSV"
			}
		}
		m.mu.Unlock()
	})
}

type refreshEvent struct {
	providerEvent
	Type           string              `json:"type"`
	Progress       float64             `json:"progress"`
	Rows           int64               `json:"rows"`
	AddedRows      int64               `json:"addedRows"`
	UpdatedRows    int64               `json:"updatedRows"`
	Columns        []string            `json:"columns"`
	Recipes        []EnrichmentRequest `json:"recipes"`
	FirstTimestamp int64               `json:"firstTimestamp"`
	LastTimestamp  int64               `json:"lastTimestamp"`
	Checksum       string              `json:"checksum"`
	CheckedThrough int64               `json:"checkedThrough"`
	Coverage       []dataRange         `json:"coverage"`
	Message        string              `json:"message"`
}

func (m *Manager) refreshFile(ctx context.Context, index int, directory string, job RefreshJob, legacy []EnrichmentRequest, asOf int64) error {
	match := downloadFilename.FindStringSubmatch(strings.ToLower(job.Name))
	if match == nil {
		return errors.New("cannot identify the Dukascopy instrument, timeframe and start date from this CSV filename; original kept")
	}
	source := filepath.Join(directory, job.Name)
	original, err := os.Lstat(source)
	if err != nil || !original.Mode().IsRegular() {
		return errors.New("source CSV is no longer a regular file")
	}
	metadata, err := readFileMetadata(directory, job.Name)
	if err != nil {
		return err
	}
	for _, recipe := range legacy {
		metadata.Recipes = addRecipe(metadata.Recipes, recipe)
	}
	work, err := os.MkdirTemp(directory, ".cryptodesk-refresh-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	output := filepath.Join(work, "refreshed.csv")
	// Recipe files keep the owner-pipe request bounded even after hundreds of
	// distinct applied indicators. They are created exclusively in owned scratch.
	recipePath := filepath.Join(work, "recipes.json")
	encoded, _ := json.Marshal(metadata.Recipes)
	if err = writeSyncedExclusive(recipePath, encoded); err != nil {
		return err
	}
	input, _ := json.Marshal(struct {
		Input            string           `json:"input"`
		Output           string           `json:"output"`
		Symbol           string           `json:"symbol"`
		Timeframe        string           `json:"timeframe"`
		From             string           `json:"from"`
		RecipeFile       string           `json:"recipeFile"`
		CoverageVerified bool             `json:"coverageVerified"`
		CoverageDigest   string           `json:"coverageDigest"`
		OwnerPipe        bool             `json:"ownerPipe"`
		AsOf             int64            `json:"asOf"`
		ProviderSession  *providerSession `json:"providerSession,omitempty"`
	}{Input: source, Output: output, Symbol: match[1], Timeframe: match[2], From: refreshFrom(match[3], metadata.Coverage), RecipeFile: recipePath, CoverageVerified: metadata.Verified && metadataMatches(metadata, original), CoverageDigest: metadata.Checksum, OwnerPipe: true, AsOf: asOf, ProviderSession: sessionFromContext(ctx)})
	cmd := exec.CommandContext(ctx, m.runtime.node, filepath.Join(filepath.Dir(m.runtime.worker), "refresh-worker.cjs"))
	configureProcess(cmd)
	cmd.Dir = work
	owner, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer owner.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start data refresh: %w", err)
	}
	if _, err = owner.Write(append(input, '\n')); err != nil {
		_ = cmd.Cancel()
		_ = cmd.Wait()
		return err
	}
	var result refreshEvent
	var done bool
	var workerError string
	replies := newProviderReplies(ctx, owner, func(message string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.refreshJobs[index].Status == "running" {
			m.refreshJobs[index].Message = message
		}
	})
	defer replies.close()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		var event refreshEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			workerError = "Refresh worker returned invalid progress"
			_ = cmd.Cancel()
			break
		}
		switch event.Type {
		case "provider-request":
			if err := replies.handle(event.providerEvent); err != nil {
				workerError = err.Error()
				_ = cmd.Cancel()
			}
		case "progress":
			m.mu.Lock()
			if !math.IsNaN(event.Progress) && !math.IsInf(event.Progress, 0) {
				m.refreshJobs[index].Progress = math.Max(m.refreshJobs[index].Progress, math.Min(99, math.Max(0, event.Progress)))
			}
			m.refreshJobs[index].Rows, m.refreshJobs[index].AddedRows, m.refreshJobs[index].UpdatedRows = event.Rows, event.AddedRows, event.UpdatedRows
			m.refreshJobs[index].Message = cleanMessage(event.Message)
			m.mu.Unlock()
		case "done":
			result, done = event, event.Rows > 0 && event.AddedRows >= 0 && event.UpdatedRows >= 0 && len(event.Recipes) <= 2000
		case "error":
			workerError = cleanMessage(event.Message)
			_ = cmd.Cancel()
		default:
			workerError = "Refresh worker returned an unknown event"
			_ = cmd.Cancel()
		}
	}
	if scanner.Err() != nil {
		workerError = "Read data refresh progress: " + scanner.Err().Error()
		_ = cmd.Cancel()
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if workerError != "" {
		return errors.New(workerError)
	}
	if err != nil {
		return fmt.Errorf("data refresh failed: %w %s", err, cleanMessage(stderr.String()))
	}
	if !done {
		return errors.New("data refresh exited without confirming its rows and indicator recipes")
	}
	for _, recipe := range result.Recipes {
		if _, err = m.validateEnrichment(recipe); err != nil {
			return fmt.Errorf("refresh returned invalid indicator recipe: %w", err)
		}
	}
	outputInfo, err := os.Lstat(output)
	if err != nil || !outputInfo.Mode().IsRegular() || outputInfo.Size() == 0 {
		return errors.New("data refresh produced no regular CSV")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err = unchangedFile(source, original); err != nil {
		return err
	}
	sources := []downloadSource{{Input: source, Name: job.Name, From: match[3], To: match[4], Bytes: original.Size(), Modified: original.ModTime().UnixMilli(), Metadata: metadata}}
	metadata = fileMetadata{Recipes: result.Recipes, Verified: true, Bytes: outputInfo.Size(), Modified: outputInfo.ModTime().UnixNano(), FirstTimestamp: result.FirstTimestamp, LastTimestamp: result.LastTimestamp, Checksum: result.Checksum, CheckedThrough: result.CheckedThrough, Coverage: result.Coverage}
	// Date filenames express an exclusive upper bound. A refresh runs through
	// its execution instant, so today's partial range ends at tomorrow's date.
	to := time.UnixMilli(asOf).UTC().Add(24 * time.Hour).Format("2006-01-02")
	name, err := canonicalHistoryName(match[1], match[2], match[3], to, sources, metadata)
	if err != nil {
		return err
	}
	backup, err := publishHistory(directory, name, output, sources, map[string]os.FileInfo{source: original}, metadata)
	if err != nil {
		return fmt.Errorf("publish refreshed history: %w", err)
	}
	m.recordHistoryRenameLocked(directory, name, sources)
	current := &m.refreshJobs[index]
	current.Name, current.FileID = name, dataFileID(directory, name)
	current.Status, current.Progress, current.Rows = "completed", 100, result.Rows
	current.AddedRows, current.UpdatedRows, current.Message = result.AddedRows, result.UpdatedRows, cleanMessage(result.Message)
	current.Columns, current.BackupPath = append([]string{}, result.Columns...), backup
	return nil
}

func refreshFrom(named string, coverage []dataRange) string {
	for _, span := range coverage {
		date := time.UnixMilli(span.From).UTC()
		if date.Year() >= 1900 && date.Format("2006-01-02") < named {
			named = date.Format("2006-01-02")
		}
	}
	return named
}
