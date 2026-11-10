package screener

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func startupAlertsManager(t *testing.T) *Manager {
	t.Helper()
	m := newManager(t.TempDir(), func() (workerRuntime, error) { return workerRuntime{}, errors.New("offline startup fixture") })
	t.Cleanup(m.Close)
	return m
}

func waitStartupService(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("startup service waited for unrelated manager work")
	}
}

func TestDisabledLiveAlertsStartupDoesNotResolveSavedRules(t *testing.T) {
	m := startupAlertsManager(t)
	l, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	advanced := make(chan struct{})
	var once sync.Once
	l.now = func() time.Time { once.Do(func() { close(advanced) }); return time.Now() }
	m.mu.Lock()
	defer m.mu.Unlock()
	l.Start(context.Background())
	waitStartupService(t, advanced)
	closed := make(chan struct{})
	go func() { l.Close(); close(closed) }()
	waitStartupService(t, closed)
	if l.resolutionPhase != "" || l.nextRun != 0 {
		t.Fatal("disabled startup resolved rules or scheduled a check")
	}
}

func TestEnabledLiveAlertsStartupAndCloseDoNotBlockOnManager(t *testing.T) {
	m := startupAlertsManager(t)
	l, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	l.state.Config.Enabled = true
	m.mu.Lock()
	defer m.mu.Unlock()
	started := make(chan struct{})
	go func() { l.Start(context.Background()); close(started) }()
	waitStartupService(t, started)
	deadline := time.Now().Add(time.Second)
	for {
		l.mu.Lock()
		waiting := l.resolutionPhase == "Waiting for Screener work to load saved conditions"
		l.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background schedule did not begin restoration")
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { l.Close(); close(closed) }()
	waitStartupService(t, closed)
	if l.nextRun != 0 || l.running {
		t.Fatal("cancelled startup published a schedule or began a check")
	}
}

func TestCurrentLiveAlertsConstructorDoesNotReadSavedConditions(t *testing.T) {
	m := startupAlertsManager(t)
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Saved hourly", ConditionID: strings.Repeat("b", 32), Period: "h1", DataSource: "hyperliquid", Enabled: true}
	state := liveAlertsState{Version: 7, Config: LiveAlertsConfig{Alerts: []LiveAlertDefinition{entry}}, Watermarks: map[string]int64{}, Recent: []LiveAlertOutcome{}}
	if err := writeState(filepath.Join(m.profile, "screener-live-alerts.json"), state); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	done := make(chan struct{})
	var restored *LiveAlerts
	var err error
	go func() { restored, err = NewLiveAlerts(m, nil); close(done) }()
	waitStartupService(t, done)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if len(restored.state.Config.Alerts) != 1 || restored.state.Config.Alerts[0].DataSource != entry.DataSource {
		t.Fatal("current stored source or entry was lost")
	}
}
