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
)

type EnrichmentJob struct {
	ID         string   `json:"id"`
	FileID     string   `json:"fileId"`
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	Progress   float64  `json:"progress"`
	Rows       int64    `json:"rows"`
	Message    string   `json:"message"`
	Columns    []string `json:"columns"`
	BackupPath string   `json:"backupPath,omitempty"`
}

type EnrichmentInfo struct {
	Directory        string          `json:"directory"`
	Active           bool            `json:"active"`
	RuntimeAvailable bool            `json:"runtimeAvailable"`
	RuntimeError     string          `json:"runtimeError,omitempty"`
	Indicator        string          `json:"indicator"`
	Total            int             `json:"total"`
	Completed        int             `json:"completed"`
	Failed           int             `json:"failed"`
	Cancelled        int             `json:"cancelled"`
	CurrentFile      string          `json:"currentFile"`
	CurrentProgress  float64         `json:"currentProgress"`
	Jobs             []EnrichmentJob `json:"jobs"`
}

func (m *Manager) enrichmentRuntimeError() string {
	if m.runtimeError != "" {
		return m.runtimeError
	}
	directory := filepath.Dir(m.runtime.worker)
	if st, err := os.Stat(filepath.Join(directory, "enrich-worker.cjs")); err != nil || !st.Mode().IsRegular() {
		return "Enrichment worker is missing; rebuild or reinstall the complete Screener runtime"
	}
	for name, version := range map[string]string{"trading-signals": m.Indicators().Version, "csv-parse": "7.0.3"} {
		data, err := os.ReadFile(filepath.Join(directory, "node_modules", name, "package.json"))
		var pkg struct{ Version string }
		if err != nil || json.Unmarshal(data, &pkg) != nil || pkg.Version != version {
			return fmt.Sprintf("Enrichment requires %s %s; run npm ci --prefix screener-runtime for development or reinstall the complete package", name, version)
		}
	}
	return ""
}

// Only visible file IDs are returned, keeping progress polling bounded even
// when many thousands of CSVs have been downloaded. Summary counts cover all.
func (m *Manager) enrichmentInfoLocked(fileIDs []string) EnrichmentInfo {
	errorText := m.enrichmentRuntimeError()
	info := EnrichmentInfo{Directory: m.directory, Active: m.enrichmentActive, RuntimeAvailable: errorText == "", RuntimeError: errorText,
		Indicator: m.enrichmentRequest.Indicator, Total: len(m.enrichmentJobs), Jobs: []EnrichmentJob{}}
	wanted := map[string]bool{}
	for _, id := range fileIDs {
		wanted[id] = true
	}
	for _, job := range m.enrichmentJobs {
		switch job.Status {
		case "completed":
			info.Completed++
		case "failed":
			info.Failed++
		case "cancelled":
			info.Cancelled++
		case "running":
			info.CurrentFile, info.CurrentProgress = job.Name, job.Progress
		}
		if wanted[job.FileID] {
			job.Columns = append([]string{}, job.Columns...)
			info.Jobs = append(info.Jobs, job)
		}
	}
	return info
}

func (m *Manager) Enrichment(fileIDs []string) (EnrichmentInfo, error) {
	if len(fileIDs) > 200 {
		return EnrichmentInfo{}, errors.New("at most 200 visible files may be polled")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enrichmentInfoLocked(fileIDs), nil
}

func (m *Manager) StartEnrichment(request EnrichmentRequest) (EnrichmentInfo, error) {
	request, err := m.validateEnrichment(request)
	if err != nil {
		return EnrichmentInfo{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.enrichmentInfoLocked(nil), errors.New("screener is closed")
	}
	if m.enrichmentActive {
		return m.enrichmentInfoLocked(nil), errors.New("an enrichment batch is already running")
	}
	if message := m.enrichmentRuntimeError(); message != "" {
		return m.enrichmentInfoLocked(nil), errors.New(message)
	}
	names, err := csvNames(m.directory)
	if err != nil {
		return m.enrichmentInfoLocked(nil), err
	}
	if len(names) == 0 {
		return m.enrichmentInfoLocked(nil), errors.New("there are no downloaded CSV files to enrich")
	}
	jobs := make([]EnrichmentJob, len(names))
	for i, name := range names {
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return m.enrichmentInfoLocked(nil), err
		}
		jobs[i] = EnrichmentJob{ID: hex.EncodeToString(random[:]), FileID: dataFileID(m.directory, name), Name: name, Status: "queued", Message: "Waiting for enrichment", Columns: []string{}}
	}
	m.enrichmentJobs, m.enrichmentRequest, m.enrichmentActive = jobs, request, true
	ctx, cancel := context.WithCancel(context.Background())
	m.enrichmentCancel = cancel
	m.enrichmentDone = make(chan struct{})
	m.wg.Add(1)
	go m.runEnrichment(ctx, m.directory, request)
	return m.enrichmentInfoLocked(nil), nil
}

func (m *Manager) CancelEnrichment() EnrichmentInfo {
	m.mu.Lock()
	if m.enrichmentCancel != nil {
		m.enrichmentCancel()
	}
	done := m.enrichmentDone
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enrichmentInfoLocked(nil)
}

func (m *Manager) runEnrichment(ctx context.Context, directory string, request EnrichmentRequest) {
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.enrichmentActive, m.enrichmentCancel = false, nil
		close(m.enrichmentDone)
	}()
	for index := range m.enrichmentJobs {
		m.mu.Lock()
		if ctx.Err() != nil {
			m.enrichmentJobs[index].Status, m.enrichmentJobs[index].Message = "cancelled", "Enrichment cancelled; original file kept"
			m.mu.Unlock()
			continue
		}
		m.enrichmentJobs[index].Status, m.enrichmentJobs[index].Message = "running", "Calculating indicator"
		job := m.enrichmentJobs[index]
		m.mu.Unlock()
		err := m.enrichFile(ctx, index, directory, job, request)
		m.mu.Lock()
		if m.enrichmentJobs[index].Status != "completed" {
			if ctx.Err() != nil {
				m.enrichmentJobs[index].Status, m.enrichmentJobs[index].Message = "cancelled", "Enrichment cancelled; original file kept"
			} else {
				m.enrichmentJobs[index].Status, m.enrichmentJobs[index].Message = "failed", cleanMessage(err.Error())
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) enrichFile(ctx context.Context, index int, directory string, job EnrichmentJob, request EnrichmentRequest) error {
	source := filepath.Join(directory, job.Name)
	original, err := os.Lstat(source)
	if err != nil || !original.Mode().IsRegular() {
		return errors.New("source CSV is no longer a regular file")
	}
	work, err := os.MkdirTemp(directory, ".cryptodesk-enrichment-*")
	if err != nil {
		return fmt.Errorf("create enrichment temporary folder: %w", err)
	}
	defer os.RemoveAll(work)
	output := filepath.Join(work, "enriched.csv")
	cmd := exec.CommandContext(ctx, m.runtime.node, filepath.Join(filepath.Dir(m.runtime.worker), "enrich-worker.cjs"))
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
	input, err := json.Marshal(struct {
		EnrichmentRequest
		Input     string `json:"input"`
		Output    string `json:"output"`
		OwnerPipe bool   `json:"ownerPipe"`
	}{request, source, output, true})
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start enrichment worker: %w", err)
	}
	if _, err = owner.Write(append(input, '\n')); err != nil {
		_ = cmd.Cancel()
		_ = cmd.Wait()
		return err
	}
	var result struct {
		Type     string   `json:"type"`
		Progress float64  `json:"progress"`
		Rows     int64    `json:"rows"`
		Columns  []string `json:"columns"`
		Message  string   `json:"message"`
	}
	var done bool
	var workerError string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 16384)
	for scanner.Scan() {
		if json.Unmarshal(scanner.Bytes(), &result) != nil {
			workerError = "Enrichment worker returned invalid progress"
			_ = cmd.Cancel()
			break
		}
		switch result.Type {
		case "progress":
			m.mu.Lock()
			if !math.IsNaN(result.Progress) && !math.IsInf(result.Progress, 0) {
				m.enrichmentJobs[index].Progress = math.Max(m.enrichmentJobs[index].Progress, math.Min(99, math.Max(0, result.Progress)))
			}
			m.enrichmentJobs[index].Message, m.enrichmentJobs[index].Rows = cleanMessage(result.Message), result.Rows
			if len(result.Columns) > 0 {
				m.enrichmentJobs[index].Columns = append([]string{}, result.Columns...)
			}
			m.mu.Unlock()
		case "done":
			done = result.Rows > 0 && len(result.Columns) > 0
		case "error":
			workerError = cleanMessage(result.Message)
			_ = cmd.Cancel()
		default:
			workerError = "Enrichment worker returned an unknown event"
			_ = cmd.Cancel()
		}
	}
	if scanner.Err() != nil {
		workerError = "Read enrichment progress: " + scanner.Err().Error()
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
		return fmt.Errorf("enrichment worker failed: %w %s", err, cleanMessage(stderr.String()))
	}
	if !done {
		return errors.New("enrichment worker exited without confirming calculated rows")
	}
	if st, e := os.Lstat(output); e != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return errors.New("enrichment worker produced no CSV")
	}
	// Cancellation and publication are indivisible. Recheck the source before
	// preserving its exact bytes, then atomically replace only that source path.
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err = unchangedFile(source, original); err != nil {
		return err
	}
	backupDirectory := filepath.Join(directory, ".cryptodesk-enrichment-backups")
	if err = os.Mkdir(backupDirectory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create original CSV backup folder: %w", err)
	}
	if st, e := os.Lstat(backupDirectory); e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("original CSV backup folder must be a real directory")
	}
	backup := filepath.Join(backupDirectory, job.ID+".csv")
	if err = os.Link(source, backup); err != nil {
		return fmt.Errorf("preserve original CSV backup: %w", err)
	}
	metadata, _ := json.Marshal(struct {
		Filename string            `json:"filename"`
		Request  EnrichmentRequest `json:"request"`
	}{job.Name, request})
	if err = writeSyncedExclusive(filepath.Join(backupDirectory, job.ID+".json"), append(metadata, '\n')); err != nil {
		return fmt.Errorf("save original CSV backup metadata: %w", err)
	}
	if parent, err := os.Open(backupDirectory); err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	if err = unchangedFile(source, original); err != nil {
		return err
	}
	if err = os.Rename(output, source); err != nil {
		return fmt.Errorf("publish enriched CSV (original kept): %w", err)
	}
	if parent, err := os.Open(directory); err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	m.enrichmentJobs[index].Status, m.enrichmentJobs[index].Progress = "completed", 100
	m.enrichmentJobs[index].Rows, m.enrichmentJobs[index].Message = result.Rows, cleanMessage(result.Message)
	m.enrichmentJobs[index].Columns, m.enrichmentJobs[index].BackupPath = append([]string{}, result.Columns...), backup
	return nil
}

func unchangedFile(path string, original os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(original, current) || current.Size() != original.Size() || !current.ModTime().Equal(original.ModTime()) {
		return errors.New("source CSV changed during enrichment; original was not replaced")
	}
	return nil
}

func writeSyncedExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	return errors.Join(writeErr, syncErr, f.Close())
}
