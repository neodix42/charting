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

func TestSerialDownloadsWaitForEachPublicationAndKeepSelectionOrder(t *testing.T) {
	manager, gates := gatedManager(t)
	if info := manager.Info(); info.Concurrency != 1 || info.MaxConcurrency != 1 {
		t.Fatalf("bad initial concurrency %+v", info)
	}
	request := validRequest()
	request.Symbols = firstSymbols(4)
	// Omitting the legacy worker setting still selects serial downloads.
	if info, err := manager.Start(request); err != nil || info.Concurrency != 1 {
		t.Fatalf("start: %+v %v", info, err)
	}
	privateDirectories := map[string]bool{}
	for index, symbol := range request.Symbols {
		child := waitGate(t, gates, symbol)
		if child.SourceBatchSize != 1 {
			t.Fatalf("source fetching was not serial: %+v", child)
		}
		directory := filepath.Dir(child.Output)
		if privateDirectories[directory] {
			t.Fatal("children share a scratch directory")
		}
		privateDirectories[directory] = true
		for i, job := range manager.Info().Jobs {
			want := "queued"
			if i < index {
				want = "completed"
			} else if i == index {
				want = "running"
			}
			if job.Symbol != request.Symbols[i] || job.Status != want {
				t.Fatalf("serial publication/order failed at %d: %+v", index, job)
			}
			if i > index {
				assertNotStarted(t, gates, job.Symbol)
			}
		}
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

func TestLegacyRequestedConcurrencyCannotEnableParallelDownloads(t *testing.T) {
	for _, requested := range []int{1, 2, 7, 20} {
		t.Run(string(rune('A'+requested)), func(t *testing.T) {
			manager, gates := gatedManager(t)
			request := validRequest()
			request.Symbols = firstSymbols(3)
			request.Concurrency = requested
			if info, err := manager.Start(request); err != nil || info.Concurrency != 1 || info.MaxConcurrency != 1 {
				t.Fatalf("legacy concurrency was not normalized: %+v %v", info, err)
			}
			if child := waitGate(t, gates, request.Symbols[0]); child.SourceBatchSize != 1 {
				t.Fatalf("source requests may overlap: %+v", child)
			}
			assertNotStarted(t, gates, request.Symbols[1])
			assertNotStarted(t, gates, request.Symbols[2])
			manager.Cancel()
			assertNoPartials(t, manager.Info().Directory)
		})
	}
}

func TestSerialFailureAllowsNextQueuedDownload(t *testing.T) {
	manager, gates := gatedManager(t)
	request := validRequest()
	request.Symbols = []string{"btcusd", "eurusd", "ethusd"}
	request.Concurrency = 2
	if _, err := manager.Start(request); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	assertNotStarted(t, gates, "eurusd")
	releaseGate(t, gates, "btcusd")
	waitGate(t, gates, "eurusd")
	assertNotStarted(t, gates, "ethusd")
	releaseGate(t, gates, "eurusd")
	waitGate(t, gates, "ethusd")
	info := manager.Info()
	if got := []string{info.Jobs[0].Status, info.Jobs[1].Status, info.Jobs[2].Status}; !reflect.DeepEqual(got, []string{"completed", "failed", "running"}) {
		t.Fatalf("one failure blocked the next queued download: %v", got)
	}
	releaseGate(t, gates, "ethusd")
	info = waitFinished(t, manager)
	if got := []string{info.Jobs[0].Status, info.Jobs[1].Status, info.Jobs[2].Status}; !reflect.DeepEqual(got, []string{"completed", "failed", "completed"}) {
		t.Fatalf("unexpected results %v", got)
	}
	assertNoPartials(t, info.Directory)
}

func TestCancelDrainsSerialWorkerPreservesCompletedAndStopsQueuedDispatch(t *testing.T) {
	manager, gates := gatedManager(t)
	request := validRequest()
	request.Symbols = firstSymbols(4)
	if _, err := manager.Start(request); err != nil {
		t.Fatal(err)
	}
	for _, symbol := range request.Symbols[:1] {
		waitGate(t, gates, symbol)
	}
	releaseGate(t, gates, request.Symbols[0])
	waitGate(t, gates, request.Symbols[1])
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
	assertNotStarted(t, gates, request.Symbols[2])
	assertNoPartials(t, info.Directory)
	if _, err := manager.ConfigureDirectory(t.TempDir()); err != nil {
		t.Fatalf("cancel returned before pool finished: %v", err)
	}
}

func TestShutdownDrainsSerialWorkerAndLeavesQueuedJobsUnstarted(t *testing.T) {
	manager, gates := gatedManager(t)
	request := validRequest()
	request.Symbols = firstSymbols(3)
	if _, err := manager.Start(request); err != nil {
		t.Fatal(err)
	}
	for _, symbol := range request.Symbols[:1] {
		waitGate(t, gates, symbol)
	}
	manager.Close()
	for _, job := range manager.Info().Jobs {
		if job.Status != "queued" {
			t.Fatalf("shutdown did not leave a resumable job %+v", job)
		}
	}
	assertNotStarted(t, gates, request.Symbols[1])
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
