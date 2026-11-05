package candlecache

import (
	"cryptodesk/internal/domain"
	"testing"
)

func TestHyperliquidPairAndBuilderDexCacheIdentitySurvivesRestart(t *testing.T) {
	directory := t.TempDir()
	s := openTest(t, directory)
	keys := []domain.FeedKey{
		{Exchange: "hyperliquid", Market: "spot", Instrument: "@107", Timeframe: "1m"},
		{Exchange: "hyperliquid", Market: "spot", Instrument: "PURR/USDC", Timeframe: "1m"},
		{Exchange: "hyperliquid", Market: "perpetual", Instrument: "HYPE", Timeframe: "1m"},
		{Exchange: "hyperliquid", Market: "perpetual", Instrument: "xyz:XYZ100", Timeframe: "1m"},
		{Exchange: "hyperliquid", Market: "perpetual", Instrument: "cash:XYZ100", Timeframe: "1m"},
	}
	for i, key := range keys {
		c := testCandle(0)
		c.Volume = float64(i + 1)
		if err := s.Upsert(key, []domain.Candle{c}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, directory)
	for i, key := range keys {
		got := readTest(t, s, key, 0, 0, 10)
		if len(got) != 1 || got[0].Volume != float64(i+1) {
			t.Fatalf("cache identity collided for %s: %#v", key, got)
		}
	}
}
