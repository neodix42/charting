package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The gate makes overlap observable without relying on elapsed-time assertions.
// History still comes from the explicit adapter and enrichment uses the actual
// pinned Node calculators, CSV publication and caches.
type parallelPublicHistory struct {
	exchange.Adapter
	mu                  sync.Mutex
	active, peak, calls int
	started             chan domain.FeedKey
	gate                <-chan struct{}
}

func (a *parallelPublicHistory) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	a.mu.Lock()
	a.active++
	a.calls++
	a.peak = max(a.peak, a.active)
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.active--; a.mu.Unlock() }()
	select {
	case a.started <- key:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-a.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return a.Adapter.History(ctx, key, from, to, limit)
}

func parallelPublicFixture(t *testing.T, assets int) (*LiveAlerts, *publicAlertFakeAdapter, *parallelPublicHistory, chan struct{}) {
	t.Helper()
	m := sourceEnrichmentManager(t, false)
	fake := &publicAlertFakeAdapter{price: 100}
	for i := 0; i < assets; i++ {
		fake.items = append(fake.items, domain.Instrument{NativeID: fmt.Sprintf("@%d", i), Market: "spot", Availability: "available"})
	}
	l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	gate := make(chan struct{})
	adapter := &parallelPublicHistory{Adapter: fake, started: make(chan domain.FeedKey, 64), gate: gate}
	l.publicData.adapters["hyperliquid"] = adapter
	return l, fake, adapter, gate
}

type parallelPublicResult struct {
	batch publicAlertBatch
	err   error
}

func runParallelPublic(l *LiveAlerts, ctx context.Context, c SavedAnalysisCondition) <-chan parallelPublicResult {
	result := make(chan parallelPublicResult, 1)
	go func() {
		b, e := l.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"m1"}}, time.Date(2026, 11, 7, 18, 0, 5, 0, time.UTC).UnixMilli())
		result <- parallelPublicResult{b, e}
	}()
	return result
}
func awaitParallelPublic(t *testing.T, result <-chan parallelPublicResult) parallelPublicResult {
	t.Helper()
	select {
	case r := <-result:
		t.Cleanup(r.batch.close)
		return r
	case <-time.After(15 * time.Second):
		t.Fatal("public pipeline did not finish or drain")
		return parallelPublicResult{}
	}
}
func awaitParallelPublicStarts(t *testing.T, adapter *parallelPublicHistory, count int) []domain.FeedKey {
	t.Helper()
	keys := make([]domain.FeedKey, 0, count)
	for len(keys) < count {
		select {
		case key := <-adapter.started:
			keys = append(keys, key)
		case <-time.After(10 * time.Second):
			t.Fatal("independent public histories did not overlap")
			return nil
		}
	}
	return keys
}

func TestPublicAlertParallelHistoryAndEnrichmentBoundAcrossBatches(t *testing.T) {
	l, fake, adapter, gate := parallelPublicFixture(t, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := publicAlertTestCondition()
	results := []<-chan parallelPublicResult{runParallelPublic(l, ctx, c), runParallelPublic(l, ctx, c)}
	keys := awaitParallelPublicStarts(t, adapter, 2)
	seen := map[domain.FeedKey]bool{}
	for _, key := range keys {
		if seen[key] {
			t.Fatal("same-series history ran concurrently", keys)
		}
		seen[key] = true
	}
	data := l.publicAlertDataState()
	data.mu.Lock()
	processes := len(data.slots)
	data.mu.Unlock()
	if processes < 2 || processes > publicAlertConcurrency {
		t.Fatal("expected independent bounded calculator processes", processes)
	}
	close(gate)
	for _, result := range results {
		r := awaitParallelPublic(t, result)
		if r.err != nil || len(r.batch.Files) != 16 || len(r.batch.Failures) != 0 {
			t.Fatal(r.batch, r.err)
		}
		for _, name := range r.batch.Files {
			content, err := os.ReadFile(filepath.Join(r.batch.Directory, name))
			if err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(strings.NewReader(string(content))).ReadAll()
			if err != nil || len(rows) < 3 {
				t.Fatal("missing enriched candles", err)
			}
		}
	}
	adapter.mu.Lock()
	active, peak, calls := adapter.active, adapter.peak, adapter.calls
	adapter.mu.Unlock()
	if active != 0 || (peak < 2 || peak > publicAlertConcurrency) || calls != 16 {
		t.Fatalf("parallel/reuse bound active=%d peak=%d calls=%d", active, peak, calls)
	}
	fake.mu.Lock()
	catalogs := fake.catalogCalls
	fake.mu.Unlock()
	if catalogs != 1 {
		t.Fatal("concurrent checks duplicated source discovery", catalogs)
	}
	data.mu.Lock()
	slots, locks := len(data.slots), len(data.locks)
	data.mu.Unlock()
	if slots != 0 || locks != 0 {
		t.Fatal("public workers/ownership not released", slots, locks)
	}
}

func TestPublicAlertParallelPlansKeepImmutableEvaluationSnapshots(t *testing.T) {
	l, _, adapter, gate := parallelPublicFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := publicAlertTestCondition()
	first.Request.Conditions = first.Request.Conditions[:1]
	second := publicAlertTestCondition()
	second.ID = strings.Repeat("b", 32)
	second.Request.Conditions = second.Request.Conditions[:1]
	second.Request.Conditions[0].Right.Field = "sma_10"
	one := runParallelPublic(l, ctx, first)
	awaitParallelPublicStarts(t, adapter, 1)
	two := runParallelPublic(l, ctx, second)
	close(gate)
	r1, r2 := awaitParallelPublic(t, one), awaitParallelPublic(t, two)
	if r1.err != nil || r2.err != nil || len(r1.batch.Files) != 1 || len(r2.batch.Files) != 1 {
		t.Fatal(r1, r2)
	}
	if r1.batch.Directory == r2.batch.Directory {
		t.Fatal("concurrent checks share mutable evaluation directory")
	}
	for index, r := range []parallelPublicResult{r1, r2} {
		want, absent := "sma_3", "sma_10"
		if index == 1 {
			want, absent = absent, want
		}
		for _, name := range r.batch.Files {
			data, err := os.ReadFile(filepath.Join(r.batch.Directory, name))
			if err != nil {
				t.Fatal(err)
			}
			header, _, _ := strings.Cut(string(data), "\n")
			if !strings.Contains(header, want) || strings.Contains(header, absent) {
				t.Fatalf("another check overwrote plan %s: %s", want, header)
			}
		}
	}
}

func TestPublicAlertParallelCancellationDrainsWorkersAndCacheWaiters(t *testing.T) {
	l, _, adapter, _ := parallelPublicFixture(t, 8)
	ctx, cancel := context.WithCancel(context.Background())
	c := publicAlertTestCondition()
	results := []<-chan parallelPublicResult{runParallelPublic(l, ctx, c), runParallelPublic(l, ctx, c)}
	awaitParallelPublicStarts(t, adapter, 2)
	cancel()
	for _, result := range results {
		r := awaitParallelPublic(t, result)
		if !errors.Is(r.err, context.Canceled) {
			t.Fatal("cancelled public batch did not report cancellation", r.err)
		}
		if len(r.batch.Files) != 0 {
			t.Fatal("cancelled download published evaluation files", r.batch.Files)
		}
	}
	adapter.mu.Lock()
	active := adapter.active
	adapter.mu.Unlock()
	data := l.publicAlertDataState()
	data.mu.Lock()
	slots, locks := len(data.slots), len(data.locks)
	data.mu.Unlock()
	if active != 0 || slots != 0 || locks != 0 {
		t.Fatal("cancellation left active workers or ownership", active, slots, locks)
	}
}

func TestPublicAlertParallelProviderRefusalStopsAndDrainsBatch(t *testing.T) {
	l, fake, adapter, gate := parallelPublicFixture(t, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := runParallelPublic(l, ctx, publicAlertTestCondition())
	awaitParallelPublicStarts(t, adapter, 2)
	fake.mu.Lock()
	fake.fail = &exchange.Error{Kind: "rateLimited", Message: "explicit refusal"}
	fake.mu.Unlock()
	close(gate)
	r := awaitParallelPublic(t, result)
	if exchange.ErrorKind(r.err) != "rateLimited" {
		t.Fatal("lost typed provider refusal", r.err)
	}
	if len(r.batch.Files) != 0 || len(r.batch.Failures) == 0 {
		t.Fatal("refused data became an evaluation target", r.batch)
	}
	adapter.mu.Lock()
	active, calls := adapter.active, adapter.calls
	adapter.mu.Unlock()
	data := l.publicAlertDataState()
	data.mu.Lock()
	slots, locks := len(data.slots), len(data.locks)
	data.mu.Unlock()
	if active != 0 || slots != 0 || locks != 0 || calls > publicAlertConcurrency {
		t.Fatal("provider refusal did not stop and drain bounded requests", active, slots, locks, calls)
	}
}
