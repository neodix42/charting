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
	Symbols   []string `json:"symbols"`
	Timeframe string   `json:"timeframe"`
	From      string   `json:"from"`
	To        string   `json:"to"`
}
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
}
type Info struct {
	Directory        string `json:"directory"`
	RuntimeAvailable bool   `json:"runtimeAvailable"`
	RuntimeError     string `json:"runtimeError,omitempty"`
	Error            string `json:"error,omitempty"`
	Jobs             []Job  `json:"jobs"`
}

// Manager serializes one selected batch at a time and downloads its symbols in
// order. Each child has a process group/tree, bounded logs and private temporary
// output. Preferences belong to the local profile, never imported workspaces.
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
}

func New(profile string) *Manager {
	absolute, err := filepath.Abs(profile)
	if err == nil {
		profile = absolute
	}
	m := &Manager{profile: profile, directory: filepath.Join(profile, "screener"), jobs: []Job{}}
	if err := m.loadPreferences(); err != nil {
		m.lastError = "Load screener settings: " + err.Error()
	}
	m.runtime, err = discoverRuntime()
	if err != nil {
		m.runtimeError = err.Error()
	}
	return m
}

func (m *Manager) infoLocked() Info {
	jobs := make([]Job, len(m.jobs))
	copy(jobs, m.jobs)
	return Info{Directory: m.directory, RuntimeAvailable: m.runtimeError == "", RuntimeError: m.runtimeError, Error: m.lastError, Jobs: jobs}
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
	if m.active {
		return m.infoLocked(), errors.New("cancel or finish the current downloads before changing their folder")
	}
	if err = checkDirectory(directory); err != nil {
		return m.infoLocked(), err
	}
	if err = m.savePreferences(directory); err != nil {
		return m.infoLocked(), fmt.Errorf("save screener settings: %w", err)
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
	if m.runtimeError != "" {
		return m.infoLocked(), errors.New(m.runtimeError)
	}
	if err := checkDirectory(m.directory); err != nil {
		return m.infoLocked(), err
	}
	jobs := make([]Job, len(request.Symbols))
	for i, symbol := range request.Symbols {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return m.infoLocked(), err
		}
		jobs[i] = Job{ID: hex.EncodeToString(id[:]), Symbol: symbol, Timeframe: request.Timeframe, From: request.From, To: request.To, Status: "queued", Message: "Waiting for download"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.jobs, m.active, m.cancel = jobs, true, cancel
	done := make(chan struct{})
	m.batchDone = done
	directory := m.directory
	m.wg.Add(1)
	go m.run(ctx, directory, done)
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
	done := m.batchDone
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	return m.Info()
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.cancelLocked()
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Manager) run(ctx context.Context, directory string, done chan struct{}) {
	defer m.wg.Done()
	defer func() { m.mu.Lock(); m.active, m.cancel = false, nil; close(done); m.mu.Unlock() }()
	for i := 0; ; i++ {
		m.mu.Lock()
		if i >= len(m.jobs) || ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		m.jobs[i].Status, m.jobs[i].Message = "running", "Starting Dukascopy downloader"
		job := m.jobs[i]
		m.mu.Unlock()
		output, message, err := m.download(ctx, i, job, directory)
		m.mu.Lock()
		if m.jobs[i].Status == "completed" {
			// Publication already committed atomically with its completed status.
		} else if ctx.Err() != nil {
			m.jobs[i].Status, m.jobs[i].Message = "cancelled", "Download cancelled"
		} else if err != nil {
			m.jobs[i].Status, m.jobs[i].Message = "failed", cleanMessage(err.Error())
		} else {
			m.jobs[i].Status, m.jobs[i].Progress, m.jobs[i].Message, m.jobs[i].OutputPath = "completed", 100, message, output
		}
		m.mu.Unlock()
	}
}

type workerEvent struct {
	Type     string  `json:"type"`
	Progress float64 `json:"progress"`
	Message  string  `json:"message"`
	Rows     int64   `json:"rows"`
}

func (m *Manager) download(ctx context.Context, index int, job Job, directory string) (string, string, error) {
	f, err := os.CreateTemp(directory, "."+job.Symbol+"-*.part")
	if err != nil {
		return "", "", err
	}
	partial := f.Name()
	defer os.Remove(partial)
	if err = f.Close(); err != nil {
		return "", "", err
	}
	request := struct{ Symbol, Timeframe, From, To, Output string }{job.Symbol, job.Timeframe, job.From, job.To, partial}
	// JSON tags in this internal protocol are lowercase; no shell ever interprets
	// symbols, dates, executable paths or destination directories.
	input, _ := json.Marshal(map[string]string{"symbol": request.Symbol, "timeframe": request.Timeframe, "from": request.From, "to": request.To, "output": request.Output})
	cmd := exec.CommandContext(ctx, m.runtime.node, m.runtime.worker)
	cmd.Dir = filepath.Dir(m.runtime.worker)
	cmd.Stdin = bytes.NewReader(input)
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
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 16384)
	var done bool
	var finalMessage, workerError string
	for scanner.Scan() {
		var event workerEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			workerError = "Downloader returned invalid progress data"
			_ = cmd.Cancel()
			break
		}
		switch event.Type {
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
		case "error":
			workerError = cleanMessage(event.Message)
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
	if st, err := os.Stat(partial); err != nil || st.Size() == 0 {
		return "", "", errors.New("downloader produced an empty file")
	}
	// Holding the lock makes publication and completion indivisible with Cancel.
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	base := fmt.Sprintf("%s-%s-%s-%s", job.Symbol, job.Timeframe, job.From, job.To)
	for suffix := 0; suffix < 10000; suffix++ {
		name := base
		if suffix > 0 {
			name = fmt.Sprintf("%s-%d", base, suffix+1)
		}
		output := filepath.Join(directory, name+".csv")
		// Link publishes atomically on the same filesystem without replacing an
		// existing name. Repeated downloads receive a fresh numbered filename.
		if err = os.Link(partial, output); err == nil {
			m.jobs[index].Status, m.jobs[index].Progress, m.jobs[index].Message, m.jobs[index].OutputPath = "completed", 100, finalMessage, output
			return output, finalMessage, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", "", fmt.Errorf("publish downloaded CSV without overwriting existing data: %w", err)
		}
	}
	return "", "", errors.New("too many existing downloads for this symbol and date range")
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
