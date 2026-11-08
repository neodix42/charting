//go:build bindings

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cryptodesk/internal/candlestorage"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/screener"
)

func TestChartCacheReadsRetainedHyperliquidHistoryWithOptionalStorageDisabled(t *testing.T) {
	profile := t.TempDir()
	// Explicit offline raw-store fixture includes the same checksum-bound native
	// provenance that Downloader publishes, rather than relying on displayed names.
	native := "xyz:BTC"
	aliasHash := sha256.Sum256([]byte(native))
	alias := "perpetual" + hex.EncodeToString(aliasHash[:])
	raw := []byte("timestamp,open,high,low,close,volume,quality\n1793577600000,10,12,9,11,0.3,authoritative\n")
	digest := sha256.Sum256(raw)
	metadata, err := json.Marshal(map[string]any{"source": "hyperliquid", "market": "perpetual", "nativeId": native, "period": "d1", "checksum": hex.EncodeToString(digest[:]), "checkedThrough": int64(1793664000)})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(profile, "screener-public-history", "hyperliquid", "raw")
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for suffix, data := range map[string][]byte{".csv": raw, ".json": metadata} {
		if err = os.WriteFile(filepath.Join(root, alias+"-d1"+suffix), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	manager := screener.New(profile)
	defer manager.Close()
	optional := candlestorage.New(profile, nil)
	defer optional.Close()
	info, err := optional.Info()
	if err != nil || info.Enabled {
		t.Fatal("expected disabled optional candle cache", info, err)
	}
	cache := chartCandleCache{manager: optional, screener: manager}
	key := domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: native, Timeframe: "1d"}
	bars, err := cache.Range(key, 0, 0, 100)
	if err != nil || len(bars) != 1 || bars[0].Time != 1793577600 || bars[0].Close != 11 || bars[0].Volume != 0.3 {
		t.Fatalf("downloaded history absent with optional cache disabled: %+v %v", bars, err)
	}
	for _, other := range []domain.FeedKey{
		{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1d"},
		{Exchange: "hyperliquid", Market: "spot", Instrument: "@107", Timeframe: "1d"},
		{Exchange: "hyperliquid", Market: "perpetual", Instrument: native, Timeframe: "1h"},
		{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1d"},
	} {
		bars, err = cache.Range(other, 0, 0, 100)
		if err != nil || len(bars) != 0 {
			t.Fatalf("downloaded bars leaked into %+v: %+v %v", other, bars, err)
		}
	}
	// Existing optional data may contain newer authoritative REST corrections.
	if _, err = optional.Configure(true, filepath.Join(profile, "candles")); err != nil {
		t.Fatal(err)
	}
	corrected := domain.Candle{Time: 1793577600, Open: 10, High: 13, Low: 9, Close: 12, Volume: 0.4, Closed: true, Quality: "authoritative"}
	optional.Put(key, []domain.Candle{corrected})
	bars, err = cache.Range(key, 0, 0, 100)
	if err != nil || len(bars) != 1 || bars[0].Close != 12 || bars[0].Volume != 0.4 {
		t.Fatal("newer authoritative cache correction lost", bars, err)
	}
}
