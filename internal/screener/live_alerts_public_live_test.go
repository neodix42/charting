package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The smoke deliberately restricts only discovery. History remains the actual
// production Hyperliquid adapter, including HTTP pacing, parsing and weekly
// correctness. This never starts the all-market alert scheduler or Telegram.
type publicAlertLiveSmokeAdapter struct {
	exchange.Adapter
	items []domain.Instrument
}

func (a publicAlertLiveSmokeAdapter) Instruments(context.Context) ([]domain.Instrument, error) {
	if a.items != nil {
		return a.items, nil
	}
	return []domain.Instrument{
		{NativeID: "@107", Market: "spot", BaseAsset: "HYPE", QuoteAsset: "USDC", Availability: "available", ProviderMetadata: map[string]string{"displaySymbol": "HYPE/USDC"}},
		{NativeID: "BTC", Market: "perpetual", BaseAsset: "BTC", QuoteAsset: "USDC", Availability: "available"},
	}, nil
}

func TestPublicAlertLiveHyperliquidSmoke(t *testing.T) {
	if os.Getenv("CRYPTODESK_LIVE_PUBLIC_ALERT_SMOKE") != "1" {
		t.Skip("opt in with CRYPTODESK_LIVE_PUBLIC_ALERT_SMOKE=1; uses real public Hyperliquid history, isolated temporary profile and no Telegram")
	}
	m := sourceEnrichmentManager(t, false)
	actual, err := exchange.New("hyperliquid")
	if err != nil {
		t.Fatal(err)
	}
	diagnostics, ok := actual.(interface{ Diagnostics() exchange.Diagnostics })
	if !ok {
		t.Fatal("production adapter does not expose actual request diagnostics")
	}
	l, err := NewLiveAlerts(m, func(context.Context, LiveAlert) error {
		t.Fatal("public smoke attempted an alert delivery")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	l.publicData = &publicAlertData{adapters: map[string]exchange.Adapter{"hyperliquid": publicAlertLiveSmokeAdapter{Adapter: actual}}}
	condition := SavedAnalysisCondition{ID: strings.Repeat("a", 32), Name: "Real public multi-period indicator smoke", Request: AnalysisRequest{Period: "d1"}}
	for _, field := range []string{"close", "weekday", "tenkan_9", "kijun_26", "senkou_a_9_26", "senkou_b_52"} {
		condition.Request.Conditions = append(condition.Request.Conditions, AnalysisCondition{Operator: "above", Left: AnalysisOperand{Kind: "field", Field: field}, Right: AnalysisOperand{Kind: "number", Value: 0}})
	}
	condition.Request.Conditions = append(condition.Request.Conditions, AnalysisCondition{Operator: "above", Left: AnalysisOperand{Kind: "field", Field: "ema_20", Timeframe: "h4"}, Right: AnalysisOperand{Kind: "number", Value: 0}})
	periods := map[string][]string{condition.ID: {"d1"}}
	asOf := time.Now().UTC().UnixMilli()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	before := diagnostics.Diagnostics().RESTRequests
	batch, err := l.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{condition}, periods, asOf)
	if err != nil {
		t.Fatalf("real public preparation failed: %v", err)
	}
	if len(batch.Assets) != 2 || len(batch.Files) != 4 || len(batch.Failures) != 0 {
		t.Fatalf("real public warm-up/companion preparation incomplete: assets=%d files=%d failures=%v", len(batch.Assets), len(batch.Files), batch.Failures)
	}
	work := t.TempDir()
	a := &analysisState{directory: batch.Directory, work: work, series: map[string]*analysisSeries{}, cache: newAnalysisRowCache(), request: condition.Request, asOf: asOf}
	for _, name := range batch.Files {
		id := dataFileID(batch.Directory, name)
		s, err := indexAnalysisCSV(ctx, batch.Directory, name, filepath.Join(work, id+".rows"), func(float64) {})
		if err != nil {
			t.Fatal(err)
		}
		s.cache = a.cache
		a.series[id] = s
	}
	for _, asset := range batch.Assets {
		name := batch.Files[asset.Symbol+"/d1"]
		s := a.series[dataFileID(batch.Directory, name)]
		closed, err := s.closedCount(asOf)
		if err != nil || closed < 53 {
			t.Fatalf("%s %s: incomplete real indicator warm-up: closed=%d error=%v", asset.NativeID, asset.Market, closed, err)
		}
		matched, err := evaluateLiveAlertLatest(ctx, a, s, closed)
		if err != nil || !matched {
			t.Fatalf("%s %s: real closed market/indicator values unavailable: matched=%v error=%v", asset.NativeID, asset.Market, matched, err)
		}
		latest, err := s.row(closed - 1)
		if err != nil {
			t.Fatal(err)
		}
		closePosition, _ := s.fieldIndex("close")
		t.Logf("real Hyperliquid %s %s: %d closed D1 candles, latest=%s close=%g; Ichimoku/weekday and H4 EMA condition evaluated successfully", asset.NativeID, asset.Market, closed, time.UnixMilli(latest.timestamp).UTC().Format(time.RFC3339), latest.values[closePosition])
	}
	after := diagnostics.Diagnostics().RESTRequests
	if after-before != 4 {
		t.Fatalf("bounded smoke made %d actual REST requests; expected one history request per asset/period", after-before)
	}
	reused, err := l.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{condition}, periods, asOf)
	if err != nil || len(reused.Files) != 4 || len(reused.Failures) != 0 {
		t.Fatalf("real-history repeat failed: files=%d failures=%v error=%v", len(reused.Files), reused.Failures, err)
	}
	if got := diagnostics.Diagnostics().RESTRequests; got != after {
		t.Fatalf("same-cutoff persisted history refetched REST: before=%d after=%d", after, got)
	}
	t.Logf("real source preparation/evaluation passed; actual REST requests=%d, same-cutoff cache requests=0, Telegram sends=0, temporary profile only", after-before)
}

func TestPublicAlertLiveReportedHyperliquidAssets(t *testing.T) {
	if os.Getenv("CRYPTODESK_LIVE_PUBLIC_ALERT_SMOKE") != "1" {
		t.Skip("explicit opt-in only: real public history, temporary profile, no Telegram")
	}
	m := sourceEnrichmentManager(t, false)
	actual, err := exchange.New("hyperliquid")
	if err != nil {
		t.Fatal(err)
	}
	items := []domain.Instrument{
		{NativeID: "xyz:RELIANCE", Market: "perpetual", Availability: "available"},
		{NativeID: "io:XIAOMI", Market: "perpetual", Availability: "available"},
		{NativeID: "@698", Market: "spot", Availability: "available"},
		{NativeID: "@300", Market: "spot", Availability: "available"},
	}
	l, err := NewLiveAlerts(m, func(context.Context, LiveAlert) error {
		t.Error("smoke attempted delivery")
		return errors.New("delivery forbidden in smoke")
	})
	if err != nil {
		t.Fatal(err)
	}
	l.publicData = &publicAlertData{adapters: map[string]exchange.Adapter{"hyperliquid": publicAlertLiveSmokeAdapter{Adapter: actual, items: items}}}
	c := publicAlertTestCondition()
	c.Request.Conditions = c.Request.Conditions[:1]
	c.Request.Conditions[0].Right.Field = "sma_27"
	asOf := time.Now().UTC().UnixMilli()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	started := time.Now()
	batch, err := l.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, asOf)
	if err != nil || len(batch.Assets) != len(items) {
		t.Fatal("real history preparation failed", batch.Failures, err)
	}
	for _, asset := range batch.Assets {
		failure := batch.Failures[asset.Symbol+"/d1"]
		if failure != nil && !errors.Is(failure, errLiveAlertsPendingBar) && !strings.Contains(failure.Error(), "after older-history backfill") {
			t.Fatal("unexpected provider/processing failure", failure)
		}
		raw, err := os.ReadFile(filepath.Join(m.profile, "screener-public-history", "hyperliquid", "raw", asset.Symbol+"-d1.csv"))
		if err != nil {
			t.Fatal(err)
		}
		bars, err := publicAlertReadRaw(raw, 86400)
		if err != nil || len(bars) == 0 {
			t.Fatal("reported market has no usable real history", asset.NativeID, err)
		}
		latest := bars[len(bars)-1]
		t.Logf("%s %s: retained %d actual closed D1 bars, first=%s latest=%s; result=%v", asset.NativeID, asset.Market, len(bars), time.Unix(bars[0].Time, 0).UTC().Format("2006-01-02"), time.Unix(latest.Time, 0).UTC().Format("2006-01-02"), failure)
	}
	t.Logf("bounded production preparation: elapsed=%s; prepared=%d unavailable=%d; no active-profile write or Telegram send", time.Since(started), len(batch.Files), len(batch.Failures))
}
