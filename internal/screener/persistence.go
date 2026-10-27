package screener

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ViewState is machine-local UI state, deliberately separate from chart exports.
type ViewState struct {
	Symbols             []string `json:"symbols"`
	Timeframe           string   `json:"timeframe"`
	From                string   `json:"from"`
	To                  string   `json:"to"`
	Concurrency         int      `json:"concurrency"`
	CollapsedCategories []string `json:"collapsedCategories"`
}

func defaultView() ViewState {
	return ViewState{Symbols: []string{}, Timeframe: "d1", From: "2010-01-01", To: time.Now().UTC().Format("2006-01-02"), Concurrency: MaxConcurrency, CollapsedCategories: []string{}}
}
func copyView(v ViewState) ViewState {
	v.Symbols = append([]string{}, v.Symbols...)
	v.CollapsedCategories = append([]string{}, v.CollapsedCategories...)
	return v
}
func validateView(v ViewState) error {
	catalog := loadCatalog()
	ids, groups := map[string]bool{}, map[string]bool{}
	for _, group := range catalog.Groups {
		groups[group.Name] = true
		for _, instrument := range group.Instruments {
			ids[instrument.ID] = true
		}
	}
	if len(v.Symbols) > len(ids) || len(v.CollapsedCategories) > len(groups) {
		return errors.New("too many saved Screener selections")
	}
	for _, id := range v.Symbols {
		if !ids[id] {
			return fmt.Errorf("unknown or duplicate saved instrument %q", id)
		}
		delete(ids, id)
	}
	for _, group := range v.CollapsedCategories {
		if !groups[group] {
			return fmt.Errorf("unknown or duplicate saved category %q", group)
		}
		delete(groups, group)
	}
	validTimeframe := false
	for _, tf := range catalog.Timeframes {
		if tf == v.Timeframe {
			validTimeframe = true
		}
	}
	if !validTimeframe || v.Concurrency < 1 || v.Concurrency > MaxConcurrency {
		return errors.New("invalid saved timeframe or concurrency")
	}
	// A date field may temporarily be empty or reversed while the user edits it.
	for _, value := range []string{v.From, v.To} {
		if value == "" {
			continue
		}
		date, err := time.Parse("2006-01-02", value)
		if err != nil || date.Year() < 1900 || date.Format("2006-01-02") != value {
			return errors.New("invalid saved UTC date")
		}
	}
	return nil
}

type savedView struct {
	Version int       `json:"version"`
	View    ViewState `json:"view"`
}
type savedDownloads struct {
	Version     int               `json:"version"`
	Directory   string            `json:"directory"`
	Concurrency int               `json:"concurrency"`
	Jobs        []Job             `json:"jobs"`
	Publication map[string]string `json:"publication,omitempty"`
}

func readState(path string, limit int64, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("saved Screener state exceeds its size limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(value); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("unexpected trailing saved Screener data")
	}
	return nil
}

func writeState(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".screener-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if parent, e := os.Open(filepath.Dir(path)); e == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return nil
}

func (m *Manager) loadView() error {
	m.view = defaultView()
	var saved savedView
	err := readState(filepath.Join(m.profile, "screener-view.json"), 256*1024, &saved)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if saved.Version != 1 {
		return errors.New("unsupported Screener view version; original file preserved")
	}
	if err = validateView(saved.View); err != nil {
		return err
	}
	m.view = copyView(saved.View)
	return nil
}
func (m *Manager) View() (ViewState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.viewError != "" {
		return ViewState{}, errors.New(m.viewError)
	}
	return copyView(m.view), nil
}
func (m *Manager) SaveView(view ViewState) (ViewState, error) {
	if err := validateView(view); err != nil {
		return ViewState{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ViewState{}, errors.New("screener is closed")
	}
	if err := writeState(filepath.Join(m.profile, "screener-view.json"), savedView{1, view}); err != nil {
		return ViewState{}, fmt.Errorf("save Screener selection: %w", err)
	}
	m.view, m.viewError = copyView(view), ""
	return copyView(m.view), nil
}

func (m *Manager) persistDownloadsLocked() error {
	if m.invalidDownloads {
		return errors.New(m.stateError)
	}
	if len(m.jobs) == 0 {
		return nil
	}
	return writeState(filepath.Join(m.profile, "screener-downloads.json"), savedDownloads{1, m.batchDirectory, m.concurrency, m.jobs, m.publication})
}
func (m *Manager) saveDownloadsLocked() bool {
	if m.invalidDownloads {
		return false
	}
	if err := m.persistDownloadsLocked(); err != nil {
		m.stateError = "Download queue could not be saved; unfinished work is paused: " + err.Error()
		if m.cancel != nil {
			m.cancel()
		}
		return false
	}
	m.stateError = ""
	return true
}

func (m *Manager) loadDownloads() error {
	var saved savedDownloads
	err := readState(filepath.Join(m.profile, "screener-downloads.json"), 16*1024*1024, &saved)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if saved.Version != 1 {
		return errors.New("unsupported download queue version; original file preserved")
	}
	directory, err := normalizeDirectory(saved.Directory)
	if err != nil || directory != saved.Directory {
		return errors.New("invalid saved download directory")
	}
	if len(saved.Jobs) == 0 || len(saved.Jobs) > 1499 || saved.Concurrency < 1 || saved.Concurrency > MaxConcurrency {
		return errors.New("invalid saved download queue limits")
	}
	request := Request{Concurrency: saved.Concurrency}
	ids := map[string]bool{}
	for _, job := range saved.Jobs {
		id, e := hex.DecodeString(job.ID)
		if e != nil || len(id) != 16 || ids[job.ID] || len(job.Message) > 4000 || math.IsNaN(job.Progress) || math.IsInf(job.Progress, 0) || job.Progress < 0 || job.Progress > 100 || job.AsOf < 0 || job.AsOf > time.Now().Add(time.Minute).UnixMilli() {
			return errors.New("invalid saved download job")
		}
		ids[job.ID] = true
		if request.Timeframe == "" {
			request.Timeframe, request.From, request.To = job.Timeframe, job.From, job.To
		}
		if job.Timeframe != request.Timeframe || job.From != request.From || job.To != request.To {
			return errors.New("saved jobs do not belong to one batch")
		}
		request.Symbols = append(request.Symbols, job.Symbol)
		switch job.Status {
		case "queued", "running", "completed", "failed", "cancelled":
		default:
			return errors.New("invalid saved job status")
		}
		if job.OutputPath != "" && (filepath.Dir(job.OutputPath) != directory || !strings.HasPrefix(strings.ToLower(filepath.Base(job.OutputPath)), job.Symbol+"-")) {
			return errors.New("invalid saved output path")
		}
	}
	if err = validateRequest(request, time.Now()); err != nil {
		return err
	}
	for id, name := range saved.Publication {
		if !ids[id] || filepath.Base(name) != name || name == "." || name == ".." || !strings.EqualFold(filepath.Ext(name), ".csv") {
			return errors.New("invalid saved publication name")
		}
	}
	if err := recoverHistoryPublications(directory); err != nil {
		return fmt.Errorf("recover published histories: %w", err)
	}
	m.jobs, m.concurrency, m.batchDirectory, m.publication = saved.Jobs, saved.Concurrency, directory, saved.Publication
	if m.publication == nil {
		m.publication = map[string]string{}
	}
	m.restorePublishedDownloads()
	for i := range m.jobs {
		job := &m.jobs[i]
		if job.Status == "running" || job.Status == "queued" {
			job.Status, job.Progress, job.Message = "queued", 0, "Resuming saved download checkpoint"
		}
	}
	return m.reconcileCompletedHistoryPaths(directory)
}

// Called after the durable publisher has finished any interrupted transaction.
// The retained private partial proves this is the exact result this queue job
// published, even if its prior status was failed after a cleanup error.
func (m *Manager) restorePublishedDownloads() {
	for i := range m.jobs {
		job := &m.jobs[i]
		if name := m.publication[job.ID]; name != "" && job.Status != "cancelled" {
			partial, e1 := os.Lstat(filepath.Join(m.jobDirectory(*job), "data.part"))
			output, e2 := os.Lstat(filepath.Join(m.batchDirectory, name))
			if e1 == nil && e2 == nil && partial.Mode().IsRegular() && output.Mode().IsRegular() && os.SameFile(partial, output) {
				job.Status, job.Progress, job.OutputPath, job.Message = "completed", 100, filepath.Join(m.batchDirectory, name), "Downloaded CSV restored"
			}
		}
	}
}

// Refresh may publish and remove its old range filename just before the app
// exits, without getting to update a previously completed Downloader entry.
// Repair only an absent, recognized old path with one unambiguous same-asset
// history. Existing files, symlinks, failed jobs and multiple ranges are left
// alone; neither publication recovery nor this lookup downloads anything.
func (m *Manager) reconcileCompletedHistoryPaths(directory string) error {
	missing := []int{}
	for index, job := range m.jobs {
		if job.Status != "completed" || job.OutputPath == "" {
			continue
		}
		match := downloadFilename.FindStringSubmatch(strings.ToLower(filepath.Base(job.OutputPath)))
		if match == nil || match[1] != job.Symbol || match[2] != job.Timeframe {
			continue
		}
		if _, err := os.Lstat(job.OutputPath); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, index)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	names, err := csvNames(directory)
	if err != nil {
		return fmt.Errorf("find renamed completed histories: %w", err)
	}
	byAsset := map[string][]string{}
	for _, name := range names {
		if match := downloadFilename.FindStringSubmatch(strings.ToLower(name)); match != nil {
			key := match[1] + "/" + match[2]
			byAsset[key] = append(byAsset[key], name)
		}
	}
	for _, index := range missing {
		job := &m.jobs[index]
		matches := byAsset[job.Symbol+"/"+job.Timeframe]
		if len(matches) == 1 {
			job.OutputPath = filepath.Join(directory, matches[0])
			m.publication[job.ID] = matches[0]
		}
	}
	return nil
}

func (m *Manager) jobDirectory(job Job) string {
	return filepath.Join(m.batchDirectory, ".cryptodesk-screener-job-"+job.ID)
}
func (m *Manager) pendingLocked() bool {
	for _, job := range m.jobs {
		if job.Status == "queued" || job.Status == "running" {
			return true
		}
	}
	return false
}

func (m *Manager) beginLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	m.active, m.cancel = true, cancel
	done := make(chan struct{})
	m.batchDone = done
	m.wg.Add(1)
	pending := 0
	for _, job := range m.jobs {
		if job.Status == "queued" {
			pending++
		}
	}
	go m.run(ctx, m.batchDirectory, done, min(m.concurrency, pending))
}

// Resume retries saved failed/paused jobs explicitly. Cancelled jobs stay cancelled.
func (m *Manager) Resume() (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.active {
		return m.infoLocked(), errors.New("downloads are closed or already active")
	}
	if m.refreshActive {
		return m.infoLocked(), errors.New("finish or cancel the data refresh before resuming downloads")
	}
	if m.enrichmentActive || m.analysisActiveLocked() {
		return m.infoLocked(), errors.New("finish or cancel enrichment and Analysis before resuming downloads")
	}
	if m.invalidDownloads {
		return m.infoLocked(), errors.New(m.stateError)
	}
	if m.runtimeError != "" {
		return m.infoLocked(), errors.New(m.runtimeError)
	}
	before := append([]Job(nil), m.jobs...)
	if m.batchDirectory != "" {
		if err := recoverHistoryPublications(m.batchDirectory); err != nil {
			return m.infoLocked(), fmt.Errorf("recover published histories before retry: %w", err)
		}
		m.restorePublishedDownloads()
		if err := m.reconcileCompletedHistoryPaths(m.batchDirectory); err != nil {
			return m.infoLocked(), err
		}
	}
	for i := range m.jobs {
		if m.jobs[i].Status == "failed" {
			m.jobs[i].Status, m.jobs[i].Message = "queued", "Retrying saved checkpoint"
		}
	}
	if !m.saveDownloadsLocked() {
		m.jobs = before
		return m.infoLocked(), errors.New(m.stateError)
	}
	for _, job := range m.jobs {
		if job.Status == "completed" {
			m.cleanupJobAt(m.batchDirectory, job)
		}
	}
	if !m.pendingLocked() {
		return m.infoLocked(), nil
	}
	m.beginLocked()
	return m.infoLocked(), nil
}
