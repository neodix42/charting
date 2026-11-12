package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPublicAlertWarmupWaitsWithoutClaimAndBecomesReadyAtEnoughRealBars(t *testing.T) {
	m, l, saved, fake := namedPublicLiveFixture(t)
	request := saved.Request
	request.Conditions[0].Left.Field = "sma_26"
	request.Conditions[0].Left.Offset = 1
	if _, err := m.UpdateAnalysisCondition(saved.ID, request); err != nil {
		t.Fatal(err)
	}
	fake.items = []domain.Instrument{{NativeID: "YOUNG", Market: "perpetual", Availability: "available"}, {NativeID: "HEALTHY", Market: "perpetual", Availability: "available"}}
	first := time.Date(2026, 10, 1, 0, 0, 5, 0, time.UTC)
	l.publicData.adapters["hyperliquid"] = selectiveAlertHistory{Adapter: fake, shortSymbol: "YOUNG", listedAt: first.Truncate(24*time.Hour).AddDate(0, 0, -16).Unix()}
	entry := LiveAlertDefinition{ID: strings.Repeat("b", 32), Name: "Daily real history", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true}
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	var sent []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { sent = append(sent, alert); return nil }
	check := func(cutoff time.Time, warming int, wantSent int) {
		t.Helper()
		if err := l.check(context.Background(), l.state.Config, cutoff.UnixMilli()); err != nil {
			t.Fatal("young listing became a scan error", err)
		}
		row := l.Info().Checks[0]
		if row.Status != "completed" || row.Error != "" || row.AssetsSkipped != 0 || row.AssetsWarmingUp != warming || row.Progress != 100 || len(sent) != wantSent {
			t.Fatalf("wrong warm-up completion: %+v sends=%d", row, len(sent))
		}
		if warming > 0 && row.Phase != "Completed; waiting for more history" {
			t.Fatal("missing history wait phase", row)
		}
	}
	check(first, 1, 1)
	youngKey := livePublicClaimKey(entry.ID, "hyperliquid", publicAlertSymbol("perpetual", "YOUNG"), "d1")
	if _, claimed := l.state.Watermarks[youngKey]; claimed {
		t.Fatal("immature asset received a processed-bar watermark")
	}
	check(first, 1, 1) // No immediate retry loop and no healthy-asset resend.
	if _, claimed := l.state.Watermarks[youngKey]; claimed {
		t.Fatal("repeated short-history check claimed a signal")
	}
	restored := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	t.Cleanup(restored.Close)
	if row := restored.Info().Checks[0]; row.AssetsWarmingUp != 1 || row.Error != "" || row.Status != "completed" {
		t.Fatal("restart lost benign readiness state", row)
	}
	check(first.AddDate(0, 0, 11), 1, 2) // 27 real bars still cannot warm up 28.
	if _, claimed := l.state.Watermarks[youngKey]; claimed {
		t.Fatal("27 bars were promoted to a 28-bar rule")
	}
	ready := first.AddDate(0, 0, 12)
	check(ready, 0, 4)
	if l.state.Watermarks[youngKey] != ready.Truncate(24*time.Hour).Add(-24*time.Hour).UnixMilli() {
		t.Fatal("first ready candle was not claimed", l.state.Watermarks)
	}
	check(ready, 0, 4)
	youngSends := 0
	for _, alert := range sent {
		if alert.Symbol == "YOUNG" {
			youngSends++
		}
	}
	if youngSends != 1 {
		t.Fatal("new listing was sent early or resent", youngSends)
	}
}

func TestPublicAlertCompanionWarmupWaitsWithoutInventingMissingHistoryError(t *testing.T) {
	m, l, saved, fake := namedPublicLiveFixture(t)
	request := saved.Request
	request.Conditions = append(request.Conditions, AnalysisCondition{ID: "daily-sma", Operator: "above", Left: AnalysisOperand{Kind: "field", Field: "sma_26", Timeframe: "d1"}, Right: AnalysisOperand{Kind: "number", Value: 0}})
	if _, err := m.UpdateAnalysisCondition(saved.ID, request); err != nil {
		t.Fatal(err)
	}
	fake.items = []domain.Instrument{{NativeID: "YOUNG", Market: "perpetual", Availability: "available"}, {NativeID: "HEALTHY", Market: "perpetual", Availability: "available"}}
	asOf := time.Date(2026, 10, 1, 12, 0, 5, 0, time.UTC)
	l.publicData.adapters["hyperliquid"] = selectiveAlertHistory{Adapter: fake, shortSymbol: "YOUNG", listedAt: asOf.AddDate(0, 0, -10).Unix()}
	entry := LiveAlertDefinition{ID: strings.Repeat("b", 32), Name: "Hourly with daily companion", ConditionID: saved.ID, Period: "h1", DataSource: "hyperliquid", Enabled: true}
	if _, err := l.Configure(LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}); err != nil {
		t.Fatal(err)
	}
	var sent []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { sent = append(sent, alert); return nil }
	if err := l.check(context.Background(), l.state.Config, asOf.UnixMilli()); err != nil {
		t.Fatal("companion warming leaked a missing-file error", err)
	}
	row := l.Info().Checks[0]
	if row.AssetsWarmingUp != 1 || row.AssetsSkipped != 0 || row.AssetsScanned != 1 || row.Error != "" || len(sent) != 1 || sent[0].Symbol != "HEALTHY" {
		t.Fatal("wrong companion outcome", row, sent)
	}
	key := livePublicClaimKey(entry.ID, "hyperliquid", publicAlertSymbol("perpetual", "YOUNG"), "h1")
	if _, claimed := l.state.Watermarks[key]; claimed {
		t.Fatal("base candle claimed without a ready daily companion")
	}
}

func TestPublicAlertWarmupDoesNotMaskHardFailuresOrEnterNetworkRetryScope(t *testing.T) {
	m, l, saved, fake := namedPublicLiveFixture(t)
	request := saved.Request
	request.Conditions[0].Left.Field = "sma_26"
	if _, err := m.UpdateAnalysisCondition(saved.ID, request); err != nil {
		t.Fatal(err)
	}
	fake.items = []domain.Instrument{{NativeID: "YOUNG", Market: "perpetual", Availability: "available"}, {NativeID: "HARD", Market: "perpetual", Availability: "available"}}
	asOf := time.Date(2026, 10, 1, 0, 0, 5, 0, time.UTC)
	l.publicData.adapters["hyperliquid"] = selectiveAlertHistory{Adapter: fake, shortSymbol: "YOUNG", listedAt: asOf.AddDate(0, 0, -10).Unix(), failSymbol: "HARD", failure: context.DeadlineExceeded}
	entries := []LiveAlertDefinition{}
	for i, symbol := range []string{"YOUNG", "HARD"} {
		entries = append(entries, LiveAlertDefinition{ID: strings.Repeat(string(rune('b'+i)), 32), Name: symbol, ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true, AssetSelections: []LiveAlertAssetSelection{{DataSource: "hyperliquid", Assets: []LiveAlertAssetRef{{Market: "perpetual", Symbol: symbol}}}}})
	}
	if _, err := l.Configure(LiveAlertsConfig{Enabled: true, Alerts: entries}); err != nil {
		t.Fatal(err)
	}
	err := l.check(context.Background(), l.state.Config, asOf.UnixMilli())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("actual timeout disappeared", err)
	}
	rows := l.Info().Checks
	if rows[0].AssetsWarmingUp != 1 || rows[0].AssetsSkipped != 0 || rows[0].Error != "" || rows[0].Status != "completed" {
		t.Fatal("peer failure mislabeled an immature-only row", rows)
	}
	if rows[1].AssetsWarmingUp != 0 || rows[1].AssetsSkipped != 1 || rows[1].Error == "" {
		t.Fatal("actual provider failure became benign", rows)
	}
	retry, ok := retryLiveAlertConfig(l.state.Config, err, asOf.UnixMilli(), asOf.Add(time.Second), false)
	if !ok || len(retry.ConditionIDs) != 1 || retry.ConditionIDs[0] != entries[1].ID {
		t.Fatal("warming row entered provider retries", retry, err)
	}
}

func TestPublicAlertReadinessFailureSelectionRequiresEveryCauseToBeBenign(t *testing.T) {
	warm := &publicAlertInsufficientHistory{Required: 28, Available: 16, Period: "d1"}
	if !publicAlertHistoryPending(fmt.Errorf("asset context: %w", warm)) || publicAlertHistoryPending(errors.New(warm.Error())) || publicAlertHistoryPending(errors.Join(warm, context.DeadlineExceeded)) {
		t.Fatal("history readiness inferred from message or masked an actual error")
	}
	failures := map[string]error{"asset/d1": warm, "asset/h4": context.DeadlineExceeded}
	selected := publicAlertRequiredHistoryFailure("asset", []string{"d1", "h4"}, failures, nil)
	if !errors.Is(selected, context.DeadlineExceeded) || publicAlertHistoryPending(selected) {
		t.Fatal("warm-up hid a failed companion", selected)
	}
}

type readinessCompanionFailure struct {
	exchange.Adapter
}

func (a readinessCompanionFailure) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if key.Instrument == "YOUNG" && key.Timeframe == "1d" {
		return nil, context.DeadlineExceeded
	}
	return a.Adapter.History(ctx, key, from, to, limit)
}

func TestPublicAlertHardCompanionFailureWinsOverAnotherWarmCompanion(t *testing.T) {
	m, l, saved, fake := namedPublicLiveFixture(t)
	request := saved.Request
	for _, period := range []string{"d1", "h4"} {
		request.Conditions = append(request.Conditions, AnalysisCondition{ID: period + "-sma", Operator: "above", Left: AnalysisOperand{Kind: "field", Field: "sma_26", Timeframe: period}, Right: AnalysisOperand{Kind: "number", Value: 0}})
	}
	if _, err := m.UpdateAnalysisCondition(saved.ID, request); err != nil {
		t.Fatal(err)
	}
	fake.items = []domain.Instrument{{NativeID: "YOUNG", Market: "perpetual", Availability: "available"}, {NativeID: "HEALTHY", Market: "perpetual", Availability: "available"}}
	asOf := time.Date(2026, 10, 1, 12, 0, 5, 0, time.UTC)
	l.publicData.adapters["hyperliquid"] = readinessCompanionFailure{Adapter: selectiveAlertHistory{Adapter: fake, shortSymbol: "YOUNG", listedAt: asOf.AddDate(0, 0, -1).Unix()}}
	entry := LiveAlertDefinition{ID: strings.Repeat("b", 32), Name: "Hourly with two companions", ConditionID: saved.ID, Period: "h1", DataSource: "hyperliquid", Enabled: true}
	if _, err := l.Configure(LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}); err != nil {
		t.Fatal(err)
	}
	var sent []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { sent = append(sent, alert); return nil }
	err := l.check(context.Background(), l.state.Config, asOf.UnixMilli())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("hard daily companion failure hidden by short H4 history", err)
	}
	row := l.Info().Checks[0]
	if row.AssetsWarmingUp != 0 || row.AssetsSkipped != 1 || row.AssetsScanned != 1 || !strings.Contains(row.Error, "context deadline exceeded") || len(sent) != 1 || sent[0].Symbol != "HEALTHY" {
		t.Fatal("wrong hard companion outcome", row, sent)
	}
	key := livePublicClaimKey(entry.ID, "hyperliquid", publicAlertSymbol("perpetual", "YOUNG"), "h1")
	if _, claimed := l.state.Watermarks[key]; claimed {
		t.Fatal("base candle claimed without a successful daily companion")
	}
}

func TestLiveAlertWarmupCountsValidateAndDeduplicateAcrossPeriods(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	run, err := l.beginCheckHistory(config, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, period := range []string{"h1", "d1"} {
		run.expectPair(id, "young", period)
		run.warmingUp(id, "young", period)
	}
	if row := l.Info().Checks[0]; row.AssetsWarmingUp != 1 || row.AssetsSkipped != 0 || row.AssetsScanned != 0 {
		t.Fatal("same immature asset counted twice", row)
	}
	if err := run.finish(nil); err != nil {
		t.Fatal(err)
	}
	if len(l.state.Watermarks) != 0 {
		t.Fatal("readiness accounting wrote delivery claims", l.state.Watermarks)
	}
	state := l.state.Checks[id]
	for _, count := range []int{-1, 20001} {
		state.AssetsWarmingUp = count
		if validateLiveCheckStates(map[string]liveAlertCheckState{id: state}, l.state.Sent) == nil {
			t.Fatal("invalid warm-up count accepted", count)
		}
	}
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if row := restored.Info().Checks[0]; row.AssetsWarmingUp != 1 || row.Error != "" {
		t.Fatal("warm-up record lost after restart", row)
	}
}
