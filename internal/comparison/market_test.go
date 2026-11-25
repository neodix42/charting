package comparison

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/market"
)

type adapter struct {
	provider       string
	reads, streams atomic.Int32
}

func (a *adapter) Instruments(context.Context) ([]domain.Instrument, error) {
	if a.provider == "hyperliquid" {
		return []domain.Instrument{item("BTC", "perpetual", "BTC", "USDC")}, nil
	}
	return []domain.Instrument{}, nil
}
func (a *adapter) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	a.reads.Add(1)
	return candles(from, to, 100), nil
}
func (a *adapter) Stream(ctx context.Context, key domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
	a.streams.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

type sharedCache struct {
	mu   sync.Mutex
	bars map[domain.FeedKey]map[int64]domain.Candle
}

func (c *sharedCache) Range(key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []domain.Candle
	for ts := from; ts <= to; ts += day {
		if bar, ok := c.bars[key][ts]; ok {
			result = append(result, bar)
		}
	}
	return result, nil
}
func (c *sharedCache) Put(key domain.FeedKey, bars []domain.Candle) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bars[key] == nil {
		c.bars[key] = map[int64]domain.Candle{}
	}
	for _, bar := range bars {
		c.bars[key][bar.Time] = bar
	}
}
func TestComparisonReusesMarketCacheWithoutOpeningAnyStream(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC) }
	hyper, binance := &adapter{provider: "hyperliquid"}, &adapter{provider: "binance"}
	service := market.New(market.Options{Now: now, Cache: &sharedCache{bars: map[domain.FeedKey]map[int64]domain.Candle{}}, Factory: func(provider string) (exchange.Adapter, error) {
		if provider == "hyperliquid" {
			return hyper, nil
		}
		return binance, nil
	}})
	defer service.Close()
	var result Result
	for i := 0; i < 2; i++ {
		manager := NewWithOptions(Options{Profile: t.TempDir(), Source: service, Now: now})
		var err error
		result, err = manager.DailyChanges(context.Background(), "cache", View{Weeks: 4, Assets: []string{"BTC"}})
		manager.Close()
		if err != nil || result.Series[0].Error != "" {
			t.Fatalf("comparison %d failed %+v %v", i, result, err)
		}
	}
	key := *result.Series[0].Asset.FeedKey
	chart, err := service.History(context.Background(), "ordinary-chart", key, result.BaselineTime, result.LastClosedTime+day, 29)
	if err != nil || len(chart.Bars) != 29 || hyper.reads.Load() != 1 || binance.reads.Load() != 0 || hyper.streams.Load() != 0 || service.Diagnostics().ActiveLeases != 0 {
		t.Fatalf("shared data/stream ownership violated reads=%d streams=%d chart=%+v error=%v", hyper.reads.Load(), hyper.streams.Load(), chart, err)
	}
}
