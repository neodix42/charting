package screener

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLiveWeeklyNaturalCloseUsesMondayUTCAndCalendarEligibility(t *testing.T) {
	weekly := calendarAlertSaved("w1", calendarAlertCondition("close", "above", 1))
	for _, test := range []struct{ after, want string }{
		{"2024-01-07T23:59:59Z", "2024-01-08T00:00:00Z"},
		{"2024-01-08T00:00:00Z", "2024-01-15T00:00:00Z"},
		{"2024-01-08T03:59:59+04:00", "2024-01-08T00:00:00Z"},
		{"2024-12-31T12:00:00Z", "2025-01-06T00:00:00Z"},
		{"1969-12-31T12:00:00Z", "1970-01-05T00:00:00Z"},
	} {
		after, want := calendarAlertTime(test.after), calendarAlertTime(test.want)
		got, ok := nextLiveAlertConditionClose(after, weekly, "w1")
		if !ok || !got.Equal(want) || !nextLiveAlertCheck(after, []string{"w1"}).Equal(want) || !liveAlertConditionDueAt(weekly, "w1", got) {
			t.Fatalf("weekly natural close %s: got %s, %t want %s", after, got, ok, want)
		}
	}
	monday := calendarAlertSaved("w1", calendarAlertCondition("weekday", "equal", 1), calendarAlertCondition("hour", "equal", 0))
	next, ok := nextLiveAlertConditionClose(calendarAlertTime("2024-01-03T12:00:00Z"), monday, "w1")
	if !ok || next.Format(time.RFC3339) != "2024-01-08T00:00:00Z" || !strings.Contains(liveAlertConditionTimingHint(monday, "w1", next), "Monday 00:00 UTC") {
		t.Fatal("weekly calendar schedule lost Monday opening", next, ok)
	}
	tuesday := calendarAlertSaved("w1", calendarAlertCondition("weekday", "equal", 2))
	if next, ok := nextLiveAlertConditionClose(calendarAlertTime("2024-01-03T12:00:00Z"), tuesday, "w1"); ok || !next.IsZero() || !strings.Contains(liveAlertConditionTimingHint(tuesday, "w1", time.Time{}), "cannot match") {
		t.Fatal("impossible weekly Tuesday-open calendar was admitted", next, ok)
	}
}

func saveWeeklyLiveCondition(t *testing.T, m *Manager, request AnalysisRequest, weeklyName string) SavedAnalysisCondition {
	t.Helper()
	saved, err := m.SaveAnalysisCondition("weekly short", request)
	if err != nil {
		t.Fatal(err)
	}
	saved.Analysis = &SavedConditionAnalysis{AnalyzedAt: time.Now().UnixMilli(), Assets: []SavedConditionProfitability{
		liveProfitRow("eurusd", "w1", weeklyName, 8, 10),
		liveProfitRow("gbpusd", "w1", "gbpusd-w1-2024-01-01-2024-01-22.csv", 7, 10),
	}}
	conditions, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	for i := range conditions {
		if conditions[i].ID == saved.ID {
			conditions[i] = saved
		}
	}
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: conditions}); err != nil {
		t.Fatal(err)
	}
	return saved
}
func weeklyLiveRequest() AnalysisRequest {
	return AnalysisRequest{Period: "w1", ProfitabilityFilter: 80, Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(1))}, TradeSimulation: &AnalysisTradeSimulation{Direction: "short", StopLoss: "high", TakeProfit: "low", Duration: 1}}
}
func TestLiveWeeklyInheritedScopeUsesExistingEightyPercentEvidenceAndPersistsClaims(t *testing.T) {
	m := analysisTestManager(t)
	const weeklyName = "eurusd-w1-2024-01-01-2024-01-22.csv"
	weekly := "timestamp,open,high,low,close,volume\n1704067200000,10,12,9,11,1\n1704672000000,20,22,19,21,1\n1705276800000,90,99,80,95,1\n"
	writeDataFile(t, m, weeklyName, weekly)
	writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-22.csv", weeklyDailyCSV(0, 14, false))
	writeDataFile(t, m, "eurusd-h1-2024-01-01-2024-01-22.csv", reviewHourlyCSV(1704067200000, []float64{10, 11}))
	writeDataFile(t, m, "gbpusd-w1-2024-01-01-2024-01-22.csv", weekly)
	saved := saveWeeklyLiveCondition(t, m, weeklyLiveRequest(), weeklyName)
	var sent []LiveAlert
	l, err := NewLiveAlerts(m, func(_ context.Context, alert LiveAlert) error { sent = append(sent, alert); return nil })
	if err != nil {
		t.Fatal(err)
	}
	asOf := calendarAlertTime("2024-01-15T00:00:00Z")
	l.now = func() time.Time { return asOf }
	var refreshed []string
	l.refresh = func(ctx context.Context, _ string, groups []historyGroup, _ int64) (map[string]bool, error) {
		pairs := map[string]bool{}
		for _, group := range groups {
			match := downloadFilename.FindStringSubmatch(group.name)
			pair := match[1] + "/" + match[2]
			pairs[pair] = true
			refreshed = append(refreshed, pair)
		}
		return pairs, ctx.Err()
	}
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}}
	info, err := l.Configure(config)
	if err != nil || len(info.ConditionScopes) != 1 || info.ConditionScopes[0].Period != "w1" || len(info.ConditionScopes[0].Targets) != 1 || info.ConditionScopes[0].Targets[0].Period != "w1" || info.ConditionScopes[0].Targets[0].Profitability.ProfitablePercent != 80 || info.NextRun != asOf.Add(7*24*time.Hour).UnixMilli() {
		t.Fatal("existing weekly evidence was not inherited without reanalysis", info, err)
	}
	if err = l.checkCycle(context.Background(), config, asOf.UnixMilli(), true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(refreshed, []string{"eurusd/d1", "eurusd/w1"}) || len(sent) != 1 || sent[0].Timeframe != "w1" || sent[0].Time != 1704672000000 || sent[0].Profitability == nil || sent[0].Profitability.ProfitablePercent != 80 {
		t.Fatal("weekly target scope, cutoff or recorded profitability changed", refreshed, sent)
	}
	if len(l.state.Watermarks) != 1 || l.state.Watermarks[saved.ID+"/eurusd/w1"] != 1704672000000 {
		t.Fatal("daily prerequisite became an alert target", l.state.Watermarks)
	}
	if err = l.checkCycle(context.Background(), config, asOf.UnixMilli(), true); err != nil || len(sent) != 1 {
		t.Fatal("weekly candle was sent twice", sent, err)
	}
	l.Close()
	reopened, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal("weekly delivery claim failed state validation", err)
	}
	defer reopened.Close()
	if reopened.state.Watermarks[saved.ID+"/eurusd/w1"] != 1704672000000 || reopened.Info().ConditionScopes[0].Period != "w1" {
		t.Fatal("weekly claim/scope lost on restart", reopened.Info())
	}
}

func TestLiveWeeklyRealRefreshUpdatesDailyThenDerivesAndEnrichesWeeklyBeforeAlert(t *testing.T) {
	m, trace, _ := weeklyTransportManager(t, false)
	const weeklyName = "eurusd-w1-2024-01-01-2024-01-15.csv"
	writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-15.csv", weeklyDailyCSV(0, 7, true))
	writeDataFile(t, m, weeklyName, "timestamp,open,high,low,close,volume,sma_2\n1704067200000,10,17,9,16,28,\n")
	request := weeklyLiveRequest()
	request.Conditions = []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisField("sma_2"))}
	saved := saveWeeklyLiveCondition(t, m, request, weeklyName)
	// A replaced file has lost its saved required indicator. Live must rebuild it.
	writeDataFile(t, m, weeklyName, "timestamp,open,high,low,close,volume\n1704067200000,10,17,9,16,28\n")
	var sent []LiveAlert
	l, err := NewLiveAlerts(m, func(_ context.Context, alert LiveAlert) error {
		page, err := m.Files(0, 10)
		if err != nil || page.Total != 2 {
			t.Fatal("daily or weekly family lost before send", page, err)
		}
		for _, file := range page.Files {
			if !containsLiveValue(file.Fields, "sma_2") {
				t.Fatal("indicator replay missing before send", file)
			}
		}
		sent = append(sent, alert)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	asOf := calendarAlertTime("2024-01-15T00:00:00Z")
	l.now = func() time.Time { return asOf }
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	if err = l.checkCycle(context.Background(), config, asOf.UnixMilli(), true); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].Time != 1704672000000 || sent[0].Timeframe != "w1" || sent[0].EntryPrice != 23 {
		t.Fatal("alert did not use newly derived closed weekly data", sent)
	}
	page, _ := m.Files(0, 10)
	for _, file := range page.Files {
		data, err := os.ReadFile(filepath.Join(m.directory, file.Name))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		want := 15
		if file.Timeframe == "w1" {
			want = 3
			if rows[2][6] != "19.5" {
				t.Fatal("weekly indicator uses daily rather than weekly bars", rows)
			}
		}
		if len(rows) != want {
			t.Fatal("derived refresh dropped daily or weekly bars", file.Name, len(rows), want)
		}
	}
	requests, err := os.ReadFile(trace)
	if err != nil || strings.Count(string(requests), "\n") != 1 {
		t.Fatal("weekly refresh did not fetch only daily data", string(requests), err)
	}
}

func TestLiveWeeklyMissingOrFailedDailyPrerequisiteCannotClaimStaleWeeklyData(t *testing.T) {
	for _, mode := range []string{"missing", "failed"} {
		t.Run(mode, func(t *testing.T) {
			m, _, gates := weeklyTransportManager(t, false)
			const weeklyName = "eurusd-w1-2024-01-01-2024-01-15.csv"
			original := "timestamp,open,high,low,close,volume\n1704067200000,10,17,9,16,28\n"
			full := writeDataFile(t, m, weeklyName, original)
			before, _ := os.Stat(full)
			if mode == "failed" {
				writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-15.csv", weeklyDailyCSV(0, 7, false))
				if err := os.WriteFile(filepath.Join(gates, "fail"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			saved := saveWeeklyLiveCondition(t, m, weeklyLiveRequest(), weeklyName)
			sends := 0
			l, err := NewLiveAlerts(m, func(context.Context, LiveAlert) error { sends++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			asOf := calendarAlertTime("2024-01-15T00:00:00Z")
			l.now = func() time.Time { return asOf }
			config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}}
			if _, err = l.Configure(config); err != nil {
				t.Fatal(err)
			}
			err = l.checkCycle(context.Background(), config, asOf.UnixMilli(), true)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "daily") || sends != 0 || len(l.state.Watermarks) != 0 {
				t.Fatal("failed daily prerequisite claimed stale weekly data", err, sends, l.state.Watermarks)
			}
			after, statErr := os.Stat(full)
			data, readErr := os.ReadFile(full)
			if statErr != nil || readErr != nil || string(data) != original || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("failed weekly refresh changed original", statErr, readErr)
			}
		})
	}
}

func TestLiveWeeklyScheduledCheckWaitsForNewlyClosedBar(t *testing.T) {
	m := analysisTestManager(t)
	const weeklyName = "eurusd-w1-2024-01-01-2024-01-15.csv"
	writeDataFile(t, m, weeklyName, "timestamp,open,high,low,close\n1704067200000,10,12,9,11\n")
	writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-15.csv", weeklyDailyCSV(0, 7, false))
	saved := saveWeeklyLiveCondition(t, m, weeklyLiveRequest(), weeklyName)
	sends := 0
	l, err := NewLiveAlerts(m, func(context.Context, LiveAlert) error { sends++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	asOf := calendarAlertTime("2024-01-15T00:00:00Z")
	l.now = func() time.Time { return asOf }
	l.refresh = func(context.Context, string, []historyGroup, int64) (map[string]bool, error) {
		return map[string]bool{"eurusd/d1": true, "eurusd/w1": true}, nil
	}
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	if err = l.checkCycle(context.Background(), config, asOf.UnixMilli(), true); !errors.Is(err, errLiveAlertsPendingBar) || sends != 0 || len(l.state.Watermarks) != 0 {
		t.Fatal("scheduled weekly check accepted preceding week's bar", err, sends, l.state.Watermarks)
	}
}

func TestLiveWeeklyAllPeriodsIncludesRecordedWeeklyWithoutBorrowingDailyProfitability(t *testing.T) {
	saved := SavedAnalysisCondition{Request: AnalysisRequest{Period: "all", ProfitabilityFilter: 80}, Analysis: &SavedConditionAnalysis{Assets: []SavedConditionProfitability{
		liveProfitRow("eurusd", "w1", "weekly.csv", 8, 10), liveProfitRow("eurusd", "d1", "daily.csv", 7, 10), liveProfitRow("eurusd", "h1", "hourly.csv", 9, 10),
	}}}
	pairs := savedConditionEligiblePairs(saved)
	if len(pairs) != 2 || pairs["eurusd/w1"] == nil || pairs["eurusd/h1"] == nil {
		t.Fatal("All periods lost weekly evidence or borrowed daily profitability", pairs)
	}
	saved.Request.Period = "w1"
	if pairs = savedConditionEligiblePairs(saved); len(pairs) != 1 || pairs["eurusd/w1"] == nil {
		t.Fatal("weekly period widened to hourly evidence", pairs)
	}
	if err := validateLiveAlertsConfig(LiveAlertsConfig{Periods: []string{"w1", "d1", "h1"}}); err != nil {
		t.Fatal(fmt.Errorf("valid weekly processed-period state rejected: %w", err))
	}
}

func TestLiveWeeklyCompanionRefreshesRetainedDailyButNeverBecomesAnAlertTarget(t *testing.T) {
	for _, failWeekly := range []bool{false, true} {
		t.Run(fmt.Sprintf("weekly-failure-%t", failWeekly), func(t *testing.T) {
			m := analysisTestManager(t)
			asOf := calendarAlertTime("2024-01-08T01:00:00Z")
			writeDataFile(t, m, "eurusd-h1-2024-01-08-2024-01-09.csv", reviewHourlyCSV(asOf.Add(-time.Hour).UnixMilli(), []float64{20}))
			writeDataFile(t, m, "eurusd-w1-2024-01-01-2024-01-08.csv", "timestamp,open,high,low,close\n1704067200000,9,11,8,10\n")
			writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-08.csv", weeklyDailyCSV(0, 7, false))
			companion := analysisField("close")
			companion.Timeframe = "w1"
			request := AnalysisRequest{Period: "h1", Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), companion)}}
			saved, err := saveLiveCondition(t, m, "Hourly with weekly operand", request)
			if err != nil {
				t.Fatal(err)
			}
			var sent []LiveAlert
			l, err := NewLiveAlerts(m, func(_ context.Context, alert LiveAlert) error { sent = append(sent, alert); return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			l.now = func() time.Time { return asOf }
			var got []string
			l.refresh = func(ctx context.Context, _ string, groups []historyGroup, _ int64) (map[string]bool, error) {
				pairs := map[string]bool{}
				for _, group := range groups {
					match := downloadFilename.FindStringSubmatch(group.name)
					pair := match[1] + "/" + match[2]
					got = append(got, pair)
					if !failWeekly || match[2] != "w1" {
						pairs[pair] = true
					}
				}
				if failWeekly {
					return pairs, errors.New("injected weekly refresh failure")
				}
				return pairs, ctx.Err()
			}
			config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}}
			if _, err = l.Configure(config); err != nil {
				t.Fatal(err)
			}
			err = l.checkCycle(context.Background(), config, asOf.UnixMilli(), true)
			if !reflect.DeepEqual(got, []string{"eurusd/d1", "eurusd/h1", "eurusd/w1"}) {
				t.Fatal("weekly operand omitted or misordered daily prerequisite", got)
			}
			if failWeekly {
				if err == nil || len(sent) != 0 || len(l.state.Watermarks) != 0 {
					t.Fatal("failed weekly operand still claimed base signal", err, sent, l.state.Watermarks)
				}
			} else if err != nil || len(sent) != 1 || sent[0].Timeframe != "h1" || len(l.state.Watermarks) != 1 {
				t.Fatal("weekly companion became an alert target or broke hourly base", err, sent, l.state.Watermarks)
			}
		})
	}
}
