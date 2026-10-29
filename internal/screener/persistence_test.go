package screener

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func reopenTestManager(t *testing.T, profile string) *Manager {
	t.Helper()
	m := newManager(profile, func() (workerRuntime, error) {
		return workerRuntime{node: os.Args[0], worker: filepath.Join(t.TempDir(), "worker")}, nil
	})
	t.Cleanup(m.Close)
	return m
}

func TestCloseAndRestartAutomaticallyResumesQueueAndRetainsCompletedOutputs(t *testing.T) {
	m, gates := gatedManager(t)
	configureTestConcurrency(t, m, 1)
	r := validRequest()
	r.Symbols = []string{"btcusd", "ethusd", "ltcusd"}
	r.Concurrency = 1
	if _, err := m.Start(r); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	releaseGate(t, gates, "btcusd")
	waitGate(t, gates, "ethusd")
	before := m.Info()
	finished := before.Jobs[0]
	if finished.Status != "completed" {
		t.Fatal(before)
	}
	data, err := os.ReadFile(finished.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	paused := m.Info()
	if paused.Active || paused.Jobs[1].Status != "queued" || paused.Jobs[2].Status != "queued" {
		t.Fatal(paused)
	}
	assertNotStarted(t, gates, "ltcusd")
	if !ownedJob(m.jobDirectory(paused.Jobs[1]), paused.Jobs[1]) {
		t.Fatal("in-flight checkpoint directory was removed on close")
	}
	if err = os.Remove(filepath.Join(gates, "ethusd.started")); err != nil {
		t.Fatal(err)
	}
	// Legacy queue metadata must not override the authoritative local setting.
	queuePath := filepath.Join(m.profile, "screener-downloads.json")
	var legacy savedDownloads
	if err = readState(queuePath, 16*1024*1024, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Concurrency = 20
	if err = writeState(queuePath, legacy); err != nil {
		t.Fatal(err)
	}
	restarted := reopenTestManager(t, m.profile)
	if child := waitGate(t, gates, "ethusd"); child.SourceBatchSize != 1 {
		t.Fatalf("legacy queue resumed parallel source fetching: %+v", child)
	}
	assertNotStarted(t, gates, "ltcusd")
	if got := restarted.Info(); got.Jobs[0].ID != finished.ID || got.Concurrency != 1 || got.Jobs[0].Status != "completed" {
		t.Fatal(got)
	}
	releaseGate(t, gates, "ethusd")
	waitGate(t, gates, "ltcusd")
	releaseGate(t, gates, "ltcusd")
	result := waitFinished(t, restarted)
	for i, job := range result.Jobs {
		if job.Status != "completed" || job.ID != before.Jobs[i].ID {
			t.Fatal(result)
		}
	}
	preserved, err := os.ReadFile(finished.OutputPath)
	if err != nil || !bytes.Equal(data, preserved) {
		t.Fatal("completed file changed", err)
	}
	files, _ := filepath.Glob(filepath.Join(result.Directory, "btcusd-*.csv"))
	if len(files) != 1 {
		t.Fatal("completed symbol downloaded again", files)
	}
	assertNoPartials(t, result.Directory)
	var persisted savedDownloads
	if err = readState(queuePath, 16*1024*1024, &persisted); err != nil || persisted.Concurrency != 1 {
		t.Fatal("resumed queue did not persist the configured workers", persisted.Concurrency, err)
	}
}

func TestExplicitCancellationDoesNotResumeOnRestart(t *testing.T) {
	m, gates := gatedManager(t)
	configureTestConcurrency(t, m, 1)
	r := validRequest()
	r.Symbols = []string{"btcusd", "ethusd"}
	r.Concurrency = 1
	if _, err := m.Start(r); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	m.Cancel()
	m.Close()
	reopened := reopenTestManager(t, m.profile)
	if reopened.Info().Active {
		t.Fatal("cancelled queue auto-started")
	}
	for _, j := range reopened.Info().Jobs {
		if j.Status != "cancelled" {
			t.Fatal(j)
		}
	}
	assertNotStarted(t, gates, "ethusd")
	assertNoPartials(t, m.Info().Directory)
}

func TestViewSelectionDatesAndCategoryExpansionRestoreExactly(t *testing.T) {
	m := testManager(t)
	view := defaultView()
	view.Symbols = []string{"btcusd", "ethusd"}
	view.Timeframe = "m15"
	view.Concurrency = 7
	view.From = "2020-01-01"
	view.To = "2024-06-01"
	view.CollapsedCategories = []string{loadCatalog().Groups[0].Name, loadCatalog().Groups[2].Name}
	if saved, err := m.SaveView(view); err != nil || saved.Concurrency != DefaultConcurrency {
		t.Fatal(err)
	}
	view.Concurrency = DefaultConcurrency
	reopened := reopenTestManager(t, m.profile)
	got, err := reopened.View()
	if err != nil || !reflect.DeepEqual(got, view) {
		t.Fatal(got, err)
	}
	got.Symbols[0] = "mutated"
	got.CollapsedCategories[0] = "mutated"
	stored, _ := reopened.View()
	if !reflect.DeepEqual(stored, view) {
		t.Fatal("view caller mutated stored preferences")
	}
	invalid := copyView(view)
	invalid.Symbols = []string{"unknown"}
	if _, err = reopened.SaveView(invalid); err == nil {
		t.Fatal("accepted invalid selection")
	}
	again := reopenTestManager(t, m.profile)
	got, _ = again.View()
	if !reflect.DeepEqual(got, view) {
		t.Fatal("failed save changed preferences")
	}
	if _, err = os.Stat(filepath.Join(m.profile, "screener-downloads.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("view change created a download queue", err)
	}
}

func TestLegacyViewConcurrencyMigratesWithoutLosingSelections(t *testing.T) {
	profile := t.TempDir()
	view := defaultView()
	view.Symbols = []string{"btcusd", "ethusd"}
	view.Timeframe, view.From, view.To = "h1", "2020-01-01", "2024-06-01"
	view.Concurrency = 20
	view.CollapsedCategories = []string{loadCatalog().Groups[0].Name}
	if err := writeState(filepath.Join(profile, "screener-view.json"), savedView{1, view}); err != nil {
		t.Fatal(err)
	}
	m := reopenTestManager(t, profile)
	view.Concurrency = DefaultConcurrency
	got, err := m.View()
	if err != nil || !reflect.DeepEqual(got, view) || m.Info().Error != "" {
		t.Fatalf("legacy settings lost selections or overrode global workers: %+v %v", got, err)
	}
}

func TestCorruptOrFutureSavedStateIsPreservedWithoutStartingDownloads(t *testing.T) {
	for _, name := range []string{"screener-downloads.json", "screener-view.json"} {
		t.Run(name, func(t *testing.T) {
			profile := t.TempDir()
			original := []byte(`{"version":999,"unknown":"original user state"}`)
			path := filepath.Join(profile, name)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			m := reopenTestManager(t, profile)
			if m.Info().Error == "" || m.Info().Active {
				t.Fatal(m.Info())
			}
			m.Close()
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(original, got) {
				t.Fatal("invalid state was overwritten", err)
			}
		})
	}
}

func TestQueueMustCommitBeforeAnyWorkerStarts(t *testing.T) {
	m, gates := gatedManager(t)
	if err := os.Mkdir(filepath.Join(m.profile, "screener-downloads.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(validRequest()); err == nil {
		t.Fatal("started with an unsaved queue")
	}
	if len(m.Info().Jobs) != 0 || m.Info().Active {
		t.Fatal(m.Info())
	}
	assertNotStarted(t, gates, "btcusd")
}

func TestCrashDuringPublicationRecoversSameFileWithoutDuplicateDownload(t *testing.T) {
	m := testManager(t)
	job := Job{ID: strings.Repeat("a", 32), Symbol: "btcusd", Timeframe: "d1", From: "2024-01-01", To: "2024-01-02", Status: "running", Progress: 95}
	m.batchDirectory = filepath.Join(m.profile, "screener")
	m.jobs = []Job{job}
	m.concurrency = 1
	m.publication = map[string]string{job.ID: "btcusd-d1-2024-01-01-2024-01-02.csv"}
	if err := os.MkdirAll(m.batchDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	directory, err := m.prepareJob(job)
	if err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(directory, "data.part")
	data := []byte("timestamp,open,high,low,close,volume\n1704067200000,1,2,1,2,3\n")
	if err = os.WriteFile(partial, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = m.persistDownloadsLocked(); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(m.batchDirectory, m.publication[job.ID])
	if err = os.Link(partial, output); err != nil {
		t.Fatal(err)
	}
	// Simulate the app dying here: do not call Close before loading its journal.
	reopened := reopenTestManager(t, m.profile)
	result := reopened.Info()
	if result.Active || result.Jobs[0].Status != "completed" || result.Jobs[0].OutputPath != output {
		t.Fatal(result)
	}
	files, _ := filepath.Glob(filepath.Join(m.batchDirectory, "*.csv"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	got, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal(err)
	}
	assertNoPartials(t, m.batchDirectory)
}
