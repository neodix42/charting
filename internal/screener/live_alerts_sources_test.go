package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sourceTestSaved(t *testing.T, m *Manager, name string, profit int, period ...string) SavedAnalysisCondition {
	t.Helper()
	request := defaultAnalysisRequest()
	request.Period = "h1"
	if len(period) > 0 {
		request.Period = period[0]
	}
	request.ProfitabilityFilter = profit
	saved, err := m.SaveAnalysisCondition(name, request)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestLiveAlertsPublicScopesScheduleWithoutBorrowedProfitabilityOrDiscovery(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	saved := sourceTestSaved(t, m, "Public rule with foreign profitability", 95)
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTCUSDT", Market: "spot", Availability: "trading"}}}
	l := newPublicAlertTestLive(t, m, "binance", fake)
	defer l.Close()
	now := time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}, ConditionSources: []LiveAlertConditionSource{{ConditionID: saved.ID, Source: "binance"}}}
	info, err := l.Configure(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.ConditionScopes) != 1 || !info.ConditionScopes[0].AllAssets || info.ConditionScopes[0].DataSource != "binance" || len(info.ConditionScopes[0].Targets) != 0 || info.ConditionScopes[0].Error != "" {
		t.Fatalf("public scope borrowed analyzed target restrictions: %+v", info)
	}
	if info.NextRun != now.Truncate(time.Hour).Add(time.Hour).UnixMilli() || len(info.Checks) != 1 || info.Checks[0].NextCheck != info.NextRun {
		t.Fatalf("public rule without Dukascopy evidence was not scheduled %+v", info)
	}
	fake.mu.Lock()
	calls := fake.catalogCalls
	fake.mu.Unlock()
	if calls != 0 {
		t.Fatal("Configure/Info contacted public provider", calls)
	}
	info.Config.ConditionSources[0].Source = "bybit"
	if l.Info().Config.ConditionSources[0].Source != "binance" {
		t.Fatal("Info source list aliases internal settings")
	}
}

func TestLiveAlertsPublicClaimsIsolateSourcesAndSpotPerpetualAndRestart(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	saved := sourceTestSaved(t, m, "All venue markets", 95, "d1")
	items := []domain.Instrument{{NativeID: "BTCUSDT", Market: "spot", Availability: "TRADING"}, {NativeID: "BTCUSDT", Market: "perpetual", Availability: "Trading"}}
	binance, bybit := &publicAlertFakeAdapter{price: 100, items: items}, &publicAlertFakeAdapter{price: 200, items: items}
	l := newPublicAlertTestLive(t, m, "binance", binance)
	defer l.Close()
	l.publicData.adapters["bybit"] = bybit
	var alerts []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { alerts = append(alerts, alert); return nil }
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}, ConditionSources: []LiveAlertConditionSource{{ConditionID: saved.ID, Source: "binance"}}}
	asOf := time.Date(2026, 11, 6, 12, 30, 0, 0, time.UTC).UnixMilli()
	for _, source := range []string{"binance", "bybit", "binance"} {
		config.ConditionSources[0].Source = source
		if _, err := l.Configure(config); err != nil {
			t.Fatal(err)
		}
		if err := l.check(context.Background(), config, asOf); err != nil {
			t.Fatal(err)
		}
	}
	if len(alerts) != 4 || l.Info().Sent != 4 || len(l.state.Watermarks) != 4 {
		t.Fatalf("cross-source or cross-market claim collision/replay: %d alerts, %+v", len(alerts), l.Info())
	}
	combinations := map[string]bool{}
	for _, alert := range alerts {
		if alert.Symbol != "BTCUSDT" || alert.Timeframe != "d1" {
			t.Fatal("public delivery lost native instrument or inherited period", alert)
		}
		if alert.Profitability != nil {
			t.Fatal("foreign profitability attached", alert)
		}
		combinations[alert.DataSource+"/"+alert.Market] = true
	}
	if len(combinations) != 4 {
		t.Fatal("spot/perpetual catalog collapsed", combinations)
	}
	for key := range l.state.Watermarks {
		parts := strings.Split(key, "/")
		if len(parts) != 3 || !validPublicLiveAlertClaim(parts[1]) {
			t.Fatal("nonportable public claim", key)
		}
	}
	restored, err := NewLiveAlerts(m, l.send)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.publicData = l.publicData
	if err = restored.check(context.Background(), config, asOf); err != nil || len(alerts) != 4 {
		t.Fatal("restart replayed public source claims", len(alerts), err)
	}
}

func TestLiveAlertsMixedDukascopyAndPublicSelectionsEvaluateTheirOwnData(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	writeDataFile(t, m, "eurusd-h1-2026-11-05-2026-11-06.csv", reviewHourlyCSV(time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC).UnixMilli(), []float64{10, 11, 12, 13}))
	request := defaultAnalysisRequest()
	request.Period = "h1"
	dukascopy, err := saveLiveCondition(t, m, "Existing Dukascopy", request)
	if err != nil {
		t.Fatal(err)
	}
	public := sourceTestSaved(t, m, "New Binance", 90)
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTCUSDT", Market: "perpetual", Availability: "TRADING"}}}
	l := newPublicAlertTestLive(t, m, "binance", fake)
	defer l.Close()
	l.refresh = func(ctx context.Context, _ string, groups []historyGroup, _ int64) (map[string]bool, error) {
		out := map[string]bool{}
		for _, group := range groups {
			match := downloadFilename.FindStringSubmatch(group.name)
			out[match[1]+"/"+match[2]] = true
		}
		return out, ctx.Err()
	}
	var alerts []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { alerts = append(alerts, alert); return nil }
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{dukascopy.ID, public.ID}, ConditionSources: []LiveAlertConditionSource{{ConditionID: public.ID, Source: "binance"}}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	if err = l.check(context.Background(), config, time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 {
		t.Fatalf("mixed sources lost or expanded scope: %+v", alerts)
	}
	found := map[string]bool{}
	for _, alert := range alerts {
		found[alert.ConditionID+"/"+alert.Symbol] = true
	}
	if !found[dukascopy.ID+"/eurusd"] || !found[public.ID+"/BTCUSDT"] {
		t.Fatal("sources evaluated each other's data", alerts)
	}
	if len(l.Info().Checks) != 2 {
		t.Fatal("mixed-source check history lost one condition", l.Info())
	}
}

func TestLiveAlertsV4EnabledDukascopyMigrationPreservesClaimsAndBytes(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	state := l.state
	state.Version = 4
	state.Config = config
	state.Watermarks = map[string]int64{config.ConditionIDs[0] + "/eurusd/h1": 1704070800000}
	state.Sent = 3
	state.Recent = []LiveAlertOutcome{{Symbol: "eurusd", Timeframe: "h1", Time: 1704070800000, Status: "sent", Message: "Previously sent"}}
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
	if !info.Config.Enabled || len(info.Config.ConditionSources) != 0 || liveAlertSource(info.Config, config.ConditionIDs[0]) != "dukascopy" || !reflect.DeepEqual(restored.state.Watermarks, state.Watermarks) || !reflect.DeepEqual(info.Recent, state.Recent) || info.Sent != 3 {
		t.Fatalf("version4 was stopped or its claims migrated incorrectly: %+v", info)
	}
	after, _ := os.ReadFile(l.path)
	if string(before) != string(after) {
		t.Fatal("loading existing enabled settings rewrote saved state")
	}
}

func TestLiveAlertsRejectsUnknownAndDuplicateDataSourcesWithoutMutation(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	before, _ := os.ReadFile(l.path)
	for _, choices := range [][]LiveAlertConditionSource{{{ConditionID: config.ConditionIDs[0], Source: "coinbase"}}, {{ConditionID: config.ConditionIDs[0], Source: "Hyperliquid"}}, {{ConditionID: config.ConditionIDs[0], Source: "binance"}, {ConditionID: config.ConditionIDs[0], Source: "bybit"}}, {{ConditionID: "bad", Source: "bybit"}}} {
		candidate := copyLiveAlertsConfig(config)
		candidate.ConditionSources = choices
		if _, err := l.Configure(candidate); err == nil {
			t.Fatal("invalid sources accepted", choices)
		}
		after, _ := os.ReadFile(l.path)
		if string(before) != string(after) {
			t.Fatal("invalid source settings changed saved data")
		}
	}
}

func TestLiveAlertsSourceChangesFenceAlreadyEvaluatedPublicAndDukascopyClaims(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	saved, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	condition := saved[0]
	staleKey := condition.ID + "/eurusd/h1"
	config.ConditionSources = []LiveAlertConditionSource{{ConditionID: condition.ID, Source: "bybit"}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	_, err = l.claimCurrentCondition(context.Background(), condition, "eurusd", "h1", staleKey, 1704070800000, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("obsolete Dukascopy result survived source switch: %v", err)
	}
	publicKey := livePublicClaimKey(condition.ID, "binance", publicAlertSymbol("spot", "BTCUSDT"), "h1")
	_, err = l.claimCurrentPublicCondition(context.Background(), condition, "binance", publicKey, 1704070800000, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("obsolete public result survived source switch", err)
	}
	if len(l.state.Watermarks) != 0 {
		t.Fatal("source switch persisted stale claim", l.state.Watermarks)
	}
}

func TestLiveAlertsDeletedUnselectedSourceChoiceIsPruned(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	unselected := sourceTestSaved(t, m, "Unselected source choice", 0)
	config.ConditionSources = []LiveAlertConditionSource{{ConditionID: unselected.ID, Source: "hyperliquid"}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteAnalysisCondition(unselected.ID); err != nil {
		t.Fatal(err)
	}
	if info := l.Info(); len(info.Config.ConditionSources) != 0 {
		t.Fatal("deleted unselected source choice retained", info.Config.ConditionSources)
	}
	var persisted liveAlertsState
	if err := readState(filepath.Join(m.profile, "screener-live-alerts.json"), liveAlertsStateBytes, &persisted); err != nil || len(persisted.Config.ConditionSources) != 0 {
		t.Fatal("deleted override retained on disk", persisted.Config.ConditionSources, err)
	}
}

// Compile-time guard: tests replace only the public transport interface.
var _ exchange.Adapter = (*publicAlertFakeAdapter)(nil)
