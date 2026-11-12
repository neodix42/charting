package screener

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestLiveAlertAttemptSentCountsResetWithoutErasingHistoricalTotals(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	l.state.Sent = 37
	l.state.Checks = map[string]liveAlertCheckState{id: {LastChecked: 1704070800000, SentAlerts: 37, Status: "completed", AssetsScanned: 21}}
	if err := writeState(l.path, l.state); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(l.path)
	legacy := l.Info().Checks[0]
	if legacy.SentAlertsKnown || legacy.SentAlerts != 0 || legacy.LifetimeSentAlerts != 37 {
		t.Fatal("legacy lifetime count presented as a latest-check result", legacy)
	}
	after, _ := os.ReadFile(l.path)
	if string(before) != string(after) {
		t.Fatal("reading old counts rewrote historical state")
	}
	run, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	row := l.Info().Checks[0]
	if !row.SentAlertsKnown || row.SentAlerts != 0 || row.LifetimeSentAlerts != 37 || l.Info().Sent != 37 {
		t.Fatal("begin did not reset just this attempt", row)
	}
	l.send = func(context.Context, LiveAlert) error { return nil }
	if err := l.deliver(context.Background(), LiveAlert{ConditionID: id}); err != nil {
		t.Fatal(err)
	}
	if err := run.finish(nil); err != nil {
		t.Fatal(err)
	}
	row = l.Info().Checks[0]
	if row.SentAlerts != 1 || row.LifetimeSentAlerts != 38 || l.Info().Sent != 38 {
		t.Fatal("confirmed signal not attributed to current attempt", row)
	}
	second, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	if l.state.Checks[id].CheckID == run.checkIDs[id] {
		t.Fatal("consecutive attempts reused an identity")
	}
	if err := second.finish(nil); err != nil {
		t.Fatal(err)
	}
	row = l.Info().Checks[0]
	if row.SentAlerts != 0 || !row.SentAlertsKnown || row.LifetimeSentAlerts != 38 || l.Info().Sent != 38 {
		t.Fatal("no-signal check retained old sent count or erased total", row)
	}
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	row = restored.Info().Checks[0]
	if row.SentAlerts != 0 || !row.SentAlertsKnown || row.LifetimeSentAlerts != 38 || restored.Info().Sent != 38 {
		t.Fatal("restart lost current zero or lifetime total", row)
	}
	if len(l.state.Watermarks) != 0 || len(restored.state.Watermarks) != 0 {
		t.Fatal("counter changes reset or invented delivery claims")
	}
}

func TestLiveAlertLateConfirmationCannotCreditOrFinishANewerAttempt(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	old, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	l.send = func(context.Context, LiveAlert) error { close(started); <-release; return nil }
	ctx := context.WithValue(context.Background(), liveAlertHistoryGroupKey{}, &liveAlertHistoryGroup{run: old})
	delivered := make(chan error, 1)
	go func() { delivered <- l.deliver(ctx, LiveAlert{ConditionID: id}) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	newer, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	newID := l.state.Checks[id].CheckID
	close(release)
	if err := <-delivered; err != nil {
		t.Fatal(err)
	}
	row := l.Info().Checks[0]
	if row.SentAlerts != 0 || row.LifetimeSentAlerts != 1 || l.Info().Sent != 1 {
		t.Fatal("late old confirmation credited a new attempt", row)
	}
	old.scanned(id, "old")
	old.skipped(id, "old", errors.New("obsolete failure"))
	old.warmingUp(id, "old", "d1")
	if err := old.finish(errors.New("obsolete completion")); err != nil {
		t.Fatal(err)
	}
	state := l.state.Checks[id]
	if state.CheckID != newID || state.Status != "running" || state.AssetsScanned != 0 || state.AssetsSkipped != 0 || state.AssetsWarmingUp != 0 || state.Error != "" {
		t.Fatal("obsolete run overwrote newer attempt metadata", state)
	}
	l.send = func(context.Context, LiveAlert) error { return nil }
	freshCtx := context.WithValue(context.Background(), liveAlertHistoryGroupKey{}, &liveAlertHistoryGroup{run: newer})
	if err := l.deliver(freshCtx, LiveAlert{ConditionID: id}); err != nil {
		t.Fatal(err)
	}
	if err := newer.finish(nil); err != nil {
		t.Fatal(err)
	}
	row = l.Info().Checks[0]
	if row.SentAlerts != 1 || row.LifetimeSentAlerts != 2 || l.Info().Sent != 2 {
		t.Fatal("new attempt confirmation attribution wrong", row)
	}
}

func TestLiveAlertConfirmationAfterCancellationCountsOnlyItsOwnAttempt(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	run, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	l.send = func(context.Context, LiveAlert) error { close(started); <-release; return nil }
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := context.WithValue(parent, liveAlertHistoryGroupKey{}, &liveAlertHistoryGroup{run: run})
	delivered := make(chan error, 1)
	go func() { delivered <- l.deliver(ctx, LiveAlert{ConditionID: id}) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	cancel()
	if err := run.finish(context.Canceled); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-delivered; err != nil {
		t.Fatal(err)
	}
	row := l.Info().Checks[0]
	if row.Status != "cancelled" || row.SentAlerts != 1 || !row.SentAlertsKnown || row.LifetimeSentAlerts != 1 || l.Info().Sent != 1 {
		t.Fatal("a real late acknowledgement was lost from its cancelled attempt", row)
	}
}

func TestLiveAlertAttemptSnapshotsAndValidationAreIndependent(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	run, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := copyLiveCheckStates(l.state.Checks)
	l.send = func(context.Context, LiveAlert) error { return nil }
	if err := l.deliver(context.Background(), LiveAlert{ConditionID: id}); err != nil {
		t.Fatal(err)
	}
	if *snapshot[id].LastCheckSentAlerts != 0 {
		t.Fatal("counter mutation altered a rollback snapshot")
	}
	copy := snapshot[id]
	*copy.LastCheckSentAlerts = 99
	if *l.state.Checks[id].LastCheckSentAlerts != 1 {
		t.Fatal("copied state exposes live counter memory")
	}
	valid := l.state.Checks[id]
	for _, mutate := range []func(*liveAlertCheckState){func(s *liveAlertCheckState) { s.CheckID = "" }, func(s *liveAlertCheckState) { v := int64(-1); s.LastCheckSentAlerts = &v }, func(s *liveAlertCheckState) { v := int64(2); s.LastCheckSentAlerts = &v }, func(s *liveAlertCheckState) { s.LastCheckSentAlerts = nil }} {
		state := valid
		mutate(&state)
		if validateLiveCheckStates(map[string]liveAlertCheckState{id: state}, l.state.Sent) == nil {
			t.Fatal("invalid attempt count/identity accepted", state)
		}
	}
	if err := run.finish(nil); err != nil {
		t.Fatal(err)
	}
	before := copyLiveCheckStates(l.state.Checks)
	l.mu.Lock()
	l.state.Sent++
	l.recordConfirmedAlertLocked(id)
	l.mu.Unlock() // Legacy recovery has no attempt attribution.
	after := l.state.Checks[id]
	if *after.LastCheckSentAlerts != *before[id].LastCheckSentAlerts || after.SentAlerts != before[id].SentAlerts+1 {
		t.Fatal("legacy recovery was credited to a modern check", after)
	}
	if reflect.DeepEqual(before, l.state.Checks) {
		t.Fatal("lifetime confirmation was dropped")
	}
}
