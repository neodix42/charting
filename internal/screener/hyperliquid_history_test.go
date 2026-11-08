package screener

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func retainedDaily(start int64, count int) []domain.Candle {
	bars := make([]domain.Candle, count)
	for i := range bars {
		bars[i] = domain.Candle{Time: start + int64(i)*86400, Open: 10, High: 12, Low: 9, Close: 11, Volume: 0.1, Closed: true, Quality: "authoritative"}
	}
	return bars
}
func TestHyperliquidRetainedHistoryMergesAndIsolatesNativeMarkets(t *testing.T) {
	profile := t.TempDir()
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC).Unix()
	asset := publicAlertAsset{Market: "perpetual", NativeID: "xyz:BTC"}
	bars := retainedDaily(start, 4)
	if err := mergeHyperliquidHistory(context.Background(), profile, asset, "d1", bars[:3]); err != nil {
		t.Fatal(err)
	}
	bars[2].Close = 11.5
	if err := mergeHyperliquidHistory(context.Background(), profile, asset, "d1", bars[2:]); err != nil {
		t.Fatal(err)
	}
	key := domain.FeedKey{Exchange: "hyperliquid", Market: asset.Market, Instrument: asset.NativeID, Timeframe: "1d"}
	got, err := readHyperliquidHistory(context.Background(), profile, key, start, start+3*86400, 10)
	if err != nil || !reflect.DeepEqual(got, bars) {
		t.Fatalf("overlap replaced older retained history: %+v %v", got, err)
	}
	for _, other := range []domain.FeedKey{
		{Exchange: "hyperliquid", Market: "spot", Instrument: "@107", Timeframe: "1d"},
		{Exchange: "hyperliquid", Market: asset.Market, Instrument: "BTC", Timeframe: "1d"},
		{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1d"},
		{Exchange: "hyperliquid", Market: asset.Market, Instrument: asset.NativeID, Timeframe: "1h"},
	} {
		got, err := readHyperliquidHistory(context.Background(), profile, other, 0, 0, 100)
		if err != nil || len(got) != 0 {
			t.Fatalf("history leaked to %+v: %+v %v", other, got, err)
		}
	}
	// Bounded queries use inclusive cache endpoints and retain the latest rows.
	got, err = readHyperliquidHistory(context.Background(), profile, key, start, start+2*86400, 2)
	if err != nil || !reflect.DeepEqual(got, bars[1:3]) {
		t.Fatal(got, err)
	}
}
func TestHyperliquidRetainedWeeksUseMondayCompleteDailyAndDecimalVolume(t *testing.T) {
	profile := t.TempDir()
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC).Unix()
	asset := publicAlertAsset{Market: "spot", NativeID: "@107"}
	bars := retainedDaily(start, 9)
	bars[0].Volume = 0.1
	bars[1].Volume = 0.2
	for i := 2; i < 7; i++ {
		bars[i].Volume = 0
	}
	if err := mergeHyperliquidHistory(context.Background(), profile, asset, "d1", bars); err != nil {
		t.Fatal(err)
	}
	key := domain.FeedKey{Exchange: "hyperliquid", Market: asset.Market, Instrument: asset.NativeID, Timeframe: "1w"}
	got, err := readHyperliquidHistory(context.Background(), profile, key, 0, 0, 100)
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	if got[0].Time != start || got[0].Volume != 0.3 || got[0].Quality != "authoritative" || !got[0].Closed {
		t.Fatal("weekly UTC/volume/close incorrect", got[0])
	}
	if got[1].Time != start+7*86400 || got[1].Quality != "provisional" || got[1].Closed {
		t.Fatal("partial week marked complete", got[1])
	}
}
func TestHyperliquidRetainedHistoryRejectsTamperingAndCancelledWriters(t *testing.T) {
	profile := t.TempDir()
	asset := publicAlertAsset{Market: "spot", NativeID: "@107"}
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC).Unix()
	if err := mergeHyperliquidHistory(context.Background(), profile, asset, "d1", retainedDaily(start, 1)); err != nil {
		t.Fatal(err)
	}
	release, err := hyperliquidHistoryLock(context.Background(), hyperliquidHistoryRoot(profile), asset, "d1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = mergeHyperliquidHistory(ctx, profile, asset, "d1", retainedDaily(start, 2))
	release()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled writer acquired series", err)
	}
	path := filepath.Join(hyperliquidHistoryRoot(profile), "raw", publicAlertSymbol(asset.Market, asset.NativeID)+"-d1.csv")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(strings.Replace(string(original), ",10,12,", ",20,22,", 1))
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err = mergeHyperliquidHistory(context.Background(), profile, asset, "d1", retainedDaily(start+86400, 1)); err == nil {
		t.Fatal("tampered retained bars accepted")
	}
	after, _ := os.ReadFile(path)
	if !reflect.DeepEqual(after, changed) {
		t.Fatal("tampered original overwritten")
	}
}
func TestHyperliquidWeeklyAlertDownloadsOnlyMissingNativeDailyBars(t *testing.T) {
	profile := t.TempDir()
	asset := publicAlertAsset{Market: "perpetual", NativeID: "BTC"}
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC).Unix()
	bars := retainedDaily(start, 14)
	// One missing day in a week and one new day in the next are fetched as
	// individual D1 gaps, rather than re-downloading two complete weekly inputs.
	keep := append(append([]domain.Candle(nil), bars[:3]...), bars[4:13]...)
	if err := mergeHyperliquidHistory(context.Background(), profile, asset, "d1", keep); err != nil {
		t.Fatal(err)
	}
	adapter := &publicAlertFakeAdapter{price: 10}
	got, err := hyperliquidAlertWeeklyHistory(context.Background(), adapter, profile, asset, start, start+14*86400)
	if err != nil || len(got) != 2 || got[0].Quality != "authoritative" || got[1].Quality != "authoritative" {
		t.Fatal(got, err)
	}
	want := [][2]int64{{start + 3*86400, start + 4*86400}, {start + 13*86400, start + 14*86400}}
	if !reflect.DeepEqual(adapter.ranges, want) {
		t.Fatalf("unexpected requests: %+v", adapter.ranges)
	}
	for _, key := range adapter.historyKeys {
		if key.Timeframe != "1d" {
			t.Fatal("weekly alert fetched native W1", key)
		}
	}
	adapter.ranges = nil
	adapter.historyKeys = nil
	if _, err = hyperliquidAlertWeeklyHistory(context.Background(), adapter, profile, asset, start, start+14*86400); err != nil {
		t.Fatal(err)
	}
	if len(adapter.ranges) != 0 {
		t.Fatal("retained daily bars downloaded again", adapter.ranges)
	}
}

func TestHyperliquidAlertsReuseDownloadedDailyAndWeeklyHistory(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	asset := publicAlertAsset{Market: "perpetual", NativeID: "BTC"}
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC).Unix()
	bars := retainedDaily(start, 28)
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", bars); err != nil {
		t.Fatal(err)
	}
	fake := &publicAlertFakeAdapter{price: 20, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	live := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	rule := publicAlertTestCondition()
	rule.Request.Period = "d1"
	rule.Request.Conditions = rule.Request.Conditions[:1]
	// Both plans have enough retained actual bars, including Monday UTC weeks.
	weekly := rule
	weekly.ID = strings.Repeat("b", 32)
	weekly.Request = copyAnalysisRequest(rule.Request)
	weekly.Request.Period = "w1"
	periods := map[string][]string{rule.ID: {"d1"}, weekly.ID: {"w1"}}
	asOf := (start+28*86400)*1000 + 5000
	batch, err := live.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{rule, weekly}, periods, asOf)
	if err != nil {
		t.Fatal(err)
	}
	defer batch.close()
	if len(batch.Failures) > 0 || len(batch.Files) != 2 {
		t.Fatalf("retained alert history not prepared: %+v", batch)
	}
	if len(fake.ranges) != 0 {
		t.Fatal("downloaded authoritative history fetched again", fake.ranges)
	}
	// A repeat check returns the same verified indicator contents with no HTTP.
	repeat, err := live.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{rule, weekly}, periods, asOf)
	if err != nil {
		t.Fatal(err)
	}
	defer repeat.close()
	if len(repeat.Failures) > 0 || len(fake.ranges) != 0 {
		t.Fatal(repeat.Failures, fake.ranges)
	}
	for pair, name := range batch.Files {
		first, _ := os.ReadFile(filepath.Join(batch.Directory, name))
		second, _ := os.ReadFile(filepath.Join(repeat.Directory, repeat.Files[pair]))
		if !reflect.DeepEqual(first, second) {
			t.Fatal("unchanged history altered indicators", pair)
		}
	}
	// The next daily close needs a single new tail with the existing explicit
	// one-bar reconciliation overlap, while historical downloaded rows remain.
	ruleOnly := map[string][]string{rule.ID: {"d1"}}
	updated, err := live.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{rule}, ruleOnly, asOf+86400*1000)
	if err != nil {
		t.Fatal(err)
	}
	defer updated.close()
	if len(updated.Failures) > 0 || len(fake.ranges) != 1 || fake.ranges[0] != [2]int64{start + 27*86400, start + 29*86400} {
		t.Fatal("update was not incremental", updated.Failures, fake.ranges)
	}
	key := domain.FeedKey{Exchange: "hyperliquid", Market: asset.Market, Instrument: asset.NativeID, Timeframe: "1d"}
	retained, err := m.HyperliquidHistory(key, 0, 0, 100)
	if err != nil || len(retained) != 29 || retained[0].Time != start {
		t.Fatal("alert update discarded downloaded older history", retained, err)
	}
}
