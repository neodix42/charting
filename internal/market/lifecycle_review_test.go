package market

import (
	"context"
	"errors"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

type terminalThenLiveAdapter struct {
	fakeAdapter
	terminalError error
}

func (a *terminalThenLiveAdapter) Stream(ctx context.Context, k domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
	a.mu.Lock()
	a.starts++
	attempt := a.starts
	a.callbacks = append(a.callbacks, emit)
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.stops++; a.mu.Unlock() }()
	if attempt == 1 {
		return a.terminalError
	}
	<-ctx.Done()
	return ctx.Err()
}
func TestImmediateRetryRestartsTerminalStreamInsideGrace(t *testing.T) {
	for name, terminal := range map[string]error{"failure": errors.New("temporarily unavailable"), "unexpected clean end": nil} {
		t.Run(name, func(t *testing.T) {
			a := &terminalThenLiveAdapter{terminalError: terminal}
			s := New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }, Grace: time.Hour})
			defer s.Close()
			first, err := s.Attach("chart", 1, key(), 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool {
				snapshot, err := s.Snapshot(first.LeaseID)
				return err == nil && (snapshot.Health.State == exchange.ErrorKind(terminal) || snapshot.Health.State == "disconnected")
			})
			s.Detach(first.LeaseID)
			second, err := s.Attach("chart", 2, key(), 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.starts == 2 })
			a.emit(bar(1700000000, 3))
			actual, err := s.Snapshot(second.LeaseID)
			if err != nil || len(actual.Bars) != 1 || actual.Health.State != "live" {
				t.Fatal("retry did not resume real updates", actual, err)
			}
		})
	}
}

type canceledHistoryAdapter struct {
	fakeAdapter
	historyCalls chan struct{}
}

func (a *canceledHistoryAdapter) History(ctx context.Context, k domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	a.historyCalls <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestPaginationCancellationFollowsLastSharedLease(t *testing.T) {
	a := &canceledHistoryAdapter{historyCalls: make(chan struct{}, 1)}
	s := New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }, Grace: 15 * time.Millisecond})
	defer s.Close()
	first, err := s.Attach("one", 1, key(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Attach("two", 1, key(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.History(context.Background(), "pagination", key(), 1699999900, 1700001000, 500)
		done <- err
	}()
	<-a.historyCalls
	s.Detach(first.LeaseID)
	select {
	case err := <-done:
		t.Fatal("shared active request canceled after only one lease detached", err)
	case <-time.After(30 * time.Millisecond):
	}
	s.Detach(second.LeaseID)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("inactive pagination request retained after grace")
	}
}
func TestPendingHistoryBoundAndCloseCancellation(t *testing.T) {
	a := &canceledHistoryAdapter{historyCalls: make(chan struct{}, 18)}
	s := New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }})
	defer s.Close()
	done := make(chan error, 18)
	for n := 0; n < 18; n++ {
		go func() {
			_, err := s.History(context.Background(), "history", key(), 1699999900, 1700001000, 500)
			done <- err
		}()
	}
	eventually(t, func() bool { return len(s.historySlots) == 18 })
	if _, err := s.History(context.Background(), "excess", key(), 1699999900, 1700001000, 500); exchange.ErrorKind(err) != "rateLimited" {
		t.Fatal("unbounded history work accepted", err)
	}
	s.Close()
	for n := 0; n < 18; n++ {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("close did not cancel history", err)
			}
		case <-time.After(time.Second):
			t.Fatal("history retained after service close")
		}
	}
	if _, err := s.History(context.Background(), "after-close", key(), 1699999900, 1700001000, 500); err == nil {
		t.Fatal("closed service accepted more work")
	}
}

func TestResyncDiagnosticsCountTransitionsNotHeartbeats(t *testing.T) {
	a := &fakeAdapter{}
	s := testService(a, nil)
	defer s.Close()
	if _, err := s.Attach("chart", 1, key(), 0, 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return started(a, 1) })
	a.mu.Lock()
	emit := a.callbacks[0]
	a.mu.Unlock()
	for _, health := range []domain.Health{{State: "live"}, {State: "reconciling", Reason: "Stream buffer overflow; repairing affected candles"}, {State: "reconciling", Reason: "Repair in progress"}, {State: "reconciling", Reason: "Repair in progress"}, {State: "live"}, {State: "reconciling", Reason: "Connection sequence gap"}} {
		emit(nil, health)
	}
	if actual := s.Diagnostics().Resyncs; actual != 2 {
		t.Fatal("resync count must represent transitions", actual)
	}
}
func TestEvictedHistoricalRangeCanBeRestoredAgain(t *testing.T) {
	a := &fakeAdapter{}
	s := testService(a, nil)
	defer s.Close()
	first := int64(1700000000)
	f := &feed{key: key(), bars: map[int64]domain.Candle{}, changed: map[int64]int64{}, loaded: map[historyRange]bool{}}
	evictedRange := historyRange{first - 100, first + 100}
	retainedRange := historyRange{first + 1000*60, first + 1001*60}
	f.loaded[evictedRange] = true
	f.loaded[retainedRange] = true
	for n := 0; n < 10001; n++ {
		at := first + int64(n)*60
		f.bars[at] = bar(at, 1)
		f.changed[at] = int64(n + 1)
	}
	s.mu.Lock()
	s.trimFeedLocked(f)
	s.mu.Unlock()
	if len(f.bars) != 10000 {
		t.Fatal("cache capacity exceeded")
	}
	if f.loaded[evictedRange] {
		t.Fatal("evicted historical range still suppresses refetch")
	}
	if !f.loaded[retainedRange] {
		t.Fatal("unaffected cached range unnecessarily invalidated")
	}
}

func TestDisjointCachedBarsDoNotSuppressMissingHistoricalWindow(t *testing.T) {
	a := &fakeAdapter{historyStarted: make(chan struct{}), historyContinue: make(chan struct{})}
	s := testService(a, nil)
	defer s.Close()
	if _, err := s.Attach("live", 1, key(), 0, 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return started(a, 1) })
	// Two previously loaded islands bracket a viewport which has never been loaded.
	a.emit(bar(1690000000, 1), bar(1710000000, 1))
	if _, err := s.Attach("historical", 1, key(), 1699999900, 1700001000); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.historyStarted:
	case <-time.After(time.Second):
		t.Fatal("min/max cached timestamps incorrectly implied interior history coverage")
	}
	close(a.historyContinue)
}
