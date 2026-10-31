package screener

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLiveRequiredFieldsFollowOperandsAndTradeLevelsWithoutExpandingScope(t *testing.T) {
	companion := analysisField("sma_3")
	companion.Timeframe = "d1"
	ticks := analysisField("price")
	ticks.Timeframe = "tick"
	between := analysisCondition("between", analysisField("price"), analysisNumber(1))
	between.Upper = analysisField("kijun_26")
	saved := SavedAnalysisCondition{ID: "selected", Request: AnalysisRequest{Period: "h1", Conditions: []AnalysisCondition{
		analysisCondition("above", analysisField("weekday"), companion), between, analysisCondition("above", ticks, analysisNumber(0)),
	}, TradeSimulation: &AnalysisTradeSimulation{StopLoss: "low", TakeProfit: "bb_upper_2_2", Duration: 2}}, Analysis: &SavedConditionAnalysis{Assets: []SavedConditionProfitability{
		{Symbol: "eurusd", Timeframe: "h1", Matches: 1, ProfitabilityUnavailable: true},
	}}}
	config := LiveAlertsConfig{ConditionIDs: []string{saved.ID}, scope: []LiveAlertConditionPeriod{{ConditionID: saved.ID, Period: "h1"}}}
	want := map[string][]string{"eurusd/h1": {"bb_upper_2_2", "close", "kijun_26", "low", "weekday"}, "eurusd/d1": {"sma_3"}, "eurusd/tick": {"bidprice"}}
	if got := liveAlertRequiredFields(config, []SavedAnalysisCondition{saved}); !reflect.DeepEqual(got, want) {
		t.Fatalf("required fields crossed an operand timeframe or added unused fields: %+v", got)
	}
	saved.Request.TradeSimulation.TakeProfitMultiple = 1
	want["eurusd/h1"] = []string{"close", "kijun_26", "low", "weekday"}
	if got := liveAlertRequiredFields(config, []SavedAnalysisCondition{saved}); !reflect.DeepEqual(got, want) {
		t.Fatalf("risk-multiple target requested an unused field: %+v", got)
	}
	config.scope = []LiveAlertConditionPeriod{}
	if got := liveAlertRequiredFields(config, []SavedAnalysisCondition{saved}); len(got) != 0 {
		t.Fatalf("not-due condition scheduled enrichment: %+v", got)
	}
}

func TestLiveRealRefreshAddsClosedBarsAndRequiredIndicatorsBeforeScanningOrSending(t *testing.T) {
	m, trace, _ := refreshManager(t, false)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	asOf := start.Add(48*time.Hour + time.Millisecond)
	name := "eurusd-d1-2024-01-01-2024-01-04.csv"
	writeDataFile(t, m, name, fmt.Sprintf("timestamp,open,high,low,close,volume,weekday,kijun_2\n%d,10,11,9,10,10,1,\n", start.UnixMilli()))
	request := AnalysisRequest{Period: "d1", Conditions: []AnalysisCondition{
		analysisCondition("equal", analysisField("weekday"), analysisNumber(2)),
		analysisCondition("above", analysisField("close"), analysisField("kijun_2")),
	}}
	request.Conditions[1].ID = "kijun-rule"
	saved, err := saveLiveCondition(t, m, "Tuesday with Kijun", request)
	if err != nil {
		t.Fatal(err)
	}
	// The recorded condition was analyzed with these fields. A replaced CSV
	// has since lost them; Live must restore them before evaluating new bars.
	writeDataFile(t, m, name, fmt.Sprintf("timestamp,open,high,low,close,volume\n%d,10,11,9,10,10\n", start.UnixMilli()))
	var sent []LiveAlert
	l, err := NewLiveAlerts(m, func(_ context.Context, alert LiveAlert) error {
		page, err := m.Files(0, 10)
		if err != nil || len(page.Files) != 1 {
			t.Fatalf("scan sent before refreshed history was published: %+v %v", page, err)
		}
		data, err := os.ReadFile(filepath.Join(m.directory, page.Files[0].Name))
		if err != nil {
			t.Fatalf("alert preceded required indicator calculation: %s %v", data, err)
		}
		rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
		if err != nil || len(rows) != 3 {
			t.Fatalf("refresh did not repair just the missing closed bar: %s %v", data, err)
		}
		values := map[string]string{}
		for index, field := range rows[0] {
			values[field] = rows[2][index]
		}
		if values["weekday"] != "2" || values["kijun_2"] != "15" || values["close"] != "20" {
			t.Fatalf("alert preceded required indicator calculation: %+v", values)
		}
		sent = append(sent, alert)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.now = func() time.Time { return asOf }
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	if err = l.check(context.Background(), config, asOf.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Time != start.Add(24*time.Hour).UnixMilli() || sent[0].EntryPrice != 20 {
		t.Fatalf("did not scan exactly the appended, enriched, closed Tuesday bar: %+v", sent)
	}
	if data, err := os.ReadFile(trace); err != nil || len(data) == 0 {
		t.Fatal("refresh did not access the substituted production source transport", err)
	}
	info := l.Info()
	if len(info.Checks) != 1 || info.Checks[0].AssetsScanned != 1 || info.Checks[0].Status != "completed" || info.Checks[0].SentAlerts != 1 || info.Checks[0].LastChecked != asOf.UnixMilli() {
		t.Fatalf("real refresh/check was not recorded truthfully: %+v", info.Checks)
	}
}

func TestLiveRequiredIndicatorFailureCannotScanClaimOrSend(t *testing.T) {
	m, _, _ := refreshManager(t, false)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	name := "eurusd-d1-2024-01-01-2024-01-04.csv"
	writeDataFile(t, m, name, fmt.Sprintf("timestamp,open,high,low,close,volume,unknown_17\n%d,10,11,9,10,10,1\n", start.UnixMilli()))
	request := AnalysisRequest{Period: "d1", Conditions: []AnalysisCondition{analysisCondition("above", analysisField("unknown_17"), analysisNumber(0))}}
	saved, err := saveLiveCondition(t, m, "Missing unknown indicator", request)
	if err != nil {
		t.Fatal(err)
	}
	writeDataFile(t, m, name, fmt.Sprintf("timestamp,open,high,low,close,volume\n%d,10,11,9,10,10\n", start.UnixMilli()))
	l, err := NewLiveAlerts(m, func(context.Context, LiveAlert) error { t.Fatal("failed enrichment sent an alert"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.now = func() time.Time { return start.Add(48 * time.Hour) }
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	err = l.check(context.Background(), config, l.now().UnixMilli())
	if err == nil || !strings.Contains(err.Error(), "unknown_17") {
		t.Fatalf("missing required indicator was silently ignored: %v", err)
	}
	if len(l.state.Watermarks) != 0 || len(l.state.Recent) != 0 {
		t.Fatal("failed enrichment consumed a signal claim")
	}
	info := l.Info()
	if len(info.Checks) != 1 || info.Checks[0].Status != "failed" || info.Checks[0].AssetsScanned != 0 || info.Checks[0].SentAlerts != 0 {
		t.Fatalf("failed pre-scan enrichment was not recorded: %+v", info.Checks)
	}
}

func TestLiveStaleScheduledCalendarScopeDoesNotRefreshOrRecordAnAttempt(t *testing.T) {
	m := analysisTestManager(t)
	writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-11.csv", "timestamp,open,high,low,close,weekday\n1704067200000,10,12,9,11,1\n")
	request := defaultAnalysisRequest()
	request.Period = "d1"
	request.Conditions[0] = analysisCondition("equal", analysisField("weekday"), analysisNumber(2))
	saved, err := saveLiveCondition(t, m, "Tuesday only", request)
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLiveAlerts(m, func(context.Context, LiveAlert) error { t.Fatal("ineligible scheduled signal sent"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.refresh = func(context.Context, string, []historyGroup, int64) (map[string]bool, error) {
		t.Fatal("ineligible queued calendar scope reached provider refresh")
		return nil, nil
	}
	l.now = func() time.Time { return time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC) }
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	if err = l.checkCycle(context.Background(), config, l.now().UnixMilli(), true); err != nil {
		t.Fatal(err)
	}
	if info := l.Info(); len(info.Checks) != 1 || info.Checks[0].Status != "never" || info.Checks[0].LastChecked != 0 {
		t.Fatalf("calendar skip was falsely recorded as execution: %+v", info.Checks)
	}
}
