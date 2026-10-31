package screener

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func scheduledAlertsFixture(t *testing.T, now time.Time) (*Manager, *LiveAlerts, LiveAlertsConfig, *atomic.Int64) {
	t.Helper()
	m := newManager(t.TempDir(), func() (workerRuntime, error) { return workerRuntime{}, errors.New("offline scheduler test") })
	t.Cleanup(m.Close)
	if err := os.MkdirAll(m.directory, 0700); err != nil {
		t.Fatal(err)
	}
	hour := now.UTC().Truncate(time.Hour).UnixMilli()
	day := now.UTC().Truncate(24 * time.Hour).UnixMilli()
	writeDataFile(t, m, "eurusd-h1-2024-01-01-2024-01-04.csv", reviewHourlyCSV(hour-3*time.Hour.Milliseconds(), []float64{10, 11, 12, 13}))
	writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-04.csv", fmt.Sprintf("timestamp,open,high,low,close\n%d,10,12,9,11\n%d,11,13,10,12\n", day-2*(24*time.Hour).Milliseconds(), day-(24*time.Hour).Milliseconds()))
	hourlyRequest := defaultAnalysisRequest()
	hourlyRequest.Period = "h1"
	hourly, err := saveLiveCondition(t, m, "Hourly condition", hourlyRequest)
	if err != nil {
		t.Fatal(err)
	}
	dailyRequest := defaultAnalysisRequest()
	dailyRequest.Period = "d1"
	daily, err := saveLiveCondition(t, m, "Daily condition", dailyRequest)
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	clock := &atomic.Int64{}
	clock.Store(now.UnixMilli())
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	l.refresh = func(ctx context.Context, _ string, groups []historyGroup, _ int64) (map[string]bool, error) {
		refreshed := map[string]bool{}
		for _, group := range groups {
			match := downloadFilename.FindStringSubmatch(group.name)
			refreshed[match[1]+"/"+match[2]] = true
		}
		return refreshed, ctx.Err()
	}
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{hourly.ID, daily.ID}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	return m, l, config, clock
}

func waitLiveSchedule(t *testing.T, l *LiveAlerts, predicate func(LiveAlertsInfo) bool) LiveAlertsInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		info := l.Info()
		if predicate(info) {
			return info
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduler did not reach expected state: %+v", info)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLiveAlertsMidHourEnableAndRestartWaitUntilExactNextClose(t *testing.T) {
	now := time.Date(2024, 1, 2, 12, 27, 19, 0, time.UTC)
	m, l, _, clock := scheduledAlertsFixture(t, now)
	want := time.Date(2024, 1, 2, 13, 0, 0, 0, time.UTC).UnixMilli()
	if info := l.Info(); info.NextRun != want || info.LastRun != 0 || info.Running {
		t.Fatalf("enable must wait for next exact hourly close: %+v", info)
	}
	var refreshes atomic.Int64
	refresh := l.refresh
	l.refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
		refreshes.Add(1)
		return refresh(ctx, directory, groups, asOf)
	}
	l.Start(context.Background())
	clock.Store(want - 1)
	l.signal()
	time.Sleep(20 * time.Millisecond)
	if info := l.Info(); refreshes.Load() != 0 || info.LastRun != 0 || info.NextRun != want {
		t.Fatalf("monitor checked before the hour closed: %+v calls=%d", info, refreshes.Load())
	}
	l.Close()
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	clock.Store(now.UnixMilli())
	restored.now = l.now
	restored.refresh = l.refresh
	restored.Start(context.Background())
	if info := restored.Info(); !info.Config.Enabled || info.NextRun != want || info.LastRun != 0 {
		t.Fatalf("restart must restore enabled selection and wait for its boundary: %+v", info)
	}
	clock.Store(want)
	restored.signal()
	info := waitLiveSchedule(t, restored, func(info LiveAlertsInfo) bool { return info.LastRun == want && !info.Running })
	if refreshes.Load() != 1 || info.NextRun != want+time.Hour.Milliseconds() {
		t.Fatalf("exact close did not start one check with next UTC boundary: %+v calls=%d", info, refreshes.Load())
	}
}

func TestLiveAlertsSchedulesOnlyInheritedDuePeriodsAndRefreshesTheirCompanions(t *testing.T) {
	now := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)
	_, l, config, _ := scheduledAlertsFixture(t, now)
	config, _ = l.resolveConfig(config)
	for _, tc := range []struct {
		name     string
		from, to time.Time
		want     []LiveAlertConditionPeriod
	}{
		{"hourly", now, now.Add(7 * time.Second), []LiveAlertConditionPeriod{{ConditionID: config.ConditionIDs[0], Period: "h1"}}},
		{"midnight", now.Truncate(24 * time.Hour), now.Truncate(24 * time.Hour).Add(7 * time.Second), config.scope},
		{"suspended-across-midnight", now.Add(11 * time.Hour), now.Add(13 * time.Hour), config.scope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := dueLiveAlertsConfig(config, tc.from, tc.to)
			if !reflect.DeepEqual(got.scope, tc.want) {
				t.Fatalf("selected wrong inherited periods: %+v want %+v", got.scope, tc.want)
			}
		})
	}
	var alerts []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { alerts = append(alerts, alert); return nil }
	if err := l.check(context.Background(), config, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 {
		t.Fatalf("each condition must run on exactly one period, got %+v", alerts)
	}
	for _, alert := range alerts {
		if !liveAlertsScopeAllows(config, alert.ConditionID, alert.Timeframe) {
			t.Fatalf("condition ran against another period: %+v", alert)
		}
	}
}

func TestLiveAlertsScheduledStaleBarIsUnclaimedButManualCheckRemainsAvailable(t *testing.T) {
	now := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)
	_, l, config, _ := scheduledAlertsFixture(t, now)
	config.ConditionIDs = config.ConditionIDs[:1]
	var sent int
	l.send = func(context.Context, LiveAlert) error { sent++; return nil }
	// The file's last row opens at 12:00. By 14:00 the 13:00 bar is due,
	// but a provider response that has not published it cannot reuse 12:00.
	asOf := now.Add(2 * time.Hour).UnixMilli()
	if err := l.checkCycle(context.Background(), config, asOf, true); !errors.Is(err, errLiveAlertsPendingBar) || sent != 0 || len(l.state.Watermarks) != 0 {
		t.Fatalf("scheduled stale signal was sent or claimed: err=%v sent=%d watermarks=%+v", err, sent, l.state.Watermarks)
	}
	if err := l.check(context.Background(), config, asOf); err != nil || sent != 1 {
		t.Fatalf("explicit manual check lost latest-closed-bar behavior: err=%v sent=%d", err, sent)
	}
}

func TestLiveAlertsBusyRetryCrossingMidnightIncludesNewDailyClose(t *testing.T) {
	start := time.Date(2024, 1, 2, 22, 30, 0, 0, time.UTC)
	m, l, _, clock := scheduledAlertsFixture(t, start)
	m.mu.Lock()
	m.active = true
	m.mu.Unlock()
	l.Start(context.Background())
	clock.Store(time.Date(2024, 1, 2, 23, 0, 0, 0, time.UTC).UnixMilli())
	l.signal()
	waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && info.Status == "Waiting to retry closed-bar check"
	})
	// Simulate a suspended app or long-running downloader spanning midnight.
	m.mu.Lock()
	m.active = false
	m.mu.Unlock()
	midnight := time.Date(2024, 1, 3, 0, 0, 1, 0, time.UTC)
	writeDataFile(t, m, "eurusd-h1-2024-01-01-2024-01-04.csv", reviewHourlyCSV(midnight.Truncate(time.Hour).Add(-2*time.Hour).UnixMilli(), []float64{11, 12}))
	writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-04.csv", fmt.Sprintf("timestamp,open,high,low,close\n%d,10,12,9,11\n", midnight.Truncate(24*time.Hour).Add(-24*time.Hour).UnixMilli()))
	var sent atomic.Int64
	l.send = func(context.Context, LiveAlert) error { sent.Add(1); return nil }
	clock.Store(midnight.UnixMilli())
	l.signal()
	info := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return info.LastRun == midnight.UnixMilli() && !info.Running })
	if sent.Load() != 2 || info.Sent != 2 {
		t.Fatalf("retry lost the newly due daily close: %+v sends=%d", info, sent.Load())
	}
	if info.NextRun != midnight.Truncate(time.Hour).Add(time.Hour).UnixMilli() {
		t.Fatalf("retry shifted the next normal hourly boundary: %+v", info)
	}
}

func TestLiveAlertsMissingDueHistoryIsReportedWithoutClaims(t *testing.T) {
	now := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)
	m, l, config, _ := scheduledAlertsFixture(t, now)
	config.ConditionIDs = config.ConditionIDs[:1]
	if err := os.Remove(filepath.Join(m.directory, "eurusd-h1-2024-01-01-2024-01-04.csv")); err != nil {
		t.Fatal(err)
	}
	if err := l.checkCycle(context.Background(), config, now.UnixMilli(), true); err == nil || len(l.state.Watermarks) != 0 {
		t.Fatalf("missing due-period history was silently accepted or claimed: %v %+v", err, l.state.Watermarks)
	}
}

func TestLiveAlertsMalformedLegacyDualPeriodStateRemainsBlocked(t *testing.T) {
	m := newManager(t.TempDir(), func() (workerRuntime, error) { return workerRuntime{}, errors.New("offline") })
	defer m.Close()
	path := filepath.Join(m.profile, "screener-live-alerts.json")
	original := []byte(`{"version":1,"config":{"enabled":true,"conditionIds":[],"periods":["d1","h1"],"symbols":[]},"watermarks":{},"sent":0,"recent":[]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	l, err := NewLiveAlerts(m, nil)
	if err == nil || l == nil || !l.blocked {
		t.Fatalf("malformed legacy enabled selection bypassed validation: %+v %v", l, err)
	}
	defer l.Close()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(original) {
		t.Fatalf("invalid original state was changed: %s %v", got, err)
	}
}
