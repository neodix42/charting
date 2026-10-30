// Package market owns shared feed leases, bounded caches and bridge revisions.
// It deliberately has no dependency on Wails or a browser.
package market

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

type Snapshot struct {
	LeaseID  string          `json:"leaseId"`
	Revision int64           `json:"revision"`
	Bars     []domain.Candle `json:"bars"`
	Health   domain.Health   `json:"health"`
}
type Event struct {
	LeaseID    string          `json:"leaseId"`
	ChartID    string          `json:"chartId"`
	Generation int64           `json:"generation"`
	FeedKey    domain.FeedKey  `json:"feedKey"`
	Revision   int64           `json:"revision"`
	Kind       string          `json:"kind"`
	Bars       []domain.Candle `json:"bars"`
	Health     domain.Health   `json:"health"`
}
type History struct {
	RequestID   string          `json:"requestId"`
	FeedKey     domain.FeedKey  `json:"feedKey"`
	Bars        []domain.Candle `json:"bars"`
	MoreHistory bool            `json:"moreHistory"`
}
type Diagnostics struct {
	ActiveLeases    int   `json:"activeLeases"`
	UniqueFeeds     int   `json:"uniqueFeeds"`
	CachedFeeds     int   `json:"cachedFeeds"`
	CachedCandles   int   `json:"cachedCandles"`
	HistoryRequests int64 `json:"historyRequests"`
	QueueDepth      int   `json:"queueDepth"`
	Resyncs         int64 `json:"resyncs"`
	Sockets         int   `json:"sockets"`
	Reconnects      int64 `json:"reconnects"`
	RESTRequests    int64 `json:"restRequests"`
}
type Options struct {
	Grace      time.Duration
	Factory    func(string) (exchange.Adapter, error)
	Emit       func(Event)
	Now        func() time.Time
	MaxCandles int
	Cache      CandleCache
}

// CandleCache uses inclusive disk bounds; the bridge uses half-open history
// bounds. Put must preserve call order and drain accepted writes on shutdown.
type CandleCache interface {
	Range(domain.FeedKey, int64, int64, int) ([]domain.Candle, error)
	Put(domain.FeedKey, []domain.Candle)
}
type lease struct {
	id, chart  string
	generation int64
	f          *feed
}
type feed struct {
	key      domain.FeedKey
	bars     map[int64]domain.Candle
	changed  map[int64]int64
	revision int64
	health   domain.Health
	owners   map[string]*lease
	cancel   context.CancelFunc
	timer    *time.Timer
	used     time.Time
	ctx      context.Context
	loading  map[historyRange]context.Context
	loaded   map[historyRange]bool
}
type instrumentCache struct {
	items []domain.Instrument
	at    time.Time
}
type historyRange struct{ from, to int64 }
type Service struct {
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	opts           Options
	feeds          map[domain.FeedKey]*feed
	leases         map[string]*lease
	adapters       map[string]exchange.Adapter
	instruments    map[string]instrumentCache
	providerEpochs map[string]uint64
	catalogEpochs  map[string]uint64
	forexPages     []forexHistoryPage
	semaphores     map[string]chan struct{}
	requests       int64
	resyncs        int64
	closed         bool
	restoreSlots   chan struct{}
	historySlots   chan struct{}
	wg             sync.WaitGroup
}

func New(opts Options) *Service {
	if opts.Grace == 0 {
		opts.Grace = 2 * time.Second
	}
	if opts.Factory == nil {
		opts.Factory = exchange.New
	}
	if opts.Emit == nil {
		opts.Emit = func(Event) {}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MaxCandles == 0 {
		opts.MaxCandles = 250000
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{ctx: ctx, cancel: cancel, opts: opts, feeds: make(map[domain.FeedKey]*feed), leases: make(map[string]*lease), adapters: make(map[string]exchange.Adapter), instruments: make(map[string]instrumentCache), providerEpochs: make(map[string]uint64), catalogEpochs: make(map[string]uint64), restoreSlots: make(chan struct{}, 18), historySlots: make(chan struct{}, 18), semaphores: map[string]chan struct{}{"binance": make(chan struct{}, 2), "bybit": make(chan struct{}, 2), "coinbase": make(chan struct{}, 2), "dukascopy": make(chan struct{}, 2)}}
}
func (s *Service) adapterLocked(provider string) (exchange.Adapter, error) {
	if a := s.adapters[provider]; a != nil {
		return a, nil
	}
	a, err := s.opts.Factory(provider)
	if err == nil {
		s.adapters[provider] = a
	}
	return a, err
}
func (s *Service) Attach(chart string, generation int64, key domain.FeedKey, from, to int64) (Snapshot, error) {
	if err := key.Validate(); err != nil {
		return Snapshot{}, err
	}
	if chart == "" || len(chart) > 128 || generation < 0 {
		return Snapshot{}, errors.New("invalid chart identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Snapshot{}, errors.New("market service closed")
	}
	if len(s.leases) >= 18 {
		return Snapshot{}, errors.New("too many active chart leases")
	}
	a, err := s.adapterLocked(key.Exchange)
	if err != nil {
		return Snapshot{}, err
	}
	f := s.feeds[key]
	if f == nil {
		f = &feed{key: key, bars: map[int64]domain.Candle{}, changed: map[int64]int64{}, owners: map[string]*lease{}, health: domain.Health{State: "loading"}}
		if s.opts.Cache != nil {
			cached, _ := s.opts.Cache.Range(key, 0, 0, 1000)
			if from > 0 && to > from && to < s.opts.Now().Unix()-2*exchange.TimeframeSeconds(key.Timeframe) {
				cached, _ = s.opts.Cache.Range(key, from, to-1, 1000)
			}
			for _, c := range cached {
				// Provider revisions restart with each connection/process. A stale
				// disk revision must never reject this session's live corrections.
				c.SourceRevision = 0
				f.bars[c.Time] = c
			}
			if len(f.bars) > 0 {
				reason := "Locally stored candles; refreshing public market data"
				if key.Exchange == "dukascopy" {
					reason = "Locally stored Dukascopy Bid candles; reconnecting to the configured account"
				}
				f.health = domain.Health{State: "cached", Reason: reason}
			}
		}
		s.feeds[key] = f
	}
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	f.used = s.opts.Now()
	id := domain.ID()
	l := &lease{id: id, chart: chart, generation: generation, f: f}
	s.leases[id] = l
	f.owners[id] = l
	if f.cancel == nil {
		ctx, cancel := context.WithCancel(s.ctx)
		f.ctx = ctx
		f.cancel = cancel
		s.wg.Add(1)
		go s.run(ctx, f, a)
	}
	s.restoreLocked(f, from, to)
	return s.snapshotLocked(l), nil
}

// Only request an initial window once per range. Reattaching cached live charts
// must not enqueue another REST task on every tab switch. The active lease cap
// also bounds waiting restore work before provider semaphores.
func (s *Service) restoreLocked(f *feed, from, to int64) {
	if from <= 0 || to <= from {
		return
	}
	step := exchange.TimeframeSeconds(f.key.Timeframe)
	if len(f.bars) > 0 && to >= s.opts.Now().Unix()-2*step {
		return
	}
	r := historyRange{exchange.BucketStart(from, step), exchange.BucketStart(to, step)}
	if f.loading == nil {
		f.loading = map[historyRange]context.Context{}
		f.loaded = map[historyRange]bool{}
	}
	if ctx := f.loading[r]; ctx != nil && ctx.Err() == nil {
		return
	}
	if f.loaded[r] {
		return
	}
	select {
	case s.restoreSlots <- struct{}{}:
	default:
		return
	}
	ctx := f.ctx
	f.loading[r] = ctx
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { <-s.restoreSlots }()
		_, err := s.history(ctx, "restore", f.key, from, to, 1000)
		s.mu.Lock()
		defer s.mu.Unlock()
		if f.loading[r] != ctx {
			return
		}
		delete(f.loading, r)
		if err == nil {
			if len(f.loaded) >= 32 {
				f.loaded = map[historyRange]bool{}
			}
			f.loaded[r] = true
		}
	}()
}
func (s *Service) run(ctx context.Context, f *feed, a exchange.Adapter) {
	defer s.wg.Done()
	err := a.Stream(ctx, f.key, func(bars []domain.Candle, h domain.Health) { s.update(ctx, f, bars, h) })
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || ctx.Err() != nil || f.ctx != ctx {
		return
	}
	// A terminal provider failure leaves leases visible for Retry, but cannot
	// leave a dead worker marked as running. Publish the error only after
	// clearing its handle, so an immediate Retry inside the grace period starts
	// a new worker instead of reviving the already-ended subscription.
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
	state, reason := "disconnected", "Public market stream ended; retry the feed"
	if err != nil {
		state, reason = exchange.ErrorKind(err), err.Error()
	}
	f.health = domain.Health{State: state, Reason: reason, LastMessageAt: f.health.LastMessageAt, LastTradeAt: f.health.LastTradeAt}
	s.emitLocked(f, nil, "health")
}
func (s *Service) snapshotLocked(l *lease) Snapshot {
	return Snapshot{LeaseID: l.id, Revision: l.f.revision, Bars: sorted(l.f.bars), Health: l.f.health}
}
func (s *Service) Snapshot(id string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.leases[id]
	if l == nil {
		return Snapshot{}, errors.New("lease expired")
	}
	return s.snapshotLocked(l), nil
}
func (s *Service) Detach(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.leases[id]
	if l == nil {
		return
	}
	delete(s.leases, id)
	f := l.f
	delete(f.owners, id)
	if len(f.owners) == 0 {
		f.timer = time.AfterFunc(s.opts.Grace, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if len(f.owners) != 0 {
				return
			}
			if f.cancel != nil {
				f.cancel()
				f.cancel = nil
			}
			f.timer = nil
			f.health.State = "paused"
			s.trimLocked()
		})
	}
}

// emitLocked serializes revision delivery with snapshot capture. Emit must enqueue
// synchronously and must never call back into Service (Wails EventsEmit satisfies this).
func (s *Service) emitLocked(f *feed, bars []domain.Candle, kind string) {
	f.revision++
	for _, l := range f.owners {
		s.opts.Emit(Event{LeaseID: l.id, ChartID: l.chart, Generation: l.generation, FeedKey: f.key, Revision: f.revision, Kind: kind, Bars: bars, Health: f.health})
	}
}
func (s *Service) update(ctx context.Context, f *feed, bars []domain.Candle, h domain.Health) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || ctx.Err() != nil || f.ctx != ctx {
		return
	}
	if h.State != "" {
		if h.State == "reconciling" && f.health.State != "reconciling" {
			s.resyncs++
		}
		f.health = h
	}
	updates := make([]domain.Candle, 0, len(bars))
	for _, c := range bars {
		if !valid(c) {
			continue
		}
		old, ok := f.bars[c.Time]
		if ok && c.SourceRevision > 0 && old.SourceRevision > c.SourceRevision && (c.Quality != "authoritative" || f.key.Exchange == "dukascopy") {
			continue
		}
		if ok && old.Closed && !c.Closed && c.Quality != "authoritative" {
			continue
		}
		if ok && old == c {
			continue
		}
		f.bars[c.Time] = c
		f.changed[c.Time] = f.revision + 1
		updates = append(updates, c)
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].Time < updates[j].Time })
	kind := "health"
	if len(updates) > 0 {
		kind = "bars"
	}
	s.emitLocked(f, updates, kind)
	if s.opts.Cache != nil {
		s.opts.Cache.Put(f.key, updates)
	}
	s.trimFeedLocked(f)
	s.trimLocked()
}
func (s *Service) Instruments(ctx context.Context, provider string) ([]domain.Instrument, error) {
	s.mu.Lock()
	epoch := s.providerEpochs[provider]
	catalogEpoch := s.catalogEpochs[provider]
	cached := s.instruments[provider]
	if len(cached.items) > 0 && s.opts.Now().Sub(cached.at) < time.Hour {
		items := append([]domain.Instrument(nil), cached.items...)
		s.mu.Unlock()
		return items, nil
	}
	a, err := s.adapterLocked(provider)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	timeout := 20 * time.Second
	if provider == "dukascopy" {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	items, err := a.Instruments(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ProviderMetadata["catalogWarning"] != "" {
			// A partial catalog remains useful, but retry must recheck the market
			// endpoint that failed rather than reuse the partial result for an hour.
			return items, nil
		}
	}
	s.mu.Lock()
	if s.closed || s.providerEpochs[provider] != epoch || s.catalogEpochs[provider] != catalogEpoch {
		s.mu.Unlock()
		return nil, errors.New("Provider configuration changed; retry instrument discovery")
	}
	s.instruments[provider] = instrumentCache{append([]domain.Instrument(nil), items...), s.opts.Now()}
	s.mu.Unlock()
	return items, nil
}
func (s *Service) History(ctx context.Context, id string, key domain.FeedKey, from, to int64, limit int) (History, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return History{}, errors.New("market service closed")
	}
	// Bound waiting callers as well as active HTTP requests. Rapid tab changes
	// may abandon frontend promises before their provider semaphore is available.
	select {
	case s.historySlots <- struct{}{}:
	default:
		s.mu.Unlock()
		return History{}, &exchange.Error{Kind: "rateLimited", Message: "Too many pending history requests; retry shortly", RetryAt: s.opts.Now().Add(time.Second).Unix()}
	}
	ownerContext := s.ctx
	if f := s.feeds[key]; f != nil && f.ctx != nil {
		ownerContext = f.ctx
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	defer func() { <-s.historySlots }()
	requestContext, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ownerContext, cancel)
	defer stop()
	return s.history(requestContext, id, key, from, to, limit)
}
func (s *Service) history(ctx context.Context, id string, key domain.FeedKey, from, to int64, limit int) (History, error) {
	if err := ctx.Err(); err != nil {
		return History{}, err
	}
	if err := key.Validate(); err != nil {
		return History{}, err
	}
	if from < 0 || to <= from || to > 32503680000 || limit < 1 || limit > 1000 || len(id) > 128 {
		return History{}, errors.New("invalid bounded history range")
	}
	s.mu.Lock()
	a, err := s.adapterLocked(key.Exchange)
	sem := s.semaphores[key.Exchange]
	providerEpoch := s.providerEpochs[key.Exchange]
	var revision int64
	if f := s.feeds[key]; f != nil {
		revision = f.revision
	}
	s.mu.Unlock()
	if err != nil {
		return History{}, err
	}
	if cached, ok := s.forexPage(key, from, to, limit); ok {
		return s.acceptHistoryEpoch(id, key, from, to, revision, cached, false, &providerEpoch)
	}
	// Only a complete contiguous page of closed, past buckets can bypass REST.
	// Gaps, delistings and current buckets still go to the provider; a sparse disk
	// page must never be mistaken for a complete market history.
	if s.opts.Cache != nil && key.Exchange != "dukascopy" && to <= exchange.BucketStart(s.opts.Now().Unix(), exchange.TimeframeSeconds(key.Timeframe)) {
		cached, cacheErr := s.opts.Cache.Range(key, from, to-1, limit)
		if err := ctx.Err(); err != nil {
			return History{}, err
		}
		if cacheErr == nil && completeCachedPage(key, from, to, limit, cached) {
			for i := range cached {
				cached[i].SourceRevision = 0
			}
			return s.acceptHistory(id, key, from, to, revision, cached, false), nil
		}
	}
	timeout := 25 * time.Second
	if key.Exchange == "dukascopy" {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return History{}, ctx.Err()
	}
	defer func() { <-sem }()
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
	bars, err := a.History(ctx, key, from, to, limit)
	if err != nil {
		return History{}, err
	}
	var epoch *uint64
	if key.Exchange == "dukascopy" {
		epoch = &providerEpoch
	}
	result, err := s.acceptHistoryEpoch(id, key, from, to, revision, bars, true, epoch)
	if err == nil {
		s.rememberForexPage(key, from, to, limit, result.Bars, providerEpoch)
	}
	return result, err
}

func completeCachedPage(key domain.FeedKey, from, to int64, limit int, bars []domain.Candle) bool {
	step := exchange.TimeframeSeconds(key.Timeframe)
	last := exchange.BucketStart(to-1, step)
	first := exchange.BucketStart(from, step)
	if first < from {
		first += step
	}
	if first > last {
		return false
	}
	count := int((last-first)/step + 1)
	if count > limit {
		count = limit
		first = last - int64(count-1)*step
	}
	if len(bars) != count {
		return false
	}
	for i, c := range bars {
		if !c.Closed || c.Time != first+int64(i)*step {
			return false
		}
	}
	return true
}

func (s *Service) acceptHistory(id string, key domain.FeedKey, from, to, revision int64, bars []domain.Candle, persist bool) History {
	result, _ := s.acceptHistoryEpoch(id, key, from, to, revision, bars, persist, nil)
	return result
}

func (s *Service) acceptHistoryEpoch(id string, key domain.FeedKey, from, to, revision int64, bars []domain.Candle, persist bool, epoch *uint64) (History, error) {
	result := map[int64]domain.Candle{}
	for _, c := range bars {
		if valid(c) && c.Time >= from && c.Time < to {
			result[c.Time] = c
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch != nil && (s.closed || s.providerEpochs[key.Exchange] != *epoch) {
		return History{}, errors.New("JForex account configuration changed; retry historical data")
	}
	if f := s.feeds[key]; f != nil {
		updates := []domain.Candle{}
		for t, c := range result {
			if f.changed[t] > revision || (!persist && f.changed[t] > 0) {
				result[t] = f.bars[t]
				continue
			}
			f.bars[t] = c
			f.changed[t] = f.revision + 1
			updates = append(updates, c)
		}
		if len(updates) > 0 {
			sort.Slice(updates, func(i, j int) bool { return updates[i].Time < updates[j].Time })
			s.emitLocked(f, updates, "bars")
			s.trimFeedLocked(f)
			s.trimLocked()
		}
	}
	ordered := sorted(result)
	// Authenticated history is saved by its adapter before releasing the
	// account reservation. Re-selecting a namespace here could race a credential
	// change after the response has returned.
	if persist && s.opts.Cache != nil && key.Exchange != "dukascopy" {
		s.opts.Cache.Put(key, ordered)
	}
	// A page must advance to an earlier timestamp before another pagination request.
	// Sparse pages do not prove the listing boundary. A nonempty earlier page
	// permits another bounded request; an empty page/progress check terminates it.
	more := len(ordered) > 0
	return History{RequestID: id, FeedKey: key, Bars: ordered, MoreHistory: more}, nil
}

// StoreCurrentCandles seeds a newly enabled/different cache with the genuine
// bars already downloaded this session, without restarting any subscriptions.
func (s *Service) StoreCurrentCandles() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.opts.Cache == nil {
		return
	}
	for key, f := range s.feeds {
		// This action belongs to the optional public cache. Forex already has
		// private durable writes while its account operation is reserved.
		if key.Exchange != "dukascopy" {
			s.opts.Cache.Put(key, sorted(f.bars))
		}
	}
}
func valid(c domain.Candle) bool {
	return c.Time >= 0 && c.Time < 32503680000 && c.Open > 0 && c.High >= c.Open && c.High >= c.Close && c.Low > 0 && c.Low <= c.Open && c.Low <= c.Close && c.Volume >= 0 && c.High < 1e100 && c.Volume < 1e100
}
func sorted(m map[int64]domain.Candle) []domain.Candle {
	out := make([]domain.Candle, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}
func (s *Service) trimFeedLocked(f *feed) {
	if len(f.bars) <= 10000 {
		return
	}
	bars := sorted(f.bars)
	// Explicit historical loads must survive cache eviction even when the feed
	// also contains newer live bars. Evict least recently written, not oldest time.
	sort.SliceStable(bars, func(i, j int) bool { return f.changed[bars[i].Time] < f.changed[bars[j].Time] })
	for _, c := range bars[:len(bars)-10000] {
		delete(f.bars, c.Time)
		delete(f.changed, c.Time)
		// A completed request is only reusable while its returned bars remain
		// cached. Retaining the marker after eviction would prevent revisiting
		// an older viewport from ever requesting its missing history again.
		for loaded := range f.loaded {
			if c.Time >= loaded.from && c.Time < loaded.to {
				delete(f.loaded, loaded)
			}
		}
	}
}
func (s *Service) trimLocked() {
	total := 0
	for _, f := range s.feeds {
		total += len(f.bars)
	}
	for total > s.opts.MaxCandles || len(s.feeds) > 256 {
		var oldest *feed
		for _, f := range s.feeds {
			if len(f.owners) == 0 && f.cancel == nil && (oldest == nil || f.used.Before(oldest.used)) {
				oldest = f
			}
		}
		if oldest == nil {
			break
		}
		total -= len(oldest.bars)
		delete(s.feeds, oldest.key)
	}
}
func (s *Service) Diagnostics() Diagnostics {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := Diagnostics{ActiveLeases: len(s.leases), CachedFeeds: len(s.feeds), HistoryRequests: s.requests, Resyncs: s.resyncs}
	for _, f := range s.feeds {
		d.CachedCandles += len(f.bars)
		if len(f.owners) > 0 {
			d.UniqueFeeds++
		}
	}
	for _, a := range s.adapters {
		if provider, ok := a.(interface{ Diagnostics() exchange.Diagnostics }); ok {
			stats := provider.Diagnostics()
			d.Sockets += stats.Sockets
			d.Reconnects += stats.Reconnects
			d.RESTRequests += stats.RESTRequests
			d.QueueDepth += stats.QueueDepth
		}
	}
	return d
}
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	for _, f := range s.feeds {
		if f.timer != nil {
			f.timer.Stop()
		}
		if f.cancel != nil {
			f.cancel()
		}
	}
	s.leases = map[string]*lease{}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Service) String() string { return fmt.Sprintf("market: %+v", s.Diagnostics()) }
