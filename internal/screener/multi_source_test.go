package screener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

type sequentialDownloadAdapter struct {
	hyperliquidDownloadAdapter
	manager        *Manager
	mu             sync.Mutex
	violations     []Job
	acceptFailures bool
}

type sequentialCatalogGate struct {
	sequentialDownloadAdapter
	catalogStarted chan struct{}
	catalogRelease chan struct{}
}

func (a *sequentialCatalogGate) Instruments(ctx context.Context) ([]domain.Instrument, error) {
	close(a.catalogStarted)
	select {
	case <-a.catalogRelease:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return a.hyperliquidDownloadAdapter.Instruments(ctx)
}

func (a *sequentialDownloadAdapter) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	a.manager.mu.Lock()
	for _, job := range a.manager.jobs {
		if normalizeDataSource(job.DataSource) == "dukascopy" && (job.Status == "queued" || job.Status == "running" || !a.acceptFailures && job.Status != "completed") {
			a.mu.Lock()
			a.violations = append(a.violations, job)
			a.mu.Unlock()
		}
	}
	a.manager.mu.Unlock()
	return a.hyperliquidDownloadAdapter.History(ctx, key, from, to, limit)
}
func allSourcesRequest(period string) Request {
	return Request{DataSource: "all", Timeframe: period, From: "2024-01-01", To: "2024-01-15", SourceSelections: []SourceSelection{
		{DataSource: "hyperliquid", Symbols: []string{"hyperliquid-" + publicAlertSymbol("perpetual", "BTC")}},
		{DataSource: "dukascopy", Symbols: []string{"btcusd"}},
	}}
}
func installSequentialAdapter(m *Manager, hold bool) *sequentialDownloadAdapter {
	a := &sequentialDownloadAdapter{manager: m}
	if hold {
		a.hold = make(chan struct{})
		a.started = make(chan struct{}, 1)
	}
	m.hyperliquidAdapter = a
	return a
}
func assertNoHyperliquidCall(t *testing.T, a *sequentialDownloadAdapter) {
	t.Helper()
	a.hyperliquidDownloadAdapter.mu.Lock()
	defer a.hyperliquidDownloadAdapter.mu.Unlock()
	if a.calls != 0 {
		t.Fatalf("Hyperliquid started before Dukascopy finished: %d", a.calls)
	}
}
func assertNoSourceOverlap(t *testing.T, a *sequentialDownloadAdapter) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.violations) > 0 {
		t.Fatalf("later source saw unfinished earlier source: %+v", a.violations)
	}
}

func TestAllSourcesWaitsForEveryDukascopyJobAndPublishesCanonicalOrder(t *testing.T) {
	m, gates := gatedManager(t)
	configureTestConcurrency(t, m, 2)
	a := installSequentialAdapter(m, true)
	r := allSourcesRequest("d1")
	r.SourceSelections[1].Symbols = []string{"btcusd", "ethusd"}
	started, err := m.Start(r)
	if err != nil || !reflect.DeepEqual(started.SourceOrder, []string{"dukascopy", "hyperliquid"}) || started.ActiveDataSource != "dukascopy" {
		t.Fatalf("start: %+v %v", started, err)
	}
	// Input ordering is not allowed to move the public source ahead of Dukascopy.
	if len(started.Jobs) != 3 || started.Jobs[0].DataSource != "dukascopy" || started.Jobs[2].DataSource != "hyperliquid" {
		t.Fatal(started.Jobs)
	}
	waitGate(t, gates, "btcusd")
	waitGate(t, gates, "ethusd")
	assertNoHyperliquidCall(t, a)
	releaseGate(t, gates, "btcusd")
	time.Sleep(20 * time.Millisecond)
	assertNoHyperliquidCall(t, a)
	releaseGate(t, gates, "ethusd")
	select {
	case <-a.started:
	case <-time.After(10 * time.Second):
		t.Fatal("next source did not start")
	}
	if info := m.Info(); info.ActiveDataSource != "hyperliquid" || info.Jobs[0].Status != "completed" || info.Jobs[1].Status != "completed" {
		t.Fatal(info)
	}
	close(a.hold)
	finished := waitHyperliquidDownload(t, m)
	assertNoSourceOverlap(t, a)
	if finished.ActiveDataSource != "" || len(finished.Jobs) != 3 {
		t.Fatal(finished)
	}
	// Returned source metadata is not a mutable alias to the durable plan.
	finished.SourceOrder[0] = "changed"
	if m.Info().SourceOrder[0] != "dukascopy" {
		t.Fatal("Info source order aliased manager")
	}
}

func TestAllSourcesCancellationStopsBothSourcesAndCannotAdvanceOnResume(t *testing.T) {
	m, gates := gatedManager(t)
	a := installSequentialAdapter(m, false)
	if _, err := m.Start(allSourcesRequest("d1")); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	info := m.Cancel()
	assertNoHyperliquidCall(t, a)
	for _, job := range info.Jobs {
		if job.Status != "cancelled" {
			t.Fatal(info.Jobs)
		}
	}
	if info.Active || info.ActiveDataSource != "" {
		t.Fatal(info)
	}
	if _, err := m.Resume(); err != nil {
		t.Fatal(err)
	}
	assertNoHyperliquidCall(t, a)
}

func TestAllSourcesLiveAlertsPauseKeepsLaterSourceQueuedThenResumesSamePlan(t *testing.T) {
	m, gates := gatedManager(t)
	a := installSequentialAdapter(m, false)
	if _, err := m.Start(allSourcesRequest("d1")); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	ids := m.Info().Jobs
	_, release, err := m.reserveAlertPriority(context.Background(), time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	paused := m.Info()
	if !paused.DownloadsPaused || !paused.Active || paused.Jobs[1].Status != "queued" {
		t.Fatal("Live Alerts lost pending source plan", paused)
	}
	assertNoHyperliquidCall(t, a)
	if err := os.Remove(filepath.Join(gates, "btcusd.started")); err != nil {
		t.Fatal(err)
	}
	release()
	waitGate(t, gates, "btcusd")
	assertNoHyperliquidCall(t, a)
	releaseGate(t, gates, "btcusd")
	finished := waitHyperliquidDownload(t, m)
	assertNoSourceOverlap(t, a)
	if finished.DownloadsPaused || finished.Jobs[0].ID != ids[0].ID || finished.Jobs[1].ID != ids[1].ID {
		t.Fatal("Live Alerts resume replaced the batch", finished)
	}
}

func TestAllSourcesPersistenceFailureAtFirstSourceCompletionPreventsAdvancing(t *testing.T) {
	m, gates := gatedManager(t)
	a := installSequentialAdapter(m, false)
	if _, err := m.Start(allSourcesRequest("d1")); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	path := filepath.Join(m.profile, "screener-downloads.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(path); _ = os.WriteFile(path, before, 0600) }()
	releaseGate(t, gates, "btcusd")
	info := waitFinished(t, m)
	assertNoHyperliquidCall(t, a)
	if info.Active || !strings.Contains(info.Error, "could not be saved") || info.Jobs[1].Status != "queued" {
		t.Fatalf("unsafe queue advanced to next source: %+v", info)
	}
}

func TestAllSourcesCompletedDukascopyThenPendingHyperliquidRestoresWithoutDukascopyRuntime(t *testing.T) {
	m := testManager(t)
	installSequentialAdapter(m, false)
	if _, err := m.Start(allSourcesRequest("d1")); err != nil {
		t.Fatal(err)
	}
	before := waitHyperliquidDownload(t, m)
	m.mu.Lock()
	m.jobs[1].Status, m.jobs[1].Progress = "queued", 0
	m.saveDownloadsLocked()
	m.mu.Unlock()
	m.Close()
	// The retained Hyperliquid coverage is complete: construction must resume
	// without either Dukascopy's runtime or any real public history request.
	restored := newManager(m.profile, func() (workerRuntime, error) {
		return workerRuntime{}, errors.New("Dukascopy runtime intentionally absent")
	})
	defer restored.Close()
	after := waitHyperliquidDownload(t, restored)
	if len(after.Jobs) != 2 || after.Jobs[0].ID != before.Jobs[0].ID || after.Jobs[1].ID != before.Jobs[1].ID || after.Jobs[0].OutputPath != before.Jobs[0].OutputPath {
		t.Fatal("mixed checkpoint identities lost", after)
	}
}

func TestAllSourcesFailedDukascopySessionDoesNotEraseHyperliquidJobs(t *testing.T) {
	m := testManager(t)
	a := installSequentialAdapter(m, false)
	a.acceptFailures = true
	if err := os.WriteFile(filepath.Join(m.profile, "screener-provider"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(allSourcesRequest("d1")); err != nil {
		t.Fatal(err)
	}
	info := waitFinished(t, m)
	if len(info.Jobs) != 2 || info.Jobs[0].Status != "failed" || info.Jobs[1].Status != "completed" {
		t.Fatalf("source failure swallowed later work: %+v", info)
	}
	assertNoSourceOverlap(t, a)
}

func TestAllSourcesQueueSaveFailureAndInvalidSelectionsLeavePriorBatchUntouched(t *testing.T) {
	m := testManager(t)
	a := installSequentialAdapter(m, false)
	path := filepath.Join(m.profile, "screener-downloads.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(allSourcesRequest("d1")); err == nil {
		t.Fatal("queue save failure accepted")
	}
	if info := m.Info(); info.Active || len(info.Jobs) > 0 || len(info.SourceOrder) > 0 {
		t.Fatal(info)
	}
	assertNoHyperliquidCall(t, a)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Timeframe = "m1" }, func(r *Request) { r.SourceSelections[0].DataSource = "bybit" }, func(r *Request) { r.SourceSelections[0].DataSource = "dukascopy" }, func(r *Request) { r.SourceSelections[1].Symbols = []string{"unsupported"} }, func(r *Request) { r.Symbols = []string{"btcusd"} }, func(r *Request) { r.SourceSelections[0].Symbols = nil; r.SourceSelections[1].Symbols = nil },
	} {
		r := allSourcesRequest("d1")
		mutate(&r)
		if _, err := m.Start(r); err == nil {
			t.Fatal("invalid All selection accepted", r)
		}
		if info := m.Info(); info.Active || len(info.Jobs) > 0 {
			t.Fatal(info)
		}
	}
}

func TestAllSourcesPauseRestartResumesEarlierSourceBeforeLaterWithoutRepeatingCompleted(t *testing.T) {
	m, gates := gatedManager(t)
	configureTestConcurrency(t, m, 1)
	installSequentialAdapter(m, false)
	r := allSourcesRequest("d1")
	r.SourceSelections[1].Symbols = []string{"btcusd", "ethusd"}
	if _, err := m.Start(r); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	releaseGate(t, gates, "btcusd")
	waitGate(t, gates, "ethusd")
	before := m.Info()
	completed := before.Jobs[0]
	st, err := os.Stat(completed.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	if err := os.Remove(filepath.Join(gates, "ethusd.started")); err != nil {
		t.Fatal(err)
	}
	restored := newManager(m.profile, func() (workerRuntime, error) { return workerRuntime{}, errors.New("offline runtime for preparation") })
	defer restored.Close()
	if restored.Info().Active {
		t.Fatal("missing runtime resumed unfinished Dukascopy")
	}
	restored.runtime, restored.runtimeError = m.runtime, ""
	a := installSequentialAdapter(restored, false)
	if _, err := restored.Resume(); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "ethusd")
	assertNoHyperliquidCall(t, a)
	releaseGate(t, gates, "ethusd")
	info := waitHyperliquidDownload(t, restored)
	assertNoSourceOverlap(t, a)
	after, err := os.Stat(completed.OutputPath)
	if err != nil || !os.SameFile(st, after) || !st.ModTime().Equal(after.ModTime()) || info.Jobs[0].ID != completed.ID || !reflect.DeepEqual(info.SourceOrder, []string{"dukascopy", "hyperliquid"}) {
		t.Fatal("completed first source was repeated or durable source order lost", info, err)
	}
}

func TestAllSourcesWeeklyFinishesDukascopyConversionBeforeHyperliquid(t *testing.T) {
	m, _, _ := weeklyTransportManager(t, false)
	a := installSequentialAdapter(m, false)
	r := allSourcesRequest("w1")
	r.SourceSelections[1].Symbols = []string{"eurusd"}
	if _, err := m.Start(r); err != nil {
		t.Fatal(err)
	}
	info := waitHyperliquidDownload(t, m)
	assertNoSourceOverlap(t, a)
	if len(info.Jobs) != 4 || info.Jobs[0].DataSource != "dukascopy" || info.Jobs[1].Timeframe != "w1" || info.Jobs[2].DataSource != "hyperliquid" || info.Jobs[3].Timeframe != "w1" {
		t.Fatal(info.Jobs)
	}
	assertWeeklyDownloadRows(t, info.Jobs[1].OutputPath)
	// Version2 round-trips the complete source plan without restarting finished work.
	data, err := os.ReadFile(filepath.Join(m.profile, "screener-downloads.json"))
	var saved savedDownloads
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.Version != 2 || len(saved.SourceOrder) != 2 {
		t.Fatal("missing durable sequential marker", string(data), err)
	}
	m.Close()
	restored := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
	defer restored.Close()
	if got := restored.Info(); got.Active || got.Error != "" || len(got.Jobs) != 4 {
		t.Fatal(got)
	}
}

func TestAllSourcesMarkerRejectsInterleavedOrUnmarkedMixedQueues(t *testing.T) {
	for _, mutate := range []func(*savedDownloads){
		func(s *savedDownloads) { s.Version = 1; s.SourceOrder = nil }, func(s *savedDownloads) { s.SourceOrder = []string{"hyperliquid", "dukascopy"} }, func(s *savedDownloads) { s.Jobs = append(s.Jobs, s.Jobs[0]); s.Jobs[2].ID = strings.Repeat("c", 32) }, func(s *savedDownloads) { s.SourceOrder = []string{"dukascopy"} },
		func(s *savedDownloads) { s.Jobs[0].DataSource = "" },
		func(s *savedDownloads) { s.Jobs[0].AsOf++ },
	} {
		profile, directory := t.TempDir(), t.TempDir()
		r := allSourcesRequest("d1")
		jobs := []Job{{ID: strings.Repeat("a", 32), DataSource: "dukascopy", Symbol: "btcusd", Timeframe: "d1", From: r.From, To: r.To, Status: "queued"}, {ID: strings.Repeat("b", 32), DataSource: "hyperliquid", Symbol: r.SourceSelections[0].Symbols[0], NativeID: "BTC", Market: "perpetual", Timeframe: "d1", From: r.From, To: r.To, Status: "queued"}}
		jobs[0].AsOf = time.Now().UnixMilli()
		jobs[1].AsOf = jobs[0].AsOf
		saved := savedDownloads{Version: 2, Directory: directory, Concurrency: 1, Jobs: jobs, SourceOrder: []string{"dukascopy", "hyperliquid"}}
		mutate(&saved)
		path := filepath.Join(profile, "screener-downloads.json")
		if err := writeState(path, saved); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(path)
		m := reopenTestManager(t, profile)
		if info := m.Info(); info.Active || info.Error == "" {
			t.Fatal("malformed mixed queue accepted", info)
		}
		m.Close()
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("invalid saved queue replaced")
		}
	}
}

func TestAllSourcesSelectionsAreCopiedBeforeCatalogDiscovery(t *testing.T) {
	m, gates := gatedManager(t)
	a := &sequentialCatalogGate{sequentialDownloadAdapter: sequentialDownloadAdapter{manager: m}, catalogStarted: make(chan struct{}), catalogRelease: make(chan struct{})}
	m.hyperliquidAdapter = a
	r := allSourcesRequest("d1")
	done := make(chan error, 1)
	go func() { _, err := m.Start(r); done <- err }()
	select {
	case <-a.catalogStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog did not start")
	}
	r.SourceSelections[1].Symbols[0] = "ethusd"
	r.SourceSelections[0].Symbols[0] = "changed"
	r.SourceSelections[1].DataSource = "changed"
	close(a.catalogRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	releaseGate(t, gates, "btcusd")
	info := waitHyperliquidDownload(t, m)
	if info.Jobs[0].Symbol != "btcusd" || info.Jobs[1].NativeID != "BTC" {
		t.Fatalf("caller mutated frozen selections: %+v", info.Jobs)
	}
}

func TestAllSourcesLimitsAndViewRoundTrip(t *testing.T) {
	m := testManager(t)
	view := defaultView()
	view.DataSource = "all"
	view.Symbols = []string{"btcusd"}
	view.HyperliquidSymbols = []string{allSourcesRequest("d1").SourceSelections[0].Symbols[0]}
	view.HyperliquidSelectionInitialized = true
	if _, err := m.SaveView(view); err != nil {
		t.Fatal(err)
	}
	m.Close()
	restored := reopenTestManager(t, m.profile)
	actual, err := restored.View()
	if err != nil || !reflect.DeepEqual(actual, view) {
		t.Fatalf("All choices not restored: %+v %v", actual, err)
	}
	view.Timeframe = "m1"
	if _, err = restored.SaveView(view); err == nil {
		t.Fatal("All accepted a nonshared timeframe")
	}
	r := allSourcesRequest("w1")
	r.SourceSelections[0].Symbols = make([]string, 20000)
	for i := range r.SourceSelections[0].Symbols {
		r.SourceSelections[0].Symbols[i] = fmt.Sprintf("hyperliquid-perpetual%064x", i)
	}
	if _, err = allSourceRequests(r, time.Now()); err == nil || !strings.Contains(err.Error(), "40000") {
		t.Fatal("unreloadable aggregate queue accepted", err)
	}
	path := filepath.Join(restored.profile, "screener-downloads.json")
	restored.mu.Lock()
	restored.jobs = []Job{{ID: strings.Repeat("a", 32), Message: strings.Repeat("x", 16*1024*1024)}}
	err = restored.persistDownloadsLocked()
	restored.jobs = nil
	restored.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "16 MiB") {
		t.Fatal("oversized state accepted", err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("oversized queue changed original state", err)
	}
}

func TestAllSourcesMarkerRejectsUnplannedHourlyDailyCompanion(t *testing.T) {
	jobs := weeklyQueueJobs()
	for i := range jobs {
		jobs[i].DataSource = "dukascopy"
		if jobs[i].Timeframe == "w1" {
			jobs[i].Timeframe = "h1"
		}
	}
	if err := validateSequentialQueue(jobs, []string{"dukascopy"}, 1); err == nil {
		t.Fatal("All H1 accepted a shape only legacy Statistics companion requests can create")
	}
	if _, err := downloadBatchRequest(jobs, 1); err != nil {
		t.Fatal("legacy companion no longer readable", err)
	}
}
