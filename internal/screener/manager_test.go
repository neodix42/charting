package screener

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type helperRequest struct {
	Symbol          string `json:"symbol"`
	Output          string `json:"output"`
	SourceBatchSize int    `json:"sourceBatchSize"`
}

// The helper is an actual separate process using the production stdin/progress/
// file protocol. It cannot be selected in application operation.
func TestMain(m *testing.M) {
	if os.Getenv("CRYPTODESK_SCREENER_TEST_HELPER") == "1" {
		var request helperRequest
		if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
			os.Exit(4)
		}
		fmt.Println(`{"type":"progress","progress":30,"message":"Fetched 1 of 3 source files"}`)
		switch os.Getenv("CRYPTODESK_SCREENER_TEST_MODE") {
		case "hold":
			_ = os.WriteFile(filepath.Join(filepath.Dir(request.Output), ".dukascopy-waf-token.json"), []byte("owned token"), 0600)
			_ = os.WriteFile(filepath.Join(filepath.Dir(request.Output), ".dukascopy-waf-token.json.helper.tmp"), []byte("owned atomic scratch"), 0600)
			time.Sleep(time.Hour)
		case "gated":
			gates := os.Getenv("CRYPTODESK_SCREENER_TEST_GATES")
			data, _ := json.Marshal(request)
			if err := os.WriteFile(filepath.Join(gates, request.Symbol+".started"), data, 0600); err != nil {
				os.Exit(6)
			}
			for {
				if _, err := os.Stat(filepath.Join(gates, request.Symbol+".release")); err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		case "malformed":
			fmt.Println("unexpected non-JSON output")
			os.Exit(1)
		case "oversized":
			fmt.Println(strings.Repeat("a", 20000))
			time.Sleep(time.Hour)
		case "empty":
			fmt.Println(`{"type":"done","rows":0,"message":"No rows"}`)
			os.Exit(0)
		}
		if request.Symbol == "eurusd" {
			fmt.Println(`{"type":"error","message":"Upstream HTTP 403"}`)
			os.Exit(1)
		}
		if err := os.WriteFile(request.Output, []byte("timestamp,open,high,low,close,volume\n1704067200000,1,2,1,2,3\n"), 0600); err != nil {
			os.Exit(5)
		}
		fmt.Println(`{"type":"progress","progress":10,"message":"Earlier progress must not lower the percent"}`)
		fmt.Println(`{"type":"done","rows":1,"message":"Downloaded 1 row"}`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testManager(t *testing.T) *Manager {
	t.Helper()
	manager := New(t.TempDir())
	t.Setenv("CRYPTODESK_SCREENER_TEST_HELPER", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("GOMAXPROCS", "2")
	manager.runtime = workerRuntime{node: os.Args[0], worker: filepath.Join(t.TempDir(), "worker")}
	manager.runtimeError = ""
	t.Cleanup(manager.Close)
	return manager
}
func validRequest() Request {
	return Request{Symbols: []string{"btcusd"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-02"}
}
func waitFinished(t *testing.T, manager *Manager) Info {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		active := manager.active
		manager.mu.Unlock()
		if !active {
			return manager.Info()
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("download did not finish")
	return Info{}
}
func waitRunning(t *testing.T, manager *Manager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info := manager.Info()
		if len(info.Jobs) > 0 && info.Jobs[0].Progress == 30 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("child did not send progress")
}
func assertNoPartials(t *testing.T, directory string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(directory, ".*.part"))
	if err != nil || len(files) > 0 {
		t.Fatalf("owned partial files remain: %v %v", files, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".cryptodesk-screener-job-") {
			t.Fatalf("owned job/token directory remains: %s", entry.Name())
		}
	}
}

func TestCatalogCompletenessAndCopies(t *testing.T) {
	manager := New(t.TempDir())
	defer manager.Close()
	catalog := manager.Catalog()
	seen := map[string]bool{}
	for _, group := range catalog.Groups {
		if group.Name == "" || len(group.Instruments) == 0 {
			t.Fatal("empty group")
		}
		for _, instrument := range group.Instruments {
			if seen[instrument.ID] || instrument.Name == "" {
				t.Fatalf("bad instrument %+v", instrument)
			}
			seen[instrument.ID] = true
			if _, err := time.Parse("2006-01-02", instrument.EarliestDate); err != nil {
				t.Fatal(err)
			}
		}
	}
	if catalog.Version != "1.51.0" || len(seen) != 1499 || len(catalog.Groups) != 39 || len(catalog.Timeframes) != 10 {
		t.Fatalf("catalog drift: %s / %d / %d", catalog.Version, len(seen), len(catalog.Groups))
	}
	catalog.Groups[0].Instruments[0].ID = "mutated"
	if manager.Catalog().Groups[0].Instruments[0].ID == "mutated" {
		t.Fatal("catalog leaked mutable data")
	}
}

func TestRequestValidationBeforeMutation(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		change func(*Request)
	}{
		{"empty symbols", func(r *Request) { r.Symbols = nil }},
		{"unknown instrument", func(r *Request) { r.Symbols = []string{"$(touch bad)"} }},
		{"duplicate", func(r *Request) { r.Symbols = []string{"btcusd", "btcusd"} }},
		{"timeframe", func(r *Request) { r.Timeframe = "1d" }},
		{"impossible date", func(r *Request) { r.From = "2024-02-30" }},
		{"nonpadded date", func(r *Request) { r.From = "2024-1-01" }},
		{"reversed", func(r *Request) { r.From = r.To }},
		{"future", func(r *Request) { r.To = "2026-10-27" }},
		{"ancient", func(r *Request) { r.From = "0001-01-01" }},
		{"negative concurrency", func(r *Request) { r.Concurrency = -1 }},
		{"concurrency above cap", func(r *Request) { r.Concurrency = MaxConcurrency + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRequest()
			tc.change(&r)
			if validateRequest(r, now) == nil {
				t.Fatalf("accepted %+v", r)
			}
		})
	}
	if err := validateRequest(validRequest(), now); err != nil {
		t.Fatal(err)
	}
	for _, timeframe := range loadCatalog().Timeframes {
		r := validRequest()
		r.Timeframe = timeframe
		if err := validateRequest(r, now); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDownloadOrderingFailuresAndOutputPreservation(t *testing.T) {
	manager := testManager(t)
	request := validRequest()
	request.Symbols = []string{"btcusd", "eurusd", "ethusd"}
	request.Concurrency = 1
	if _, err := manager.Start(request); err != nil {
		t.Fatal(err)
	}
	info := waitFinished(t, manager)
	if got := []string{info.Jobs[0].Status, info.Jobs[1].Status, info.Jobs[2].Status}; !reflect.DeepEqual(got, []string{"completed", "failed", "completed"}) {
		t.Fatalf("unexpected status %v", got)
	}
	if !strings.Contains(info.Jobs[1].Message, "403") {
		t.Fatalf("upstream failure hidden: %+v", info.Jobs[1])
	}
	if info.Jobs[0].Symbol != "btcusd" || info.Jobs[2].Symbol != "ethusd" || info.Jobs[0].Progress != 100 {
		t.Fatal("order or progress lost")
	}
	original, err := os.ReadFile(info.Jobs[0].OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	firstPath := info.Jobs[0].OutputPath
	info.Jobs[0].Status = "modified"
	if manager.Info().Jobs[0].Status != "completed" {
		t.Fatal("mutable snapshot leaked")
	}
	if _, err = manager.Start(validRequest()); err != nil {
		t.Fatal(err)
	}
	second := waitFinished(t, manager).Jobs[0]
	if second.Status != "completed" || second.OutputPath == firstPath || !strings.HasSuffix(second.OutputPath, "-2.csv") {
		t.Fatalf("overwrite instead of new output: %+v", second)
	}
	preserved, err := os.ReadFile(firstPath)
	if err != nil || !reflect.DeepEqual(original, preserved) {
		t.Fatal("existing data changed")
	}
	assertNoPartials(t, info.Directory)
	// Cancelling a completed batch must preserve its committed status and output.
	if got := manager.Cancel().Jobs[0]; got.Status != "completed" || got.OutputPath != second.OutputPath {
		t.Fatal("cancel changed completed output")
	}
}

func TestCancellationDrainsChildAndQueueBeforeReturn(t *testing.T) {
	manager := testManager(t)
	t.Setenv("CRYPTODESK_SCREENER_TEST_MODE", "hold")
	request := validRequest()
	request.Symbols = []string{"btcusd", "ethusd"}
	request.Concurrency = 1
	if _, err := manager.Start(request); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, manager)
	if _, err := manager.Start(request); err == nil {
		t.Fatal("accepted simultaneous batch")
	}
	if _, err := manager.ConfigureDirectory(t.TempDir()); err == nil {
		t.Fatal("changed active download folder")
	}
	info := manager.Cancel()
	for _, job := range info.Jobs {
		if job.Status != "cancelled" || job.OutputPath != "" {
			t.Fatalf("unexpected cancelled job %+v", job)
		}
	}
	assertNoPartials(t, info.Directory)
	if _, err := manager.ConfigureDirectory(t.TempDir()); err != nil {
		t.Fatalf("cancel returned before worker drained: %v", err)
	}
	t.Setenv("CRYPTODESK_SCREENER_TEST_MODE", "")
	if _, err := manager.Start(validRequest()); err != nil {
		t.Fatal(err)
	}
	if job := waitFinished(t, manager).Jobs[0]; job.Status != "completed" {
		t.Fatalf("could not resume after cancellation %+v", job)
	}
}

func TestShutdownCancelsAndRejectsFutureWork(t *testing.T) {
	manager := testManager(t)
	t.Setenv("CRYPTODESK_SCREENER_TEST_MODE", "hold")
	if _, err := manager.Start(validRequest()); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, manager)
	manager.Close()
	if manager.Info().Jobs[0].Status != "cancelled" {
		t.Fatal("shutdown did not cancel")
	}
	assertNoPartials(t, manager.Info().Directory)
	if _, err := manager.Start(validRequest()); err == nil {
		t.Fatal("closed manager accepted work")
	}
}

func TestBadWorkerProtocolAndEmptyDataNeverPublish(t *testing.T) {
	for _, mode := range []string{"malformed", "oversized", "empty"} {
		t.Run(mode, func(t *testing.T) {
			manager := testManager(t)
			t.Setenv("CRYPTODESK_SCREENER_TEST_MODE", mode)
			if _, err := manager.Start(validRequest()); err != nil {
				t.Fatal(err)
			}
			info := waitFinished(t, manager)
			if job := info.Jobs[0]; job.Status != "failed" || job.OutputPath != "" {
				t.Fatalf("published bad result %+v", job)
			}
			assertNoPartials(t, info.Directory)
		})
	}
}

func TestMachineLocalPreferencesRoundTripAndCorruption(t *testing.T) {
	profile := t.TempDir()
	manager := New(profile)
	defer manager.Close()
	if manager.Info().Directory != filepath.Join(profile, "screener") {
		t.Fatal("bad default")
	}
	destination := filepath.Join(t.TempDir(), "Unicode data Ж with spaces")
	if _, err := manager.ConfigureDirectory(destination); err != nil {
		t.Fatal(err)
	}
	loaded := New(profile)
	defer loaded.Close()
	if loaded.Info().Directory != destination {
		t.Fatal("directory not restored")
	}
	before, err := os.ReadFile(filepath.Join(profile, "screener.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ConfigureDirectory("relative/path"); err == nil {
		t.Fatal("accepted relative directory")
	}
	after, _ := os.ReadFile(filepath.Join(profile, "screener.json"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed setting changed preferences")
	}
	broken := []byte(`{"directory":"relative/path","extra":true}`)
	if err := os.WriteFile(filepath.Join(profile, "screener.json"), broken, 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := New(profile)
	defer corrupt.Close()
	if corrupt.Info().Error == "" || corrupt.Info().Directory != filepath.Join(profile, "screener") {
		t.Fatal("invalid preferences silently accepted")
	}
	preserved, _ := os.ReadFile(filepath.Join(profile, "screener.json"))
	if !reflect.DeepEqual(preserved, broken) {
		t.Fatal("corrupt preferences overwritten on startup")
	}
}

func TestRuntimeOverrideMissingIsExplicit(t *testing.T) {
	t.Setenv("CRYPTODESK_SCREENER_RUNTIME", t.TempDir())
	manager := New(t.TempDir())
	defer manager.Close()
	if info := manager.Info(); info.RuntimeAvailable || info.RuntimeError == "" {
		t.Fatal("missing runtime hidden")
	}
	if _, err := manager.Start(validRequest()); err == nil {
		t.Fatal("unavailable runtime accepted work")
	}
}
