package screener

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

type missingAvailabilityHistory struct{ exchange.Adapter }

func (a missingAvailabilityHistory) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if key.Instrument == "@698" {
		return []domain.Candle{}, nil
	}
	return a.Adapter.History(ctx, key, from, to, limit)
}

func TestPublicAlertCycleReportsExistingClaimsMissingSourceAndWarmupSeparately(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	now := time.Date(2026, 11, 25, 5, 50, 0, 0, time.UTC)
	end := now.Truncate(24 * time.Hour).Unix()
	request := publicWarmupCondition("d1", "sma_3").Request
	request.Conditions[0].ID = "availability-rule"
	request.Conditions[0].Left.Field = "sma_3"
	request.Conditions[0].Right = AnalysisOperand{Kind: "number", Value: 1e9}
	saved, err := m.SaveAnalysisCondition("Availability summary", request)
	if err != nil {
		t.Fatal(err)
	}
	fake := &publicAlertFakeAdapter{price: 100}
	for _, symbol := range []string{"BTC", "FRESH", "YOUNG", "TIMEOUT", "@698"} {
		market := "perpetual"
		if symbol == "@698" {
			market = "spot"
		}
		fake.items = append(fake.items, domain.Instrument{NativeID: symbol, Market: market, Availability: "available"})
	}
	l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	t.Cleanup(l.Close)
	l.now = func() time.Time { return now }
	l.publicData.adapters["hyperliquid"] = selectiveAlertHistory{Adapter: missingAvailabilityHistory{fake}, failSymbol: "TIMEOUT", failure: &exchange.Error{Kind: "timeout", Message: "temporary per-asset timeout"}, shortSymbol: "YOUNG", listedAt: end - 86400}
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Daily availability", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true}
	if _, err := l.Configure(LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}); err != nil {
		t.Fatal(err)
	}
	knownKey := livePublicClaimKey(entry.ID, "hyperliquid", publicAlertSymbol("perpetual", "BTC"), "d1")
	l.state.Watermarks[knownKey] = (end - 86400) * 1000
	if err := l.check(context.Background(), l.state.Config, now.UnixMilli()); err == nil || !errors.Is(err, errLiveAlertsPendingBar) {
		t.Fatal("missing source and timeout errors were hidden", err)
	}
	row := l.Info().Checks[0]
	if row.AssetsAlreadyProcessed != 1 || row.AssetsMissingCandle != 1 || row.AssetsSkipped != 2 || row.AssetsWarmingUp != 1 || row.AssetsScanned != 1 || row.SentAlerts != 0 || row.Error == "" {
		t.Fatal("actual source availability was misreported", row)
	}
	if len(l.state.Watermarks) != 2 || l.state.Watermarks[knownKey] != (end-86400)*1000 || l.Info().Sent != 0 || len(l.Info().Recent) != 0 {
		t.Fatal("availability summary changed prior claims or claimed missing/warming/failed data", l.state.Watermarks)
	}
}

func TestLiveAlertAvailabilityCountsAreDistinctPersistedAndResetPerAttempt(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	run, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, period := range []string{"d1", "h1", "d1"} {
		run.expectPair(id, "BTC", period)
		run.alreadyProcessedPair(id, "BTC", period)
	}
	run.expectPair(id, "ETH", "d1")
	run.alreadyProcessedPair(id, "ETH", "d1")
	run.expectPair(id, "FRESH", "d1")
	run.scanned(id, "FRESH")
	run.processedPair(id, "FRESH", "d1")
	missing := publicAlertMissingCandleError(time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC).Unix(), 86400)
	run.skipped(id, "@698", missing)
	run.skipped(id, "@698", missing)
	run.skipped(id, "@300", fmt.Errorf("source read: %w", missing))
	run.skipped(id, "TIMEOUT", &exchange.Error{Kind: "timeout", Message: "provider timed out"})
	run.skipped(id, "PROVISIONAL", fmt.Errorf("%w; latest public candle is provisional", errLiveAlertsPendingBar))
	run.skipped(id, "MIXED", errors.Join(missing, &exchange.Error{Kind: "timeout", Message: "companion timed out"}))
	run.warmingUp(id, "YOUNG", "d1")
	assertCounts := func(row LiveAlertCheck) {
		t.Helper()
		if row.AssetsAlreadyProcessed != 2 || row.AssetsMissingCandle != 2 || row.AssetsSkipped != 5 || row.AssetsScanned != 1 || row.AssetsWarmingUp != 1 || row.SentAlerts != 0 {
			t.Fatalf("wrong unique-asset availability counts or delivery mutation: %+v", row)
		}
	}
	assertCounts(l.Info().Checks[0])
	if err := run.finish(finishedLiveAlertScan(run.failures[id])); err != nil {
		t.Fatal(err)
	}
	row := l.Info().Checks[0]
	assertCounts(row)
	if row.Status != "completed" || row.Error == "" || len(l.state.Watermarks) != 0 || l.state.Sent != 0 {
		t.Fatal("availability accounting changed skip errors or manufactured source claims", row)
	}
	l.mu.Lock()
	compact := l.stopInfoLocked()
	l.mu.Unlock()
	assertCounts(compact.Checks[0])
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	assertCounts(restored.Info().Checks[0])
	if _, err := l.beginCheckHistory(config, false); err != nil {
		t.Fatal(err)
	}
	run.alreadyProcessedPair(id, "LATE", "d1")
	run.skipped(id, "LATE", missing)
	state := l.state.Checks[id]
	if state.AssetsAlreadyProcessed != 0 || state.AssetsMissingCandle != 0 || state.AssetsScanned != 0 || state.AssetsSkipped != 0 || state.AssetsWarmingUp != 0 || state.Error != "" {
		t.Fatal("new attempt retained previous counts or accepted late completion", state)
	}
}

func TestLiveAlertAvailabilityCountsRejectObsoleteRunningAttemptAndInvalidState(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	old, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := l.beginCheckHistory(config, true)
	if err != nil {
		t.Fatal(err)
	}
	missing := publicAlertMissingCandleError(1795564800, 86400)
	old.alreadyProcessedPair(id, "OLD", "d1")
	old.skipped(id, "OLD", missing)
	if err := old.finish(finishedLiveAlertScan(missing)); err != nil {
		t.Fatal(err)
	}
	if row := l.Info().Checks[0]; row.Status != "running" || row.AssetsAlreadyProcessed != 0 || row.AssetsMissingCandle != 0 || row.AssetsSkipped != 0 {
		t.Fatal("obsolete worker modified newer attempt availability", row)
	}
	fresh.alreadyProcessedPair(id, "NEW", "d1")
	if err := fresh.finish(nil); err != nil {
		t.Fatal(err)
	}
	valid := l.state.Checks[id]
	for _, invalid := range []struct{ already, missing, skipped int }{{-1, 0, 0}, {20001, 0, 0}, {0, -1, 0}, {0, 1, 0}, {0, 20001, 20001}} {
		state := valid
		state.AssetsAlreadyProcessed, state.AssetsMissingCandle, state.AssetsSkipped = invalid.already, invalid.missing, invalid.skipped
		if validateLiveCheckStates(map[string]liveAlertCheckState{id: state}, l.state.Sent) == nil {
			t.Fatal("invalid or non-subset availability counters were accepted", state)
		}
	}
	legacy := liveAlertCheckState{LastChecked: 1, AssetsSkipped: 3, Status: "completed"}
	if err := validateLiveCheckStates(map[string]liveAlertCheckState{id: legacy}, 0); err != nil {
		t.Fatal("legacy state without optional availability counters was rejected", err)
	}
}
