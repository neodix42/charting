package market

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"sync"
	"testing"
	"time"
)

type fakeAdapter struct {
	mu              sync.Mutex
	starts, stops   int
	callbacks       []func([]domain.Candle, domain.Health)
	historyStarted  chan struct{}
	historyContinue chan struct{}
}

func (a *fakeAdapter) Diagnostics() exchange.Diagnostics {
	a.mu.Lock()
	defer a.mu.Unlock()
	return exchange.Diagnostics{Sockets: a.starts - a.stops}
}
func (a *fakeAdapter) Instruments(context.Context) ([]domain.Instrument, error) {
	return []domain.Instrument{}, nil
}
func (a *fakeAdapter) History(ctx context.Context, k domain.FeedKey, from, to int64, n int) ([]domain.Candle, error) {
	if a.historyStarted != nil {
		close(a.historyStarted)
		select {
		case <-a.historyContinue:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []domain.Candle{bar(1700000000, 1)}, nil
}
func (a *fakeAdapter) Stream(ctx context.Context, k domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
	a.mu.Lock()
	a.starts++
	a.callbacks = append(a.callbacks, emit)
	a.mu.Unlock()
	<-ctx.Done()
	a.mu.Lock()
	a.stops++
	a.mu.Unlock()
	return ctx.Err()
}
func (a *fakeAdapter) emit(bars ...domain.Candle) {
	a.mu.Lock()
	cb := a.callbacks[len(a.callbacks)-1]
	a.mu.Unlock()
	cb(bars, domain.Health{State: "live"})
}
func bar(at int64, volume float64) domain.Candle {
	return domain.Candle{Time: at, Open: 10, High: 12, Low: 9, Close: 11, Volume: volume, SourceRevision: int64(volume), Quality: "live"}
}
func key() domain.FeedKey {
	return domain.FeedKey{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1m"}
}
func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	end := time.Now().Add(time.Second)
	for time.Now().Before(end) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not satisfied")
}
func testService(a *fakeAdapter, emit func(Event)) *Service {
	return New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }, Grace: 15 * time.Millisecond, Emit: emit})
}
func started(a *fakeAdapter, n int) bool { a.mu.Lock(); defer a.mu.Unlock(); return a.starts == n }
func TestLeaseSharingGraceAndSnapshotHandover(t *testing.T) {
	a := &fakeAdapter{}
	var mu sync.Mutex
	events := []Event{}
	s := testService(a, func(e Event) { mu.Lock(); events = append(events, e); mu.Unlock() })
	defer s.Close()
	x, err := s.Attach("a", 1, key(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return started(a, 1) })
	a.emit(bar(1700000000, 3), bar(1700000060, 1))
	y, _ := s.Attach("b", 9, key(), 0, 0)
	if y.Revision != 1 || len(y.Bars) != 2 {
		t.Fatal(y)
	}
	s.Detach(x.LeaseID)
	s.Detach(x.LeaseID)
	z, _ := s.Attach("c", 2, key(), 0, 0)
	if z.Revision != 1 || s.Diagnostics().UniqueFeeds != 1 {
		t.Fatal(z, s.Diagnostics())
	}
	a.emit(bar(1700000060, 5))
	snap, _ := s.Snapshot(z.LeaseID)
	if snap.Revision != 2 || snap.Bars[1].Volume != 5 {
		t.Fatal(snap)
	}
	mu.Lock()
	for _, e := range events {
		if e.ChartID == "b" && e.Revision <= y.Revision {
			t.Fatal("snapshot watermark replay", e)
		}
	}
	mu.Unlock()
	s.Detach(y.LeaseID)
	s.Detach(z.LeaseID)
	eventually(t, func() bool { return s.Diagnostics().Sockets == 0 })
	if s.Diagnostics().ActiveLeases != 0 {
		t.Fatal(s.Diagnostics())
	}
}
func TestCumulativeDuplicateStaleAndBoundaryBars(t *testing.T) {
	a := &fakeAdapter{}
	s := testService(a, nil)
	defer s.Close()
	l, _ := s.Attach("a", 1, key(), 0, 0)
	eventually(t, func() bool { return started(a, 1) })
	for _, v := range []float64{3, 5, 5, 3} {
		a.emit(bar(1700000000, v))
	}
	a.emit(bar(1700000060, 1))
	snap, _ := s.Snapshot(l.LeaseID)
	if len(snap.Bars) != 2 || snap.Bars[0].Volume != 5 {
		t.Fatal(snap)
	}
	c := bar(1700000000, 2)
	c.Quality = "authoritative"
	c.Closed = true
	a.emit(c)
	snap, _ = s.Snapshot(l.LeaseID)
	if snap.Bars[0].Volume != 2 {
		t.Fatal("authoritative correction must be able to reduce volume", snap)
	}
}
func TestInFlightHistoryCannotOverwriteNewLiveRevision(t *testing.T) {
	a := &fakeAdapter{historyStarted: make(chan struct{}), historyContinue: make(chan struct{})}
	s := testService(a, nil)
	defer s.Close()
	l, _ := s.Attach("a", 1, key(), 0, 0)
	eventually(t, func() bool { return started(a, 1) })
	done := make(chan History, 1)
	go func() {
		h, e := s.History(context.Background(), "r", key(), 1699999900, 1700001000, 500)
		if e != nil {
			panic(e)
		}
		done <- h
	}()
	<-a.historyStarted
	a.emit(bar(1700000000, 5))
	close(a.historyContinue)
	h := <-done
	if h.Bars[0].Volume != 5 {
		t.Fatal(h)
	}
	snap, _ := s.Snapshot(l.LeaseID)
	if snap.Bars[0].Volume != 5 {
		t.Fatal(snap)
	}
}
func TestTwoHundredTabSwitchesReleaseAllLeases(t *testing.T) {
	a := &fakeAdapter{}
	s := testService(a, nil)
	defer s.Close()
	for n := 0; n < 200; n++ {
		l, e := s.Attach("a", int64(n), key(), 0, 0)
		if e != nil {
			t.Fatal(e)
		}
		s.Detach(l.LeaseID)
	}
	eventually(t, func() bool { return s.Diagnostics().Sockets == 0 })
	d := s.Diagnostics()
	if d.ActiveLeases != 0 || d.CachedFeeds != 1 {
		t.Fatal(d)
	}
}

func TestSharedRestoreOnlyStartsOneRequestBeforeSemaphore(t *testing.T) {
	a := &fakeAdapter{historyStarted: make(chan struct{}), historyContinue: make(chan struct{})}
	s := testService(a, nil)
	defer s.Close()
	leases := []string{}
	for n := 0; n < 9; n++ {
		snapshot, err := s.Attach(domain.ID(), int64(n), key(), 1699999900, 1700001000)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, snapshot.LeaseID)
	}
	<-a.historyStarted
	if s.Diagnostics().HistoryRequests != 1 {
		t.Fatal("identical restores did not coalesce", s.Diagnostics())
	}
	close(a.historyContinue)
	eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.feeds[key()].loaded) == 1 })
	for _, id := range leases {
		s.Detach(id)
	}
	for n := 0; n < 200; n++ {
		snapshot, err := s.Attach("cached", int64(n), key(), 1699999900, 1700001000)
		if err != nil {
			t.Fatal(err)
		}
		s.Detach(snapshot.LeaseID)
	}
	if s.Diagnostics().HistoryRequests != 1 {
		t.Fatal("cached tab switching refetched history", s.Diagnostics())
	}
}
