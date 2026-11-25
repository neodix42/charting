package market

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

type snapshotHistoryAdapter struct {
	fakeAdapter
	reads   atomic.Int32
	active  atomic.Int32
	peak    atomic.Int32
	entered chan context.Context
	release chan struct{}
}

func (a *snapshotHistoryAdapter) History(ctx context.Context, _ domain.FeedKey, from, _ int64, _ int) ([]domain.Candle, error) {
	a.reads.Add(1)
	active := a.active.Add(1)
	defer a.active.Add(-1)
	for {
		peak := a.peak.Load()
		if active <= peak || a.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	if a.entered != nil {
		a.entered <- ctx
	}
	if a.release != nil {
		select {
		case <-a.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []domain.Candle{snapshotClosedBar(from)}, nil
}

func snapshotClosedBar(at int64) domain.Candle {
	return domain.Candle{Time: at, Open: 10, High: 12, Low: 9, Close: 11, Closed: true, Quality: "authoritative"}
}

func snapshotKey() domain.FeedKey {
	return domain.FeedKey{Exchange: "binance", Market: "spot", Instrument: "DCRUSDT", Timeframe: "1d"}
}

func TestHistorySnapshotIgnoresChartFeedCancellation(t *testing.T) {
	for _, paused := range []bool{true, false} {
		name := "chart closes during snapshot"
		if paused {
			name = "retained paused chart"
		}
		t.Run(name, func(t *testing.T) {
			a := &snapshotHistoryAdapter{entered: make(chan context.Context, 1), release: make(chan struct{})}
			s := New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }, Grace: time.Millisecond})
			defer s.Close()
			lease, err := s.Attach("ordinary-DCR-chart", 1, snapshotKey(), 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool { return started(&a.fakeAdapter, 1) })
			waitStopped := func() { eventually(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.stops == 1 }) }
			if paused {
				s.Detach(lease.LeaseID)
				waitStopped()
			}
			from := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC).Unix()
			done := make(chan error, 1)
			go func() {
				result, err := s.HistorySnapshot(context.Background(), "comparison-DCR", snapshotKey(), from, from+86400, 1)
				if err == nil && (len(result.Bars) != 1 || result.Bars[0] != snapshotClosedBar(from)) {
					err = errors.New("snapshot did not return the real provider candle")
				}
				done <- err
			}()
			select {
			case <-a.entered:
			case err := <-done:
				t.Fatalf("healthy snapshot inherited cancelled chart context: %v", err)
			case <-time.After(time.Second):
				t.Fatal("snapshot did not reach provider")
			}
			if !paused {
				s.Detach(lease.LeaseID)
				waitStopped()
			}
			select {
			case err := <-done:
				t.Fatalf("chart lifetime ended an independent snapshot: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			close(a.release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if a.reads.Load() != 1 || s.Diagnostics().ActiveLeases != 0 || s.Diagnostics().Sockets != 0 {
				t.Fatal("snapshot restarted or retained a chart subscription", a.reads.Load(), s.Diagnostics())
			}
		})
	}
}

func TestHistorySnapshotRespectsCallerAndServiceCancellation(t *testing.T) {
	for _, owner := range []string{"caller", "service"} {
		t.Run(owner, func(t *testing.T) {
			a := &snapshotHistoryAdapter{entered: make(chan context.Context, 1), release: make(chan struct{})}
			s := New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }})
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := s.HistorySnapshot(ctx, "snapshot", snapshotKey(), 1700000000, 1700001000, 1)
				done <- err
			}()
			select {
			case <-a.entered:
			case <-time.After(time.Second):
				t.Fatal("snapshot did not begin")
			}
			if owner == "caller" {
				cancel()
			} else {
				s.Close()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("snapshot ignored its actual owner", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled snapshot retained provider work")
			}
			if s.Diagnostics().ActiveLeases != 0 || s.Diagnostics().Sockets != 0 {
				t.Fatal("snapshot opened a subscription", s.Diagnostics())
			}
		})
	}
}

func TestHistorySnapshotReusesSharedCacheWithoutStreams(t *testing.T) {
	from := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC).Unix()
	a := &snapshotHistoryAdapter{}
	cache := &recordingDiskCache{rangeFn: func(key domain.FeedKey, first, last int64, limit int) ([]domain.Candle, error) {
		if key != snapshotKey() || first != from || last != from+86400-1 || limit != 1 {
			t.Fatalf("cache query changed identity or bounds: %+v %d %d %d", key, first, last, limit)
		}
		return []domain.Candle{snapshotClosedBar(from)}, nil
	}}
	s := New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }, Cache: cache, Now: func() time.Time { return time.Unix(from+86400, 0) }})
	defer s.Close()
	result, err := s.HistorySnapshot(context.Background(), "cache-only-snapshot", snapshotKey(), from, from+86400, 1)
	if err != nil || len(result.Bars) != 1 || result.Bars[0] != snapshotClosedBar(from) || a.reads.Load() != 0 || s.Diagnostics().ActiveLeases != 0 || s.Diagnostics().Sockets != 0 {
		t.Fatal("snapshot bypassed compatible cache or opened a feed", result, err, s.Diagnostics())
	}
}

func TestHistorySnapshotSharesPendingBoundsAndReleasesCancelledWaiters(t *testing.T) {
	a := &snapshotHistoryAdapter{entered: make(chan context.Context, 18), release: make(chan struct{})}
	s := New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }})
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 18)
	for n := 0; n < 18; n++ {
		go func() {
			_, err := s.HistorySnapshot(ctx, "bounded-snapshot", snapshotKey(), 1700000000, 1700001000, 1)
			done <- err
		}()
	}
	eventually(t, func() bool { return len(s.historySlots) == 18 })
	eventually(t, func() bool { return a.active.Load() == 2 })
	for name, read := range map[string]func(context.Context, string, domain.FeedKey, int64, int64, int) (History, error){"snapshot": s.HistorySnapshot, "chart": s.History} {
		if _, err := read(context.Background(), "excess", snapshotKey(), 1700000000, 1700001000, 1); exchange.ErrorKind(err) != "rateLimited" {
			t.Fatalf("%s bypassed shared request bound: %v", name, err)
		}
	}
	cancel()
	for n := 0; n < 18; n++ {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("queued snapshot ignored caller cancellation", err)
			}
		case <-time.After(time.Second):
			t.Fatal("snapshot retained a provider or pending slot")
		}
	}
	if len(s.historySlots) != 0 || a.active.Load() != 0 || a.peak.Load() != 2 || s.Diagnostics().ActiveLeases != 0 || s.Diagnostics().Sockets != 0 {
		t.Fatal("snapshot exceeded provider concurrency or retained work", a.reads.Load(), s.Diagnostics())
	}
}
