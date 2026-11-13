package screener

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func TestLiveAlertDurationUsesBackendTriggerNotHistoricalCandleAndFreezesAfterRestart(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	trigger := time.Date(2026, 11, 13, 12, 30, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(trigger.UnixMilli())
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	refresh := l.refresh
	l.refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
		clock.Add(4 * time.Second.Milliseconds())
		return refresh(ctx, directory, groups, asOf)
	}
	l.send = func(context.Context, LiveAlert) error {
		clock.Add(3 * time.Second.Milliseconds())
		return nil
	}
	// A manual check can evaluate an old bar without accumulating the years
	// between that candle and the real backend request.
	asOf := time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()
	if err := l.check(context.Background(), config, asOf); err != nil {
		t.Fatal(err)
	}
	assert := func(row LiveAlertCheck) {
		t.Helper()
		if !row.DurationKnown || row.TriggeredAt != trigger.UnixMilli() || row.DurationMs != 7000 || row.LastChecked != trigger.Add(7*time.Second).UnixMilli() || row.SentAlerts != 1 || row.LifetimeSentAlerts != 1 || row.Status != "completed" {
			t.Fatalf("incorrect total check timing/counters: %+v", row)
		}
	}
	assert(l.Info().Checks[0])
	clock.Add(24 * time.Hour.Milliseconds())
	assert(l.ProgressInfo().Checks[0])
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.now = l.now
	assert(restored.Info().Checks[0])
	assert(restored.ProgressInfo().Checks[0])
}

func TestLiveAlertDurationIncludesManualQueueAndStalledMetadataAndDelivery(t *testing.T) {
	m, l, entries, _, clock := rowCheckFixture(t)
	id := entries[0].ID
	trigger := clock.Load()
	prior, err := l.beginCheckHistory(subsetLiveAlertsConfig(l.state.Config, []string{id}), true, time.UnixMilli(trigger-10000))
	if err != nil {
		t.Fatal(err)
	}
	if err := prior.finish(nil); err != nil {
		t.Fatal(err)
	}
	previous := checkRow(l.ProgressInfo(), id)
	m.mu.Lock()
	locked := true
	defer func() {
		if locked {
			m.mu.Unlock()
		}
	}()
	queued, err := l.CheckAlertNow(id)
	if err != nil {
		t.Fatal(err)
	}
	if row := checkRow(queued, id); row.DurationMs != previous.DurationMs || row.TriggeredAt != previous.TriggeredAt || !row.DurationKnown || row.LastChecked != previous.LastChecked {
		t.Fatal("queueing replaced the previous actual check timing", row, previous)
	}
	sending, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	l.send = func(context.Context, LiveAlert) error {
		clock.Add(5000)
		close(sending)
		<-release
		return nil
	}
	l.Start(context.Background())
	waitRowResolution(t, l)
	clock.Add(7000)
	m.mu.Unlock()
	locked = false
	select {
	case <-sending:
	case <-time.After(3 * time.Second):
		t.Fatal("manual check never reached delivery")
	}
	// Compact progress reads must update elapsed time without waiting for the
	// Manager lock that the full metadata endpoint needs.
	m.mu.Lock()
	locked = true
	before, _ := os.ReadFile(l.path)
	done := make(chan LiveAlertsInfo, 1)
	go func() { done <- l.ProgressInfo() }()
	select {
	case snapshot := <-done:
		row := checkRow(snapshot, id)
		if !row.DurationKnown || row.DurationMs != 12000 || row.TriggeredAt != trigger || row.Status != "running" || row.SentAlerts != 0 || row.LastChecked != trigger+7000 {
			t.Fatal("live timing excluded queue/delivery or changed count semantics", row)
		}
	case <-time.After(time.Second):
		t.Fatal("timing progress blocked on metadata")
	}
	after, _ := os.ReadFile(l.path)
	if string(before) != string(after) {
		t.Fatal("reading elapsed time wrote the state file")
	}
	m.mu.Unlock()
	locked = false
	close(release)
	finished := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return !info.Running && checkRow(info, id).Status == "completed" })
	row := checkRow(finished, id)
	if !row.DurationKnown || row.DurationMs != 12000 || row.TriggeredAt != trigger || row.SentAlerts != 1 {
		t.Fatal("manual check lost original trigger or total elapsed", row)
	}
}

func TestLiveAlertDurationScheduledTriggerPrecedesMetadataAndUsesActualWakeup(t *testing.T) {
	m, l, entries, _, clock := rowCheckFixture(t)
	id := entries[0].ID
	initial := l.Info()
	trigger := initial.NextRun + 5000 // Actual wakeup after the natural bar close.
	m.mu.Lock()
	locked := true
	defer func() {
		if locked {
			m.mu.Unlock()
		}
	}()
	l.send = func(context.Context, LiveAlert) error { clock.Add(3000); return nil }
	clock.Store(trigger)
	l.Start(context.Background())
	waitRowResolution(t, l)
	clock.Add(8000)
	m.mu.Unlock()
	locked = false
	finished := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return !info.Running && checkRow(info, id).Status == "completed" })
	row := checkRow(finished, id)
	if row.Manual || !row.DurationKnown || row.TriggeredAt != trigger || row.DurationMs != 11000 || row.LastChecked != trigger+11000 {
		t.Fatal("scheduled duration used candle cutoff or skipped preparation", row)
	}
}

func TestLiveAlertDurationRetriesStartNewActualAttempts(t *testing.T) {
	_, l, saved, adapter := midnightAlertFixture(t)
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(day.Add(-time.Minute).UnixMilli())
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	adapter.outage.Store(true)
	adapter.onFailure = func() { clock.Add(6000) }
	entry := LiveAlertDefinition{ID: strings.Repeat("e", 32), Name: "Timed daily retry", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true}
	if _, err := l.Configure(LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}); err != nil {
		t.Fatal(err)
	}
	l.send = func(context.Context, LiveAlert) error { clock.Add(2000); return nil }
	l.Start(context.Background())
	clock.Store(day.UnixMilli())
	l.signal()
	failed := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && info.Status == "Waiting to retry closed-bar check"
	})
	first := failed.Checks[0]
	if first.TriggeredAt != day.UnixMilli() || !first.DurationKnown || first.DurationMs != 6000 {
		t.Fatal("failed attempt did not freeze its own elapsed time", first)
	}
	l.mu.Lock()
	oldID := l.state.Checks[entry.ID].CheckID
	l.mu.Unlock()
	adapter.outage.Store(false)
	clock.Store(failed.NextRun)
	l.signal()
	finished := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return !info.Running && info.Sent == 1 })
	row := finished.Checks[0]
	if !row.DurationKnown || row.TriggeredAt != failed.NextRun || row.DurationMs != 2000 || row.Manual {
		t.Fatal("retry included prior attempt or idle retry delay", row, first)
	}
	l.mu.Lock()
	newID := l.state.Checks[entry.ID].CheckID
	l.mu.Unlock()
	if oldID == newID {
		t.Fatal("retry reused the earlier attempt identity")
	}
}

func TestLiveAlertDurationIncludesHigherTimeframeWaitAndEveryAsset(t *testing.T) {
	_, l, entries, fake, clock := rowCheckFixture(t)
	fake.items = []domain.Instrument{{NativeID: "BTC", Market: "perpetual", Availability: "TRADING"}, {NativeID: "ETH", Market: "perpetual", Availability: "TRADING"}}
	trigger := clock.Load()
	l.send = func(context.Context, LiveAlert) error { clock.Add(2000); return nil }
	if err := l.check(context.Background(), l.state.Config, trigger); err != nil {
		t.Fatal(err)
	}
	info := l.Info()
	daily, hourly := checkRow(info, entries[1].ID), checkRow(info, entries[0].ID)
	if daily.TriggeredAt != trigger || hourly.TriggeredAt != trigger || !daily.DurationKnown || !hourly.DurationKnown || daily.DurationMs != 4000 || hourly.DurationMs != 8000 || daily.AssetsScanned != 2 || hourly.AssetsScanned != 2 || daily.SentAlerts != 2 || hourly.SentAlerts != 2 {
		t.Fatal("row total excluded earlier timeframe queue or one of its assets", daily, hourly)
	}
}

func TestLiveAlertDurationCancelledAndObsoleteRunsCannotOverwriteNewAttempt(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	old, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	stopped := copyLiveAlertsConfig(config)
	stopped.Enabled = false
	info, err := l.Configure(stopped)
	if err != nil || !info.Checks[0].DurationKnown || info.Checks[0].DurationMs != 3000 {
		t.Fatal("Stop response hid draining attempt time", info, err)
	}
	now = now.Add(2 * time.Second)
	if err := old.finish(context.Canceled); err != nil {
		t.Fatal(err)
	}
	row := l.ProgressInfo().Checks[0]
	if row.Status != "cancelled" || !row.DurationKnown || row.DurationMs != 5000 {
		t.Fatal("cancellation froze at button click instead of worker completion", row)
	}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	obsolete, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	newTrigger := now.UnixMilli()
	current, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Second)
	if err := obsolete.finish(errors.New("late failed attempt")); err != nil {
		t.Fatal(err)
	}
	row = l.ProgressInfo().Checks[0]
	if row.Status != "running" || row.TriggeredAt != newTrigger || row.DurationMs != 4000 || !row.DurationKnown || l.state.Checks[id].DurationMs != nil {
		t.Fatal("obsolete run froze/replaced the current timer", row)
	}
	if err := current.finish(errors.New("current failure")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	row = l.ProgressInfo().Checks[0]
	if row.Status != "failed" || row.DurationMs != 4000 || !row.DurationKnown {
		t.Fatal("failed attempt kept accumulating elapsed time", row)
	}
}

func TestLiveAlertDurationInterruptedAndLegacyRemainUnknownWithoutWritingOnRead(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	if _, err := l.beginCheckHistory(config, true); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(l.path)
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.now = func() time.Time { return now.Add(48 * time.Hour) }
	row := restored.Info().Checks[0]
	if row.DurationKnown || row.DurationMs != 0 || row.TriggeredAt != now.UnixMilli() || row.Status != "cancelled" || !strings.Contains(row.Error, "interrupted") {
		t.Fatal("restart invented an interruption endpoint or accrued downtime", row)
	}
	after, _ := os.ReadFile(l.path)
	if string(before) != string(after) {
		t.Fatal("reading interrupted duration modified saved state")
	}
	l.state.Checks[id] = liveAlertCheckState{LastChecked: now.UnixMilli(), Status: "completed", SentAlerts: 0}
	row = l.ProgressInfo().Checks[0]
	encoded, err := json.Marshal(row)
	if err != nil || row.DurationKnown || row.TriggeredAt != 0 || row.DurationMs != 0 || !strings.Contains(string(encoded), `"durationKnown":false`) || !strings.Contains(string(encoded), `"durationMs":0`) || !strings.Contains(string(encoded), `"triggeredAt":0`) {
		t.Fatal("legacy timing was guessed or omitted explicit clear values", row, string(encoded), err)
	}
}

func TestLiveAlertDurationSnapshotsAndValidation(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	run, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	// Clock corrections cannot produce negative durations.
	now = now.Add(-time.Second)
	if row := l.ProgressInfo().Checks[0]; !row.DurationKnown || row.DurationMs != 0 {
		t.Fatal("backward clock produced a negative duration", row)
	}
	if err := run.finish(nil); err != nil {
		t.Fatal(err)
	}
	before := copyLiveCheckStates(l.state.Checks)
	changed := l.state.Checks[id]
	*changed.DurationMs = 99
	if *before[id].DurationMs != 0 {
		t.Fatal("snapshot aliases the duration pointer")
	}
	if err := validateLiveCheckStates(before, 0); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []func(*liveAlertCheckState){
		func(s *liveAlertCheckState) { s.TriggeredAt = -1 },
		func(s *liveAlertCheckState) { s.TriggeredAt = 0 },
		func(s *liveAlertCheckState) { s.CheckID = "bad" },
		func(s *liveAlertCheckState) { s.Status = "running" },
		func(s *liveAlertCheckState) { n := int64(-1); s.DurationMs = &n },
	} {
		records := copyLiveCheckStates(before)
		state := records[id]
		corrupt(&state)
		records[id] = state
		if err := validateLiveCheckStates(records, 0); err == nil {
			t.Fatal("invalid timing state accepted", state)
		}
	}
	if !reflect.DeepEqual(before, copyLiveCheckStates(before)) {
		t.Fatal("copy changed timing values")
	}
}
