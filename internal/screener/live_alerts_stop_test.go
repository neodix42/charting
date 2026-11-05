package screener

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLiveAlertsStopCancelsAndReturnsWithoutWaitingForManager(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.mu.Lock()
	l.cycleCancel = cancel
	l.running = true
	l.requested = true
	l.nextRun = 1704070800000
	l.retryConfig = &config
	l.state.Sent = 17
	l.state.Checks = map[string]liveAlertCheckState{id: {AssetsScanned: 4, LastChecked: 1704070800000, SentAlerts: 3, Status: "running"}}
	l.state.Watermarks = map[string]int64{id + "/eurusd/h1": 1704070800000}
	l.mu.Unlock()
	config.Enabled = false
	// Holding the manager lock simulates active catalog/saved-evidence work.
	// A stop must cancel and acknowledge its durable state before this is released.
	m.mu.Lock()
	released := false
	defer func() {
		if !released {
			m.mu.Unlock()
		}
	}()
	done := make(chan struct {
		info LiveAlertsInfo
		err  error
	}, 1)
	go func() {
		info, err := l.Configure(config)
		done <- struct {
			info LiveAlertsInfo
			err  error
		}{info, err}
	}()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		m.mu.Unlock()
		released = true
		t.Fatal("Stop waited for catalog work before cancelling its active check")
	}
	select {
	case result := <-done:
		if result.err != nil || result.info.Config.Enabled || !result.info.Running || result.info.NextRun != 0 || result.info.Sent != 17 || len(result.info.Checks) != 1 || result.info.Checks[0].SentAlerts != 3 {
			t.Fatalf("stop lost durable state or truthful worker status: %+v %v", result.info, result.err)
		}
		var saved liveAlertsState
		if err := readState(l.path, liveAlertsStateBytes, &saved); err != nil || saved.Config.Enabled || saved.Sent != 17 || saved.Watermarks[id+"/eurusd/h1"] != 1704070800000 {
			t.Fatalf("stop acknowledged before durable config or lost claim: %+v %v", saved, err)
		}
	case <-time.After(time.Second):
		m.mu.Unlock()
		released = true
		t.Fatal("Stop waited for full Info after cancellation")
	}
	m.mu.Unlock()
	released = true
	l.mu.Lock()
	if l.requested || l.retryConfig != nil || l.nextBoundary != 0 {
		t.Fatal("stopped scheduler retained a pending/retry check")
	}
	l.mu.Unlock()
}

func TestLiveAlertsStopPreservesNamedDraftWithoutResolvingItsSavedCondition(t *testing.T) {
	m, l, legacy, _ := liveAlertsFixture(t)
	entry := LiveAlertDefinition{ID: "1234567890abcdef1234567890abcdef", Name: "My weekly coins", ConditionID: legacy.ConditionIDs[0], Period: "w1", DataSource: "bybit", Enabled: false}
	config := LiveAlertsConfig{Enabled: false, Alerts: []LiveAlertDefinition{entry}}
	l.state.Checks = map[string]liveAlertCheckState{entry.ID: {LastChecked: 1704070800000, Status: "completed", SentAlerts: 2}}
	l.state.Sent = 2
	m.mu.Lock()
	done := make(chan struct {
		info LiveAlertsInfo
		err  error
	}, 1)
	go func() {
		info, err := l.Configure(config)
		done <- struct {
			info LiveAlertsInfo
			err  error
		}{info, err}
	}()
	var result struct {
		info LiveAlertsInfo
		err  error
	}
	select {
	case result = <-done:
	case <-time.After(time.Second):
		m.mu.Unlock()
		t.Fatal("named stop resolved saved-condition evidence before returning")
	}
	m.mu.Unlock()
	if result.err != nil || !reflect.DeepEqual(result.info.Config.Alerts, config.Alerts) || result.info.Config.Enabled || len(result.info.Checks) != 1 || result.info.Checks[0].Name != entry.Name || result.info.Checks[0].AlertID != entry.ID || result.info.Checks[0].SentAlerts != 2 {
		t.Fatalf("named stop lost draft or check presentation: %+v %v", result.info, result.err)
	}
	var saved liveAlertsState
	if err := readState(l.path, liveAlertsStateBytes, &saved); err != nil || !reflect.DeepEqual(saved.Config.Alerts, config.Alerts) {
		t.Fatal("named draft was not durably retained", err)
	}
}

func TestLiveAlertsStopInvalidatesAnEarlierPendingStart(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	// The old enabled mutation cannot get past the manager lock. Wait until it
	// reaches that lock so Stop's later committed revision must win.
	m.mu.Lock()
	startDone := make(chan error, 1)
	go func(startConfig LiveAlertsConfig) { _, err := l.Configure(startConfig); startDone <- err }(copyLiveAlertsConfig(config))
	deadline := time.Now().Add(time.Second)
	for {
		buffer := make([]byte, 64*1024)
		stacks := string(buffer[:runtime.Stack(buffer, true)])
		if strings.Contains(stacks, "(*LiveAlerts).selectedConditions") && strings.Contains(stacks, "(*LiveAlerts).Configure") {
			break
		}
		if time.Now().After(deadline) {
			m.mu.Unlock()
			t.Fatal("start did not reach its manager read")
		}
		runtime.Gosched()
	}
	config.Enabled = false
	info, err := l.Configure(config)
	if err != nil || info.Config.Enabled {
		m.mu.Unlock()
		t.Fatal("stop could not commit while start was waiting", err)
	}
	m.mu.Unlock()
	select {
	case err := <-startDone:
		if err == nil {
			t.Fatal("pending earlier start re-enabled alerts after stop")
		}
	case <-time.After(time.Second):
		t.Fatal("obsolete pending start did not finish")
	}
	if l.Info().Config.Enabled {
		t.Fatal("stale preparation replaced the stopped configuration")
	}
}

func TestLiveAlertsStopSaveFailurePreservesEnabledConfigurationAndClaims(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	l.state.Watermarks[id+"/eurusd/h1"] = 1704070800000
	l.state.Sent = 3
	before := copyLiveAlertsConfig(l.state.Config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.cycleCancel = cancel
	l.path = filepath.Join(t.TempDir(), "not-a-directory", "state.json")
	if err := os.WriteFile(filepath.Dir(l.path), []byte("block state write"), 0600); err != nil {
		t.Fatal(err)
	}
	config.Enabled = false
	info, err := l.Configure(config)
	if err == nil || !info.Config.Enabled || !reflect.DeepEqual(l.state.Config, before) || info.Sent != 3 || l.state.Watermarks[id+"/eurusd/h1"] != 1704070800000 {
		t.Fatalf("failed stop lied about durability or lost claims: %+v %v", info, err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("failed durable stop still must cancel the in-flight check")
	}
}

func TestLiveAlertsStopDoesNotReadMissingSavedEvidenceAndPreservesSubmittedDraft(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	if err := os.WriteFile(filepath.Join(m.profile, "screener-saved-conditions.json"), []byte("unavailable saved evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	config.Enabled = false
	config.ConditionSources = []LiveAlertConditionSource{{ConditionID: config.ConditionIDs[0], Source: "bybit"}}
	info, err := l.Configure(config)
	if err != nil || info.Config.Enabled || liveAlertSource(info.Config, config.ConditionIDs[0]) != "bybit" {
		t.Fatalf("stop read saved evidence or dropped the submitted edits: %+v %v", info, err)
	}
	var saved liveAlertsState
	if err = readState(l.path, liveAlertsStateBytes, &saved); err != nil || liveAlertSource(saved.Config, config.ConditionIDs[0]) != "bybit" {
		t.Fatal("submitted draft was not saved", err)
	}
}
