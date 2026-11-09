package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLiveAlertChartDukascopyUsesCheckedClosedSnapshotWithoutAnalysis(t *testing.T) {
	m, l, config, path := liveAlertsFixture(t)
	if m.analysis != nil {
		t.Fatal("fixture unexpectedly requires an Analysis run")
	}
	var sent []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { sent = append(sent, alert); return nil }
	asOf := time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()
	if err := l.check(context.Background(), config, asOf); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Chart == nil || sent[0].ChartError != "" {
		t.Fatalf("matched signal did not own a chart: %+v", sent)
	}
	alert, chart := sent[0], sent[0].Chart
	if len(chart.Bars) != 2 || chart.MatchIndex != 1 || chart.StartIndex != 0 || chart.MatchTime != alert.Time || chart.Symbol != alert.Symbol || chart.Timeframe != "h1" {
		t.Fatalf("chart includes wrong candles/identity: %+v", chart)
	}
	want := []AnalysisBar{
		{Index: 0, Time: 1704067200000, Values: map[string]float64{"timestamp": 1704067200000, "open": 10, "high": 12, "low": 9, "close": 11, "volume": 1}},
		{Index: 1, Time: 1704070800000, Values: map[string]float64{"timestamp": 1704070800000, "open": 11, "high": 14, "low": 10, "close": 13, "volume": 1, "sma_2": 12}},
	}
	if !reflect.DeepEqual(chart.Bars, want) || chart.Trade != nil || chart.TradeExecution != nil || len(chart.ProfitableMatches) != 0 {
		t.Fatalf("chart did not preserve checked enriched values without future outcomes: %+v", chart)
	}
	if chart.Signal == nil || chart.Signal.Entry != alert.EntryPrice || chart.Signal.StopLoss == nil || *chart.Signal.StopLoss != 9 || chart.Signal.TakeProfit == nil || *chart.Signal.TakeProfit != 21 {
		t.Fatalf("chart levels disagree with text signal: %+v", chart.Signal)
	}
	// Snapshot owns every map and optional level, and survives private index
	// cleanup plus later replacement of the downloaded source.
	*alert.StopLoss = 1
	if *chart.Signal.StopLoss != 9 {
		t.Fatal("chart levels alias the outbound alert")
	}
	if err := os.WriteFile(path, []byte("changed after delivery"), 0600); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(chart.Bars, want) {
		t.Fatal("source modification changed an already prepared chart")
	}
	if work, _ := filepath.Glob(filepath.Join(m.profile, ".screener-live-analysis-*")); len(work) != 0 {
		t.Fatal("live chart retained its temporary index", work)
	}
	encoded, err := json.Marshal(sent[0])
	if err != nil || strings.Contains(string(encoded), "chart") || strings.Contains(string(encoded), "sma_2") {
		t.Fatalf("ephemeral media leaked into serialized alert: %s %v", encoded, err)
	}
	state, err := os.ReadFile(l.path)
	if err != nil || strings.Contains(string(state), "sma_2") || strings.Contains(string(state), "\"bars\"") {
		t.Fatalf("chart persisted in Recent: %s %v", state, err)
	}
}

func TestLiveAlertChartPublicRowsKeepPeriodSourceAndNativeAssetIdentity(t *testing.T) {
	m, l, saved, fake := namedPublicLiveFixture(t)
	fake.items = []domain.Instrument{{NativeID: "@268", ProviderMetadata: map[string]string{"displaySymbol": "AAPL/USDC"}, Market: "spot", Availability: "TRADING"}}
	l.publicData.adapters["hyperliquid"] = fake
	l.publicData.adapters["binance"] = &publicAlertFakeAdapter{price: 200, items: []domain.Instrument{{NativeID: "BTCUSDT", Market: "spot", Availability: "TRADING"}}}
	request := saved.Request
	request.Conditions[0].Right = AnalysisOperand{Kind: "field", Field: "sma_3", Timeframe: "d1", Aggregate: "value", Window: 1}
	request.Conditions[0].Operator = "equal"
	var err error
	saved, err = m.UpdateAnalysisCondition(saved.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	entries := []LiveAlertDefinition{
		{ID: strings.Repeat("a", 32), Name: "Hourly HL", ConditionID: saved.ID, Period: "h1", DataSource: "hyperliquid", Enabled: true},
		{ID: strings.Repeat("b", 32), Name: "Daily HL", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true},
		{ID: strings.Repeat("c", 32), Name: "Daily other source", ConditionID: saved.ID, Period: "d1", DataSource: "binance", Enabled: true},
	}
	config := LiveAlertsConfig{Enabled: true, Alerts: entries}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	var sent []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { sent = append(sent, alert); return nil }
	asOf := time.Date(2026, 11, 5, 12, 0, 5, 0, time.UTC).UnixMilli()
	if err = l.check(context.Background(), l.state.Config, asOf); err != nil {
		t.Fatal(err)
	}
	if len(sent) != len(entries) {
		t.Fatalf("independent source/period rows did not produce snapshots: %+v", sent)
	}
	for _, alert := range sent {
		chart := alert.Chart
		symbol, name, price := "@268", "AAPL/USDC", float64(101)
		if alert.DataSource == "binance" {
			symbol, name, price = "BTCUSDT", "BTCUSDT", 201
		}
		if chart == nil || alert.ChartError != "" || chart.Symbol != symbol || chart.Name != name || chart.Timeframe != alert.Timeframe || chart.ResultID != alert.AlertID || chart.MatchTime != alert.Time || chart.Signal.Entry != price {
			t.Fatalf("native source metadata or matching row was lost: %+v", alert)
		}
		if len(chart.Bars) == 0 || len(chart.Bars) > liveAlertChartPreviousBars+1 || chart.Bars[len(chart.Bars)-1].Time != alert.Time {
			t.Fatalf("unbounded or mismatched chart: %+v", chart)
		}
		field := "sma_3"
		if alert.Timeframe == "h1" {
			field = "d1:sma_3"
		}
		if chart.Bars[len(chart.Bars)-1].Values[field] != price || chart.Bars[len(chart.Bars)-1].Values["close"] != alert.EntryPrice {
			t.Fatalf("prepared/enriched series differs from evaluated series: %+v", chart.Bars[len(chart.Bars)-1])
		}
		for _, bar := range chart.Bars {
			if analysisClosedAt(bar.Time, alert.Timeframe) > asOf {
				t.Fatal("public chart contains an unclosed bar")
			}
		}
	}
	if work, _ := filepath.Glob(filepath.Join(m.profile, ".screener-public-analysis-*")); len(work) != 0 {
		t.Fatal("public chart retained temporary index", work)
	}
}

func liveAlertChartIndexedState(t *testing.T, files map[string]string, request AnalysisRequest, asOf int64) (*analysisState, map[string]*analysisSeries) {
	t.Helper()
	directory, work := t.TempDir(), t.TempDir()
	a := &analysisState{directory: directory, work: work, request: request, series: map[string]*analysisSeries{}, cache: newAnalysisRowCache(), asOf: asOf}
	for name, csv := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(csv), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := indexAnalysisCSV(context.Background(), directory, name, filepath.Join(work, name+".rows"), func(float64) {})
		if err != nil {
			t.Fatal(err)
		}
		s.cache = a.cache
		a.series[name] = s
	}
	return a, a.series
}

func TestLiveAlertChartBoundsFieldsAndBarsAndRetainsRequiredIndicators(t *testing.T) {
	var csv strings.Builder
	csv.WriteString("timestamp,open,high,low,close,volume")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&csv, ",indicator_%d", i)
	}
	csv.WriteString(",required_stop,required_signal\n")
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&csv, "%d,10,12,9,11,3", start+int64(i)*60000)
		for j := 0; j < 80; j++ {
			fmt.Fprintf(&csv, ",%d", j)
		}
		csv.WriteString(",8,42\n")
	}
	request := AnalysisRequest{Conditions: []AnalysisCondition{analysisCondition("above", analysisField("required_signal"), analysisNumber(0))}, TradeSimulation: &AnalysisTradeSimulation{StopLoss: "required_stop", TakeProfitMultiple: 2}}
	name := "eurusd-m1-2024-01-01-2024-01-03.csv"
	a, series := liveAlertChartIndexedState(t, map[string]string{name: csv.String()}, request, start+149*60000+30000)
	s := series[name]
	latest, _ := s.row(148)
	alert, err := liveAlertFromMatch(SavedAnalysisCondition{ID: "test", Request: request}, s, 148, latest)
	if err != nil {
		t.Fatal(err)
	}
	chart, err := prepareLiveAlertChart(context.Background(), a, s, 148, alert)
	if err != nil {
		t.Fatal(err)
	}
	if len(chart.Bars) != 121 || chart.StartIndex != 28 || chart.Bars[0].Index != 28 || chart.Bars[120].Index != 148 || len(chart.Fields) != 64 {
		t.Fatalf("chart bounds incorrect: %d bars %d fields start %d", len(chart.Bars), len(chart.Fields), chart.StartIndex)
	}
	for _, bar := range chart.Bars {
		if len(bar.Values) != 64 || bar.Values["required_signal"] != 42 || bar.Values["required_stop"] != 8 || bar.Values["close"] != 11 {
			t.Fatalf("required late CSV fields were displaced by unrelated columns: %+v", bar)
		}
	}
	if chart.Signal.StopLoss == nil || *chart.Signal.StopLoss != 8 || chart.Signal.TakeProfit == nil || *chart.Signal.TakeProfit != 17 {
		t.Fatalf("level fields disagree with text: %+v", chart.Signal)
	}
}

func TestLiveAlertChartCompanionsUseClosedAlignmentAndOperandNames(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	base, companion := "eurusd-h1-2024-01-01-2024-01-04.csv", "eurusd-d1-2023-12-30-2024-01-04.csv"
	right := analysisField("sma_3")
	right.Timeframe, right.Offset, right.Aggregate, right.Window = "d1", 1, "highest", 2
	request := AnalysisRequest{Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), right)}}
	a, series := liveAlertChartIndexedState(t, map[string]string{
		base:      fmt.Sprintf("timestamp,close\n%d,100\n%d,101\n%d,102\n", start+23*3600000, start+24*3600000, start+25*3600000),
		companion: fmt.Sprintf("timestamp,close,sma_3\n%d,1,5\n%d,1,7\n%d,1,3\n%d,1,999\n", start-2*86400000, start-86400000, start, start+86400000),
	}, request, start+26*3600000)
	s := series[base]
	row, _ := s.row(2)
	alert, err := liveAlertFromMatch(SavedAnalysisCondition{Request: request}, s, 2, row)
	if err != nil {
		t.Fatal(err)
	}
	chart, err := prepareLiveAlertChart(context.Background(), a, s, 2, alert)
	if err != nil {
		t.Fatal(err)
	}
	key := "d1:sma_3[offset 1][highest 2]"
	if !reflect.DeepEqual(chart.Fields, []string{"timestamp", "close", key}) {
		t.Fatal("companion operand label differs from Analysis", chart.Fields)
	}
	for _, bar := range chart.Bars {
		if bar.Values[key] != 7 {
			t.Fatalf("companion used future/unclosed data or wrong offset/window: %+v", bar)
		}
	}
}

func TestLiveAlertChartPartialLevelsMissingIndicatorsAndPreparationFailure(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	name := "eurusd-h1-2024-01-01-2024-01-03.csv"
	request := AnalysisRequest{Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(0))}, TradeSimulation: &AnalysisTradeSimulation{StopLoss: "missing", TakeProfit: "target"}}
	a, series := liveAlertChartIndexedState(t, map[string]string{name: fmt.Sprintf("timestamp,close,sma_3,target\n%d,11,,15\n", start)}, request, start+3600000)
	s := series[name]
	row, _ := s.row(0)
	alert, err := liveAlertFromMatch(SavedAnalysisCondition{Request: request}, s, 0, row)
	if err != nil {
		t.Fatal(err)
	}
	if err = attachLiveAlertChart(context.Background(), a, s, 0, &alert); err != nil || alert.Chart == nil {
		t.Fatal("partial live levels prevented media", alert.ChartError, err)
	}
	if alert.Chart.Signal.StopLoss != nil || alert.Chart.Signal.TakeProfit == nil || *alert.Chart.Signal.TakeProfit != 15 {
		t.Fatal("optional SL/TP was fabricated or lost", alert.Chart.Signal)
	}
	if _, exists := alert.Chart.Bars[0].Values["sma_3"]; exists {
		t.Fatal("missing indicator was filled with a fake zero")
	}
	if err = os.WriteFile(s.source, []byte("malformed replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	failed := LiveAlert{Time: row.timestamp, EntryPrice: 11}
	if err = attachLiveAlertChart(context.Background(), a, s, 0, &failed); err != nil || failed.Chart != nil || failed.ChartError == "" || failed.EntryPrice != 11 {
		t.Fatalf("media preparation error lost the signal or had no fallback reason: %+v %v", failed, err)
	}
}

func TestLiveAlertChartCancellationAndUnclosedSignalAreNotPrepared(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	name := "eurusd-h1-2024-01-01-2024-01-03.csv"
	a, series := liveAlertChartIndexedState(t, map[string]string{name: fmt.Sprintf("timestamp,close\n%d,11\n", start)}, AnalysisRequest{}, start+1800000)
	s := series[name]
	alert := LiveAlert{Time: start, EntryPrice: 11}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := attachLiveAlertChart(ctx, a, s, 0, &alert); !errors.Is(err, context.Canceled) || alert.Chart != nil || alert.ChartError != "" {
		t.Fatalf("canceled preparation was treated as a sendable fallback: %+v %v", alert, err)
	}
	if _, err := prepareLiveAlertChart(context.Background(), a, s, 0, alert); err == nil {
		t.Fatal("still-open signal was included in media")
	}
}

func TestLiveAlertChartOversizedRulePreservesTextSignalAndDurableClaim(t *testing.T) {
	m, l, config, path := liveAlertsFixture(t)
	var csv strings.Builder
	csv.WriteString("timestamp,close")
	request := AnalysisRequest{Period: "h1"}
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&csv, ",indicator_%d", i)
	}
	csv.WriteString("\n1704067200000,11")
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&csv, ",%d", 2-i%2)
	}
	csv.WriteByte('\n')
	for i := 0; i < 32; i++ {
		condition := analysisCondition("above", analysisField(fmt.Sprintf("indicator_%d", i*2)), analysisField(fmt.Sprintf("indicator_%d", i*2+1)))
		condition.ID = fmt.Sprintf("condition-%d", i)
		request.Conditions = append(request.Conditions, condition)
	}
	if err := os.WriteFile(path, []byte(csv.String()), 0600); err != nil {
		t.Fatal(err)
	}
	saved, err := saveLiveCondition(t, m, "More than 64 required chart fields", request)
	if err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = []string{saved.ID}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	var sent []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { sent = append(sent, alert); return nil }
	asOf := time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()
	if err = l.check(context.Background(), config, asOf); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Chart != nil || !strings.Contains(sent[0].ChartError, "more than 64") || sent[0].EntryPrice != 11 {
		t.Fatalf("oversized chart silently lost its signal: %+v", sent)
	}
	if err = l.check(context.Background(), config, asOf); err != nil || len(sent) != 1 {
		t.Fatal("chart failure bypassed durable signal deduplication", err, len(sent))
	}
}
