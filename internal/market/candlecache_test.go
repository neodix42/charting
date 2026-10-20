package market

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"cryptodesk/internal/candlecache"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

// This adapter supplies controlled provider responses at the existing public
// adapter boundary. The store and market logic exercised below are production.
type diskHistoryAdapter struct {
	fakeAdapter
	historyMu      sync.Mutex
	historyCalls   int
	historyBars    []domain.Candle
	historyError   error
	historyStarted chan struct{}
	historyRelease chan struct{}
}

func (a *diskHistoryAdapter) History(ctx context.Context, _ domain.FeedKey, _, _ int64, _ int) ([]domain.Candle, error) {
	a.historyMu.Lock()
	a.historyCalls++
	bars := append([]domain.Candle(nil), a.historyBars...)
	err := a.historyError
	started, release := a.historyStarted, a.historyRelease
	a.historyMu.Unlock()
	if started != nil {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return bars, err
}

func (a *diskHistoryAdapter) calls() int {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	return a.historyCalls
}

type diskCacheCall struct {
	key  domain.FeedKey
	bars []domain.Candle
}

// Writes normally use the real SQLite store. pauseWrites models an enabled cache
// that still contains an older snapshot while new accepted writes are queued.
type recordingDiskCache struct {
	store       *candlecache.Store
	mu          sync.Mutex
	writes      []diskCacheCall
	errors      []error
	pauseWrites bool
	rangeFn     func(domain.FeedKey, int64, int64, int) ([]domain.Candle, error)
}

func (c *recordingDiskCache) Range(key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if c.rangeFn != nil {
		return c.rangeFn(key, from, to, limit)
	}
	return c.store.Range(key, from, to, limit)
}

func (c *recordingDiskCache) Put(key domain.FeedKey, bars []domain.Candle) {
	if len(bars) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, diskCacheCall{key, append([]domain.Candle(nil), bars...)})
	if !c.pauseWrites {
		if err := c.store.Upsert(key, bars); err != nil {
			c.errors = append(c.errors, err)
		}
	}
}

func (c *recordingDiskCache) written(t *testing.T) []diskCacheCall {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.errors) > 0 {
		t.Fatal(c.errors)
	}
	return append([]diskCacheCall(nil), c.writes...)
}

func cacheTestBar(index int) domain.Candle {
	return domain.Candle{Time: 1700000040 + 60*int64(index), Open: 100, High: 102, Low: 99, Close: 101, Volume: float64(index + 1), Closed: true, Quality: "authoritative", SourceRevision: int64(index + 1)}
}

func newMarketDiskCache(t *testing.T) *recordingDiskCache {
	t.Helper()
	store, err := candlecache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return &recordingDiskCache{store: store}
}

func newDiskTestService(t *testing.T, cache CandleCache, adapter exchange.Adapter, now int64) *Service {
	t.Helper()
	s := New(Options{Cache: cache, Factory: func(string) (exchange.Adapter, error) { return adapter, nil }, Now: func() time.Time { return time.Unix(now, 0) }, Grace: time.Millisecond})
	t.Cleanup(s.Close)
	return s
}

func readDiskTest(t *testing.T, cache *recordingDiskCache, key domain.FeedKey) []domain.Candle {
	t.Helper()
	got, err := cache.store.Range(key, 0, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCandleDiskSnapshotIsMarkedCachedAndStartsRealStream(t *testing.T) {
	cache := newMarketDiskCache(t)
	old := cacheTestBar(0)
	old.SourceRevision = 9999999999999
	if err := cache.store.Upsert(key(), []domain.Candle{old}); err != nil {
		t.Fatal(err)
	}
	a := &diskHistoryAdapter{}
	s := newDiskTestService(t, cache, a, old.Time+600)
	snapshot, err := s.Attach("one", 1, key(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := old
	want.SourceRevision = 0
	if snapshot.Health.State != "cached" || len(snapshot.Bars) != 1 || snapshot.Bars[0] != want {
		t.Fatalf("disk snapshot was presented as fresh: %#v", snapshot)
	}
	eventually(t, func() bool { return started(&a.fakeAdapter, 1) })
	fresh := old
	fresh.Quality, fresh.SourceRevision, fresh.Volume = "live", 5, 3.25
	a.emit(fresh)
	after, err := s.Snapshot(snapshot.LeaseID)
	if err != nil || after.Health.State != "live" || len(after.Bars) != 1 || after.Bars[0] != fresh {
		t.Fatalf("old disk revision rejected new stream: %#v %v", after, err)
	}
	second, err := s.Attach("two", 2, key(), 0, 0)
	if err != nil || len(second.Bars) != 1 || second.Bars[0] != fresh {
		t.Fatalf("shared feed failed: %#v %v", second, err)
	}
	if got := readDiskTest(t, cache, key()); !reflect.DeepEqual(got, []domain.Candle{fresh}) {
		t.Fatalf("accepted fresh update was not durable: %#v", got)
	}
	if !started(&a.fakeAdapter, 1) {
		t.Fatal("disk restore created duplicate subscriptions")
	}
	s.Detach(snapshot.LeaseID)
	s.Detach(second.LeaseID)
	s.Close()
	if s.Diagnostics().Sockets != 0 {
		t.Fatal("stream not closed", s.Diagnostics())
	}
}

func TestCandleDiskSnapshotStillReportsProviderFailure(t *testing.T) {
	cache := newMarketDiskCache(t)
	c := cacheTestBar(0)
	if err := cache.store.Upsert(key(), []domain.Candle{c}); err != nil {
		t.Fatal(err)
	}
	a := &terminalThenLiveAdapter{terminalError: errors.New("public provider unavailable")}
	s := newDiskTestService(t, cache, a, c.Time+600)
	snapshot, err := s.Attach("one", 1, key(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		next, err := s.Snapshot(snapshot.LeaseID)
		return err == nil && next.Health.State == "offline" && next.Health.Reason == "public provider unavailable" && len(next.Bars) == 1
	})
	if len(cache.written(t)) != 0 {
		t.Fatal("cached bars were falsely repersisted as fresh provider output")
	}
}

func TestCandleDiskHistoryCompletenessAndLiveFallback(t *testing.T) {
	base := cacheTestBar(0).Time
	for _, tc := range []struct {
		name       string
		indices    []int
		from, to   int64
		limit      int
		open       bool
		wantREST   bool
		wantCached []int
	}{
		{"complete", []int{0, 1, 2}, base, base + 180, 10, false, false, []int{0, 1, 2}},
		{"latest bounded page", []int{0, 1, 2, 3, 4}, base, base + 300, 2, false, false, []int{3, 4}},
		{"unaligned lower bound", []int{1, 2}, base + 1, base + 180, 10, false, false, []int{1, 2}},
		{"partial upper bucket", []int{0, 1, 2}, base, base + 121, 10, false, false, []int{0, 1, 2}},
		{"sparse", []int{0, 2}, base, base + 180, 10, false, true, nil},
		{"missing oldest", []int{1, 2}, base, base + 180, 10, false, true, nil},
		{"missing newest", []int{0, 1}, base, base + 180, 10, false, true, nil},
		{"provisional", []int{0, 1, 2}, base, base + 180, 10, true, true, nil},
		{"current bucket", []int{10}, base + 600, base + 660, 10, false, true, nil},
		{"empty", nil, base, base + 180, 10, false, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := newMarketDiskCache(t)
			seed := make([]domain.Candle, 0, len(tc.indices))
			for _, index := range tc.indices {
				c := cacheTestBar(index)
				if tc.open && index == 1 {
					c.Closed = false
				}
				seed = append(seed, c)
			}
			if err := cache.store.Upsert(key(), seed); err != nil {
				t.Fatal(err)
			}
			fresh := cacheTestBar(0)
			fresh.Time = exchange.BucketStart(tc.to-1, 60)
			fresh.Volume = 123.5
			a := &diskHistoryAdapter{historyBars: []domain.Candle{fresh}}
			s := newDiskTestService(t, cache, a, base+600)
			got, err := s.History(context.Background(), "page", key(), tc.from, tc.to, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := 0
			if tc.wantREST {
				wantCalls = 1
				if !reflect.DeepEqual(got.Bars, []domain.Candle{fresh}) || len(cache.written(t)) != 1 {
					t.Fatalf("fallback response not accepted/persisted: %#v writes=%#v", got, cache.written(t))
				}
			} else {
				want := make([]domain.Candle, 0, len(tc.wantCached))
				for _, index := range tc.wantCached {
					c := cacheTestBar(index)
					c.SourceRevision = 0
					want = append(want, c)
				}
				if !reflect.DeepEqual(got.Bars, want) || len(cache.written(t)) != 0 {
					t.Fatalf("cache page: %#v want=%#v writes=%#v", got, want, cache.written(t))
				}
			}
			if a.calls() != wantCalls || int(s.Diagnostics().HistoryRequests) != wantCalls {
				t.Fatalf("REST calls=%d stats=%#v want=%d", a.calls(), s.Diagnostics(), wantCalls)
			}
			if !got.MoreHistory {
				t.Fatal("nonempty page cannot paginate further")
			}
		})
	}
}

func TestCandleDiskWeeklyHistoryUsesMondayBuckets(t *testing.T) {
	cache := newMarketDiskCache(t)
	k := key()
	k.Timeframe = "1w"
	const monday = int64(1792368000)
	const week = int64(604800)
	var seed []domain.Candle
	for i := 0; i < 3; i++ {
		c := cacheTestBar(i)
		c.Time = monday + int64(i)*week
		seed = append(seed, c)
	}
	if err := cache.store.Upsert(k, seed); err != nil {
		t.Fatal(err)
	}
	a := &diskHistoryAdapter{}
	s := newDiskTestService(t, cache, a, monday+10*week)
	got, err := s.History(context.Background(), "weekly", k, monday, monday+3*week, 2)
	if err != nil || a.calls() != 0 || len(got.Bars) != 2 || got.Bars[0].Time != monday+week || got.Bars[1].Time != monday+2*week {
		t.Fatalf("weekly disk buckets: %#v calls=%d err=%v", got, a.calls(), err)
	}
}

func TestCandleDiskReadFailureFallsBackToProvider(t *testing.T) {
	cache := newMarketDiskCache(t)
	cache.rangeFn = func(domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		return nil, errors.New("integrity checksum mismatch")
	}
	c := cacheTestBar(0)
	a := &diskHistoryAdapter{historyBars: []domain.Candle{c}}
	s := newDiskTestService(t, cache, a, c.Time+600)
	got, err := s.History(context.Background(), "page", key(), c.Time, c.Time+60, 1)
	if err != nil || a.calls() != 1 || len(got.Bars) != 1 || got.Bars[0] != c {
		t.Fatalf("cache read failure replaced real provider: %#v %v", got, err)
	}
}

func TestCandleDiskHistoryNeverOverwritesAlreadyFreshMemory(t *testing.T) {
	cache := newMarketDiskCache(t)
	cache.pauseWrites = true
	old := cacheTestBar(0)
	if err := cache.store.Upsert(key(), []domain.Candle{old}); err != nil {
		t.Fatal(err)
	}
	a := &diskHistoryAdapter{}
	s := newDiskTestService(t, cache, a, old.Time+600)
	initial, err := s.Attach("chart", 1, key(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return started(&a.fakeAdapter, 1) })
	fresh := old
	fresh.Quality, fresh.Volume, fresh.SourceRevision = "live", 123, 50
	a.emit(fresh)
	got, err := s.History(context.Background(), "same-range", key(), old.Time, old.Time+60, 1)
	if err != nil || !reflect.DeepEqual(got.Bars, []domain.Candle{fresh}) {
		t.Fatalf("old disk snapshot replaced an already accepted live candle: %#v %v", got, err)
	}
	snapshot, err := s.Snapshot(initial.LeaseID)
	if err != nil || snapshot.Bars[0] != fresh {
		t.Fatalf("stale disk data changed live feed: %#v %v", snapshot, err)
	}
	if a.calls() != 0 || len(cache.written(t)) != 1 {
		t.Fatalf("unexpected REST/persist after cache hit: %d %#v", a.calls(), cache.written(t))
	}
}

func TestCandleDiskPersistsOnlyAcceptedLiveChangesAndCorrections(t *testing.T) {
	cache := newMarketDiskCache(t)
	a := &diskHistoryAdapter{}
	s := newDiskTestService(t, cache, a, cacheTestBar(0).Time+600)
	initial, err := s.Attach("chart", 1, key(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return started(&a.fakeAdapter, 1) })
	fresh := cacheTestBar(0)
	fresh.Quality, fresh.Volume, fresh.SourceRevision = "live", 5, 10
	a.emit(fresh, fresh) // The duplicate should not enqueue another durable value.
	stale := fresh
	stale.Volume, stale.SourceRevision = 3, 9
	a.emit(stale)
	reopen := fresh
	reopen.Closed, reopen.SourceRevision = false, 11
	a.emit(reopen)
	invalid := fresh
	invalid.Volume = math.NaN()
	a.emit(invalid)
	correction := fresh
	correction.Quality, correction.SourceRevision, correction.Volume = "authoritative", 0, 2
	a.emit(correction)
	writes := cache.written(t)
	if len(writes) != 2 || !reflect.DeepEqual(writes[0].bars, []domain.Candle{fresh}) || !reflect.DeepEqual(writes[1].bars, []domain.Candle{correction}) {
		t.Fatalf("stale/invalid/duplicate rows reached disk: %#v", writes)
	}
	snapshot, err := s.Snapshot(initial.LeaseID)
	if err != nil || snapshot.Bars[0] != correction {
		t.Fatalf("authoritative correction not accepted: %#v %v", snapshot, err)
	}
	if got := readDiskTest(t, cache, key()); !reflect.DeepEqual(got, []domain.Candle{correction}) {
		t.Fatalf("disk correction mismatch: %#v", got)
	}
}

func TestCandleDiskRESTResponseCannotPersistOverRacingLiveUpdate(t *testing.T) {
	cache := newMarketDiskCache(t)
	c := cacheTestBar(0)
	a := &diskHistoryAdapter{historyBars: []domain.Candle{c}, historyStarted: make(chan struct{}), historyRelease: make(chan struct{})}
	s := newDiskTestService(t, cache, a, c.Time+600)
	initial, err := s.Attach("chart", 1, key(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return started(&a.fakeAdapter, 1) })
	type result struct {
		history History
		err     error
	}
	done := make(chan result, 1)
	go func() {
		h, err := s.History(context.Background(), "race", key(), c.Time, c.Time+60, 1)
		done <- result{h, err}
	}()
	<-a.historyStarted
	fresh := c
	fresh.Volume, fresh.SourceRevision, fresh.Quality = 35, 100, "live"
	a.emit(fresh)
	close(a.historyRelease)
	response := <-done
	if response.err != nil || !reflect.DeepEqual(response.history.Bars, []domain.Candle{fresh}) {
		t.Fatalf("stale REST beat racing live update: %#v %v", response.history, response.err)
	}
	if got := readDiskTest(t, cache, key()); !reflect.DeepEqual(got, []domain.Candle{fresh}) {
		t.Fatalf("stale REST persisted over current candle: %#v", got)
	}
	snapshot, err := s.Snapshot(initial.LeaseID)
	if err != nil || snapshot.Bars[0] != fresh {
		t.Fatalf("feed differs from durable value: %#v %v", snapshot, err)
	}
}

func TestCandleDiskCanceledReadDoesNotPublishCachedPage(t *testing.T) {
	cache := newMarketDiskCache(t)
	started, release := make(chan struct{}), make(chan struct{})
	c := cacheTestBar(0)
	cache.rangeFn = func(domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
		close(started)
		<-release
		return []domain.Candle{c}, nil
	}
	a := &diskHistoryAdapter{}
	s := newDiskTestService(t, cache, a, c.Time+600)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.History(ctx, "canceled", key(), c.Time, c.Time+60, 1)
		done <- err
	}()
	<-started
	cancel()
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled cache read returned a page: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled cache read retained")
	}
	if a.calls() != 0 || len(cache.written(t)) != 0 {
		t.Fatal("canceled cache read performed provider or persistence work")
	}
}

func TestCandleDiskEnablingSeedsSessionBarsWithoutNewSubscriptions(t *testing.T) {
	cache := newMarketDiskCache(t)
	cache.pauseWrites = true // Stand in for disabled persistence before enabling.
	a := &diskHistoryAdapter{}
	s := newDiskTestService(t, cache, a, cacheTestBar(0).Time+600)
	if _, err := s.Attach("chart", 1, key(), 0, 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return started(&a.fakeAdapter, 1) })
	first, second := cacheTestBar(0), cacheTestBar(1)
	a.emit(second, first)
	if got := readDiskTest(t, cache, key()); len(got) != 0 {
		t.Fatal("paused persistence wrote bars")
	}
	cache.mu.Lock()
	cache.pauseWrites = false
	cache.mu.Unlock()
	s.StoreCurrentCandles()
	if got := readDiskTest(t, cache, key()); !reflect.DeepEqual(got, []domain.Candle{first, second}) {
		t.Fatalf("enable did not seed existing session data: %#v", got)
	}
	if !started(&a.fakeAdapter, 1) || a.calls() != 0 {
		t.Fatal("enabling persistence restarted/refetched providers")
	}
	s.Close()
	before := len(cache.written(t))
	s.StoreCurrentCandles()
	if len(cache.written(t)) != before {
		t.Fatal("closed service seeded additional writes")
	}
}
