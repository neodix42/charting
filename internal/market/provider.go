package market

import (
	"context"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

// Provider configuration and credentials never belong in portable feed keys.
// Invalidating discovery prevents older in-flight catalog responses from being
// reused. Same-account connection tests leave historical queries valid.
func (s *Service) InvalidateInstruments(provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalogEpochs[provider]++
	delete(s.instruments, provider)
}

func (s *Service) ProviderActive(provider string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providerActiveLocked(provider)
}

func (s *Service) providerActiveLocked(provider string) bool {
	for key, f := range s.feeds {
		if key.Exchange == provider && len(f.owners) > 0 {
			return true
		}
	}
	return false
}

// ForgetProvider drops only dormant account-bound market buffers. Callers must
// finish/reselect active charts before changing their global credentials.
func (s *Service) ForgetProvider(provider string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.providerActiveLocked(provider) {
		return false
	}
	s.providerEpochs[provider]++
	delete(s.instruments, provider)
	for key, f := range s.feeds {
		if key.Exchange != provider {
			continue
		}
		if f.timer != nil {
			f.timer.Stop()
		}
		if f.cancel != nil {
			f.cancel()
		}
		delete(s.feeds, key)
	}
	if provider == "dukascopy" {
		s.forexPages = nil
	}
	return true
}

// RestartUnconfiguredProvider recovers charts opened before the provider's first
// credentials were saved. Callers must only use this for an unconfigured account;
// changing an existing account requires releasing its leases and ForgetProvider.
func (s *Service) RestartUnconfiguredProvider(provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.providerEpochs[provider]++
	delete(s.instruments, provider)
	if provider == "dukascopy" {
		s.forexPages = nil
	}
	for key, f := range s.feeds {
		if key.Exchange != provider {
			continue
		}
		if f.timer != nil {
			f.timer.Stop()
			f.timer = nil
		}
		if f.cancel != nil {
			f.cancel()
		}
		if len(f.owners) == 0 {
			delete(s.feeds, key)
			continue
		}
		ranges := make(map[historyRange]bool)
		for r := range f.loading {
			ranges[r] = true
		}
		for r := range f.loaded {
			ranges[r] = true
		}
		f.bars = map[int64]domain.Candle{}
		f.changed = map[int64]int64{}
		f.loading, f.loaded = nil, nil
		f.health = domain.Health{State: "connecting"}
		ctx, cancel := context.WithCancel(s.ctx)
		f.ctx, f.cancel = ctx, cancel
		s.wg.Add(1)
		go s.run(ctx, f, s.adapters[provider])
		s.emitLocked(f, nil, "health")
		for r := range ranges {
			s.restoreLocked(f, r.from, r.to)
		}
	}
}

type forexHistoryPage struct {
	key      domain.FeedKey
	from, to int64
	limit    int
	bars     []domain.Candle
}

// Forex holidays/weekends are legitimate gaps. Remember exact successful past
// queries, including empty pages, instead of requiring invented 24/7 bars to
// prove coverage. The SDK owns its persistent, environment/account-scoped cache.
func (s *Service) forexPage(key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, bool) {
	if key.Exchange != "dukascopy" || to > exchange.BucketStart(s.opts.Now().Unix(), exchange.TimeframeSeconds(key.Timeframe)) {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, page := range s.forexPages {
		if page.key == key && page.from == from && page.to == to && page.limit == limit {
			bars := append([]domain.Candle{}, page.bars...)
			for i := range bars {
				bars[i].SourceRevision = 0
			}
			return bars, true
		}
	}
	return nil, false
}

func (s *Service) rememberForexPage(key domain.FeedKey, from, to int64, limit int, bars []domain.Candle, epoch uint64) {
	if key.Exchange != "dukascopy" || to > exchange.BucketStart(s.opts.Now().Unix(), exchange.TimeframeSeconds(key.Timeframe)) {
		return
	}
	previous := int64(-1)
	for _, c := range bars {
		if !valid(c) || !c.Closed || c.Time < from || c.Time >= to || c.Time <= previous {
			return
		}
		previous = c.Time
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.providerEpochs[key.Exchange] != epoch {
		return
	}
	for _, page := range s.forexPages {
		if page.key == key && page.from == from && page.to == to && page.limit == limit {
			return
		}
	}
	if len(s.forexPages) == 32 {
		s.forexPages = s.forexPages[1:]
	}
	s.forexPages = append(s.forexPages, forexHistoryPage{key: key, from: from, to: to, limit: limit, bars: append([]domain.Candle{}, bars...)})
}
