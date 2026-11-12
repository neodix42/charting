package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type selectiveAlertHistory struct {
	exchange.Adapter
	failSymbol  string
	failure     error
	shortSymbol string
	listedAt    int64
}

func (a selectiveAlertHistory) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if key.Instrument == a.failSymbol {
		return nil, a.failure
	}
	rows, err := a.Adapter.History(ctx, key, from, to, limit)
	if key.Instrument == a.shortSymbol {
		filtered := rows[:0]
		for _, row := range rows {
			if row.Time >= a.listedAt {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	return rows, err
}

func TestLiveAlertsIndividualHistoryTimeoutAndShortWarmupContinueAcrossAlerts(t *testing.T) {
	for _, mode := range []string{"timeout", "warmup"} {
		t.Run(mode, func(t *testing.T) {
			m, l, saved, fake := namedPublicLiveFixture(t)
			request := saved.Request
			request.Conditions[0].Left.Field = "sma_26"
			if _, err := m.UpdateAnalysisCondition(saved.ID, request); err != nil {
				t.Fatal(err)
			}
			fake.items = []domain.Instrument{{NativeID: "SHORT", Market: "perpetual", Availability: "available"}, {NativeID: "HEALTHY", Market: "perpetual", Availability: "available"}}
			adapter := selectiveAlertHistory{Adapter: fake}
			if mode == "timeout" {
				adapter.failSymbol, adapter.failure = "SHORT", context.DeadlineExceeded
			} else {
				adapter.shortSymbol = "SHORT"
				adapter.listedAt = time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -10).Unix()
			}
			l.publicData.adapters["hyperliquid"] = adapter
			entries := []LiveAlertDefinition{
				{ID: strings.Repeat("a", 32), Name: "First", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true},
				{ID: strings.Repeat("b", 32), Name: "Next", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true},
			}
			config := LiveAlertsConfig{Enabled: true, Alerts: entries}
			if _, err := l.Configure(config); err != nil {
				t.Fatal(err)
			}
			sent := 0
			var sendsMu sync.Mutex
			l.send = func(_ context.Context, alert LiveAlert) error {
				if alert.Symbol != "HEALTHY" {
					t.Fatal("unavailable asset produced a signal", alert)
				}
				sendsMu.Lock()
				sent++
				sendsMu.Unlock()
				return nil
			}
			cutoff := time.Date(2026, 11, 6, 12, 0, 5, 0, time.UTC).UnixMilli()
			err := l.check(context.Background(), config, cutoff)
			if sent != 2 {
				t.Fatalf("unavailable asset stopped healthy peers: sent=%d err=%v", sent, err)
			}
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("timeout cause lost", err)
			}
			if mode == "warmup" && err != nil {
				t.Fatal("genuine short listing became an error", err)
			}
			for _, row := range l.Info().Checks {
				if row.Status != "completed" || row.Progress != 100 || row.SentAlerts != 1 {
					t.Fatalf("finished scan left incomplete progress: %+v", row)
				}
				if mode == "timeout" && (row.Phase != "Completed with errors" || row.AssetsSkipped != 1 || row.AssetsWarmingUp != 0 || row.Error == "") {
					t.Fatal("real timeout hidden", row)
				}
				if mode == "warmup" && (row.Phase != "Completed; waiting for more history" || row.AssetsSkipped != 0 || row.AssetsWarmingUp != 1 || row.Error != "") {
					t.Fatal("immature listing not reported as history wait", row)
				}
			}
			if mode == "warmup" {
				files, _ := filepath.Glob(filepath.Join(m.profile, "screener-public-history", "hyperliquid", "raw", publicAlertSymbol("perpetual", "SHORT")+"*.csv"))
				if len(files) != 1 {
					t.Fatal("short real history not retained for later reuse", files)
				}
			}
		})
	}
}

func TestLiveAlertsPartialTelegramFailureContinuesEveryAssetAndFollowingAlert(t *testing.T) {
	_, l, saved, fake := namedPublicLiveFixture(t)
	fake.items = append(fake.items, domain.Instrument{NativeID: "ETHUSDT", Market: "perpetual", Availability: "trading"})
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{
		{ID: strings.Repeat("a", 32), Name: "First", ConditionID: saved.ID, Period: "d1", DataSource: "binance", Enabled: true},
		{ID: strings.Repeat("b", 32), Name: "Following", ConditionID: saved.ID, Period: "d1", DataSource: "binance", Enabled: true},
	}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var sendsMu sync.Mutex
	l.send = func(_ context.Context, alert LiveAlert) error {
		sendsMu.Lock()
		defer sendsMu.Unlock()
		calls++
		if alert.AlertID == config.Alerts[0].ID && alert.Symbol == "BTCUSDT" {
			return confirmedDeliveryTestError{count: 1}
		}
		return nil
	}
	cutoff := time.Date(2026, 11, 6, 12, 0, 5, 0, time.UTC).UnixMilli()
	if err := l.check(context.Background(), config, cutoff); err == nil || calls != 4 || l.Info().Sent != 4 {
		t.Fatalf("partial delivery stopped peers or miscounted confirmed signals: %v calls=%d info=%+v", err, calls, l.Info())
	}
	rows := l.Info().Checks
	if rows[0].Status != "completed" || rows[0].Error == "" || rows[0].Progress != 100 || rows[0].SentAlerts != 2 || rows[1].Status != "completed" || rows[1].Error != "" || rows[1].SentAlerts != 2 {
		t.Fatal("following alert did not finish", rows)
	}
	if len(l.state.Watermarks) != 4 {
		t.Fatal("attempted claims lost")
	}
	if err := l.check(context.Background(), config, cutoff); err != nil || calls != 4 {
		t.Fatal("already attempted bars resent", err, calls)
	}
	// A subsequent scheduled close follows the same continuation policy.
	next := cutoff + 24*time.Hour.Milliseconds()
	l.now = func() time.Time { return time.UnixMilli(next) }
	if err := l.checkCycle(context.Background(), config, next, true); err == nil || calls != 8 || l.Info().Sent != 8 {
		t.Fatal("scheduled delivery failure stopped following assets/alerts", err, calls)
	}
	for _, row := range l.Info().Checks {
		if row.Manual || row.Progress != 100 || row.Status != "completed" {
			t.Fatal("scheduled scan did not finish", row)
		}
	}
}

func TestLiveAlertsPublicAssetSelectionPrecedesHistoryAndPreservesNativeMarket(t *testing.T) {
	_, l, saved, fake := namedPublicLiveFixture(t)
	fake.items = append(fake.items, domain.Instrument{NativeID: "BTCUSDT", Market: "perpetual", Availability: "TRADING"}, domain.Instrument{NativeID: "xyz:RELIANCE", Market: "perpetual", Availability: "available"})
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Selected", ConditionID: saved.ID, Period: "d1", DataSource: "binance", Enabled: true, AssetSelections: []LiveAlertAssetSelection{
		{DataSource: "binance", Assets: []LiveAlertAssetRef{{Market: "spot", Symbol: "BTCUSDT"}, {Market: "perpetual", Symbol: "xyz:DELISTED"}}},
		{DataSource: "hyperliquid", Assets: []LiveAlertAssetRef{{Market: "perpetual", Symbol: "xyz:RELIANCE"}}},
	}}
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
	info, err := l.Configure(config)
	if err != nil {
		t.Fatal(err)
	}
	if info.ConditionScopes[0].AllAssets || len(info.ConditionScopes[0].Targets) != 2 || len(fake.historyKeys) != 0 {
		t.Fatal("custom scope queried data or inherited all assets", info.ConditionScopes)
	}
	// Both caller and returned snapshot must be detached from persisted choices.
	config.Alerts[0].AssetSelections[0].Assets[0].Symbol = "MUTATED"
	info.Config.Alerts[0].AssetSelections[0].Assets[0].Symbol = "MUTATED AGAIN"
	config = l.Info().Config
	var alerts []LiveAlert
	l.send = func(_ context.Context, a LiveAlert) error { alerts = append(alerts, a); return nil }
	if err = l.check(context.Background(), config, time.Date(2026, 11, 6, 12, 0, 5, 0, time.UTC).UnixMilli()); err == nil {
		t.Fatal("missing selected market hidden")
	}
	if len(fake.historyKeys) != 1 || fake.historyKeys[0].Instrument != "BTCUSDT" || fake.historyKeys[0].Market != "spot" || len(alerts) != 1 {
		t.Fatal("excluded or unknown assets fetched", fake.historyKeys, alerts)
	}
	row := l.Info().Checks[0]
	if row.AssetsSkipped != 1 || row.Progress != 100 || !strings.Contains(row.Error, "xyz:DELISTED") {
		t.Fatal("missing selected asset did not finish as skipped", row)
	}
	selected, err := l.selectedConditions(config)
	if err != nil {
		t.Fatal(err)
	}
	old := selected[0]
	config.Alerts[0].AssetSelections[0].Assets = []LiveAlertAssetRef{{Market: "perpetual", Symbol: "BTCUSDT"}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	claimed, err := l.claimCurrentPublicCondition(context.Background(), old, "binance", livePublicClaimKey(entry.ID, "binance", publicAlertSymbol("spot", "BTCUSDT"), "d1"), 1794000000000, nil)
	if err != nil || claimed {
		t.Fatal("selection edit allowed an obsolete in-flight claim", claimed, err)
	}
}

func TestLiveAlertsDukascopySelectionRestrictsRefreshAndCompanionFields(t *testing.T) {
	m, l, _, path := liveAlertsFixture(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeDataFile(t, m, "btcusd-h1-2024-01-01-2024-01-03.csv", string(data))
	writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-03.csv", string(data))
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-03.csv", string(data))
	saved, err := saveLiveCondition(t, m, "Companion rule", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	request := saved.Request
	request.Period = "h1"
	request.Conditions = append(request.Conditions, AnalysisCondition{ID: "daily", Operator: "above", Left: AnalysisOperand{Kind: "field", Field: "sma_2", Timeframe: "d1"}, Right: AnalysisOperand{Kind: "number", Value: 0}})
	if _, err = m.UpdateAnalysisCondition(saved.ID, request); err != nil {
		t.Fatal(err)
	}
	// Re-record eligible evidence after the rule edit (editing correctly invalidates it).
	conditions, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	for i := range conditions {
		if conditions[i].ID == saved.ID {
			conditions[i].Analysis = &SavedConditionAnalysis{AnalyzedAt: 1704153600000, Assets: []SavedConditionProfitability{{Symbol: "eurusd", Timeframe: "h1", FileName: filepath.Base(path), Matches: 1, ProfitabilityUnavailable: true}, {Symbol: "btcusd", Timeframe: "h1", FileName: "btcusd-h1-2024-01-01-2024-01-03.csv", Matches: 1, ProfitabilityUnavailable: true}}}
		}
	}
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: conditions}); err != nil {
		t.Fatal(err)
	}
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Only EUR", ConditionID: saved.ID, Period: "h1", DataSource: "dukascopy", Enabled: true, AssetSelections: []LiveAlertAssetSelection{{DataSource: "dukascopy", Assets: []LiveAlertAssetRef{{Symbol: "eurusd"}}}}}
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
	info, err := l.Configure(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.ConditionScopes[0].AvailableTargets) != 2 || len(info.ConditionScopes[0].Targets) != 1 {
		t.Fatal("unchecked choices disappeared or entered selected scope", info.ConditionScopes)
	}
	selected, err := l.selectedConditions(l.state.Config)
	if err != nil {
		t.Fatal(err)
	}
	fields := liveAlertRequiredFields(config, selected)
	if len(fields) != 2 || !reflect.DeepEqual(fields["eurusd/d1"], []string{"sma_2"}) {
		t.Fatal("unselected assets enriched or selected companion lost", fields)
	}
	calls := 0
	l.refresh = func(_ context.Context, _ string, groups []historyGroup, _ int64) (map[string]bool, error) {
		calls++
		pairs := map[string]bool{}
		for _, g := range groups {
			match := downloadFilename.FindStringSubmatch(g.name)
			if match[1] != "eurusd" {
				t.Fatal("unselected history refreshed", g.name)
			}
			pairs[match[1]+"/"+match[2]] = true
		}
		if len(pairs) != 2 {
			t.Fatal("required companion not refreshed", pairs)
		}
		return pairs, nil
	}
	l.send = func(context.Context, LiveAlert) error { return nil }
	_ = l.check(context.Background(), config, time.Date(2024, 1, 2, 2, 30, 0, 0, time.UTC).UnixMilli())
	if calls != 1 {
		t.Fatal("selected alert did not refresh")
	}
}

func TestLiveAlertsV6DefaultsAllWithoutRewriteAndV7SelectionSurvivesRestart(t *testing.T) {
	m, l, saved, _ := namedPublicLiveFixture(t)
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Active", ConditionID: saved.ID, Period: "d1", DataSource: "binance", Enabled: true}
	state := liveAlertsState{Version: 6, Config: LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}, Watermarks: map[string]int64{livePublicClaimKey(entry.ID, "binance", publicAlertSymbol("spot", "BTCUSDT"), "d1"): 1793836800000}, Sent: 3, Checks: map[string]liveAlertCheckState{entry.ID: {Status: "completed", LastChecked: 1793923200000, SentAlerts: 3}}}
	if err := writeState(l.path, state); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(l.path)
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	info := restored.Info()
	after, _ := os.ReadFile(l.path)
	if string(before) != string(after) || !info.Config.Enabled || !info.Config.Alerts[0].Enabled || !info.ConditionScopes[0].AllAssets || !reflect.DeepEqual(restored.state.Watermarks, state.Watermarks) || info.Sent != 3 {
		t.Fatal("loading changed active legacy state", info)
	}
	info.Config.Alerts[0].AssetSelections = []LiveAlertAssetSelection{{DataSource: "binance", Assets: []LiveAlertAssetRef{{Market: "spot", Symbol: "BTCUSDT"}}}}
	if _, err = restored.Configure(info.Config); err != nil {
		t.Fatal(err)
	}
	again, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.state.Version != 7 || !reflect.DeepEqual(again.Info().Config.Alerts, info.Config.Alerts) || !reflect.DeepEqual(again.state.Watermarks, state.Watermarks) || again.Info().Checks[0].SentAlerts != 3 {
		t.Fatal("selection restart lost claims/config/history", again.Info())
	}
}

func TestLiveAlertRefreshRelaysWorkerProgressOutsideManagerLock(t *testing.T) {
	m, _, _, path := liveAlertsFixture(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	dir := t.TempDir()
	script := `const readline=require('node:readline');readline.createInterface({input:process.stdin}).once('line',()=>{for(const progress of [45,61])console.log(JSON.stringify({type:'progress',progress,message:'Downloading closed history'}));console.log(JSON.stringify({type:'error',message:'Synthetic download failure'}));process.exit(1);});`
	if err = os.WriteFile(filepath.Join(dir, "refresh-worker.cjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	m.runtime = workerRuntime{node: node, worker: filepath.Join(dir, "worker.cjs")}
	job := RefreshJob{ID: "progress", Name: filepath.Base(path), Status: "running"}
	m.refreshJobs = []RefreshJob{job}
	before, _ := os.ReadFile(path)
	var progress []float64
	err = m.refreshFileRequiredProgress(context.Background(), 0, m.directory, job, nil, 1793923200000, nil, func(value float64, message string) {
		if !m.mu.TryLock() {
			t.Fatal("progress callback holds manager lock")
		}
		m.mu.Unlock()
		if message != "Downloading closed history" {
			t.Fatal(message)
		}
		progress = append(progress, value)
	})
	after, _ := os.ReadFile(path)
	if err == nil || !reflect.DeepEqual(progress, []float64{45, 61}) || string(before) != string(after) {
		t.Fatal("refresh progress missing or failure modified history", progress, err)
	}
}

func TestLiveAlertAssetSelectionValidationRejectsAmbiguousOrUnboundedChoices(t *testing.T) {
	for _, selections := range [][]LiveAlertAssetSelection{
		{{DataSource: "binance"}},
		{{DataSource: "binance", Assets: []LiveAlertAssetRef{{Market: "spot", Symbol: "BTCUSDT"}, {Market: "spot", Symbol: "BTCUSDT"}}}},
		{{DataSource: "dukascopy", Assets: []LiveAlertAssetRef{{Market: "spot", Symbol: "eurusd"}}}},
		{{DataSource: "dukascopy", Assets: []LiveAlertAssetRef{{Symbol: "unknown"}}}},
		{{DataSource: "hyperliquid", Assets: []LiveAlertAssetRef{{Market: "perpetual", Symbol: "xyz:BAD\n"}}}},
		{{DataSource: "bybit", Assets: []LiveAlertAssetRef{{Market: "spot", Symbol: strings.Repeat("X", 513)}}}},
		{{DataSource: "binance", Assets: []LiveAlertAssetRef{{Market: "spot", Symbol: "BTCUSDT"}}}, {DataSource: "binance", Assets: []LiveAlertAssetRef{{Market: "perpetual", Symbol: "BTCUSDT"}}}},
	} {
		if _, err := validateLiveAlertAssetSelections(selections); err == nil {
			t.Fatal("invalid selection accepted", selections)
		}
	}
	valid := []LiveAlertAssetSelection{{DataSource: "hyperliquid", Assets: []LiveAlertAssetRef{{Market: "spot", Symbol: "@107"}, {Market: "perpetual", Symbol: "xyz:RELIANCE"}}}}
	if count, err := validateLiveAlertAssetSelections(valid); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestLiveAlertsUnsafeOutcomeWriteCancelsPeersAndRetainsBeforeSendClaim(t *testing.T) {
	m, l, saved, _ := namedPublicLiveFixture(t)
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{}}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		source := "bybit"
		if id == "a" || id == "e" {
			source = "binance"
		}
		config.Alerts = append(config.Alerts, LiveAlertDefinition{ID: strings.Repeat(id, 32), Name: id, ConditionID: saved.ID, Period: "d1", DataSource: source, Enabled: true})
	}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	// Peers have started but cannot deliver before the failing result. A fifth
	// condition remains queued behind the bounded worker pool.
	gate, started := make(chan struct{}), make(chan struct{})
	defer close(gate)
	l.publicData.adapters["bybit"] = &sequentialAlertAdapter{base: &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTCUSDT", Market: "spot", Availability: "available"}}}, source: "bybit", audit: &sequentialAlertAudit{}, historyGate: gate, historyStarted: started}
	path := filepath.Join(t.TempDir(), "durable-state.json")
	calls := 0
	l.send = func(context.Context, LiveAlert) error {
		calls++
		// Preserve the exact durable before-send file, then force subsequent
		// atomic renames to fail without racing a mutation of l.path.
		l.mu.Lock()
		defer l.mu.Unlock()
		if err := os.Rename(l.path, path); err != nil {
			t.Error(err)
			return err
		}
		return os.Mkdir(l.path, 0700)
	}
	err := l.check(context.Background(), config, time.Date(2026, 11, 6, 12, 0, 5, 0, time.UTC).UnixMilli())
	if !fatalLiveAlertPersistence(err) || calls != 1 {
		t.Fatal("unsafe persistence continued delivery", err, calls)
	}
	if _, started := l.state.Checks[config.Alerts[4].ID]; started {
		t.Fatal("queued condition started after unsafe persistence")
	}
	var disk liveAlertsState
	if err = readState(path, liveAlertsStateBytes, &disk); err != nil || len(disk.Watermarks) != 1 || len(disk.Recent) != 1 || disk.Recent[0].Status != "claimed" {
		t.Fatal("before-send claim was not durable", err, disk)
	}
	if err = os.Remove(l.path); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, l.path); err != nil {
		t.Fatal(err)
	}
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if !reflect.DeepEqual(restored.state.Watermarks, disk.Watermarks) || restored.Info().Recent[0].Status != "uncertain" {
		t.Fatal("restart lost the no-resend claim")
	}
}
