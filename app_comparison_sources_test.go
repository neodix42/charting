package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/comparison"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/market"
	"cryptodesk/internal/storage"
)

type comparisonVenueAdapter struct {
	provider string
	reads    chan domain.FeedKey
	streams  *atomic.Int32
}

func (a *comparisonVenueAdapter) Instruments(context.Context) ([]domain.Instrument, error) {
	base, id, quote, kind := "GRAM", "GRAM", "USDC", "perpetual"
	switch a.provider {
	case "binance", "bybit":
		id, quote, kind = "GRAMUSDT", "USDT", "spot"
	case "coinbase":
		base, id, quote, kind = "TON", "TON-USD", "USD", "spot"
	}
	return []domain.Instrument{{NativeID: id, BaseAsset: base, QuoteAsset: quote, Market: kind, Availability: "available", SupportedIntervals: []string{"1d"}}}, nil
}
func (a *comparisonVenueAdapter) History(ctx context.Context, key domain.FeedKey, from, to int64, _ int) ([]domain.Candle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.reads <- key
	day := int64(86400)
	firstGRAM := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC).Unix()
	gapStart := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC).Unix()
	var bars []domain.Candle
	for at := from; at < to; at += day {
		if a.provider == "hyperliquid" && at < firstGRAM || a.provider == "bybit" && at >= gapStart && at < gapStart+5*day {
			continue
		}
		price := 100 + float64((at-from)/day)
		bars = append(bars, domain.Candle{Time: at, Open: price, High: price, Low: price, Close: price, Closed: true, Quality: "authoritative"})
	}
	return bars, nil
}
func (a *comparisonVenueAdapter) Stream(context.Context, domain.FeedKey, func([]domain.Candle, domain.Health)) error {
	a.streams.Add(1)
	return errors.New("Comparison must not request streams")
}

func TestComparisonFacadeRetainsExplicitVenuesAndIndependentTONHistory(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reads := make(chan domain.FeedKey, 8)
	var streams atomic.Int32
	service := market.New(market.Options{Factory: func(provider string) (exchange.Adapter, error) {
		return &comparisonVenueAdapter{provider: provider, reads: reads, streams: &streams}, nil
	}})
	defer service.Close()
	now := func() time.Time { return time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) }
	app := &App{store: store, market: service, ctx: context.Background()}
	app.comparison = comparison.NewWithOptions(comparison.Options{Profile: store.Directory(), Source: comparisonMarketSource{market: service}, Now: now})
	defer func() { app.comparison.Close() }()
	catalog, err := app.GetComparisonCatalog()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"coinbase|spot|TON-USD", "bybit|spot|GRAMUSDT", "hyperliquid|perpetual|GRAM"}
	available := map[string]comparison.Asset{}
	for _, asset := range catalog.Markets {
		available[asset.ID] = asset
	}
	for _, id := range want {
		if available[id].Symbol != "TON" || available[id].FeedKey == nil {
			t.Fatalf("verified TON source missing: %s %+v", id, catalog)
		}
	}
	view := comparison.View{Weeks: 26, Assets: want}
	if _, err = app.SaveComparisonView(view); err != nil {
		t.Fatal(err)
	}
	app.comparison.Close()
	app.comparison = comparison.NewWithOptions(comparison.Options{Profile: store.Directory(), Source: comparisonMarketSource{market: service}, Now: now})
	restored, err := app.GetComparisonView()
	if err != nil || !reflect.DeepEqual(restored, view) {
		t.Fatalf("explicit sources did not survive restart: %+v %v", restored, err)
	}
	data, err := os.ReadFile(filepath.Join(store.Directory(), "comparison-view.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Version int `json:"version"`
	}
	if err = json.Unmarshal(data, &saved); err != nil || saved.Version != 2 {
		t.Fatalf("explicit sources not saved in version2: %s %v", data, err)
	}
	result, err := app.GetComparisonDailyChanges("three-TON-venues", restored)
	if err != nil || len(result.Series) != 3 {
		t.Fatalf("source-qualified comparison failed: %+v %v", result, err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("normal source notices appeared: %v", result.Warnings)
	}
	for index, series := range result.Series {
		if series.Asset.ID != want[index] || series.Asset.Symbol != "TON" {
			t.Fatalf("duplicate base sources collapsed or reordered: %+v", result.Series)
		}
	}
	for index := 0; index < 2; index++ {
		series := result.Series[index]
		if series.Error != "" || len(series.Points) != 183 || series.Points[0].Pct == nil || *series.Points[0].Pct != 0 {
			t.Fatalf("available 26-week history rejected: %+v", series)
		}
	}
	coinbaseGaps, bybitGaps := 0, 0
	for _, point := range result.Series[0].Points {
		if point.Pct == nil {
			coinbaseGaps++
		}
	}
	for _, point := range result.Series[1].Points {
		if point.Pct == nil {
			bybitGaps++
		}
	}
	if coinbaseGaps != 0 || bybitGaps != 5 {
		t.Fatal("provider-native gaps filled or providers mixed", coinbaseGaps, bybitGaps)
	}
	missing := result.Series[2]
	if len(missing.Points) != 0 || !strings.Contains(missing.Error, "2026-05-27") || !strings.Contains(missing.Error, "2026-07-02") {
		t.Fatalf("missing baseline fabricated or not explained: %+v", missing)
	}
	seen := map[domain.FeedKey]bool{}
	for count := 0; count < 3; count++ {
		select {
		case key := <-reads:
			seen[key] = true
		default:
			t.Fatal("requested source was not read")
		}
	}
	for _, id := range want {
		if !seen[*available[id].FeedKey] {
			t.Fatalf("wrong source requested for %s: %+v", id, seen)
		}
	}
	if len(reads) != 0 || streams.Load() != 0 || service.Diagnostics().ActiveLeases != 0 {
		t.Fatal("source selection caused retries, substitutions or streams")
	}
}
