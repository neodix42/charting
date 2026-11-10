package screener

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func TestHyperliquidHourlyDownloadGroupEnrichmentAndReplayRetainsNativeIdentity(t *testing.T) {
	m := hyperliquidDownloadManager(t)
	catalog, err := m.CatalogForSource(context.Background(), "hyperliquid")
	if err != nil || strings.Join(catalog.Timeframes, ",") != "h1,d1,w1" {
		t.Fatalf("H1 discovery: %+v %v", catalog, err)
	}
	id := "hyperliquid-" + publicAlertSymbol("perpetual", "BTC")
	request := Request{DataSource: "hyperliquid", Symbols: []string{id}, Timeframe: "h1", From: "2000-01-01", To: "2024-01-10"}
	if _, err = m.Start(request); err != nil {
		t.Fatal(err)
	}
	info := waitHyperliquidDownload(t, m)
	if len(info.Jobs) != 1 || info.Jobs[0].Timeframe != "h1" {
		t.Fatalf("hourly generated other periods: %+v", info)
	}
	first := info.Jobs[0].OutputPath
	if !strings.Contains(filepath.Base(first), "-h1-2024-01-03-") {
		t.Fatalf("listing-clamped readable name: %s", first)
	}
	bars, err := readHyperliquidVerifiedCSV(m.directory, filepath.Base(first), 3600, time.Now().UnixMilli())
	if err != nil || len(bars) != 168 {
		t.Fatalf("hourly rows: %d %v", len(bars), err)
	}
	for i, bar := range bars {
		if !bar.Closed || bar.Time%3600 != 0 || (i > 0 && bar.Time-bars[i-1].Time != 3600) {
			t.Fatal("invalid H1 candle boundary/closure")
		}
	}
	if _, err = m.StartEnrichment(EnrichmentRequest{DataSource: "hyperliquid", Group: "Day / Week ranges", Period: "h1"}); err != nil {
		t.Fatal(err)
	}
	enriched := waitEnrichment(t, m, []string{dataFileID(m.directory, filepath.Base(first))})
	if enriched.Completed != 1 || enriched.Failed != 0 || len(enriched.Jobs[0].Columns) != len(calendarRangeIDs) {
		t.Fatalf("H1 ranges: %+v", enriched)
	}
	adapter := m.hyperliquidAdapter.(*hyperliquidDownloadAdapter)
	adapter.mu.Lock()
	before := adapter.calls
	adapter.mu.Unlock()
	if _, err = m.Start(request); err != nil {
		t.Fatal(err)
	}
	waitHyperliquidDownload(t, m)
	adapter.mu.Lock()
	after := adapter.calls
	adapter.mu.Unlock()
	if after != before {
		t.Fatal("already covered H1 history was redownloaded")
	}
	request.To = "2024-01-20"
	if _, err = m.Start(request); err != nil {
		t.Fatal(err)
	}
	info = waitHyperliquidDownload(t, m)
	output := info.Jobs[0].OutputPath
	data, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(data), "today_low,today_high,previous_day_low,previous_day_high,week_low,week_high,previous_week_low,previous_week_high") {
		t.Fatalf("H1 recipe replay lost columns: %v", err)
	}
	retained, err := m.HyperliquidHistory(domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1h"}, 0, 0, 5000)
	if err != nil || len(retained) != 400 {
		t.Fatalf("retained chart H1: %d %v", len(retained), err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	for _, key := range adapter.keys {
		if key.Timeframe != "1h" || key.Market != "perpetual" || key.Instrument != "BTC" {
			t.Fatalf("wrong native H1 request: %+v", key)
		}
	}
}

func TestHyperliquidHourlyCoverageClosesAtHourRatherThanMidnight(t *testing.T) {
	m := hyperliquidDownloadManager(t)
	name := "hyperliquid_btc-h1-2024-01-03-2024-01-04.csv"
	text := "timestamp,open,high,low,close,volume\n1704276000000,1,3,0,2,1\n"
	path := writeDataFile(t, m, name, text)
	digest := sha256.Sum256([]byte(text))
	start := time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC).UnixMilli()
	end := start + 2*3600000
	source := downloadSource{Input: path, Name: name, Metadata: fileMetadata{Checksum: hex.EncodeToString(digest[:]), Coverage: []dataRange{{From: start, To: end}}}}
	ranges, err := hyperliquidCoveredRanges([]downloadSource{source}, end+15*60000, 3600)
	if err != nil || len(ranges) != 1 || ranges[0].To != end {
		t.Fatalf("H1 coverage lost same-day closes: %+v %v", ranges, err)
	}
}
