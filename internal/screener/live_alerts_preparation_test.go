package screener

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestLiveAlertProgressSnapshotRemainsAvailableDuringManagerWork(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	l.mu.Lock()
	l.state.Checks = map[string]liveAlertCheckState{id: {LastChecked: 1704070800000, AssetsScanned: 7, SentAlerts: 2, Status: "completed", Manual: true}}
	l.state.Sent = 2
	l.queueCheckProgressLocked([]string{id}, true)
	l.startCheckProgressLocked(id)
	l.updateCheckProgressIDLocked(id, "Enriching indicators", 38, "EURUSD h1")
	before := copyLiveCheckStates(l.state.Checks)
	l.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	done := make(chan LiveAlertsInfo, 1)
	go func() { done <- l.ProgressInfo() }()
	select {
	case info := <-done:
		if len(info.Checks) != 1 {
			t.Fatal("lost check rows", info)
		}
		row := info.Checks[0]
		if row.Phase != "Enriching indicators" || row.Progress != 38 || row.CurrentAsset != "EURUSD h1" || row.AssetsScanned != 7 || row.LastChecked != 1704070800000 || row.SentAlerts != 0 || row.SentAlertsKnown || row.LifetimeSentAlerts != 2 || row.Status != "completed" {
			t.Fatalf("progress snapshot changed attempt history or omitted worker stage: %+v", row)
		}
		info.Config.ConditionIDs[0] = "caller mutation"
		if l.state.Config.ConditionIDs[0] != id {
			t.Fatal("progress snapshot aliased owned configuration")
		}
		if !reflect.DeepEqual(l.state.Checks, before) {
			t.Fatal("progress read persisted an invented check")
		}
	case <-time.After(time.Second):
		t.Fatal("progress endpoint blocked behind Manager metadata/history work")
	}
}

func TestLiveAlertCheckNowReportsExistingRuleResolutionAndManagerWait(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.mu.Lock()
	released := false
	defer func() {
		if !released {
			m.mu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() { _, err := l.selectedConditionsForCheck(ctx, config); done <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		l.mu.Lock()
		phase := l.resolutionPhase
		l.mu.Unlock()
		if phase == "Waiting for Screener work to load saved conditions" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rule resolution never exposed its Manager wait")
		}
		time.Sleep(time.Millisecond)
	}
	info, err := l.CheckNow()
	if err != nil || len(info.Checks) != 1 || info.Checks[0].Phase != "Waiting for Screener work to load saved conditions" || info.Checks[0].ProgressState != "queued" || info.Checks[0].LastChecked != 0 || info.Checks[0].Status != "never" {
		t.Fatalf("check acknowledgement hid existing scope work or invented an attempt: %+v %v", info, err)
	}
	if info.Checks[0].ConditionID != id {
		t.Fatal("wrong row identity")
	}
	m.mu.Unlock()
	released = true
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("rule resolution did not finish after Manager was released")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.resolutionPhase != "" {
		t.Fatal("completed rule resolution retained a stale phase", l.resolutionPhase)
	}
}

func TestLiveAlertPreparationDoesNotOverwriteStoppedRows(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queueCheckProgressLocked(config.ConditionIDs, true)
	l.cancelCheckProgressLocked("Cancelled")
	l.status = "Disabled"
	l.prepareCheckProgressLocked(config.ConditionIDs, "Loading and validating saved conditions")
	if l.status != "Disabled" || l.checkProgress[config.ConditionIDs[0]].state != "cancelled" {
		t.Fatal("obsolete preparation resurrected stopped presentation")
	}
}

func TestLiveAlertIdleRuleResolutionPreservesFutureRetryProgress(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	l.mu.Lock()
	l.queueCheckProgressLocked(config.ConditionIDs, true)
	l.prepareCheckProgressLocked(config.ConditionIDs, "Waiting to retry check")
	l.status = "Waiting to retry closed-bar check"
	l.nextRun = l.now().Add(30 * time.Second).UnixMilli()
	l.mu.Unlock()
	m.mu.Lock()
	done := make(chan error, 1)
	go func() { _, err := l.selectedConditionsForCheck(context.Background(), config); done <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		l.mu.Lock()
		phase := l.resolutionPhase
		l.mu.Unlock()
		if phase == "Waiting for Screener work to load saved conditions" {
			break
		}
		if time.Now().After(deadline) {
			m.mu.Unlock()
			t.Fatal("background scope resolution did not start")
		}
		time.Sleep(time.Millisecond)
	}
	assertWaiting := func() {
		t.Helper()
		info := l.ProgressInfo()
		if info.Status != "Waiting to retry closed-bar check" || len(info.Checks) != 1 || info.Checks[0].ConditionID != id || info.Checks[0].Phase != "Waiting to retry check" || info.Checks[0].ProgressState != "queued" || info.Checks[0].LastChecked != 0 {
			t.Fatalf("idle metadata work replaced a pending retry: %+v", info)
		}
	}
	assertWaiting()
	m.mu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle rule resolution did not finish")
	}
	assertWaiting()
}
