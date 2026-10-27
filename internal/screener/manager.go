package screener

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Request struct {
	Symbols     []string `json:"symbols"`
	Timeframe   string   `json:"timeframe"`
	From        string   `json:"from"`
	To          string   `json:"to"`
	Concurrency int      `json:"concurrency,omitempty"`
}

const MaxConcurrency = 20

type Job struct {
	ID         string  `json:"id"`
	Symbol     string  `json:"symbol"`
	Timeframe  string  `json:"timeframe"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Status     string  `json:"status"`
	Progress   float64 `json:"progress"`
	Message    string  `json:"message"`
	OutputPath string  `json:"outputPath,omitempty"`
	AsOf       int64   `json:"asOf,omitempty"`
}
type Info struct {
	Directory         string   `json:"directory"`
	RuntimeAvailable  bool     `json:"runtimeAvailable"`
	RuntimeError      string   `json:"runtimeError,omitempty"`
	Error             string   `json:"error,omitempty"`
	Jobs              []Job    `json:"jobs"`
	DownloadedSymbols []string `json:"downloadedSymbols"`
	Concurrency       int      `json:"concurrency"`
	MaxConcurrency    int      `json:"maxConcurrency"`
	Active            bool     `json:"active"`
}

// Manager runs one selected batch at a time through a bounded symbol worker
// pool while preserving its jobs in selection order. Each child has a process
// group/tree, bounded logs and a private temporary directory. The supported WAF
// session cache is shared by the batch and retained in the local profile.
// Preferences belong to the local profile, never imported workspaces.
type Manager struct {
	mu                      sync.Mutex
	wg                      sync.WaitGroup
	profile, directory      string
	runtime                 workerRuntime
	runtimeError, lastError string
	jobs                    []Job
	active, closed          bool
	cancel                  context.CancelFunc
	batchDone               chan struct{}
	concurrency             int
	batchDirectory          string
	publication             map[string]string
	stateError, viewError   string
	invalidDownloads        bool
	view                    ViewState
	enrichmentJobs          []EnrichmentJob
	enrichmentRequest       EnrichmentRequest
	enrichmentActive        bool
	enrichmentCancel        context.CancelFunc
	enrichmentDone          chan struct{}
	refreshJobs             []RefreshJob
	refreshActive           bool
	refreshAsOf             int64
	refreshCancel           context.CancelFunc
	refreshDone             chan struct{}
	analysis                *analysisState
}

func New(profile string) *Manager {
	return newManager(profile, discoverRuntime)
}
func newManager(profile string, discover func() (workerRuntime, error)) *Manager {
	absolute, err := filepath.Abs(profile)
	if err == nil {
		profile = absolute
	}
	m := &Manager{profile: profile, directory: filepath.Join(profile, "screener"), jobs: []Job{}, concurrency: MaxConcurrency}
	if err := m.loadPreferences(); err != nil {
		m.lastError = "Load screener settings: " + err.Error()
	}
	if err := m.loadView(); err != nil {
		m.viewError = "Restore Screener selection: " + err.Error()
	}
	if err := recoverHistoryPublications(m.directory); err != nil {
		m.lastError = "Recover Screener history: " + err.Error()
	}
	if err := m.loadDownloads(); err != nil {
		m.stateError, m.invalidDownloads = "Restore download queue: "+err.Error(), true
	}
	m.runtime, err = discover()
	if err != nil {
		m.runtimeError = err.Error()
	}
	if !m.invalidDownloads && len(m.jobs) > 0 {
		m.mu.Lock()
		if m.saveDownloadsLocked() {
			for _, job := range m.jobs {
				if job.Status == "completed" || job.Status == "cancelled" {
					m.cleanupJobAt(m.batchDirectory, job)
				}
			}
			if m.runtimeError == "" && m.pendingLocked() {
				m.beginLocked()
			}
		}
		m.mu.Unlock()
	}
	return m
}

func (m *Manager) infoLocked() Info {
	jobs := make([]Job, len(m.jobs))
	copy(jobs, m.jobs)
	symbols, err := downloadedSymbols(m.directory)
	fileError := ""
	if err != nil {
		fileError = err.Error()
	}
	return Info{Directory: m.directory, RuntimeAvailable: m.runtimeError == "", RuntimeError: m.runtimeError, Error: strings.TrimSpace(strings.Join([]string{m.lastError, m.stateError, m.viewError, fileError}, " ")), Jobs: jobs, DownloadedSymbols: symbols, Concurrency: m.concurrency, MaxConcurrency: MaxConcurrency, Active: m.active}
}
func (m *Manager) Info() Info { m.mu.Lock(); defer m.mu.Unlock(); return m.infoLocked() }

func normalizeDirectory(directory string) (string, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" || strings.ContainsRune(directory, 0) || !filepath.IsAbs(directory) {
		return "", errors.New("screener directory must be an absolute folder path")
	}
	return filepath.Clean(directory), nil
}

func (m *Manager) ConfigureDirectory(directory string) (Info, error) {
	if strings.TrimSpace(directory) == "" {
		directory = filepath.Join(m.profile, "screener")
	}
	directory, err := normalizeDirectory(directory)
	if err != nil {
		return m.Info(), err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.infoLocked(), errors.New("screener is closed")
	}
	if m.active || m.pendingLocked() {
		return m.infoLocked(), errors.New("cancel or finish the current downloads before changing their folder")
	}
	if m.enrichmentActive {
		return m.infoLocked(), errors.New("cancel or finish enrichment before changing the Screener folder")
	}
	if m.refreshActive {
		return m.infoLocked(), errors.New("cancel or finish the data refresh before changing the Screener folder")
	}
	if m.analysisActiveLocked() {
		return m.infoLocked(), errors.New("cancel or finish analysis before changing the Screener folder")
	}
	if err = checkDirectory(directory); err != nil {
		return m.infoLocked(), err
	}
	if err = recoverHistoryPublications(directory); err != nil {
		return m.infoLocked(), err
	}
	if err = m.savePreferences(directory); err != nil {
		return m.infoLocked(), fmt.Errorf("save screener settings: %w", err)
	}
	if directory != m.directory {
		m.cleanupAnalysisLocked()
		m.enrichmentJobs, m.enrichmentRequest = nil, EnrichmentRequest{}
		m.refreshJobs = nil
	}
	m.directory, m.lastError = directory, ""
	return m.infoLocked(), nil
}

func checkDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create screener folder: %w", err)
	}
	f, err := os.CreateTemp(directory, ".cryptodesk-write-check-*")
	if err != nil {
		return fmt.Errorf("screener folder is not writable: %w", err)
	}
	name := f.Name()
	err = f.Close()
	removeErr := os.Remove(name)
	return errors.Join(err, removeErr)
}

func validateRequest(request Request, now time.Time) error {
	if request.Concurrency < 0 || request.Concurrency > MaxConcurrency {
		return fmt.Errorf("download concurrency must be between 1 and %d (or omitted for the default)", MaxConcurrency)
	}
	catalog := loadCatalog()
	known := make(map[string]bool)
	for _, group := range catalog.Groups {
		for _, instrument := range group.Instruments {
			known[instrument.ID] = true
		}
	}
	if len(request.Symbols) == 0 || len(request.Symbols) > len(known) {
		return errors.New("select at least one supported instrument")
	}
	selected := make(map[string]bool)
	for _, symbol := range request.Symbols {
		if !known[symbol] {
			return fmt.Errorf("unsupported Dukascopy instrument %q", symbol)
		}
		if selected[symbol] {
			return fmt.Errorf("duplicate Dukascopy instrument %q", symbol)
		}
		selected[symbol] = true
	}
	supported := false
	for _, timeframe := range catalog.Timeframes {
		if request.Timeframe == timeframe {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("unsupported Dukascopy timeframe %q", request.Timeframe)
	}
	from, err := time.Parse("2006-01-02", request.From)
	if err != nil || from.Format("2006-01-02") != request.From {
		return errors.New("from must be a valid UTC date in YYYY-MM-DD format")
	}
	to, err := time.Parse("2006-01-02", request.To)
	if err != nil || to.Format("2006-01-02") != request.To {
		return errors.New("to must be a valid UTC date in YYYY-MM-DD format")
	}
	tomorrow := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	if from.Year() < 1900 || !from.Before(to) || to.After(tomorrow) {
		return errors.New("date range must start in 1900 or later, end after its start, and not extend beyond tomorrow UTC; to is exclusive")
	}
	return nil
}

func (m *Manager) Start(request Request) (Info, error) {
	if err := validateRequest(request, time.Now()); err != nil {
		return m.Info(), err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.infoLocked(), errors.New("screener is closed")
	}
	if m.active {
		return m.infoLocked(), errors.New("a screener download batch is already running")
	}
	if m.refreshActive {
		return m.infoLocked(), errors.New("finish or cancel the data refresh before starting downloads")
	}
	if m.enrichmentActive || m.analysisActiveLocked() {
		return m.infoLocked(), errors.New("finish or cancel enrichment and Analysis before updating downloaded files")
	}
	if m.invalidDownloads || m.pendingLocked() {
		return m.infoLocked(), errors.New("resume or cancel the saved download queue before starting another batch: " + m.stateError)
	}
	if m.runtimeError != "" {
		return m.infoLocked(), errors.New(m.runtimeError)
	}
	if err := checkDirectory(m.directory); err != nil {
		return m.infoLocked(), err
	}
	if err := recoverHistoryPublications(m.directory); err != nil {
		return m.infoLocked(), err
	}
	jobs := make([]Job, len(request.Symbols))
	asOf := time.Now().UnixMilli()
	for i, symbol := range request.Symbols {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return m.infoLocked(), err
		}
		jobs[i] = Job{ID: hex.EncodeToString(id[:]), Symbol: symbol, Timeframe: request.Timeframe, From: request.From, To: request.To, AsOf: asOf, Status: "queued", Message: "Waiting for download"}
	}
	oldJobs, oldDirectory, oldConcurrency, oldPublication := m.jobs, m.batchDirectory, m.concurrency, m.publication
	m.jobs, m.batchDirectory, m.publication = jobs, m.directory, map[string]string{}
	m.concurrency = request.Concurrency
	if m.concurrency == 0 {
		m.concurrency = MaxConcurrency
	}
	if !m.saveDownloadsLocked() {
		m.jobs, m.batchDirectory, m.concurrency, m.publication = oldJobs, oldDirectory, oldConcurrency, oldPublication
		return m.infoLocked(), errors.New(m.stateError)
	}
	// A replacement batch discards only this application's old failed scratch
	// files, after its new queue has committed. Completed CSVs are separate links.
	for _, old := range oldJobs {
		m.cleanupJobAt(oldDirectory, old)
	}
	m.beginLocked()
	return m.infoLocked(), nil
}

func (m *Manager) cancelLocked() {
	if m.cancel != nil {
		m.cancel()
	}
	for i := range m.jobs {
		if m.jobs[i].Status == "queued" || m.jobs[i].Status == "running" {
			m.jobs[i].Status, m.jobs[i].Message = "cancelled", "Download cancelled"
		}
	}
}
func (m *Manager) Cancel() Info {
	m.mu.Lock()
	m.cancelLocked()
	m.saveDownloadsLocked()
	done := m.batchDone
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	m.mu.Lock()
	if m.saveDownloadsLocked() {
		for _, job := range m.jobs {
			if job.Status == "cancelled" {
				m.cleanupJobAt(m.batchDirectory, job)
			}
		}
	}
	m.mu.Unlock()
	return m.Info()
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
	if m.analysis != nil && m.analysis.cancel != nil {
		m.analysis.cancel()
	}
	if m.enrichmentCancel != nil {
		m.enrichmentCancel()
	}
	if m.cancel != nil {
		m.cancel()
	}
	for i := range m.jobs {
		if m.jobs[i].Status == "running" || m.jobs[i].Status == "queued" {
			m.jobs[i].Status, m.jobs[i].Message = "queued", "Paused; resumes when CryptoDesk starts"
		}
	}
	m.saveDownloadsLocked()
	m.mu.Unlock()
	m.wg.Wait()
	m.mu.Lock()
	m.cleanupAnalysisLocked()
	m.saveDownloadsLocked()
	m.mu.Unlock()
}

func (m *Manager) run(ctx context.Context, directory string, done chan struct{}, workers int) {
	defer m.wg.Done()
	defer func() { m.mu.Lock(); m.active, m.cancel = false, nil; close(done); m.mu.Unlock() }()
	ctx, cleanup, err := withProviderSession(ctx, m.profile)
	if err != nil {
		m.mu.Lock()
		for i := range m.jobs {
			if m.jobs[i].Status == "queued" {
				m.jobs[i].Status, m.jobs[i].Message = "failed", "Prepare Dukascopy session: "+cleanMessage(err.Error())
			}
		}
		m.saveDownloadsLocked()
		m.mu.Unlock()
		return
	}
	defer cleanup()
	// The batch's aggregate upstream request budget stays at twenty, including
	// the library's per-symbol fetching parallelism. A single symbol retains its
	// existing four-file bound; twenty symbols fetch one source file each.
	sourceBatchSize := min(4, MaxConcurrency/workers)
	next := 0
	var pool sync.WaitGroup
	pool.Add(workers)
	for range workers {
		go func() {
			defer pool.Done()
			for {
				m.mu.Lock()
				if next >= len(m.jobs) || ctx.Err() != nil {
					m.mu.Unlock()
					return
				}
				i := next
				next++
				if m.jobs[i].Status != "queued" {
					m.mu.Unlock()
					continue
				}
				m.jobs[i].Status, m.jobs[i].Message = "running", "Starting Dukascopy downloader"
				job := m.jobs[i]
				m.mu.Unlock()
				output, message, err := m.download(ctx, i, job, directory, sourceBatchSize)
				m.mu.Lock()
				if m.jobs[i].Status == "completed" {
					// Publication already committed atomically with its completed status.
				} else if ctx.Err() != nil {
					if m.jobs[i].Status != "cancelled" {
						m.jobs[i].Status, m.jobs[i].Message = "queued", "Paused; resumes from its saved checkpoint"
					}
				} else if err != nil {
					m.jobs[i].Status, m.jobs[i].Message = "failed", cleanMessage(err.Error())
				} else {
					m.jobs[i].Status, m.jobs[i].Progress, m.jobs[i].Message, m.jobs[i].OutputPath = "completed", 100, message, output
				}
				saved := m.saveDownloadsLocked()
				terminal := m.jobs[i].Status == "completed" || m.jobs[i].Status == "cancelled"
				if m.jobs[i].Status == "failed" {
					if _, checkpointErr := os.Stat(filepath.Join(m.jobDirectory(job), "checkpoint.json")); errors.Is(checkpointErr, os.ErrNotExist) {
						// Existing histories keep one checkpoint per missing range.
						_, rangesErr := os.Stat(filepath.Join(m.jobDirectory(job), "ranges.json"))
						terminal = errors.Is(rangesErr, os.ErrNotExist)
					}
				}
				m.mu.Unlock()
				if saved && terminal {
					m.cleanupJobAt(directory, job)
				}
			}
		}()
	}
	pool.Wait()
}

type workerEvent struct {
	providerEvent
	Type           string              `json:"type"`
	Progress       float64             `json:"progress"`
	Message        string              `json:"message"`
	Rows           int64               `json:"rows"`
	FirstTimestamp int64               `json:"firstTimestamp"`
	LastTimestamp  int64               `json:"lastTimestamp"`
	Checksum       string              `json:"checksum"`
	CheckedThrough int64               `json:"checkedThrough"`
	Coverage       []dataRange         `json:"coverage"`
	Recipes        []EnrichmentRequest `json:"recipes"`
	NoChange       bool                `json:"noChange"`
}

func (m *Manager) download(ctx context.Context, index int, job Job, directory string, sourceBatchSize int) (string, string, error) {
	// Each worker has private WAF/session files. Removing this exclusively-owned
	// directory also cleans atomic token-cache scratch files after cancellation.
	jobDirectory, err := m.prepareJob(job)
	if err != nil {
		return "", "", err
	}
	plan, originals, err := prepareDownloadPlan(job, directory, jobDirectory)
	if err != nil {
		return "", "", err
	}
	partial := filepath.Join(jobDirectory, "data.part")
	if info, e := os.Lstat(partial); e == nil && !info.Mode().IsRegular() {
		return "", "", errors.New("saved download partial must be a regular file")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return "", "", e
	}
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", "", err
	}
	if err = f.Close(); err != nil {
		return "", "", err
	}
	// JSON tags in this internal protocol are lowercase; no shell ever interprets
	// symbols, dates, executable paths or destination directories.
	input, _ := json.Marshal(struct {
		Symbol          string           `json:"symbol"`
		Timeframe       string           `json:"timeframe"`
		From            string           `json:"from"`
		To              string           `json:"to"`
		Output          string           `json:"output"`
		SourceBatchSize int              `json:"sourceBatchSize"`
		Resume          bool             `json:"resume"`
		OwnerPipe       bool             `json:"ownerPipe"`
		PlanFile        string           `json:"planFile,omitempty"`
		AsOf            int64            `json:"asOf"`
		ProviderSession *providerSession `json:"providerSession,omitempty"`
	}{job.Symbol, job.Timeframe, job.From, job.To, partial, sourceBatchSize, true, true, plan.workerFile(jobDirectory), plan.AsOf, sessionFromContext(ctx)})
	cmd := exec.CommandContext(ctx, m.runtime.node, m.runtime.worker)
	cmd.Dir = filepath.Dir(m.runtime.worker)
	owner, err := cmd.StdinPipe()
	if err != nil {
		return "", "", err
	}
	defer owner.Close()
	cmd.WaitDelay = 2 * time.Second
	configureProcess(cmd)
	var stderr tailBuffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	if err = cmd.Start(); err != nil {
		return "", "", fmt.Errorf("start downloader: %w", err)
	}
	if _, err = owner.Write(append(input, '\n')); err != nil {
		_ = cmd.Cancel()
		_ = cmd.Wait()
		return "", "", err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var done bool
	var result workerEvent
	var finalMessage, workerError string
	replies := newProviderReplies(ctx, owner, func(message string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.jobs[index].Status == "running" {
			m.jobs[index].Message = message
		}
	})
	defer replies.close()
	for scanner.Scan() {
		var event workerEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			workerError = "Downloader returned invalid progress data"
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
			if math.IsNaN(event.Progress) || math.IsInf(event.Progress, 0) || event.Progress < 0 || event.Progress > 100 {
				continue
			}
			m.mu.Lock()
			if m.jobs[index].Status == "running" {
				m.jobs[index].Progress = math.Max(m.jobs[index].Progress, math.Min(event.Progress, 99))
				m.jobs[index].Message = cleanMessage(event.Message)
			}
			m.mu.Unlock()
		case "done":
			done, finalMessage = event.Rows > 0, cleanMessage(event.Message)
			result = event
		case "error":
			workerError = cleanMessage(event.Message)
			_ = cmd.Cancel()
		default:
			workerError = "Downloader returned an unknown progress event"
			_ = cmd.Cancel()
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		workerError = "Read downloader progress: " + scanErr.Error()
		_ = cmd.Cancel()
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	if workerError != "" {
		return "", "", errors.New(workerError)
	}
	if err != nil {
		detail := cleanMessage(stderr.String())
		if detail != "" {
			return "", "", fmt.Errorf("downloader failed: %s", detail)
		}
		return "", "", fmt.Errorf("downloader failed: %w", err)
	}
	if !done {
		return "", "", errors.New("downloader exited without confirming any downloaded rows")
	}
	if len(plan.Inputs) > 0 {
		return m.publishExtendedDownload(ctx, index, job, directory, jobDirectory, partial, plan, originals, result)
	}
	if st, err := os.Stat(partial); err != nil || st.Size() == 0 {
		return "", "", errors.New("downloader produced an empty file")
	}
	// Holding the lock makes publication and completion indivisible with Cancel.
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	name := fmt.Sprintf("%s-%s-%s-%s.csv", job.Symbol, job.Timeframe, job.From, job.To)
	// A source introduced after the plan was frozen must not cause a second
	// history. Retry the request with a new plan instead of inventing a suffix.
	names, err := csvNames(directory)
	if err != nil {
		return "", "", err
	}
	for _, existing := range names {
		match := downloadFilename.FindStringSubmatch(strings.ToLower(existing))
		if match != nil && match[1] == job.Symbol && match[2] == job.Timeframe {
			return "", "", errors.New("another history appeared while downloading; retry to merge its data")
		}
	}
	output := filepath.Join(directory, name)
	m.publication[job.ID] = name
	if !m.saveDownloadsLocked() {
		return "", "", errors.New(m.stateError)
	}
	if err = os.Link(partial, output); err != nil {
		return "", "", fmt.Errorf("publish downloaded CSV without overwriting existing data: %w", err)
	}
	if result.Checksum != "" {
		if metadataErr := writeDownloadedMetadata(directory, name, partial, result); metadataErr != nil {
			finalMessage += "; coverage metadata was not saved: " + cleanMessage(metadataErr.Error())
		}
	}
	m.jobs[index].Status, m.jobs[index].Progress, m.jobs[index].Message, m.jobs[index].OutputPath = "completed", 100, finalMessage, output
	m.saveDownloadsLocked()
	return output, finalMessage, nil
}

type tailBuffer struct{ data []byte }

func (b *tailBuffer) Write(data []byte) (int, error) {
	n := len(data)
	if len(data) > 4096 {
		data = data[len(data)-4096:]
	}
	b.data = append(b.data, data...)
	if len(b.data) > 4096 {
		b.data = b.data[len(b.data)-4096:]
	}
	return n, nil
}
func (b *tailBuffer) String() string { return string(b.data) }
func cleanMessage(message string) string {
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, message)
	runes := []rune(strings.TrimSpace(message))
	if len(runes) > 1000 {
		runes = runes[:1000]
	}
	return string(runes)
}

func (m *Manager) loadPreferences() error {
	data, err := os.ReadFile(filepath.Join(m.profile, "screener.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) > 16384 {
		return errors.New("screener settings are too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var prefs struct {
		Directory string `json:"directory"`
	}
	if err = decoder.Decode(&prefs); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected trailing screener settings")
	}
	directory, err := normalizeDirectory(prefs.Directory)
	if err != nil {
		return err
	}
	m.directory = directory
	return nil
}
func (m *Manager) savePreferences(directory string) error {
	if err := os.MkdirAll(m.profile, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Directory string `json:"directory"`
	}{directory})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(m.profile, ".screener-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(m.profile, "screener.json")); err != nil {
		return err
	}
	if parent, err := os.Open(m.profile); err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return nil
}
