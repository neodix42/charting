package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func namedPublicLiveFixture(t *testing.T) (*Manager, *LiveAlerts, SavedAnalysisCondition, *publicAlertFakeAdapter) {
	t.Helper()
	m := sourceEnrichmentManager(t, false)
	request := AnalysisRequest{Period: "d1", Conditions: []AnalysisCondition{{ID: "positive", Operator: "above", Left: AnalysisOperand{Kind: "field", Field: "close"}, Right: AnalysisOperand{Kind: "number", Value: 0}}}}
	saved, err := m.SaveAnalysisCondition("Original reusable condition", request)
	if err != nil {
		t.Fatal(err)
	}
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTCUSDT", Market: "spot", Availability: "TRADING"}}}
	l := newPublicAlertTestLive(t, m, "binance", fake)
	t.Cleanup(l.Close)
	return m, l, saved, fake
}

func TestNamedLiveAlertsIndependentIdentityPeriodSourceAndPause(t *testing.T) {
	_, l, saved, fake := namedPublicLiveFixture(t)
	l.publicData.adapters["bybit"] = fake
	entries := []LiveAlertDefinition{
		{ID: strings.Repeat("a", 32), Name: "Hourly Binance", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true},
		{ID: strings.Repeat("b", 32), Name: "Daily Bybit", ConditionID: saved.ID, Period: "d1", DataSource: "bybit", Enabled: true},
		{ID: strings.Repeat("c", 32), Name: "Paused weekly", ConditionID: saved.ID, Period: "w1", DataSource: "binance", Enabled: false},
	}
	config := LiveAlertsConfig{Enabled: true, Alerts: entries}
	info, err := l.Configure(config)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := l.selectedConditions(l.state.Config)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].ID != entries[0].ID || selected[0].Request.Period != "h1" || selected[1].Request.Period != "d1" || selected[0].liveAlertDefinition.ConditionID != saved.ID {
		t.Fatalf("entries collapsed or saved periods leaked: %+v", selected)
	}
	if len(info.Config.Alerts) != 3 || len(info.ConditionScopes) != 3 || len(info.Checks) != 3 || liveAlertSource(info.Config, entries[1].ID) != "bybit" {
		t.Fatalf("independent entry presentation lost: %+v", info)
	}
	for _, scope := range info.ConditionScopes {
		if scope.AlertID == "" || scope.SavedConditionID != saved.ID || scope.ConditionID != scope.AlertID {
			t.Fatalf("scope identity not joinable: %+v", scope)
		}
	}
	if len(fake.historyKeys) != 0 || fake.catalogCalls != 0 {
		t.Fatal("configure/poll contacted a public provider")
	}
	info.Config.Alerts[0].Name = "mutated copy"
	if l.Info().Config.Alerts[0].Name != entries[0].Name {
		t.Fatal("named definitions were not deep copied")
	}
	for i := range config.Alerts {
		config.Alerts[i].Enabled = false
	}
	info, err = l.Configure(config)
	if err != nil || !info.Config.Enabled || len(info.Config.ConditionIDs) != 0 || info.NextRun != 0 {
		t.Fatalf("pausing all entries changed master or scheduled checks: %+v %v", info, err)
	}
}

func TestNamedLiveAlertsDuplicateRuleHasIndependentDeliveriesAndNoResends(t *testing.T) {
	_, l, saved, _ := namedPublicLiveFixture(t)
	entries := []LiveAlertDefinition{
		{ID: strings.Repeat("a", 32), Name: "First independent alert", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true},
		{ID: strings.Repeat("b", 32), Name: "Second independent alert", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true},
	}
	var sent []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { sent = append(sent, alert); return nil }
	config := LiveAlertsConfig{Enabled: true, Alerts: entries}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 11, 5, 12, 0, 5, 0, time.UTC).UnixMilli()
	if err := l.check(context.Background(), l.state.Config, cutoff); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 {
		t.Fatalf("same rule/combo collapsed independent entries: %d sends", len(sent))
	}
	for _, alert := range sent {
		if alert.AlertID == "" || alert.AlertID != alert.ConditionID || alert.AlertName != alert.ConditionName || alert.SavedConditionID != saved.ID || alert.SavedConditionName != saved.Name {
			t.Fatalf("entry/original rule attribution lost: %+v", alert)
		}
	}
	for i := range config.Alerts {
		config.Alerts[i].Enabled = false
	}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	for i := range config.Alerts {
		config.Alerts[i].Enabled = true
		config.Alerts[i].Name += " renamed"
	}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	if err := l.check(context.Background(), l.state.Config, cutoff); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 {
		t.Fatal("renaming/re-enabling resent a claimed candle")
	}
	info := l.Info()
	if info.Sent != 2 || len(info.Checks) != 2 || info.Checks[0].SentAlerts != 1 || info.Checks[1].SentAlerts != 1 {
		t.Fatalf("independent check counters collapsed: %+v", info.Checks)
	}
	if len(info.Recent) != 2 || info.Recent[0].SavedConditionID != saved.ID {
		t.Fatal("delivery history lost original rule attribution")
	}
}

func TestNamedLiveAlertClaimFencesDefinitionAndOriginalRuleChanges(t *testing.T) {
	m, l, saved, _ := namedPublicLiveFixture(t)
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Original entry", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true}
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	conditions, err := l.selectedConditions(l.state.Config)
	if err != nil {
		t.Fatal(err)
	}
	evaluated := conditions[0]
	for _, change := range []func(*LiveAlertDefinition){func(e *LiveAlertDefinition) { e.Name = "renamed" }, func(e *LiveAlertDefinition) { e.Period = "d1" }, func(e *LiveAlertDefinition) { e.DataSource = "bybit" }, func(e *LiveAlertDefinition) { e.Enabled = false }} {
		config.Alerts[0] = entry
		change(&config.Alerts[0])
		if _, err = l.Configure(config); err != nil {
			t.Fatal(err)
		}
		claimed, err := l.claimCurrentPublicCondition(context.Background(), evaluated, "binance", livePublicClaimKey(entry.ID, "binance", publicAlertSymbol("spot", "BTCUSDT"), "h1"), 1793876400000, nil)
		if err != nil || claimed {
			t.Fatalf("obsolete edited entry claimed a candle: %v %v", claimed, err)
		}
	}
	config.Alerts[0] = entry
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	request := saved.Request
	request.Conditions[0].Right.Value = 999
	if _, err = m.UpdateAnalysisCondition(saved.ID, request); err != nil {
		t.Fatal(err)
	}
	claimed, err := l.claimCurrentPublicCondition(context.Background(), evaluated, "binance", livePublicClaimKey(entry.ID, "binance", publicAlertSymbol("spot", "BTCUSDT"), "h1"), 1793876400000, nil)
	if err != nil || claimed || len(l.state.Watermarks) != 0 {
		t.Fatalf("obsolete original saved rule claimed a candle: %v %v", claimed, err)
	}
}

func TestNamedLiveAlertsMigrateV5EnabledAndPausedHistoryWithoutRewrite(t *testing.T) {
	m, l, saved, _ := namedPublicLiveFixture(t)
	second, err := m.SaveAnalysisCondition("Previously paused", saved.Request)
	if err != nil {
		t.Fatal(err)
	}
	state := liveAlertsState{Version: 5, Config: LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}, ConditionSources: []LiveAlertConditionSource{{ConditionID: saved.ID, Source: "binance"}}}, Watermarks: map[string]int64{saved.ID + "/eurusd/d1": 1704067200000, second.ID + "/eurusd/d1": 1704067200000}, Sent: 1, Checks: map[string]liveAlertCheckState{second.ID: {LastChecked: 1704153600000, SentAlerts: 1, Status: "completed"}}}
	if err = writeState(l.path, state); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(l.path)
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	info := restored.Info()
	if !info.Config.Enabled || len(info.Config.Alerts) != 2 || !reflect.DeepEqual(info.Config.ConditionIDs, []string{saved.ID}) || !reflect.DeepEqual(restored.state.Watermarks, state.Watermarks) || info.Sent != 1 {
		t.Fatalf("migration lost selections/history: %+v", info)
	}
	for _, entry := range info.Config.Alerts {
		if entry.ID != entry.ConditionID {
			t.Fatal("legacy delivery identity changed")
		}
		if entry.ID == second.ID && entry.Enabled {
			t.Fatal("paused legacy condition was activated")
		}
	}
	after, _ := os.ReadFile(l.path)
	if string(before) != string(after) {
		t.Fatal("loading legacy definitions rewrote disk")
	}
	info.Config.Enabled = false
	if _, err = restored.Configure(info.Config); err != nil {
		t.Fatal(err)
	}
	var persisted liveAlertsState
	if err = readState(l.path, liveAlertsStateBytes, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Version != 7 || len(persisted.Config.Alerts) != 2 || len(persisted.Watermarks) != 2 {
		t.Fatal("version7 save lost named rows or claims")
	}
}

func TestNamedLiveAlertsDeletingOneEntryPreservesPeerAndDeliveryTotal(t *testing.T) {
	_, l, saved, _ := namedPublicLiveFixture(t)
	first := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "First", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true}
	second := first
	second.ID, second.Name = strings.Repeat("b", 32), "Second"
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{first, second}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	l.state.Sent = 2
	l.state.Checks = map[string]liveAlertCheckState{first.ID: {LastChecked: 1704067200000, SentAlerts: 1, Status: "completed"}, second.ID: {LastChecked: 1704067200000, SentAlerts: 1, Status: "completed"}}
	l.state.Watermarks = map[string]int64{first.ID + "/eurusd/h1": 1704067200000, second.ID + "/eurusd/h1": 1704067200000}
	config.Alerts = config.Alerts[1:]
	info, err := l.Configure(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Config.Alerts) != 1 || len(info.Checks) != 1 || info.Checks[0].ConditionID != second.ID || len(l.state.Watermarks) != 1 || info.Sent != 2 {
		t.Fatalf("removing one entry lost peer/global history: %+v", info)
	}
}

var _ exchange.Adapter = (*publicAlertFakeAdapter)(nil)

type namedLiveBlockingAdapter struct {
	exchange.Adapter
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *namedLiveBlockingAdapter) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	a.once.Do(func() { close(a.entered) })
	<-a.release
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return a.Adapter.History(ctx, key, from, to, limit)
}

func TestNamedLiveAlertCycleEditDoesNotRestoreObsoleteRetry(t *testing.T) {
	for _, masterStop := range []bool{false, true} {
		t.Run(map[bool]string{false: "pause all rows", true: "stop master"}[masterStop], func(t *testing.T) {
			_, l, saved, fake := namedPublicLiveFixture(t)
			blocking := &namedLiveBlockingAdapter{Adapter: fake, entered: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(blocking.release) }) })
			l.publicData.adapters["binance"] = blocking
			entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Running row", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true}
			config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
			if _, err := l.Configure(config); err != nil {
				t.Fatal(err)
			}
			l.Start(context.Background())
			if _, err := l.CheckNow(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-blocking.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("check never reached controlled historical transport")
			}
			if masterStop {
				config.Enabled = false
			} else {
				config.Alerts[0].Enabled = false
			}
			info, err := l.Configure(config)
			if err != nil {
				t.Fatal(err)
			}
			if info.NextRun != 0 {
				t.Fatal("edit retained old scheduled work")
			}
			release.Do(func() { close(blocking.release) })
			deadline := time.Now().Add(3 * time.Second)
			for l.Info().Running && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			info = l.Info()
			if info.Running || info.NextRun != 0 || info.Error != "" || l.retryConfig != nil {
				t.Fatalf("old cycle overwrote new pause/Stop state: %+v", info)
			}
			want := "All alerts are disabled"
			if masterStop {
				want = "Disabled"
			}
			if info.Status != want {
				t.Fatalf("drained cycle retained obsolete status: %q want %q", info.Status, want)
			}
		})
	}
}

func TestNamedLiveAlertPausedOnlyEditsDoNotCancelActiveCheck(t *testing.T) {
	_, l, saved, fake := namedPublicLiveFixture(t)
	blocking := &namedLiveBlockingAdapter{Adapter: fake, entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(blocking.release) }) })
	l.publicData.adapters["binance"] = blocking
	first := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Active", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true}
	paused := first
	paused.ID, paused.Name, paused.Enabled = strings.Repeat("b", 32), "Paused", false
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{first, paused}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	var sends int
	l.send = func(context.Context, LiveAlert) error { sends++; return nil }
	l.Start(context.Background())
	if _, err := l.CheckNow(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocking.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("active check never reached history")
	}
	l.mu.Lock()
	boundary, revision := l.nextRun, l.configRevision
	l.mu.Unlock()
	config.Alerts[1].Name = "Paused renamed"
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	same := l.nextRun == boundary && l.configRevision == revision
	l.mu.Unlock()
	if !same {
		t.Fatal("paused-only edit changed active scheduling/fence")
	}
	release.Do(func() { close(blocking.release) })
	deadline := time.Now().Add(3 * time.Second)
	for l.Info().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	info := l.Info()
	if info.Running || info.Error != "" || sends != 1 {
		t.Fatalf("paused-row edit interrupted unaffected active check: sends=%d info=%+v", sends, info)
	}
}

func TestNamedLiveAlertMigrationRetainsUnselectedSourcePreference(t *testing.T) {
	_, l, saved, _ := namedPublicLiveFixture(t)
	migrated := migrateLiveAlertEntries(LiveAlertsConfig{ConditionIDs: []string{}, ConditionSources: []LiveAlertConditionSource{{ConditionID: saved.ID, Source: "bybit"}}}, []SavedAnalysisCondition{saved}, nil, nil)
	if len(migrated.Alerts) != 1 || migrated.Alerts[0].Enabled || migrated.Alerts[0].DataSource != "bybit" || migrated.Alerts[0].ID != saved.ID {
		t.Fatalf("inactive legacy source preference lost: %+v", migrated)
	}
	_ = l
}
