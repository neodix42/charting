package market

import (
	"context"
	"testing"

	"cryptodesk/internal/domain"
)

func TestForexHistoryPersistenceStaysWithAccountAdapterAndAcceptedStream(t *testing.T) {
	cache := newMarketDiskCache(t)
	candle := cacheTestBar(0)
	key := domain.FeedKey{Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Timeframe: "1m"}
	adapter := &diskHistoryAdapter{historyBars: []domain.Candle{candle}}
	service := newDiskTestService(t, cache, adapter, candle.Time+600)
	result, err := service.History(context.Background(), "account-history", key, candle.Time, candle.Time+60, 10)
	if err != nil || len(result.Bars) != 1 {
		t.Fatalf("provider history failed: %+v %v", result, err)
	}
	if len(cache.written(t)) != 0 {
		t.Fatal("history was saved again after its account reservation ended")
	}
	if _, err = service.Attach("forex", 1, key, 0, 0); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return started(&adapter.fakeAdapter, 1) })
	adapter.emit(candle)
	if len(cache.written(t)) != 1 {
		t.Fatal("accepted native update did not reach the private cache")
	}
	service.StoreCurrentCandles()
	if len(cache.written(t)) != 1 {
		t.Fatal("changing public storage rewrote an account-bound forex feed")
	}
}
