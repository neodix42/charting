package screener

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func firstSymbols(count int) []string {
	symbols := make([]string, 0, count)
	for _, group := range loadCatalog().Groups {
		for _, instrument := range group.Instruments {
			symbols = append(symbols, instrument.ID)
			if len(symbols) == count {
				return symbols
			}
		}
	}
	panic("insufficient supported test symbols")
}

func gatedManager(t *testing.T) (*Manager, string) {
	t.Helper()
	manager := testManager(t)
	gates := t.TempDir()
	t.Setenv("CRYPTODESK_SCREENER_TEST_MODE", "gated")
	t.Setenv("CRYPTODESK_SCREENER_TEST_GATES", gates)
	return manager, gates
}

func waitGate(t *testing.T, gates, symbol string) helperRequest {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(gates, symbol+".started"))
		var request helperRequest
		if err == nil && json.Unmarshal(data, &request) == nil {
			return request
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("real helper process did not start for %s", symbol)
	return helperRequest{}
}

func releaseGate(t *testing.T, gates, symbol string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(gates, symbol+".release"), []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertNotStarted(t *testing.T, gates, symbol string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(gates, symbol+".started")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("queued helper %s started too soon: %v", symbol, err)
	}
}

func TestTwentyRealWorkersDefaultCapRefillAndStableResultOrder(t *testing.T) {
	manager, gates := gatedManager(t)
	if info := manager.Info(); info.Concurrency != 20 || info.MaxConcurrency != 20 {
		t.Fatalf("bad initial concurrency %+v", info)
	}
	request := validRequest()
	request.Symbols = firstSymbols(22)
	// An omitted zero value selects the default of twenty.
	if info, err := manager.Start(request); err != nil || info.Concurrency != 20 {
		t.Fatalf("start: %+v %v", info, err)
	}
	privateDirectories := map[string]bool{}
	for _, symbol := range request.Symbols[:20] {
		child := waitGate(t, gates, symbol)
		if child.SourceBatchSize != 1 {
			t.Fatalf("twenty workers multiply the source request budget: %+v", child)
		}
		directory := filepath.Dir(child.Output)
		if privateDirectories[directory] {
			t.Fatal("concurrent children share WAF token directory")
		}
		privateDirectories[directory] = true
	}
	info := manager.Info()
	for i, job := range info.Jobs {
		if job.Symbol != request.Symbols[i] {
			t.Fatal("selection order changed")
		}
		if i < 20 && job.Status != "running" || i >= 20 && job.Status != "queued" {
			t.Fatalf("pool cap not enforced for job %d: %+v", i, job)
		}
	}
	assertNotStarted(t, gates, request.Symbols[20])
	assertNotStarted(t, gates, request.Symbols[21])
	releaseGate(t, gates, request.Symbols[0])
	waitGate(t, gates, request.Symbols[20])
	if got := manager.Info(); got.Jobs[0].Status != "completed" || got.Jobs[21].Status != "queued" {
		t.Fatalf("completion did not refill exactly one slot: %+v", got.Jobs)
	}
	assertNotStarted(t, gates, request.Symbols[21])
	for _, symbol := range request.Symbols[1:] {
		releaseGate(t, gates, symbol)
	}
	finished := waitFinished(t, manager)
	for i, job := range finished.Jobs {
		if job.Symbol != request.Symbols[i] || job.Status != "completed" || job.OutputPath == "" {
			t.Fatalf("unordered/incomplete result %d: %+v", i, job)
		}
	}
	assertNoPartials(t, finished.Directory)
}

func TestConfiguredPoolSizeAndSourceRequestBudget(t *testing.T) {
	for _, test := range []struct{ workers, symbols, batch int }{{1, 2, 4}, {2, 3, 4}, {5, 6, 4}, {6, 7, 3}, {10, 11, 2}, {11, 12, 1}} {
		t.Run(string(rune('A'+test.workers)), func(t *testing.T) {
			manager, gates := gatedManager(t)
			request := validRequest()
			request.Symbols = firstSymbols(test.symbols)
			request.Concurrency = test.workers
			if info, err := manager.Start(request); err != nil || info.Concurrency != test.workers {
				t.Fatalf("configured concurrency missing: %+v %v", info, err)
			}
			for _, symbol := range request.Symbols[:test.workers] {
				if child := waitGate(t, gates, symbol); child.SourceBatchSize != test.batch {
					t.Fatalf("unexpected source batch %+v", child)
				}
			}
			if test.workers*test.batch > MaxConcurrency {
				t.Fatal("test did not enforce aggregate request budget")
			}
			assertNotStarted(t, gates, request.Symbols[test.workers])
			manager.Cancel()
			assertNoPartials(t, manager.Info().Directory)
		})
	}
}

func TestParallelFailureRefillsWithoutBlockingOtherWorker(t *testing.T) {
	manager, gates := gatedManager(t)
	request := validRequest()
	request.Symbols = []string{"btcusd", "eurusd", "ethusd"}
	request.Concurrency = 2
	if _, err := manager.Start(request); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	waitGate(t, gates, "eurusd")
	releaseGate(t, gates, "eurusd")
	waitGate(t, gates, "ethusd")
	info := manager.Info()
	if got := []string{info.Jobs[0].Status, info.Jobs[1].Status, info.Jobs[2].Status}; !reflect.DeepEqual(got, []string{"running", "failed", "running"}) {
		t.Fatalf("one failure blocked independent workers: %v", got)
	}
	releaseGate(t, gates, "ethusd")
	releaseGate(t, gates, "btcusd")
	info = waitFinished(t, manager)
	if got := []string{info.Jobs[0].Status, info.Jobs[1].Status, info.Jobs[2].Status}; !reflect.DeepEqual(got, []string{"completed", "failed", "completed"}) {
		t.Fatalf("unexpected results %v", got)
	}
	assertNoPartials(t, info.Directory)
}

func TestCancelDrainsTwentyWorkersPreservesCompletedAndStopsQueuedDispatch(t *testing.T) {
	manager, gates := gatedManager(t)
	request := validRequest()
	request.Symbols = firstSymbols(22)
	if _, err := manager.Start(request); err != nil {
		t.Fatal(err)
	}
	for _, symbol := range request.Symbols[:20] {
		waitGate(t, gates, symbol)
	}
	releaseGate(t, gates, request.Symbols[0])
	waitGate(t, gates, request.Symbols[20])
	completed := manager.Info().Jobs[0]
	before, err := os.ReadFile(completed.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	info := manager.Cancel()
	for i, job := range info.Jobs {
		if i == 0 {
			if job.Status != "completed" || job.OutputPath != completed.OutputPath {
				t.Fatalf("cancel changed committed result %+v", job)
			}
		} else if job.Status != "cancelled" || job.OutputPath != "" {
			t.Fatalf("cancel failed for job %d: %+v", i, job)
		}
	}
	after, err := os.ReadFile(completed.OutputPath)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("completed output changed during cancellation")
	}
	assertNotStarted(t, gates, request.Symbols[21])
	assertNoPartials(t, info.Directory)
	if _, err := manager.ConfigureDirectory(t.TempDir()); err != nil {
		t.Fatalf("cancel returned before pool finished: %v", err)
	}
}

func TestShutdownDrainsTwentyWorkersAndLeavesQueuedJobsUnstarted(t *testing.T) {
	manager, gates := gatedManager(t)
	request := validRequest()
	request.Symbols = firstSymbols(21)
	if _, err := manager.Start(request); err != nil {
		t.Fatal(err)
	}
	for _, symbol := range request.Symbols[:20] {
		waitGate(t, gates, symbol)
	}
	manager.Close()
	for _, job := range manager.Info().Jobs {
		if job.Status != "queued" {
			t.Fatalf("shutdown did not leave a resumable job %+v", job)
		}
	}
	assertNotStarted(t, gates, request.Symbols[20])
	if manager.Info().Active {
		t.Fatal("shutdown left the pool active")
	}
	if _, err := manager.Start(request); err == nil {
		t.Fatal("closed pool accepted work")
	}
}

func TestInvalidConcurrencyDoesNotReplaceExistingJobs(t *testing.T) {
	manager := testManager(t)
	if _, err := manager.Start(validRequest()); err != nil {
		t.Fatal(err)
	}
	before := waitFinished(t, manager)
	for _, concurrency := range []int{-1, 21, 1000} {
		request := validRequest()
		request.Concurrency = concurrency
		if _, err := manager.Start(request); err == nil {
			t.Fatalf("accepted concurrency %d", concurrency)
		}
		if !reflect.DeepEqual(before, manager.Info()) {
			t.Fatal("invalid concurrency changed existing batch")
		}
	}
}
