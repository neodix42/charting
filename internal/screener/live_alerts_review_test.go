package screener

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reviewLiveSeries(t *testing.T, m *Manager, name, csv string) *analysisSeries {
	t.Helper()
	writeDataFile(t, m, name, csv)
	return reviewIndexLiveSeries(t, m, name)
}

func reviewIndexLiveSeries(t *testing.T, m *Manager, name string) *analysisSeries {
	t.Helper()
	s, err := indexAnalysisCSV(context.Background(), m.directory, name, filepath.Join(t.TempDir(), "indexed.rows"), func(float64) {})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func reviewHourlyCSV(start int64, values []float64) string {
	var csv strings.Builder
	csv.WriteString("timestamp,open,high,low,close\n")
	for i, value := range values {
		fmt.Fprintf(&csv, "%d,%g,%g,%g,%g\n", start+int64(i)*3_600_000, value, value+1, value-0.5, value)
	}
	return csv.String()
}

func TestLiveAlertLatestEvaluatorWarmsTemporalComparisonsAndEvaluatesOnlyClosedSignal(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	crossWithin := analysisCondition("occurredWithin", analysisField("close"), analysisNumber(1))
	crossWithin.Comparison, crossWithin.Bars = "crossesAbove", 3
	remained := analysisCondition("remainedFor", analysisField("close"), analysisNumber(1))
	remained.Comparison, remained.Bars = "above", 3
	andCross := analysisCondition("crossesAbove", analysisField("close"), analysisNumber(1))
	andCross.ID = "second"
	for _, test := range []struct {
		name       string
		values     []float64
		conditions []AnalysisCondition
		want       bool
	}{
		{"open-bar-is-excluded", []float64{1, 2, 4, 1, 2, 100}, []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(50))}, false},
		{"historical-match-is-not-replayed", []float64{100, 2, 4, 1, 2, 100}, []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(50))}, false},
		{"latest-cross-has-preceding-state", []float64{1, 1, 1, 1, 2, 100}, []AnalysisCondition{andCross}, true},
		{"cross-at-first-bar-of-window", []float64{1, 1, 2, 2, 2, 100}, []AnalysisCondition{crossWithin}, true},
		{"cross-before-window-is-excluded", []float64{1, 2, 2, 2, 2, 100}, []AnalysisCondition{crossWithin}, false},
		{"remained-counts-latest-closed-bars", []float64{1, 1, 2, 2, 2, 100}, []AnalysisCondition{remained}, true},
		{"remained-rejects-interrupted-window", []float64{1, 2, 2, 1, 2, 100}, []AnalysisCondition{remained}, false},
		{"every-and-row-advances-when-another-is-false", []float64{1, 1, 1, 1, 2, 100}, []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(1)), andCross}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := analysisTestManager(t)
			s := reviewLiveSeries(t, m, "btcusd-h1-2024-01-01-2024-01-02.csv", reviewHourlyCSV(base, test.values))
			asOf := base + 5*3_600_000 + 30*60_000
			closed, err := s.closedCount(asOf)
			if err != nil || closed != 5 {
				t.Fatalf("closed rows: %d %v", closed, err)
			}
			a := &analysisState{request: AnalysisRequest{Conditions: test.conditions}, series: map[string]*analysisSeries{"base": s}, asOf: asOf}
			matched, err := evaluateLiveAlertLatest(context.Background(), a, s, closed)
			if err != nil || matched != test.want {
				t.Fatalf("latest match %t, want %t: %v", matched, test.want, err)
			}
		})
	}
}

func TestLiveAlertLatestUsesOnlyAlignedClosedCompanionAndEarlierBarOffsets(t *testing.T) {
	m := analysisTestManager(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	daily := reviewLiveSeries(t, m, "btcusd-d1-2024-01-01-2024-01-04.csv", fmt.Sprintf("timestamp,close\n%d,10\n%d,100\n", base, base+86_400_000))
	hourly := reviewLiveSeries(t, m, "btcusd-h1-2024-01-01-2024-01-04.csv", reviewHourlyCSV(base+23*3_600_000, []float64{40, 50, 200}))
	asOf := base + 25*3_600_000 + 30*60_000
	closed, err := hourly.closedCount(asOf)
	if err != nil || closed != 2 {
		t.Fatalf("closed hourly rows: %d %v", closed, err)
	}
	companion := analysisField("close")
	companion.Timeframe = "d1"
	previous := analysisField("close")
	previous.Offset = 1
	conditions := []AnalysisCondition{
		analysisCondition("above", analysisField("close"), companion),
		{ID: "earlier", Operator: "equal", Left: previous, Right: analysisNumber(40)},
	}
	a := &analysisState{request: AnalysisRequest{Conditions: conditions}, series: map[string]*analysisSeries{"base": hourly, "daily": daily}, asOf: asOf}
	matched, err := evaluateLiveAlertLatest(context.Background(), a, hourly, closed)
	if err != nil || !matched {
		t.Fatalf("future daily candle leaked into an hourly signal or earlier offset failed: %t %v", matched, err)
	}
}

func TestLiveAlertLatestAcceptsVerifiedWeekendCompanionCoverageWithoutInventingBars(t *testing.T) {
	m := analysisTestManager(t)
	friday := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC).UnixMilli()
	monday := friday + 3*86_400_000
	dailyName := "btcusd-d1-2024-01-05-2024-01-06.csv"
	daily := reviewLiveSeries(t, m, dailyName, fmt.Sprintf("timestamp,close\n%d,10\n", friday))
	hourly := reviewLiveSeries(t, m, "btcusd-h1-2024-01-08-2024-01-09.csv", reviewHourlyCSV(monday, []float64{20}))
	asOf := monday + 3_600_000 + 30_000
	companion := analysisField("close")
	companion.Timeframe = "d1"
	a := &analysisState{request: AnalysisRequest{Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), companion)}}, series: map[string]*analysisSeries{"base": hourly, "daily": daily}, asOf: asOf}
	if matched, err := evaluateLiveAlertLatest(context.Background(), a, hourly, 1); err != nil || matched {
		t.Fatalf("unverified stale companion unexpectedly matched: %t %v", matched, err)
	}
	path := filepath.Join(m.directory, dailyName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata := fileMetadata{Version: 1, Verified: true, Bytes: st.Size(), Modified: st.ModTime().UnixNano(), Checksum: fmt.Sprintf("%x", sha256.Sum256(data)), CheckedThrough: asOf}
	if err = writeFileMetadata(m.directory, dailyName, metadata); err != nil {
		t.Fatal(err)
	}
	a.series["daily"] = reviewIndexLiveSeries(t, m, dailyName)
	if matched, err := evaluateLiveAlertLatest(context.Background(), a, hourly, 1); err != nil || !matched {
		t.Fatalf("verified market closure discarded its latest actual companion: %t %v", matched, err)
	}
	if a.series["daily"].count != 1 {
		t.Fatal("weekend freshness invented missing market candles")
	}
}

func TestLiveAlertIndicativeLevelsUseSignalCloseStopOffsetAndRiskMultiple(t *testing.T) {
	m := analysisTestManager(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	s := reviewLiveSeries(t, m, "btcusd-h1-2024-01-01-2024-01-02.csv", fmt.Sprintf("timestamp,open,high,low,close\n%d,10,12,8,11\n%d,11,13,9,12\n%d,12,14,10,13\n%d,99,100,98,99\n", base, base+3_600_000, base+2*3_600_000, base+3*3_600_000))
	row, err := s.row(2)
	if err != nil {
		t.Fatal(err)
	}
	saved := SavedAnalysisCondition{ID: strings.Repeat("a", 32), Name: "Signal", Request: AnalysisRequest{TradeSimulation: &AnalysisTradeSimulation{StopLoss: "low", StopLossBarsAgo: 1, TakeProfitMultiple: 2, Duration: 5}}}
	alert, err := liveAlertFromMatch(saved, s, 2, row)
	if err != nil || alert.EntryPrice != 13 || alert.StopLoss == nil || *alert.StopLoss != 9 || alert.TakeProfit == nil || *alert.TakeProfit != 21 || alert.Time != row.timestamp {
		t.Fatalf("incorrect signal-time indicative prices: %+v %v", alert, err)
	}
	// A field target comes from the signal bar even when SL uses an older bar.
	saved.Request.TradeSimulation.TakeProfitMultiple = 0
	saved.Request.TradeSimulation.TakeProfit = "high"
	alert, err = liveAlertFromMatch(saved, s, 2, row)
	if err != nil || alert.TakeProfit == nil || *alert.TakeProfit != 14 || alert.StopLoss == nil || *alert.StopLoss != 9 {
		t.Fatalf("signal-field target or stop offset changed: %+v %v", alert, err)
	}
	saved.Request.TradeSimulation = nil
	alert, err = liveAlertFromMatch(saved, s, 2, row)
	if err != nil || alert.EntryPrice != 13 || alert.StopLoss != nil || alert.TakeProfit != nil {
		t.Fatalf("a condition without trade levels invented levels: %+v %v", alert, err)
	}
}

func TestLiveAlertsPartialRefreshEvaluatesSuccessfulAssetsAndKeepsFailedAssetsUnclaimed(t *testing.T) {
	m, l, config, path := liveAlertsFixture(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeDataFile(t, m, "btcusd-h1-2024-01-01-2024-01-03.csv", string(data))
	config.Symbols = []string{"btcusd", "eurusd"}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	refreshFailure := errors.New("BTCUSD provider access was refused")
	l.refresh = func(context.Context, string, []historyGroup, int64) (map[string]bool, error) {
		return map[string]bool{"eurusd/h1": true}, refreshFailure
	}
	var alerts []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { alerts = append(alerts, alert); return nil }
	asOf := time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()
	err = l.check(context.Background(), config, asOf)
	if !errors.Is(err, refreshFailure) || len(alerts) != 1 || alerts[0].Symbol != "eurusd" {
		t.Fatalf("partial refresh dropped a current signal or hid its failure: %v %+v", err, alerts)
	}
	if len(l.state.Watermarks) != 1 || l.state.Watermarks[config.ConditionIDs[0]+"/eurusd/h1"] != 1704070800000 {
		t.Fatalf("failed asset was claimed or successful asset unclaimed: %+v", l.state.Watermarks)
	}
	if _, exists := l.state.Watermarks[config.ConditionIDs[0]+"/btcusd/h1"]; exists {
		t.Fatal("stale failed asset cannot be claimed from its previous CSV")
	}
}

func TestLiveAlertsPartialRefreshNeverUsesFailedCompanionAsStaleFallback(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	daily := analysisField("close")
	daily.Timeframe = "d1"
	request := AnalysisRequest{Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), daily)}}
	saved, err := m.SaveAnalysisCondition("Daily companion", request)
	if err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = []string{saved.ID}
	writeDataFile(t, m, "eurusd-d1-2023-12-31-2024-01-03.csv", "timestamp,close\n1703980800000,1\n")
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	refreshFailure := errors.New("daily companion refresh failed")
	l.refresh = func(context.Context, string, []historyGroup, int64) (map[string]bool, error) {
		return map[string]bool{"eurusd/h1": true}, refreshFailure
	}
	sends := 0
	l.send = func(context.Context, LiveAlert) error { sends++; return nil }
	err = l.check(context.Background(), config, time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli())
	if !errors.Is(err, refreshFailure) || !strings.Contains(err.Error(), "no usable d1 data") || sends != 0 || len(l.state.Watermarks) != 0 {
		t.Fatalf("failed companion fell back to old history: %v sends=%d watermarks=%+v", err, sends, l.state.Watermarks)
	}
}

func TestLiveAlertsPrunesOnlyDeletedConditionHistoryAndPersistsBeforeNewChecks(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	retained, err := m.SaveAnalysisCondition("Inactive condition", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := m.SaveAnalysisCondition("Deleted condition", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	currentKey := config.ConditionIDs[0] + "/eurusd/h1"
	inactiveKey := retained.ID + "/eurusd/h1"
	deletedKey := deleted.ID + "/eurusd/h1"
	l.state.Watermarks = map[string]int64{currentKey: 1, inactiveKey: 2, deletedKey: 3}
	if err = writeState(l.path, l.state); err != nil {
		t.Fatal(err)
	}
	if err = m.DeleteAnalysisCondition(deleted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	if len(l.state.Watermarks) != 2 || l.state.Watermarks[currentKey] != 1 || l.state.Watermarks[inactiveKey] != 2 {
		t.Fatalf("pruning discarded inactive existing history or retained a deleted ID: %+v", l.state.Watermarks)
	}
	restarted, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if len(restarted.state.Watermarks) != 2 || restarted.state.Watermarks[inactiveKey] != 2 {
		t.Fatalf("pruned history was not durable: %+v", restarted.state.Watermarks)
	}
	// Selecting the formerly inactive condition retains its previous checkpoint.
	config.ConditionIDs = []string{retained.ID}
	if _, err = restarted.Configure(config); err != nil || restarted.state.Watermarks[inactiveKey] != 2 || restarted.state.Watermarks[currentKey] != 1 {
		t.Fatalf("reselecting a saved condition discarded duplicate prevention: %+v %v", restarted.state.Watermarks, err)
	}
}

func TestLiveAlertsDeletedConditionPruneWriteFailureRollsBack(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	deleted, err := m.SaveAnalysisCondition("Removed", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	key := deleted.ID + "/eurusd/h1"
	l.state.Watermarks[key] = 123
	if err = m.DeleteAnalysisCondition(deleted.ID); err != nil {
		t.Fatal(err)
	}
	l.path = t.TempDir() // A directory cannot be replaced by the state JSON.
	if _, err = l.Configure(config); err == nil || l.state.Watermarks[key] != 123 {
		t.Fatalf("failed prune changed checkpoint memory or hid the error: %+v %v", l.state.Watermarks, err)
	}
}
