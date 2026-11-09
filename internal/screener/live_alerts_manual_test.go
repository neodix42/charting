package screener

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func rowCheckFixture(t *testing.T) (*Manager, *LiveAlerts, []LiveAlertDefinition, *publicAlertFakeAdapter, *atomic.Int64) {
	t.Helper()
	m, l, saved, fake := namedPublicLiveFixture(t)
	fake.items = []domain.Instrument{{NativeID: "BTC", Market: "perpetual", Availability: "TRADING"}}
	l.publicData.adapters["hyperliquid"] = fake
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC).UnixMilli())
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	entries := []LiveAlertDefinition{
		{ID: strings.Repeat("a", 32), Name: "Only this hourly alert", ConditionID: saved.ID, Period: "h1", DataSource: "hyperliquid", Enabled: true},
		{ID: strings.Repeat("b", 32), Name: "Same rule different daily alert", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true},
		{ID: strings.Repeat("c", 32), Name: "Paused alert", ConditionID: saved.ID, Period: "h1", DataSource: "hyperliquid", Enabled: false},
	}
	if _, err := l.Configure(LiveAlertsConfig{Enabled: true, Alerts: entries}); err != nil {
		t.Fatal(err)
	}
	return m, l, entries, fake, clock
}

func waitRowResolution(t *testing.T, l *LiveAlerts) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		l.mu.Lock()
		phase := l.resolutionPhase
		l.mu.Unlock()
		if phase == "Waiting for Screener work to load saved conditions" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduler did not expose its blocked metadata read")
		}
		time.Sleep(time.Millisecond)
	}
}

func checkRow(info LiveAlertsInfo, id string) LiveAlertCheck {
	for _, row := range info.Checks {
		if row.ConditionID == id {
			return row
		}
	}
	return LiveAlertCheck{}
}

func TestCheckAlertNowRejectsInvalidDisabledAndOverlappingRequests(t *testing.T) {
	_, l, entries, _, _ := rowCheckFixture(t)
	before, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "bad", strings.Repeat("d", 32), entries[0].ConditionID, entries[2].ID} {
		if _, err = l.CheckAlertNow(id); err == nil {
			t.Fatal("invalid, saved-rule or disabled identity accepted", id)
		}
	}
	for _, pending := range []string{"running", "executing", "retry", "queued"} {
		l.mu.Lock()
		l.running = pending == "running"
		l.checkExecuting = pending == "executing"
		if pending == "retry" {
			config := copyLiveAlertsConfig(l.state.Config)
			l.retryConfig = &config
		}
		if pending == "queued" {
			l.queueCheckProgressLocked([]string{entries[1].ID}, true)
		}
		l.mu.Unlock()
		if _, err = l.CheckAlertNow(entries[0].ID); err == nil {
			t.Fatal("overlapping work accepted", pending)
		}
		l.mu.Lock()
		l.running, l.checkExecuting, l.retryConfig, l.checkProgress = false, false, nil, nil
		l.mu.Unlock()
	}
	info, err := l.CheckAlertNow(entries[0].ID)
	if err != nil || !l.requested || !reflect.DeepEqual(l.requestedIDs, []string{entries[0].ID}) || checkRow(info, entries[0].ID).ProgressState != "queued" || checkRow(info, entries[1].ID).ProgressState != "" {
		t.Fatal("one-row request did not queue exactly its alert", info, err)
	}
	if _, err = l.CheckAlertNow(entries[1].ID); err == nil {
		t.Fatal("second manual request was accepted before the first started")
	}
	after, err := os.ReadFile(l.path)
	if err != nil || string(before) != string(after) {
		t.Fatal("queueing changed the persisted alert selection", err)
	}
	config := copyLiveAlertsConfig(l.state.Config)
	config.Enabled = false
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	if _, err = l.CheckAlertNow(entries[0].ID); err == nil || l.requested || l.requestedIDs != nil {
		t.Fatal("stopped monitor accepted/retained a manual row request", err)
	}
}

func TestCheckAlertNowFencesIdleResolutionAndChecksOnlyOneNamedAlert(t *testing.T) {
	m, l, entries, fake, _ := rowCheckFixture(t)
	var sends atomic.Int64
	var globalAllowed atomic.Bool
	l.send = func(_ context.Context, alert LiveAlert) error {
		if !globalAllowed.Load() && (alert.AlertID != entries[0].ID || alert.Timeframe != "h1") {
			t.Errorf("row check evaluated/delivered another alert: %+v", alert)
		}
		sends.Add(1)
		return nil
	}
	initial := l.Info()
	l.Start(context.Background())
	m.mu.Lock()
	locked := true
	defer func() {
		if locked {
			m.mu.Unlock()
		}
	}()
	l.signal()
	waitRowResolution(t, l)
	queued, err := l.CheckAlertNow(entries[0].ID)
	if err != nil || checkRow(queued, entries[0].ID).ProgressState != "queued" || checkRow(queued, entries[1].ID).ProgressState != "" {
		t.Fatal("old idle resolution queued unrelated rows", queued, err)
	}
	m.mu.Unlock()
	locked = false
	finished := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && checkRow(info, entries[0].ID).Status == "completed"
	})
	if sends.Load() != 1 || checkRow(finished, entries[0].ID).SentAlerts != 1 || !checkRow(finished, entries[0].ID).Manual {
		t.Fatal("selected row did not execute normally", finished, sends.Load())
	}
	if !reflect.DeepEqual(checkRow(initial, entries[1].ID), checkRow(finished, entries[1].ID)) || !reflect.DeepEqual(checkRow(initial, entries[2].ID), checkRow(finished, entries[2].ID)) || finished.NextRun != initial.NextRun || !reflect.DeepEqual(finished.Config.Alerts, initial.Config.Alerts) {
		t.Fatal("row check changed unrelated history, schedule or selection", initial, finished)
	}
	fake.mu.Lock()
	for _, key := range fake.historyKeys {
		if key.Timeframe != "1h" {
			t.Error("row check downloaded an unrelated period", key)
		}
	}
	fake.mu.Unlock()
	// A later global manual check still queues every enabled row. The shared
	// rule's second row has its own identity and remains independently checkable.
	globalAllowed.Store(true)
	queued, err = l.CheckNow()
	l.mu.Lock()
	allRows := l.requestedIDs == nil
	l.mu.Unlock()
	if err != nil || checkRow(queued, entries[1].ID).ProgressState != "queued" || !allRows {
		t.Fatal("row request scope leaked into the later global request", queued, err)
	}
	waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && checkRow(info, entries[1].ID).Status == "completed"
	})
}

func TestStopClearsPendingRowWhileOldIdleResolutionIsBlocked(t *testing.T) {
	m, l, entries, fake, _ := rowCheckFixture(t)
	l.Start(context.Background())
	m.mu.Lock()
	locked := true
	defer func() {
		if locked {
			m.mu.Unlock()
		}
	}()
	l.signal()
	waitRowResolution(t, l)
	if _, err := l.CheckAlertNow(entries[0].ID); err != nil {
		t.Fatal(err)
	}
	config := copyLiveAlertsConfig(l.state.Config)
	config.Enabled = false
	info, err := l.Configure(config)
	if err != nil || info.Config.Enabled || l.requested || l.requestedIDs != nil {
		t.Fatal("Stop did not clear the transient row request", info, err)
	}
	m.mu.Unlock()
	locked = false
	l.Close()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.catalogCalls != 0 || len(fake.historyKeys) != 0 || len(l.state.Checks) != 0 {
		t.Fatal("old idle resolution resurrected the stopped row", fake.catalogCalls, fake.historyKeys, l.state.Checks)
	}
}

func TestFailedStopStillCancelsTheTransientRowRequest(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "requested", true: "retry"}[retry], func(t *testing.T) {
			_, l, entries, fake, _ := rowCheckFixture(t)
			if _, err := l.CheckAlertNow(entries[0].ID); err != nil {
				t.Fatal(err)
			}
			if retry {
				subset := subsetLiveAlertsConfig(l.state.Config, []string{entries[0].ID})
				l.clearRequestedCheckLocked()
				l.retryConfig, l.retryTargeted, l.retryManual = &subset, true, true
				l.nextRun = l.now().Add(30 * time.Second).UnixMilli()
			}
			l.path = filepath.Join(t.TempDir(), "not-a-directory", "alerts.json")
			if err := os.WriteFile(filepath.Dir(l.path), []byte("blocked"), 0600); err != nil {
				t.Fatal(err)
			}
			config := copyLiveAlertsConfig(l.state.Config)
			config.Enabled = false
			info, err := l.Configure(config)
			if err == nil || !info.Config.Enabled || l.requested || l.requestedIDs != nil || l.retryTargeted || l.retryConfig != nil || checkRow(info, entries[0].ID).ProgressState != "cancelled" {
				t.Fatal("failed Stop resurrected a pending row or claimed it was durably disabled", info, err)
			}
			if retry && l.nextRun != l.nextBoundary {
				t.Fatal("cancelled row retry retained its timer", l.nextRun, l.nextBoundary)
			}
			if fake.catalogCalls != 0 || len(fake.historyKeys) != 0 {
				t.Fatal("rejected Stop contacted providers")
			}
		})
	}
}

func TestTargetedRetryKeepsScopeAndDoesNotConsumeOtherRowsNaturalClose(t *testing.T) {
	m, l, entries, _, clock := rowCheckFixture(t)
	entries[1].Period = "h1"
	start := time.Date(2026, 11, 5, 12, 59, 50, 0, time.UTC)
	clock.Store(start.UnixMilli())
	if _, err := l.Configure(LiveAlertsConfig{Enabled: true, Alerts: entries}); err != nil {
		t.Fatal(err)
	}
	type observed struct {
		id     string
		manual bool
	}
	sent := make(chan observed, 8)
	l.send = func(_ context.Context, alert LiveAlert) error {
		l.mu.Lock()
		manual := l.state.Checks[alert.AlertID].Manual
		l.mu.Unlock()
		sent <- observed{alert.AlertID, manual}
		return nil
	}
	m.mu.Lock()
	m.active = true
	m.mu.Unlock()
	l.Start(context.Background())
	if _, err := l.CheckAlertNow(entries[0].ID); err != nil {
		t.Fatal(err)
	}
	waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && info.Status == "Waiting to retry closed-bar check"
	})
	l.mu.Lock()
	retryAt, boundary := l.nextRun, l.nextBoundary
	var retry *LiveAlertsConfig
	if l.retryConfig != nil {
		copy := copyLiveAlertsConfig(*l.retryConfig)
		retry = &copy
	}
	targeted := l.retryTargeted
	l.mu.Unlock()
	if retry == nil || !targeted || !reflect.DeepEqual(retry.ConditionIDs, []string{entries[0].ID}) || len(retry.Alerts) != 1 {
		t.Fatal("retry lost the one-row execution scope", retry)
	}
	if retryAt != start.Add(30*time.Second).UnixMilli() || boundary != start.Truncate(time.Hour).Add(time.Hour).UnixMilli() {
		t.Fatal("manual retry moved the natural boundary", retryAt, boundary)
	}
	clock.Store(boundary + 5000)
	l.signal()
	time.Sleep(20 * time.Millisecond)
	l.mu.Lock()
	next, last, running := l.nextRun, l.lastRun, l.running
	l.mu.Unlock()
	if next != retryAt || last != start.UnixMilli() || running {
		t.Fatal("past natural boundary caused an immediate scoped retry loop", next, last)
	}
	m.mu.Lock()
	m.active = false
	m.mu.Unlock()
	clock.Store(retryAt)
	l.signal()
	final := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && checkRow(info, entries[1].ID).Status == "completed"
	})
	close(sent)
	manualCount, otherScheduled := 0, false
	for call := range sent {
		if call.manual {
			manualCount++
			if call.id != entries[0].ID {
				t.Fatal("row retry widened into another row's scheduled work", call)
			}
		} else if call.id == entries[1].ID {
			otherScheduled = true
		}
	}
	if manualCount != 1 || !otherScheduled || final.NextRun != boundary+time.Hour.Milliseconds() {
		t.Fatal("scoped retry consumed the other row's natural check", manualCount, otherScheduled, final)
	}
}
