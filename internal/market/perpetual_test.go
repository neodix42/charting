package market

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

type mixedMarketAdapter struct {
	mu           sync.Mutex
	catalog      []domain.Instrument
	catalogCalls int
	streams      map[domain.FeedKey]func([]domain.Candle, domain.Health)
	starts       map[domain.FeedKey]int
	stops        map[domain.FeedKey]int
	historyKeys  []domain.FeedKey
}

func (a *mixedMarketAdapter) Instruments(context.Context) ([]domain.Instrument, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.catalogCalls++
	return append([]domain.Instrument(nil), a.catalog...), nil
}

func (a *mixedMarketAdapter) History(_ context.Context, k domain.FeedKey, _, _ int64, _ int) ([]domain.Candle, error) {
	a.mu.Lock()
	a.historyKeys = append(a.historyKeys, k)
	a.mu.Unlock()
	c := bar(1700000040, 4)
	if k.Market == "perpetual" {
		c.Volume = 8
	}
	c.Closed, c.Quality = true, "authoritative"
	return []domain.Candle{c}, nil
}

func (a *mixedMarketAdapter) Stream(ctx context.Context, k domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
	a.mu.Lock()
	if a.streams == nil {
		a.streams = map[domain.FeedKey]func([]domain.Candle, domain.Health){}
		a.starts, a.stops = map[domain.FeedKey]int{}, map[domain.FeedKey]int{}
	}
	a.streams[k] = emit
	a.starts[k]++
	a.mu.Unlock()
	<-ctx.Done()
	a.mu.Lock()
	delete(a.streams, k)
	a.stops[k]++
	a.mu.Unlock()
	return ctx.Err()
}

func (a *mixedMarketAdapter) emit(k domain.FeedKey, c domain.Candle) {
	a.mu.Lock()
	emit := a.streams[k]
	a.mu.Unlock()
	emit([]domain.Candle{c}, domain.Health{State: "live"})
}

func TestSpotAndPerpetualShareOnlyTheirOwnLeasesHistoryAndCache(t *testing.T) {
	cache := newMarketDiskCache(t)
	a := &mixedMarketAdapter{}
	var eventsMu sync.Mutex
	var events []Event
	s := New(Options{Cache: cache, Grace: time.Millisecond, Factory: func(string) (exchange.Adapter, error) { return a, nil }, Emit: func(e Event) {
		eventsMu.Lock()
		events = append(events, e)
		eventsMu.Unlock()
	}})
	t.Cleanup(s.Close)
	spot, perpetual := key(), key()
	perpetual.Market = "perpetual"
	keys := []domain.FeedKey{spot, perpetual, spot}
	charts := []string{"spot-one", "perpetual", "spot-two"}
	leases := make([]string, len(keys))
	for i, k := range keys {
		snapshot, err := s.Attach(charts[i], 1, k, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		leases[i] = snapshot.LeaseID
	}
	eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.starts[spot] == 1 && a.starts[perpetual] == 1
	})
	a.emit(spot, bar(1700000040, 3))
	a.emit(perpetual, bar(1700000040, 7))
	for i, volume := range []float64{3, 7, 3} {
		snapshot, err := s.Snapshot(leases[i])
		if err != nil || len(snapshot.Bars) != 1 || snapshot.Bars[0].Volume != volume {
			t.Fatalf("cross-market live data in %s: %#v %v", charts[i], snapshot, err)
		}
	}
	for _, k := range []domain.FeedKey{spot, perpetual} {
		h, err := s.History(context.Background(), k.Market, k, 1700000040, 1700000100, 1)
		if err != nil || h.FeedKey != k {
			t.Fatal("history lost market identity", h, err)
		}
	}
	for i, volume := range []float64{4, 8, 4} {
		snapshot, err := s.Snapshot(leases[i])
		if err != nil || len(snapshot.Bars) != 1 || snapshot.Bars[0].Volume != volume {
			t.Fatalf("cross-market history in %s: %#v %v", charts[i], snapshot, err)
		}
		if disk := readDiskTest(t, cache, keys[i]); len(disk) != 1 || disk[0].Volume != volume {
			t.Fatal("durable feed key lost market", keys[i], disk)
		}
	}
	eventsMu.Lock()
	for _, e := range events {
		for i, chart := range charts {
			if e.ChartID == chart && e.FeedKey != keys[i] {
				t.Fatal("event crossed market boundaries", e)
			}
		}
	}
	eventsMu.Unlock()
	if stats := s.Diagnostics(); stats.ActiveLeases != 3 || stats.UniqueFeeds != 2 || stats.CachedFeeds != 2 {
		t.Fatal("wrong feed sharing", stats)
	}
	s.Detach(leases[0])
	if stats := s.Diagnostics(); stats.ActiveLeases != 2 || stats.UniqueFeeds != 2 {
		t.Fatal("one spot lease detached its co-owner", stats)
	}
	s.Detach(leases[2])
	eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.stops[spot] == 1 && a.stops[perpetual] == 0
	})
	next := bar(1700000100, 9)
	a.emit(perpetual, next)
	if snapshot, err := s.Snapshot(leases[1]); err != nil || len(snapshot.Bars) != 2 || snapshot.Bars[1] != next {
		t.Fatal("detaching spot interrupted perpetual", snapshot, err)
	}
	s.Detach(leases[1])
	eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.stops[perpetual] == 1
	})
}

func TestInstrumentCacheKeepsSameSymbolAcrossMarketsAndRetriesPartialCatalog(t *testing.T) {
	spot := domain.Instrument{NativeID: "BTCUSDT", Market: "spot"}
	perpetual := domain.Instrument{NativeID: "BTCUSDT", Market: "perpetual"}
	partial := spot
	partial.ProviderMetadata = map[string]string{"catalogWarning": "Perpetual symbols unavailable; retry discovery"}
	a := &mixedMarketAdapter{catalog: []domain.Instrument{partial}}
	s := New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }})
	t.Cleanup(s.Close)
	for i := 0; i < 2; i++ {
		items, err := s.Instruments(context.Background(), "binance")
		if err != nil || !reflect.DeepEqual(items, []domain.Instrument{partial}) {
			t.Fatal("partial catalog unavailable or warning lost", items, err)
		}
	}
	a.mu.Lock()
	if a.catalogCalls != 2 {
		t.Fatal("partial discovery was cached for an hour", a.catalogCalls)
	}
	a.catalog = []domain.Instrument{spot, perpetual}
	a.mu.Unlock()
	for i := 0; i < 2; i++ {
		items, err := s.Instruments(context.Background(), "binance")
		if err != nil || !reflect.DeepEqual(items, []domain.Instrument{spot, perpetual}) {
			t.Fatal("same native symbol across markets collapsed", items, err)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.catalogCalls != 3 {
		t.Fatal("complete catalog was not cached", a.catalogCalls)
	}
}
