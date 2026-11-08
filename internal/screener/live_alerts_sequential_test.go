package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type sequentialAlertAudit struct {
	mu     sync.Mutex
	events []string
}

func (a *sequentialAlertAudit) add(value string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, value)
}
func (a *sequentialAlertAudit) snapshot() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.events...)
}

type sequentialAlertAdapter struct {
	base           *publicAlertFakeAdapter
	source         string
	audit          *sequentialAlertAudit
	firstHistory   sync.Once
	historyStarted chan struct{}
	historyGate    <-chan struct{}
}

func (a *sequentialAlertAdapter) Instruments(ctx context.Context) ([]domain.Instrument, error) {
	a.audit.add("catalog:" + a.source)
	return a.base.Instruments(ctx)
}
func (a *sequentialAlertAdapter) Stream(ctx context.Context, key domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
	return a.base.Stream(ctx, key, emit)
}
func (a *sequentialAlertAdapter) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	var stopped error
	a.firstHistory.Do(func() {
		if a.historyStarted != nil {
			close(a.historyStarted)
			select {
			case <-a.historyGate:
			case <-ctx.Done():
				stopped = ctx.Err()
			}
		}
	})
	if stopped != nil {
		return nil, stopped
	}
	a.audit.add("history:" + a.source)
	return a.base.History(ctx, key, from, to, limit)
}

func sequentialAlertFixture(t *testing.T) (*LiveAlerts, LiveAlertsConfig, *sequentialAlertAudit, chan struct{}, <-chan struct{}) {
	t.Helper()
	_, l, saved, _ := namedPublicLiveFixture(t)
	// Deliberately differ from ID sorting and the old fixed venue order; the
	// same Bybit source and same saved rule appear on two independent rows.
	entries := []LiveAlertDefinition{
		{ID: strings.Repeat("c", 32), Name: "Top Bybit", ConditionID: saved.ID, Period: "h1", DataSource: "bybit", Enabled: true},
		{ID: strings.Repeat("a", 32), Name: "Middle Binance", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true},
		{ID: strings.Repeat("b", 32), Name: "Bottom Bybit", ConditionID: saved.ID, Period: "h1", DataSource: "bybit", Enabled: true},
		{ID: strings.Repeat("d", 32), Name: "Paused Hyperliquid", ConditionID: saved.ID, Period: "h1", DataSource: "hyperliquid", Enabled: false},
	}
	now := time.Date(2026, 11, 6, 12, 0, 5, 0, time.UTC)
	l.now = func() time.Time { return now }
	config := LiveAlertsConfig{Enabled: true, Alerts: entries}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	audit := &sequentialAlertAudit{}
	gate, started := make(chan struct{}), make(chan struct{})
	for _, source := range []string{"bybit", "binance", "hyperliquid"} {
		adapter := &sequentialAlertAdapter{base: &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTCUSDT", Market: "spot", Availability: "available"}}}, source: source, audit: audit}
		if source == "bybit" {
			adapter.historyGate, adapter.historyStarted = gate, started
		}
		l.publicData.adapters[source] = adapter
	}
	l.mu.Lock()
	l.state.Sent = 12
	l.state.Checks = map[string]liveAlertCheckState{
		entries[1].ID: {AssetsScanned: 7, LastChecked: now.Add(-2 * time.Hour).UnixMilli(), SentAlerts: 5, Status: "failed", Manual: true, Error: "Previous attempt"},
		entries[2].ID: {AssetsScanned: 8, LastChecked: now.Add(-time.Hour).UnixMilli(), SentAlerts: 7, Status: "completed", Manual: false},
	}
	l.mu.Unlock()
	return l, config, audit, gate, started
}

func sequentialCheckRow(t *testing.T, l *LiveAlerts, id string) LiveAlertCheck {
	t.Helper()
	for _, row := range l.Info().Checks {
		if row.ConditionID == id {
			return row
		}
	}
	t.Fatalf("missing check row %s", id)
	return LiveAlertCheck{}
}

func TestLiveAlertParallelRowsOverlapForManualAndScheduledChecks(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		name := "manual"
		if scheduled {
			name = "scheduled"
		}
		t.Run(name, func(t *testing.T) {
			l, config, audit, gate, started := sequentialAlertFixture(t)
			if scheduled {
				resolved, err := l.resolveConfig(config)
				if err != nil {
					t.Fatal(err)
				}
				config = dueLiveAlertsConfig(resolved, l.now().Truncate(time.Hour), l.now())
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			independentSend := make(chan struct{})
			l.send = func(_ context.Context, alert LiveAlert) error {
				audit.add("send:" + alert.AlertID)
				if alert.AlertID == config.Alerts[1].ID {
					close(independentSend)
				}
				return nil
			}
			done := make(chan error, 1)
			go func() { done <- l.checkCycle(ctx, config, l.now().UnixMilli(), scheduled) }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("Bybit never reached gated history")
			}
			// A slow first provider cannot hold the independent condition behind it.
			select {
			case <-independentSend:
			case <-time.After(5 * time.Second):
				t.Fatal("independent Binance condition did not finish while Bybit was blocked")
			}
			for _, entry := range []LiveAlertDefinition{config.Alerts[0], config.Alerts[2]} {
				row := sequentialCheckRow(t, l, entry.ID)
				if row.ProgressState != "running" || row.ProgressMode != name || row.Phase == "Sending alert" {
					t.Fatalf("peer progress was overwritten: %+v", row)
				}
			}
			close(gate)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("parallel checks did not drain")
			}
			for _, entry := range config.Alerts[:3] {
				row := sequentialCheckRow(t, l, entry.ID)
				if row.ProgressState != "completed" || row.Progress != 100 || row.Status != "completed" || row.Manual == scheduled {
					t.Fatalf("finished row progress/history incorrect %+v", row)
				}
			}
			if paused := sequentialCheckRow(t, l, config.Alerts[3].ID); paused.Status != "never" || paused.ProgressState == "queued" || paused.ProgressState == "running" {
				t.Fatalf("disabled row was attempted %+v", paused)
			}
			for _, outcome := range l.Info().Recent {
				if outcome.Status != "sent" {
					t.Fatalf("concurrent delivery result attributed to wrong claim: %+v", outcome)
				}
			}
			// Durable identities, rather than execution order, suppress repeats.
			l.send = func(context.Context, LiveAlert) error { t.Error("repeated cutoff resent a claim"); return nil }
			if err := l.checkCycle(context.Background(), config, l.now().UnixMilli(), scheduled); err != nil {
				t.Fatal(err)
			}
			if l.Info().Sent != 15 {
				t.Fatalf("unexpected total after repeat: %d", l.Info().Sent)
			}
		})
	}
}

func TestLiveAlertParallelCancellationPreservesQueuedHistoryAndClaims(t *testing.T) {
	l, config, audit, gate, started := sequentialAlertFixture(t)
	// All four workers wait on one provider; the fifth condition stays queued.
	config.Alerts = config.Alerts[:3]
	for i := range config.Alerts {
		config.Alerts[i].DataSource = "bybit"
	}
	for _, id := range []string{"e", "f"} {
		entry := config.Alerts[0]
		entry.ID = strings.Repeat(id, 32)
		entry.Name = id
		config.Alerts = append(config.Alerts, entry)
	}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	beforeChecks := copyLiveCheckStates(l.state.Checks)
	beforeClaims := copyWatermarksForDeliveryTest(l.state.Watermarks)
	l.send = func(context.Context, LiveAlert) error { t.Error("cancelled history delivered an alert"); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- l.checkCycle(ctx, config, l.now().UnixMilli(), false) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("history did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation hidden")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled checks did not drain")
	}
	last := config.Alerts[4]
	if got := l.state.Checks[last.ID]; got != beforeChecks[last.ID] {
		t.Fatalf("queued history changed: %+v", got)
	}
	for _, entry := range config.Alerts {
		row := sequentialCheckRow(t, l, entry.ID)
		if row.ProgressState == "running" || row.ProgressState == "queued" {
			t.Fatalf("cancelled row still active %+v", row)
		}
	}
	if !reflect.DeepEqual(l.state.Watermarks, beforeClaims) || l.state.Sent != 12 || !l.state.Config.Enabled {
		t.Fatal("cancellation changed claims or enabled state")
	}
	close(gate)
	l.send = func(_ context.Context, alert LiveAlert) error { audit.add("send:" + alert.AlertID); return nil }
	for i := 0; i < 2; i++ {
		if err := l.checkCycle(context.Background(), config, l.now().UnixMilli(), false); err != nil {
			t.Fatal(err)
		}
	}
	counts := map[string]int{}
	for _, event := range audit.snapshot() {
		if strings.HasPrefix(event, "send:") {
			counts[strings.TrimPrefix(event, "send:")]++
		}
	}
	for _, entry := range config.Alerts {
		if counts[entry.ID] != 1 {
			t.Fatalf("resume skipped or repeated %s: %v", entry.ID, counts)
		}
	}
}

func TestLiveAlertSequentialCheckNowAcknowledgesQueueWithoutManagerReads(t *testing.T) {
	l, config, audit, _, _ := sequentialAlertFixture(t)
	beforeChecks := copyLiveCheckStates(l.state.Checks)
	beforeClaims := copyWatermarksForDeliveryTest(l.state.Watermarks)
	l.manager.mu.Lock()
	released := false
	defer func() {
		if !released {
			l.manager.mu.Unlock()
		}
	}()
	type result struct {
		info LiveAlertsInfo
		err  error
	}
	done := make(chan result, 1)
	go func() { info, err := l.CheckNow(); done <- result{info, err} }()
	select {
	case response := <-done:
		if response.err != nil {
			t.Fatal(response.err)
		}
		for _, entry := range config.Alerts[:3] {
			found := false
			for _, row := range response.info.Checks {
				if row.ConditionID == entry.ID {
					found = true
					if row.ProgressState != "queued" || row.ProgressMode != "manual" || row.Progress != 0 {
						t.Fatalf("queued response lacks row progress %+v", row)
					}
				}
			}
			if !found {
				t.Fatalf("queued response omitted %s", entry.Name)
			}
		}
	case <-time.After(time.Second):
		l.manager.mu.Unlock()
		released = true
		t.Fatal("Check now waited for Manager metadata before returning its queue acknowledgment")
	}
	go func() { info, err := l.CheckNow(); done <- result{info, err} }()
	select {
	case response := <-done:
		if response.err == nil {
			t.Fatal("duplicate pending Check now was accepted")
		}
	case <-time.After(time.Second):
		l.manager.mu.Unlock()
		released = true
		t.Fatal("duplicate Check now error waited on Manager metadata")
	}
	config.Enabled = false
	response, err := l.Configure(config)
	if err != nil || response.Config.Enabled {
		t.Fatal("Stop could not cancel a queued manual check", err)
	}
	for _, row := range response.Checks {
		if row.ProgressState == "queued" || row.ProgressState == "running" {
			t.Fatalf("Stop retained active row progress %+v", row)
		}
	}
	l.manager.mu.Unlock()
	released = true
	if !reflect.DeepEqual(l.state.Checks, beforeChecks) || !reflect.DeepEqual(l.state.Watermarks, beforeClaims) || l.state.Sent != 12 {
		t.Fatal("queue acknowledgment or Stop changed actual attempt history or delivery claims")
	}
	if len(audit.snapshot()) != 0 {
		t.Fatal("Check now acknowledgment performed provider work without a worker")
	}
}

func TestLiveAlertSequentialAllDukascopyRowsFinishBeforeTheNextRefresh(t *testing.T) {
	_, l, legacy, _ := liveAlertsFixture(t)
	entries := []LiveAlertDefinition{
		{ID: strings.Repeat("c", 32), Name: "Top local row", ConditionID: legacy.ConditionIDs[0], Period: "h1", DataSource: "dukascopy", Enabled: true},
		{ID: strings.Repeat("a", 32), Name: "Second local row", ConditionID: legacy.ConditionIDs[0], Period: "h1", DataSource: "dukascopy", Enabled: true},
	}
	config := LiveAlertsConfig{Enabled: true, Alerts: entries}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	audit := &sequentialAlertAudit{}
	refreshes := 0
	l.refresh = func(ctx context.Context, _ string, groups []historyGroup, _ int64) (map[string]bool, error) {
		refreshes++
		if refreshes == 2 {
			l.mu.Lock()
			first := l.state.Checks[entries[0].ID]
			l.mu.Unlock()
			if first.Status != "completed" || first.SentAlerts != 1 {
				t.Errorf("second row refreshed before first attempt was durably finished: %+v", first)
			}
		}
		audit.add("refresh")
		refreshed := map[string]bool{}
		for _, group := range groups {
			match := downloadFilename.FindStringSubmatch(group.name)
			refreshed[match[1]+"/"+match[2]] = true
		}
		return refreshed, ctx.Err()
	}
	l.send = func(_ context.Context, alert LiveAlert) error { audit.add("send:" + alert.AlertID); return nil }
	if err := l.checkCycle(context.Background(), config, now.UnixMilli(), false); err != nil {
		t.Fatal(err)
	}
	want := []string{"refresh", "send:" + entries[0].ID, "refresh", "send:" + entries[1].ID}
	if got := audit.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("all-Dukascopy check still batched across alert rows: %v want%v", got, want)
	}
	for _, entry := range entries {
		row := sequentialCheckRow(t, l, entry.ID)
		if row.ProgressState != "completed" || row.Progress != 100 || row.SentAlerts != 1 {
			t.Fatalf("local completed row progress or identity lost %+v", row)
		}
	}
}

func TestLiveAlertSequentialPausedRowEditDoesNotCancelUnchangedActiveProgress(t *testing.T) {
	l, config, _, gate, started := sequentialAlertFixture(t)
	l.send = func(context.Context, LiveAlert) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- l.checkCycle(ctx, config, l.now().UnixMilli(), false) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first row did not begin")
	}
	edited := copyLiveAlertsConfig(config)
	edited.Alerts[3].Name = "Renamed paused row"
	edited.Alerts[3].Period = "w1"
	if _, err := l.Configure(edited); err != nil {
		t.Fatal(err)
	}
	close(gate)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unchanged active rows did not finish")
	}
	for _, entry := range edited.Alerts[:3] {
		row := sequentialCheckRow(t, l, entry.ID)
		if row.ProgressState != "completed" || row.Progress != 100 || row.Status != "completed" {
			t.Fatalf("unrelated paused edit cancelled successful active progress %+v", row)
		}
	}
	if l.Info().Sent != 15 {
		t.Fatal("paused edit changed active deliveries")
	}
}

var _ exchange.Adapter = (*sequentialAlertAdapter)(nil)
