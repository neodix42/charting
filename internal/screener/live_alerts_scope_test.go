package screener

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These Live Alerts fixtures explicitly seed historical match evidence. Tests
// of the Analysis recorder itself run real scans in profitability_test.go.
func saveLiveCondition(t *testing.T, m *Manager, name string, request AnalysisRequest) (SavedAnalysisCondition, error) {
	t.Helper()
	saved, err := m.SaveAnalysisCondition(name, request)
	if err != nil {
		return saved, err
	}
	names, err := csvNames(m.directory)
	if err != nil {
		return saved, err
	}
	saved.Analysis = &SavedConditionAnalysis{AnalyzedAt: time.Now().UnixMilli(), Assets: []SavedConditionProfitability{}}
	for _, name := range names {
		file := describeFile(m.directory, name)
		if !savedConditionPeriodAllowed(saved, file.Timeframe) || !includesAnalysisCategory(request.Categories, file.Category) {
			continue
		}
		fieldsPresent := true
		for _, condition := range request.Conditions {
			for _, operand := range conditionOperands(condition) {
				if operand.Kind == "field" && (operand.Timeframe == "" || operand.Timeframe == "same") && !containsLiveValue(file.Fields, operand.Field) {
					fieldsPresent = false
				}
			}
		}
		if !fieldsPresent {
			continue
		}
		saved.Analysis.Assets = append(saved.Analysis.Assets, SavedConditionProfitability{Symbol: file.Symbol, Timeframe: file.Timeframe, FileName: name, Matches: 10, ProfitabilityUnavailable: true})
	}
	conditions, err := m.SavedAnalysisConditions()
	if err != nil {
		return saved, err
	}
	for i := range conditions {
		if conditions[i].ID == saved.ID {
			conditions[i] = saved
		}
	}
	err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: conditions})
	return saved, err
}

func TestLiveAlertsInheritRealAnalysisMatchesWithoutSimulationAndAllRecordedPeriods(t *testing.T) {
	m := analysisTestManager(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	writeDataFile(t, m, "eurusd-h1-2024-01-01-2024-01-04.csv", reviewHourlyCSV(base.UnixMilli(), []float64{100, 101, 102}))
	writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-04.csv", "timestamp,open,high,low,close\n1704067200000,100,110,90,100\n1704153600000,101,111,91,101\n")
	writeDataFile(t, m, "gbpusd-h1-2024-01-01-2024-01-04.csv", reviewHourlyCSV(base.UnixMilli(), []float64{10, 11, 12}))
	request := defaultAnalysisRequest()
	request.Period = "all"
	request.Conditions[0].Right.Value = 50
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	result := waitAnalysis(t, m)
	if result.Failed != 0 || result.Error != "" {
		t.Fatal("fixture scan failed", result)
	}
	saved, err := m.SaveAnalysisCondition("All matched periods", request)
	if err != nil || saved.Analysis == nil || len(saved.Analysis.Assets) != 2 {
		t.Fatal("real scan did not record matching assets without simulation", saved, err)
	}
	l, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.now = func() time.Time { return base.Add(36*time.Hour + 30*time.Minute) }
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
	if err != nil || len(info.ConditionScopes) != 1 || len(info.ConditionScopes[0].Targets) != 2 || info.ConditionScopes[0].Period != "all" || info.NextRun != base.Add(37*time.Hour).UnixMilli() {
		t.Fatal("all-period inheritance or hourly schedule failed", info, err)
	}
	for _, target := range info.ConditionScopes[0].Targets {
		if target.Symbol != "eurusd" || target.Profitability != nil {
			t.Fatal("nonmatching asset or invented stats", target)
		}
	}
	resolved, err := l.resolveConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	due := dueLiveAlertsConfig(resolved, base.Add(36*time.Hour), base.Add(36*time.Hour+time.Second))
	if !reflect.DeepEqual(due.scope, []LiveAlertConditionPeriod{{ConditionID: saved.ID, Period: "h1"}}) {
		t.Fatal("all condition included a daily bar before its close", due.scope)
	}
	var alerts []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { alerts = append(alerts, alert); return nil }
	if err = l.check(context.Background(), due, base.Add(36*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(refreshed, []string{"eurusd/h1"}) || len(alerts) != 1 || alerts[0].Timeframe != "h1" || alerts[0].Profitability != nil {
		t.Fatal("due scan widened to unmatched symbol or daily period", refreshed, alerts)
	}
	refreshed = nil
	if err = l.check(context.Background(), config, base.Add(72*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	sort.Strings(refreshed)
	if !reflect.DeepEqual(refreshed, []string{"eurusd/d1", "eurusd/h1"}) || len(alerts) != 2 || alerts[1].Timeframe != "d1" {
		t.Fatal("manual all-period check did not retain its recorded pair scope", refreshed, alerts)
	}
}

func TestLiveAlertsZeroFilterRequiresMatchingEvidenceAndDoesNotInventProfitability(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	saved, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	if stats, allowed := SavedConditionProfitabilityFor(saved[0], "eurusd", "h1"); !allowed || stats != nil {
		t.Fatal("unsimulated recorded matches must qualify without stats", stats, allowed)
	}
	saved[0].Analysis = nil
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: saved}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	l.refresh = func(context.Context, string, []historyGroup, int64) (map[string]bool, error) {
		calls++
		return nil, errors.New("unexpected refresh")
	}
	if _, err = l.Configure(config); err == nil || !strings.Contains(err.Error(), "recorded Analysis matching assets") {
		t.Fatal("zero filter without matches widened to inventory", err)
	}
	if err = l.check(context.Background(), config, time.Now().UnixMilli()); err == nil || calls != 0 || len(l.state.Watermarks) != 0 {
		t.Fatal("missing matching evidence refreshed or claimed", err, calls)
	}
	if scope := l.Info().ConditionScopes[0]; len(scope.Targets) != 0 || scope.Error == "" {
		t.Fatal("missing matching evidence was not visible", scope)
	}
}

func TestLiveAlertsScopeChangeDuringRefreshCannotClaimObsoleteSignal(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	saved, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	request := copyAnalysisRequest(saved[0].Request)
	request.Conditions[0].Right.Value = 1000
	refresh := l.refresh
	l.refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
		if _, err := m.UpdateAnalysisCondition(saved[0].ID, request); err != nil {
			t.Fatal(err)
		}
		return refresh(ctx, directory, groups, asOf)
	}
	sends := 0
	l.send = func(context.Context, LiveAlert) error { sends++; return nil }
	err = l.check(context.Background(), config, time.Date(2024, 1, 1, 3, 30, 0, 0, time.UTC).UnixMilli())
	if err == nil || !strings.Contains(err.Error(), "changed while") || sends != 0 || len(l.state.Watermarks) != 0 {
		t.Fatal("in-flight obsolete evaluation claimed or sent", err, sends, l.state.Watermarks)
	}
	if data, err := os.ReadFile(l.path); err != nil || !strings.Contains(string(data), `"conditionIds"`) {
		t.Fatal("current configuration was lost", err)
	}
}

func TestLiveAlertsSavedPeriodUpdateWakesDailyWaitAndRejectsQueuedObsoleteScope(t *testing.T) {
	start := time.Date(2024, 1, 2, 12, 27, 0, 0, time.UTC)
	m, l, config, clock := scheduledAlertsFixture(t, start)
	conditions, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	hourly, daily := conditions[0], conditions[1]
	config.ConditionIDs = []string{daily.ID}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	stale, err := l.resolveConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := daily.ID + "/eurusd/d1"
	l.state.Watermarks[oldKey] = 123
	var refreshes, sends atomic.Int64
	refresh := l.refresh
	l.refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
		refreshes.Add(1)
		for _, group := range groups {
			if !strings.Contains(group.name, "-h1-") {
				t.Error("new hourly scope refreshed old daily history", group.name)
			}
		}
		return refresh(ctx, directory, groups, asOf)
	}
	l.send = func(_ context.Context, alert LiveAlert) error {
		sends.Add(1)
		if alert.ConditionID != daily.ID || alert.Timeframe != "h1" {
			t.Error("updated condition identity/period changed", alert)
		}
		return nil
	}
	l.Start(context.Background())
	if info := l.Info(); info.NextRun != start.Truncate(24*time.Hour).Add(24*time.Hour).UnixMilli() {
		t.Fatal("daily rule did not wait for midnight", info)
	}
	updated, err := m.UpdateAnalysisCondition(daily.ID, hourly.Request)
	if err != nil || updated.ID != daily.ID || updated.Analysis == nil {
		t.Fatal("save did not retain identity and compatible recorded hourly assets", updated, err)
	}
	want := start.Truncate(time.Hour).Add(time.Hour).UnixMilli()
	waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return info.NextRun == want && !info.Running })
	if info := l.Info(); !info.Config.Enabled || !reflect.DeepEqual(info.Config.ConditionIDs, []string{daily.ID}) || refreshes.Load() != 0 || sends.Load() != 0 {
		t.Fatal("saved edit disabled or prematurely ran monitoring", info)
	}
	// A check already queued for the old daily period cannot evaluate its new
	// hourly rule at a daily cutoff, nor refresh obsolete daily targets.
	if err = l.check(context.Background(), stale, start.UnixMilli()); err == nil || refreshes.Load() != 0 || sends.Load() != 0 {
		t.Fatal("queued obsolete period widened into the updated scope", err)
	}
	clock.Store(want)
	l.signal()
	info := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return info.LastRun == want && !info.Running })
	if info.Error != "" || refreshes.Load() != 1 || sends.Load() != 1 || l.state.Watermarks[oldKey] != 123 || l.state.Watermarks[daily.ID+"/eurusd/h1"] == 0 {
		t.Fatal("updated scope missed its next natural boundary or lost claims", info, l.state.Watermarks)
	}
}

func TestLiveAlertsSavedConditionRevisionTracksSuccessfulChangesOnly(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	saved, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	initial := l.Info().SavedConditionsRevision
	if _, err = m.UpdateAnalysisCondition(config.ConditionIDs[0], saved[0].Request); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RenameAnalysisCondition(saved[0].ID, saved[0].Name); err != nil {
		t.Fatal(err)
	}
	if _, err = m.SaveAnalysisCondition(saved[0].Name, saved[0].Request); err == nil {
		t.Fatal("duplicate save fixture unexpectedly succeeded")
	}
	if info := l.Info(); info.SavedConditionsRevision != initial {
		t.Fatal("unchanged or failed saves changed metadata revision", info.SavedConditionsRevision, initial)
	}
	if _, err = m.RenameAnalysisCondition(saved[0].ID, "Updated metadata"); err != nil {
		t.Fatal(err)
	}
	if info := l.Info(); info.SavedConditionsRevision != initial+1 || info.ConditionScopes[0].ConditionID != saved[0].ID {
		t.Fatal("successful metadata save did not advance authoritative scope revision", info)
	}
	if err = m.DeleteAnalysisCondition(saved[0].ID); err != nil {
		t.Fatal(err)
	}
	if info := l.Info(); info.SavedConditionsRevision != initial+2 || len(info.ConditionScopes) != 0 {
		t.Fatal("deletion did not update the revision and scopes together", info)
	}
}
