package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLiveAlertRowProgressKeepsPendingPublicationRetryAndThenCompletes(t *testing.T) {
	_, l, saved, fake := namedPublicLiveFixture(t)
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Awaiting close", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true}
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 12, 0, 5, 0, time.UTC)
	l.now = func() time.Time { return now }
	fake.pending = true
	var sent int
	l.send = func(context.Context, LiveAlert) error { sent++; return nil }
	err := l.check(context.Background(), config, now.UnixMilli())
	if !errors.Is(err, errLiveAlertsPendingBar) {
		t.Fatalf("missing closed candle was not retryable: %v", err)
	}
	row := sequentialCheckRow(t, l, entry.ID)
	if row.ProgressState != "completed" || row.Progress != 100 || row.Phase != "Completed with errors" || row.Status != "completed" || row.AssetsSkipped != 1 || row.Error == "" || row.LastChecked == 0 || row.ProgressMode != "manual" || sent != 0 {
		t.Fatalf("actual pending attempt was overwritten as unstarted: %+v sent=%d", row, sent)
	}
	fake.pending = false
	now = now.Add(30 * time.Second)
	if err = l.check(context.Background(), config, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	row = sequentialCheckRow(t, l, entry.ID)
	if row.ProgressState != "completed" || row.Progress != 100 || row.Status != "completed" || sent != 1 {
		t.Fatalf("publication retry did not complete once: %+v sent=%d", row, sent)
	}
}

func TestLiveAlertRowProgressRefusalStillAttemptsFollowingRows(t *testing.T) {
	_, l, saved, binance := namedPublicLiveFixture(t)
	binance.fail = &exchange.Error{Kind: "restricted", Message: "synthetic regional restriction"}
	bybit := &publicAlertFakeAdapter{price: 500, items: []domain.Instrument{{NativeID: "BTCUSDT", Market: "spot", Availability: "Trading"}}}
	l.publicData.adapters["bybit"] = bybit
	entries := []LiveAlertDefinition{
		{ID: strings.Repeat("a", 32), Name: "Refused", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true},
		{ID: strings.Repeat("b", 32), Name: "Independent alert on same provider", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true},
		{ID: strings.Repeat("c", 32), Name: "Independent source", ConditionID: saved.ID, Period: "h1", DataSource: "bybit", Enabled: true},
	}
	config := LiveAlertsConfig{Enabled: true, Alerts: entries}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	prior := liveAlertCheckState{LastChecked: 1704067200000, SentAlerts: 2, AssetsScanned: 7, Status: "completed", Manual: true}
	l.state.Checks = map[string]liveAlertCheckState{entries[1].ID: prior}
	l.state.Sent = 2
	var sent int
	l.send = func(context.Context, LiveAlert) error { sent++; return nil }
	err := l.check(context.Background(), config, time.Date(2026, 11, 6, 12, 0, 5, 0, time.UTC).UnixMilli())
	if !publicAlertProviderRefused(err) {
		t.Fatalf("provider refusal lost: %v", err)
	}
	if len(binance.historyKeys) != 2 || len(bybit.historyKeys) != 1 || sent != 1 {
		t.Fatalf("refusal repeated or independent venue was skipped: Binance=%d Bybit=%d sends=%d", len(binance.historyKeys), len(bybit.historyKeys), sent)
	}
	if next := l.state.Checks[entries[1].ID]; next.LastChecked == prior.LastChecked || next.SentAlerts != prior.SentAlerts || next.Status != "completed" || next.Error == "" {
		t.Fatal("following alert did not run or lost delivery history", next)
	}
	refused := sequentialCheckRow(t, l, entries[0].ID)
	skipped := sequentialCheckRow(t, l, entries[1].ID)
	completed := sequentialCheckRow(t, l, entries[2].ID)
	if refused.ProgressState != "completed" || refused.Progress != 100 || refused.AssetsSkipped != 1 || skipped.ProgressState != "completed" || skipped.Progress != 100 || skipped.AssetsSkipped != 1 || completed.ProgressState != "completed" || completed.Progress != 100 {
		t.Fatalf("failure/skip/completion percentages were invented: %+v %+v %+v", refused, skipped, completed)
	}
}

func TestLiveAlertRowProgressIsTransientAndLegacyCompletedDoesNotInventZeroPercent(t *testing.T) {
	_, l, saved, _ := namedPublicLiveFixture(t)
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Previous completed", ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true}
	if _, err := l.Configure(LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}); err != nil {
		t.Fatal(err)
	}
	l.state.Checks = map[string]liveAlertCheckState{entry.ID: {LastChecked: 1704067200000, Status: "completed"}}
	row := sequentialCheckRow(t, l, entry.ID)
	data, _ := json.Marshal(row)
	if strings.Contains(string(data), `"progress"`) || row.ProgressState != "" {
		t.Fatalf("restored history invented zero-percent progress: %s", data)
	}
	l.mu.Lock()
	l.queueCheckProgressLocked([]string{entry.ID}, true)
	l.mu.Unlock()
	data, _ = json.Marshal(l.state)
	if strings.Contains(string(data), "progressState") || strings.Contains(string(data), "Queued") {
		t.Fatalf("transient queue leaked into persisted state: %s", data)
	}
	joined := errors.Join(&exchange.Error{Kind: "unknownInstrument", Message: "synthetic old symbol"}, fmt.Errorf("wrapped refusal: %w", &exchange.Error{Kind: "rateLimited", Message: "synthetic cooldown"}))
	if !publicAlertProviderRefused(joined) {
		t.Fatal("a later typed refusal inside joined asset failures was hidden by the first symbol error")
	}
}

func TestLiveAlertRowProgressDukascopyFailureStillAttemptsLaterRow(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	second, err := saveLiveCondition(t, m, "Later Dukascopy row", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = append(config.ConditionIDs, second.ID)
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	prior := liveAlertCheckState{LastChecked: 1704067200000, SentAlerts: 1, AssetsScanned: 3, Status: "completed", Manual: false}
	l.state.Checks = map[string]liveAlertCheckState{second.ID: prior}
	l.state.Sent = 1
	calls := 0
	refusal := errors.New("synthetic Dukascopy worker HTTP 403 access-denial")
	l.refresh = func(context.Context, string, []historyGroup, int64) (map[string]bool, error) {
		calls++
		return nil, refusal
	}
	if err = l.check(context.Background(), config, time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()); !errors.Is(err, refusal) {
		t.Fatalf("Dukascopy refusal hidden: %v", err)
	}
	if next := l.state.Checks[second.ID]; calls != 2 || next.LastChecked == prior.LastChecked || next.SentAlerts != prior.SentAlerts || next.Error == "" {
		t.Fatalf("later Dukascopy row refreshed or replaced history after refusal: calls=%d state=%+v", calls, l.state.Checks[second.ID])
	}
	row := sequentialCheckRow(t, l, second.ID)
	if row.ProgressState != "completed" || row.Progress != 100 || row.Phase != "Completed with errors" || row.AssetsSkipped != 1 {
		t.Fatalf("Dukascopy cooldown was not shown on unattempted row: %+v", row)
	}
}

func TestLiveAlertRowProgressSkipsQueuedCalendarChangeWithoutInventingCompletion(t *testing.T) {
	l, config, _, gate, started := sequentialAlertFixture(t)
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
	prior := copyLiveCheckStates(l.state.Checks)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(gate) }) })
	finished := make(chan error, 1)
	go func() { finished <- l.checkCycle(context.Background(), config, l.now().UnixMilli(), true) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first row did not reach history")
	}
	saved, err := l.manager.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	request := saved[0].Request
	request.Conditions = append(request.Conditions, AnalysisCondition{ID: "new weekday", Operator: "equal", Left: AnalysisOperand{Kind: "field", Field: "weekday"}, Right: AnalysisOperand{Kind: "number", Value: 1}})
	if _, err = l.manager.UpdateAnalysisCondition(saved[0].ID, request); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(gate) })
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("calendar-change check did not finish")
	}
	for _, entry := range config.Alerts[4:] {
		row := sequentialCheckRow(t, l, entry.ID)
		if row.ProgressState != "skipped" || row.Progress != 0 || row.Phase != "No eligible closed bar" || !reflect.DeepEqual(l.state.Checks[entry.ID], prior[entry.ID]) {
			t.Fatalf("ineligible queued row invented a completed check: %+v", row)
		}
	}
}

func TestLiveAlertConditionRenameClearsUnstartedManualQueue(t *testing.T) {
	m, l, saved, adapter := namedPublicLiveFixture(t)
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: saved.Name, ConditionID: saved.ID, Period: "h1", DataSource: "binance", Enabled: true}
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	queued, err := l.CheckNow()
	if err != nil || len(queued.Checks) != 1 || queued.Checks[0].ProgressState != "queued" {
		t.Fatalf("manual check was not queued: %+v %v", queued, err)
	}
	renamed, err := m.RenameAnalysisCondition(saved.ID, "Renamed queued condition")
	if err != nil {
		t.Fatal(err)
	}
	config.Alerts[0].Name = renamed.Name
	info, err := l.Configure(config)
	if err != nil {
		t.Fatal(err)
	}
	row := info.Checks[0]
	if l.requested || info.Running || row.ProgressState == "queued" || row.ProgressState == "running" {
		t.Fatalf("renaming left an unstarted manual check stuck in progress: requested=%v row=%+v", l.requested, row)
	}
	if !info.Config.Enabled || row.LastChecked != 0 || row.Status != "never" || row.SentAlerts != 0 || len(adapter.historyKeys) != 0 {
		t.Fatalf("renaming invented an attempt or changed monitoring: %+v", info)
	}
	if _, err = l.CheckNow(); err != nil {
		t.Fatalf("renamed alert could not be checked again: %v", err)
	}
}
