package screener

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveAlertsCheckHistoryCountsActualUniqueAssetsAndSurvivesRestart(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	now := time.Date(2024, 1, 1, 3, 30, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	run, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	run.scanned(id, "eurusd")
	run.scanned(id, "eurusd")
	run.scanned(id, "gbpusd")
	if row := l.Info().Checks[0]; row.Status != "running" || row.AssetsScanned != 2 || !row.Manual || row.LastChecked != now.UnixMilli() {
		t.Fatal("running check did not count actual distinct assets", row)
	}
	// Confirmed delivery uses the production state update and persistence path.
	l.send = func(context.Context, LiveAlert) error { return nil }
	if err = l.deliver(context.Background(), LiveAlert{ConditionID: id}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err = run.finish(nil); err != nil {
		t.Fatal(err)
	}
	info := l.Info()
	if len(info.Checks) != 1 || info.Checks[0].Status != "completed" || info.Checks[0].AssetsScanned != 2 || info.Checks[0].LastChecked != now.UnixMilli() || info.Checks[0].SentAlerts != 1 || info.Checks[0].NextCheck != now.Truncate(time.Hour).Add(time.Hour).UnixMilli() {
		t.Fatal("completed history lost actual counters or timing", info.Checks)
	}
	info.Checks[0].Name = "Caller mutation"
	if l.Info().Checks[0].Name == "Caller mutation" {
		t.Fatal("Info exposed mutable check history")
	}
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.now = l.now
	row := restored.Info().Checks[0]
	if row.Status != "completed" || row.AssetsScanned != 2 || row.SentAlerts != 1 || row.LastChecked != now.UnixMilli() {
		t.Fatal("restart lost completed condition history", row)
	}
	// Failed/uncertain delivery never inflates this condition's confirmed count.
	restored.send = func(context.Context, LiveAlert) error { return errors.New("uncertain transport failure") }
	if err = restored.deliver(context.Background(), LiveAlert{ConditionID: id}); err == nil {
		t.Fatal("fixture transport did not fail")
	}
	if restored.Info().Checks[0].SentAlerts != 1 {
		t.Fatal("uncertain send incremented confirmed condition count")
	}
}

func TestLiveAlertsCheckHistoryAttributesFailuresCancellationAndInterruptedRestart(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	second, err := saveLiveCondition(t, m, "Other active condition", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = append(config.ConditionIDs, second.ID)
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	run, err := l.beginCheckHistory(config, false)
	if err != nil {
		t.Fatal(err)
	}
	run.expectPair(config.ConditionIDs[0], "eurusd", "h1")
	run.expectPair(second.ID, "eurusd", "h1")
	run.scanned(config.ConditionIDs[0], "eurusd")
	run.processedPair(config.ConditionIDs[0], "eurusd", "h1")
	run.scanned(second.ID, "eurusd")
	failure := errors.New("one condition was missing a required value")
	run.failed(second.ID, failure)
	if err = run.finish(failure); err != nil {
		t.Fatal(err)
	}
	rows := l.Info().Checks
	if rows[0].Status != "completed" || rows[0].Error != "" || rows[1].Status != "failed" || !strings.Contains(rows[1].Error, "missing a required value") || rows[0].Manual || rows[1].Manual {
		t.Fatal("one condition's failure mislabeled another", rows)
	}
	run, err = l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	run.scanned(second.ID, "eurusd")
	if err = run.finish(context.Canceled); err != nil {
		t.Fatal(err)
	}
	for _, row := range l.Info().Checks {
		if row.Status != "cancelled" || !row.Manual {
			t.Fatal("cancelled manual check was labeled successful", row)
		}
	}
	if _, err = l.beginCheckHistory(config, false); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(l.path)
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for _, row := range restored.Info().Checks {
		if row.Status != "cancelled" || !strings.Contains(row.Error, "interrupted") || row.AssetsScanned != 0 {
			t.Fatal("interrupted check was represented as completed", row)
		}
	}
	after, _ := os.ReadFile(l.path)
	if string(before) != string(after) {
		t.Fatal("reading interrupted history rewrote the original state")
	}
}

func TestLiveAlertsHistoryDoesNotMarkPartiallyVisitedConditionCompleted(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	second, err := saveLiveCondition(t, m, "Second condition", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = append(config.ConditionIDs, second.ID)
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	run, err := l.beginCheckHistory(config, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range config.ConditionIDs {
		for _, symbol := range []string{"btcusd", "eurusd"} {
			run.expectPair(id, symbol, "h1")
		}
	}
	for _, id := range config.ConditionIDs {
		run.scanned(id, "btcusd")
		run.processedPair(id, "btcusd", "h1")
	}
	run.scanned(config.ConditionIDs[0], "eurusd")
	failure := errors.New("first condition delivery failed before second EUR evaluation")
	run.failed(config.ConditionIDs[0], failure)
	run.processedPair(config.ConditionIDs[0], "eurusd", "h1")
	if err = run.finish(failure); err != nil {
		t.Fatal(err)
	}
	rows := l.Info().Checks
	if rows[0].Status != "failed" || rows[0].AssetsScanned != 2 || rows[1].Status != "failed" || rows[1].AssetsScanned != 1 || rows[1].Error == "" {
		t.Fatal("partially visited condition was reported completed", rows)
	}
}

func TestLiveAlertsHistoryDeduplicatedConditionCompletesDespiteOtherRefreshFailure(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	second, err := saveLiveCondition(t, m, "Failed refresh condition", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = append(config.ConditionIDs, second.ID)
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	run, err := l.beginCheckHistory(config, false)
	if err != nil {
		t.Fatal(err)
	}
	run.expectPair(config.ConditionIDs[0], "eurusd", "h1")
	run.expectPair(second.ID, "gbpusd", "h1")
	// A's pair was visited and already watermarked, so it required no new scan.
	run.processedPair(config.ConditionIDs[0], "eurusd", "h1")
	failure := errors.New("GBP refresh refused")
	run.failed(second.ID, failure)
	if err = run.finish(failure); err != nil {
		t.Fatal(err)
	}
	rows := l.Info().Checks
	if rows[0].Status != "completed" || rows[0].AssetsScanned != 0 || rows[0].Error != "" || rows[1].Status != "failed" || rows[1].AssetsScanned != 0 {
		t.Fatal("deduplicated completed condition inherited unrelated failure", rows)
	}
}

func TestLiveAlertsRetryHistoryHintDescribesRetryInsteadOfNextWeeklyClose(t *testing.T) {
	now := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)
	m, l, _, _ := scheduledAlertsFixture(t, now)
	saved, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	daily := saved[1]
	daily.Request.Conditions[0].Operator = "equal"
	daily.Request.Conditions[0].Left.Field = "weekday"
	daily.Request.Conditions[0].Right.Value = 2
	saved[1] = daily
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: saved}); err != nil {
		t.Fatal(err)
	}
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{daily.ID}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	resolved, err := l.resolveConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	retryAt := now.Add(30 * time.Second).UnixMilli()
	l.mu.Lock()
	l.retryConfig = &resolved
	l.retryAsOf = now.UnixMilli()
	l.nextRun = retryAt
	l.mu.Unlock()
	row := l.Info().Checks[0]
	if row.NextCheck != retryAt || !strings.Contains(row.TimingHint, "Retrying the bar-close check") || !strings.Contains(row.TimingHint, "2024-01-03 00:00:30 UTC") || strings.Contains(row.TimingHint, "2024-01-10") {
		t.Fatal("retry time retained a misleading next-week hint", row)
	}
	l.mu.Lock()
	l.retryManual = true
	l.mu.Unlock()
	if row = l.Info().Checks[0]; !strings.Contains(row.TimingHint, "manual check") {
		t.Fatal("manual retry was described as automatic calendar close", row)
	}
}

func TestLiveAlertsActualDeliveryAbortLeavesPartiallyVisitedHistoryFailed(t *testing.T) {
	m, l, config, path := liveAlertsFixture(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	btcName := "btcusd-h1-2024-01-01-2024-01-03.csv"
	writeDataFile(t, m, btcName, string(data))
	second, err := saveLiveCondition(t, m, "Second shared asset condition", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	saved[0].Analysis.Assets = append(saved[0].Analysis.Assets, SavedConditionProfitability{Symbol: "btcusd", Timeframe: "h1", FileName: btcName, Matches: 10, ProfitabilityUnavailable: true})
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: saved}); err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = append(config.ConditionIDs, second.ID)
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	calls := 0
	failure := errors.New("A EUR delivery aborted the rest of the cycle")
	l.send = func(context.Context, LiveAlert) error {
		calls++
		if calls == 3 {
			return failure
		}
		return nil
	}
	if err = l.check(context.Background(), config, time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()); !errors.Is(err, failure) {
		t.Fatal("fixture delivery did not abort", err)
	}
	rows := l.Info().Checks
	if calls != 3 || rows[0].Status != "failed" || rows[0].AssetsScanned != 2 || rows[0].SentAlerts != 1 || rows[1].Status != "failed" || rows[1].AssetsScanned != 1 || rows[1].SentAlerts != 1 {
		t.Fatal("real pipeline marked partial condition complete or inflated confirmed sends", rows, calls)
	}
}

func TestLiveAlertsActualDedupVisitCompletesDespiteOtherAssetRefreshFailure(t *testing.T) {
	m, l, config, path := liveAlertsFixture(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gbpName := "gbpusd-h1-2024-01-01-2024-01-03.csv"
	writeDataFile(t, m, gbpName, string(data))
	second, err := saveLiveCondition(t, m, "GBP refresh condition", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	saved[1].Analysis.Assets = []SavedConditionProfitability{{Symbol: "gbpusd", Timeframe: "h1", FileName: gbpName, Matches: 10, ProfitabilityUnavailable: true}}
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: saved}); err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = append(config.ConditionIDs, second.ID)
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	l.state.Watermarks[config.ConditionIDs[0]+"/eurusd/h1"] = 1704070800000
	failure := errors.New("GBP provider refused refresh")
	l.refresh = func(context.Context, string, []historyGroup, int64) (map[string]bool, error) {
		return map[string]bool{"eurusd/h1": true}, failure
	}
	if err = l.check(context.Background(), config, time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()); !errors.Is(err, failure) {
		t.Fatal("fixture refresh did not fail", err)
	}
	rows := l.Info().Checks
	if rows[0].Status != "completed" || rows[0].AssetsScanned != 0 || rows[0].Error != "" || rows[1].Status != "failed" || rows[1].AssetsScanned != 0 {
		t.Fatal("real dedup visit inherited the unrelated refresh failure", rows)
	}
}

func TestLiveAlertsDeletePrunesOnlyCurrentConditionRowsAndUpdatesNames(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	retained, err := saveLiveCondition(t, m, "Retained condition", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = append(config.ConditionIDs, retained.ID)
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	run, err := l.beginCheckHistory(config, false)
	if err != nil {
		t.Fatal(err)
	}
	run.scanned(config.ConditionIDs[0], "eurusd")
	run.scanned(retained.ID, "eurusd")
	if err = run.finish(nil); err != nil {
		t.Fatal(err)
	}
	deletedID := config.ConditionIDs[0]
	l.state.Watermarks[deletedID+"/eurusd/h1"] = 123
	l.state.Watermarks[retained.ID+"/eurusd/h1"] = 456
	l.state.Recent = []LiveAlertOutcome{{ConditionName: "Historic deleted label", Status: "sent", Time: 123}}
	if err = writeState(l.path, l.state); err != nil {
		t.Fatal(err)
	}
	if err = m.DeleteAnalysisCondition(deletedID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RenameAnalysisCondition(retained.ID, "Fresh condition label"); err != nil {
		t.Fatal(err)
	}
	info := l.Info()
	if !info.Config.Enabled || !reflect.DeepEqual(info.Config.ConditionIDs, []string{retained.ID}) || len(info.Checks) != 1 || info.Checks[0].ConditionID != retained.ID || info.Checks[0].Name != "Fresh condition label" || len(info.Recent) != 1 || info.Recent[0].ConditionName != "Historic deleted label" {
		t.Fatal("deleted/renamed metadata left ghosts or altered historic delivery", info)
	}
	if len(l.state.Watermarks) != 1 || l.state.Watermarks[retained.ID+"/eurusd/h1"] != 456 {
		t.Fatal("deletion lost unrelated deduplication state", l.state.Watermarks)
	}
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if got := restored.Info(); !reflect.DeepEqual(got.Config.ConditionIDs, []string{retained.ID}) || len(got.Checks) != 1 || got.Checks[0].Name != "Fresh condition label" {
		t.Fatal("restart resurrected a deleted condition", got)
	}
	if err = m.DeleteAnalysisCondition(retained.ID); err != nil {
		t.Fatal(err)
	}
	if got := restored.Info(); got.Config.Enabled || len(got.Config.ConditionIDs) != 0 || len(got.Checks) != 0 || len(got.Recent) != 1 {
		t.Fatal("deletion of final condition left enabled ghost rows", got)
	}
}

func TestLiveAlertsInvalidCheckHistoryRemainsBlockedAndUnchanged(t *testing.T) {
	for _, kind := range []string{"count", "timestamp", "status", "sent"} {
		t.Run(kind, func(t *testing.T) {
			m, l, config, _ := liveAlertsFixture(t)
			state := l.state
			record := liveAlertCheckState{LastChecked: 1, Status: "completed"}
			switch kind {
			case "count":
				record.AssetsScanned = -1
			case "timestamp":
				record.LastChecked = 0
			case "status":
				record.Status = "invented"
			case "sent":
				record.SentAlerts = 1
			}
			state.Checks = map[string]liveAlertCheckState{config.ConditionIDs[0]: record}
			if err := writeState(l.path, state); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(l.path)
			restored, err := NewLiveAlerts(m, nil)
			if err == nil || restored == nil || !restored.blocked {
				t.Fatal("corrupt check history restored", kind, err)
			}
			defer restored.Close()
			restored.Start(context.Background())
			after, _ := os.ReadFile(l.path)
			if string(before) != string(after) || restored.started {
				t.Fatal("corrupt check state was rewritten or started")
			}
		})
	}
}

func TestLiveAlertsCheckHistoryPublicationFailurePreservesPriorState(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	before := copyLiveCheckStates(l.state.Checks)
	originalPath := l.path
	l.path = filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(l.path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := l.beginCheckHistory(config, true); err == nil || !reflect.DeepEqual(l.state.Checks, before) {
		t.Fatal("failed history start mutated memory", err, l.state.Checks)
	}
	l.path = originalPath
	run, err := l.beginCheckHistory(config, false)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(originalPath)
	l.path = t.TempDir()
	if err = run.finish(errors.New("provider refused history")); err == nil {
		t.Fatal("history completion write failure was hidden")
	}
	after, _ := os.ReadFile(originalPath)
	if string(original) != string(after) {
		t.Fatal("failed completion write replaced prior state")
	}
}

func TestLiveAlertsTuesdayDailyConditionSchedulesWednesdayCloseAndManualCheckBypassesCalendar(t *testing.T) {
	m := analysisTestManager(t)
	name := "eurusd-d1-2024-01-01-2024-01-11.csv"
	data := "timestamp,open,high,low,close,weekday\n1704067200000,10,12,9,11,1\n1704153600000,11,13,10,12,2\n1704240000000,12,14,11,13,3\n"
	writeDataFile(t, m, name, data)
	request := defaultAnalysisRequest()
	request.Period = "d1"
	request.Conditions[0].Operator = "equal"
	request.Conditions[0].Left.Field = "weekday"
	request.Conditions[0].Right.Value = 2
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	if info := waitAnalysis(t, m); info.Failed != 0 || info.Error != "" {
		t.Fatal("calendar fixture analysis failed", info)
	}
	saved, err := m.SaveAnalysisCondition("Tuesday daily signal", request)
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	clock := &atomic.Int64{}
	clock.Store(time.Date(2024, 1, 5, 12, 0, 0, 0, time.UTC).UnixMilli())
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	var refreshes, sends atomic.Int64
	want := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC).UnixMilli()
	l.refresh = func(ctx context.Context, _ string, _ []historyGroup, asOf int64) (map[string]bool, error) {
		refreshes.Add(1)
		if asOf >= want {
			if err := os.WriteFile(filepath.Join(m.directory, name), []byte(data+"1704758400000,13,15,12,14,2\n"), 0600); err != nil {
				return nil, err
			}
		}
		return map[string]bool{"eurusd/d1": true}, ctx.Err()
	}
	l.send = func(context.Context, LiveAlert) error { sends.Add(1); return nil }
	info, err := l.Configure(LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}})
	if err != nil || info.NextRun != want || len(info.Checks) != 1 || info.Checks[0].NextCheck != want || !strings.Contains(info.Checks[0].TimingHint, "Tuesday") {
		t.Fatal("Tuesday signal did not schedule its Wednesday UTC close", info, err)
	}
	l.Start(context.Background())
	sunday := time.Date(2024, 1, 7, 12, 0, 0, 0, time.UTC).UnixMilli()
	clock.Store(sunday)
	l.signal()
	if info = l.Info(); info.NextRun != want || info.LastRun != 0 || refreshes.Load() != 0 {
		t.Fatal("calendar condition ran on an ineligible daily close", info)
	}
	if _, err = l.CheckNow(); err != nil {
		t.Fatal(err)
	}
	info = waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return info.LastRun == sunday && !info.Running })
	if refreshes.Load() != 1 || sends.Load() != 0 || info.Checks[0].Status != "completed" || !info.Checks[0].Manual || info.Checks[0].AssetsScanned != 1 || info.Checks[0].NextCheck != want {
		t.Fatal("manual check skipped real data or moved eligible close", info, refreshes.Load())
	}
	clock.Store(want)
	l.signal()
	info = waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return info.LastRun == want && !info.Running })
	if info.Error != "" || refreshes.Load() != 2 || sends.Load() != 1 || info.Checks[0].Status != "completed" || info.Checks[0].Manual || info.Checks[0].AssetsScanned != 1 || info.Checks[0].SentAlerts != 1 || info.Checks[0].NextCheck != want+(7*24*time.Hour).Milliseconds() {
		t.Fatal("eligible Tuesday daily bar did not produce one Wednesday check", info, refreshes.Load(), sends.Load())
	}
}

func TestLiveAlertsSleepPastEligibleCalendarCloseWaitsWithoutInventingCheck(t *testing.T) {
	start := time.Date(2024, 1, 5, 12, 0, 0, 0, time.UTC)
	m, l, _, clock := scheduledAlertsFixture(t, start)
	saved, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	daily := saved[1]
	daily.Request.Conditions[0].Operator = "equal"
	daily.Request.Conditions[0].Left.Field = "weekday"
	daily.Request.Conditions[0].Right.Value = 2
	saved[1] = daily
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: saved}); err != nil {
		t.Fatal(err)
	}
	if _, err = l.Configure(LiveAlertsConfig{Enabled: true, ConditionIDs: []string{daily.ID}}); err != nil {
		t.Fatal(err)
	}
	var refreshes atomic.Int64
	l.refresh = func(context.Context, string, []historyGroup, int64) (map[string]bool, error) {
		refreshes.Add(1)
		return nil, errors.New("unexpected refresh outside calendar")
	}
	l.Start(context.Background())
	// The app wakes on Thursday, after the Tuesday bar closed on Wednesday.
	// Only the newest closed bar is eligible for alert evaluation; old calendar
	// matches must not be replayed or represented as completed scans.
	clock.Store(time.Date(2024, 1, 11, 12, 0, 0, 0, time.UTC).UnixMilli())
	l.signal()
	want := time.Date(2024, 1, 17, 0, 0, 0, 0, time.UTC).UnixMilli()
	info := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return info.NextRun == want && !info.Running })
	if info.LastRun != 0 || info.Error != "" || refreshes.Load() != 0 || info.Checks[0].Status != "never" || info.Checks[0].LastChecked != 0 {
		t.Fatal("suspension invented an empty or historic calendar check", info, refreshes.Load())
	}
}
