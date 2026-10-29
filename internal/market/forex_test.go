package market

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"reflect"
	"testing"
	"time"
)

func forexKey() domain.FeedKey {
	return domain.FeedKey{Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Timeframe: "1h"}
}

func TestForexHistoryRemembersSparseAndEmptyClosedRangesWithoutInventingCandles(t *testing.T) {
	friday := time.Date(2026, 10, 23, 20, 0, 0, 0, time.UTC).Unix()
	bar := domain.Candle{Time: friday, Open: 1.1, High: 1.2, Low: 1, Close: 1.15, Volume: 3, Closed: true, Quality: "authoritative"}
	a := &diskHistoryAdapter{historyBars: []domain.Candle{bar}}
	s := newDiskTestService(t, nil, a, friday+5*86400)
	key := forexKey()
	for i := 0; i < 2; i++ {
		page, err := s.History(context.Background(), "weekend", key, friday, friday+3*86400, 1000)
		if err != nil || !reflect.DeepEqual(page.Bars, []domain.Candle{bar}) {
			t.Fatalf("sparse history: %+v %v", page, err)
		}
	}
	if a.calls() != 1 {
		t.Fatal("legitimate weekend gaps were repeatedly fetched", a.calls())
	}
	a.historyMu.Lock()
	a.historyBars = nil
	a.historyMu.Unlock()
	for i := 0; i < 2; i++ {
		page, err := s.History(context.Background(), "closed", key, friday+86400, friday+2*86400, 1000)
		if err != nil || len(page.Bars) != 0 {
			t.Fatalf("closed-market page: %+v %v", page, err)
		}
	}
	if a.calls() != 2 {
		t.Fatal("empty checked range was not remembered", a.calls())
	}
	if !s.ForgetProvider("dukascopy") {
		t.Fatal("idle provider could not be invalidated")
	}
	if _, err := s.History(context.Background(), "new-account", key, friday, friday+3*86400, 1000); err != nil {
		t.Fatal(err)
	}
	if a.calls() != 3 {
		t.Fatal("account change reused old checked coverage")
	}
}

func TestForexOpenCandlesNeverUsePastQueryCoverage(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 30, 0, 0, time.UTC).Unix()
	a := &diskHistoryAdapter{}
	s := newDiskTestService(t, nil, a, now)
	for i := 0; i < 2; i++ {
		if _, err := s.History(context.Background(), "current", forexKey(), now-3600, now, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if a.calls() != 2 {
		t.Fatal("current partial range was treated as complete")
	}
}

func TestForexAccountChangeDropsInflightHistoryAndProtectsActiveLeases(t *testing.T) {
	bar := domain.Candle{Time: 1700002800, Open: 1, High: 2, Low: 1, Close: 2, Closed: true}
	a := &diskHistoryAdapter{historyBars: []domain.Candle{bar}, historyStarted: make(chan struct{}), historyRelease: make(chan struct{})}
	s := newDiskTestService(t, nil, a, bar.Time+3*86400)
	done := make(chan error, 1)
	go func() {
		_, err := s.History(context.Background(), "old-account", forexKey(), bar.Time, bar.Time+3600, 100)
		done <- err
	}()
	<-a.historyStarted
	if !s.ForgetProvider("dukascopy") {
		t.Fatal("idle account change blocked")
	}
	close(a.historyRelease)
	if err := <-done; err == nil {
		t.Fatal("old account data was published after credential change")
	}
	if len(s.forexPages) != 0 {
		t.Fatal("old account repopulated history coverage")
	}
	// Active chart leases, even while startup is pending, prohibit credentials
	// from changing beneath an existing feed.
	snap, err := s.Attach("forex-chart", 1, forexKey(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !s.ProviderActive("dukascopy") || s.ForgetProvider("dukascopy") {
		t.Fatal("active forex provider was discarded")
	}
	s.Detach(snap.LeaseID)
	if !s.ForgetProvider("dukascopy") {
		t.Fatal("released chart retained account data")
	}
}

func TestFirstProviderConfigurationRestartsExistingLeaseAndRejectsOldWorker(t *testing.T) {
	a := &fakeAdapter{}
	s := testService(a, nil)
	defer s.Close()
	snapshot, err := s.Attach("unconfigured", 1, forexKey(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return started(a, 1) })
	a.mu.Lock()
	oldCallback := a.callbacks[0]
	a.mu.Unlock()
	s.RestartUnconfiguredProvider("dukascopy")
	eventually(t, func() bool { return started(a, 2) })
	oldCallback([]domain.Candle{bar(1700000000, 1)}, domain.Health{State: "restricted"})
	a.emit(bar(1700000060, 2))
	actual, err := s.Snapshot(snapshot.LeaseID)
	if err != nil || len(actual.Bars) != 1 || actual.Bars[0].Time != 1700000060 || actual.Health.State != "live" || s.Diagnostics().ActiveLeases != 1 {
		t.Fatalf("first configuration did not recover the original lease: %+v %v", actual, err)
	}
}

type delayedForexCatalog struct {
	fakeAdapter
	requested chan struct{}
	proceed   chan struct{}
}

func (a *delayedForexCatalog) Instruments(context.Context) ([]domain.Instrument, error) {
	a.requested <- struct{}{}
	<-a.proceed
	return []domain.Instrument{{Market: "forex", NativeID: "EURUSD"}}, nil
}

func TestCatalogInvalidationRejectsStaleDiscoveryWithoutDiscardingSameAccountHistory(t *testing.T) {
	catalog := &delayedForexCatalog{requested: make(chan struct{}, 1), proceed: make(chan struct{})}
	s := New(Options{Factory: func(string) (exchange.Adapter, error) { return catalog, nil }})
	defer s.Close()
	done := make(chan error, 1)
	go func() { _, err := s.Instruments(context.Background(), "dukascopy"); done <- err }()
	<-catalog.requested
	s.InvalidateInstruments("dukascopy")
	close(catalog.proceed)
	if err := <-done; err == nil {
		t.Fatal("obsolete discovery was accepted")
	}
	if items, err := s.Instruments(context.Background(), "dukascopy"); err != nil || len(items) != 1 {
		t.Fatalf("fresh catalog: %+v %v", items, err)
	}

	bar := domain.Candle{Time: 1700002800, Open: 1, High: 2, Low: 1, Close: 2, Closed: true}
	history := &diskHistoryAdapter{historyBars: []domain.Candle{bar}, historyStarted: make(chan struct{}), historyRelease: make(chan struct{})}
	h := newDiskTestService(t, nil, history, bar.Time+86400)
	go func() {
		_, err := h.History(context.Background(), "same-account", forexKey(), bar.Time, bar.Time+3600, 100)
		done <- err
	}()
	<-history.historyStarted
	h.InvalidateInstruments("dukascopy")
	close(history.historyRelease)
	if err := <-done; err != nil {
		t.Fatal("catalog refresh discarded same-account historical data", err)
	}
}

// Ensure the forex semaphore is populated: a valid provider must not block on a
// nil request channel just because the public-provider registry predates it.
var _ exchange.Adapter = (*diskHistoryAdapter)(nil)
