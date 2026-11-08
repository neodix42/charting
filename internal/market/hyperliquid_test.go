package market

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

func TestHyperliquidQuotesTravelThroughSnapshotAndEventsWithFeedIsolation(t *testing.T) {
	a := &mixedMarketAdapter{}
	var mu sync.Mutex
	var events []Event
	s := New(Options{Grace: time.Millisecond, Factory: func(string) (exchange.Adapter, error) { return a, nil }, Emit: func(e Event) { mu.Lock(); events = append(events, e); mu.Unlock() }})
	defer s.Close()
	spot := domain.FeedKey{Exchange: "hyperliquid", Market: "spot", Instrument: "@107", Timeframe: "1m"}
	perp := domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "HYPE", Timeframe: "1m"}
	first, err := s.Attach("spot", 1, spot, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Attach("perp", 9, perp, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.streams[spot] != nil && a.streams[perp] != nil })
	a.mu.Lock()
	emit, emitPerp := a.streams[spot], a.streams[perp]
	a.mu.Unlock()
	bid, ask := float64(23.4), float64(23.5)
	h := domain.Health{State: "live", Quote: &domain.Quote{Bid: &bid, Ask: &ask, Time: 1700000000000}}
	emit(nil, h)
	snap, err := s.Snapshot(first.LeaseID)
	if err != nil || snap.Health.Quote == nil || *snap.Health.Quote.Bid != bid {
		t.Fatal("quote absent from snapshot", snap, err)
	}
	joined, err := s.Attach("spot-two", 2, spot, 0, 0)
	if err != nil || joined.Health.Quote == nil || *joined.Health.Quote.Ask != ask {
		t.Fatal("quote absent at shared attach", joined, err)
	}
	untouched, _ := s.Snapshot(other.LeaseID)
	if untouched.Health.Quote != nil {
		t.Fatal("spot quote leaked to perpetual", untouched)
	}
	emitPerp(nil, domain.Health{State: "live", Quote: &domain.Quote{Ask: &ask, Time: 1700000001000}})
	mu.Lock()
	last := events[len(events)-1]
	mu.Unlock()
	if last.ChartID != "perp" || last.Generation != 9 || last.FeedKey != perp || last.Health.Quote == nil || last.Health.Quote.Bid != nil {
		t.Fatal("quote event lost feed identity or empty side", last)
	}
	data, err := json.Marshal(last)
	if err != nil || !strings.Contains(string(data), `"bid":null`) {
		t.Fatal("empty book side not encoded as null", string(data), err)
	}
	// Closing and reopening a chart must reject callbacks from the canceled feed.
	s.Detach(first.LeaseID)
	s.Detach(joined.LeaseID)
	eventually(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.stops[spot] == 1 })
	next, err := s.Attach("spot", 3, spot, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	emit(nil, domain.Health{State: "live", Quote: &domain.Quote{Time: 1}})
	after, _ := s.Snapshot(next.LeaseID)
	if after.Health.Quote != nil && after.Health.Quote.Time == 1 {
		t.Fatal("canceled stream quote replaced new generation", after)
	}
	emitPerp(nil, domain.Health{State: "disconnected"})
	after, _ = s.Snapshot(other.LeaseID)
	if after.Health.Quote != nil {
		t.Fatal("disconnected quote retained", after)
	}
}

// Discovery walks every currently advertised DEX at provider pacing, and needs
// more time than a single crypto category. Verify the actual service deadline.
type hyperliquidDeadlineAdapter struct {
	fakeAdapter
	remaining time.Duration
}

func (a *hyperliquidDeadlineAdapter) Instruments(ctx context.Context) ([]domain.Instrument, error) {
	deadline, _ := ctx.Deadline()
	a.remaining = time.Until(deadline)
	return []domain.Instrument{{NativeID: "BTC", Market: "perpetual"}}, nil
}
func TestHyperliquidDiscoveryAllowsCompleteDexCatalog(t *testing.T) {
	a := &hyperliquidDeadlineAdapter{}
	s := New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }})
	defer s.Close()
	if _, err := s.Instruments(context.Background(), "hyperliquid"); err != nil {
		t.Fatal(err)
	}
	if a.remaining < 4*time.Minute || a.remaining > 5*time.Minute {
		t.Fatal("unexpected catalog deadline", a.remaining)
	}
}

type hyperliquidHistoryDeadlineAdapter struct {
	fakeAdapter
	remaining time.Duration
}

func (a *hyperliquidHistoryDeadlineAdapter) History(ctx context.Context, _ domain.FeedKey, _, _ int64, _ int) ([]domain.Candle, error) {
	deadline, _ := ctx.Deadline()
	a.remaining = time.Until(deadline)
	return []domain.Candle{}, nil
}
func TestHyperliquidHistoryDeadlineIncludesWeightedRequestQueue(t *testing.T) {
	a := &hyperliquidHistoryDeadlineAdapter{}
	s := New(Options{Factory: func(string) (exchange.Adapter, error) { return a, nil }})
	defer s.Close()
	k := domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "xyz:XYZ100", Timeframe: "1w"}
	if _, err := s.History(context.Background(), "history", k, 1700000000, 1700001000, 1000); err != nil {
		t.Fatal(err)
	}
	if a.remaining < 4*time.Minute || a.remaining > 5*time.Minute {
		t.Fatal("unexpected paced history deadline", a.remaining)
	}
}

func TestHyperliquidHistoryMergesSparseRetainedBarsWithProviderPage(t *testing.T) {
	const day = int64(86400)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC).Unix()
	makeBar := func(ts int64, price float64) domain.Candle {
		return domain.Candle{Time: ts, Open: price, High: price + 1, Low: price - 1, Close: price, Volume: 1, Closed: true, Quality: "authoritative"}
	}
	retained := []domain.Candle{makeBar(start, 10), makeBar(start+day, 11)}
	cache := &recordingDiskCache{rangeFn: func(domain.FeedKey, int64, int64, int) ([]domain.Candle, error) { return retained, nil }, pauseWrites: true}
	adapter := &diskHistoryAdapter{historyBars: []domain.Candle{makeBar(start+day, 12), makeBar(start+2*day, 13)}}
	service := newDiskTestService(t, cache, adapter, start+10*day)
	key := domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1d"}
	got, err := service.History(context.Background(), "retained", key, start, start+3*day, 1000)
	if err != nil || len(got.Bars) != 3 || got.Bars[0].Close != 10 || got.Bars[1].Close != 12 || got.Bars[2].Close != 13 || adapter.calls() != 1 {
		t.Fatalf("sparse retained bars discarded: %+v %v calls=%d", got, err, adapter.calls())
	}
}
func TestHyperliquidSparseRetainedHistorySurvivesEmptyProviderPage(t *testing.T) {
	const day = int64(86400)
	start := time.Date(2020, 10, 19, 0, 0, 0, 0, time.UTC).Unix()
	bar := domain.Candle{Time: start, Open: 10, High: 12, Low: 9, Close: 11, Volume: 1, Closed: true, Quality: "authoritative"}
	cache := &recordingDiskCache{rangeFn: func(domain.FeedKey, int64, int64, int) ([]domain.Candle, error) { return []domain.Candle{bar}, nil }, pauseWrites: true}
	adapter := &diskHistoryAdapter{}
	service := newDiskTestService(t, cache, adapter, start+6000*day)
	key := domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1d"}
	got, err := service.History(context.Background(), "retained", key, start, start+30*day, 1000)
	if err != nil || len(got.Bars) != 1 || got.Bars[0].Time != start || adapter.calls() != 1 {
		t.Fatalf("retained page discarded after empty provider response: %+v %v calls=%d", got, err, adapter.calls())
	}
}
