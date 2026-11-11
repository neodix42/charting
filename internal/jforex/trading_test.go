package jforex

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleTrade() TradeRequest {
	return TradeRequest{Instrument: "EURUSD", Side: "buy", Quantity: 1000, StopLoss: 1.09, TakeProfit: 1.12}
}
func TestTradePreviewConfirmationClaimAndDuplicate(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("CRYPTODESK_JFOREX_PROTOCOL_TRACE", trace)
	m, _ := testManager(t, "trade")
	preview, err := m.PreviewTrade(context.Background(), sampleTrade())
	if err != nil || preview.ID == "" || preview.AccountLeverage != 100 || preview.Environment != "demo" {
		t.Fatal(preview, err)
	}
	before, _ := os.ReadFile(trace)
	if strings.Contains(string(before), "tradeSubmit") {
		t.Fatal("preview placed a trade")
	}
	result, err := m.SubmitTrade(context.Background(), preview.ID)
	if err != nil || result.Status != "filled" || !result.ProtectionConfirmed {
		t.Fatal(result, err)
	}
	duplicate, err := m.SubmitTrade(context.Background(), preview.ID)
	if err != nil || duplicate != result {
		t.Fatal("confirmed result was not reused", duplicate, err)
	}
	after, _ := os.ReadFile(trace)
	if strings.Count(string(after), "tradeSubmit") != 1 {
		t.Fatal("order retried", string(after))
	}
	claim, err := os.ReadFile(filepath.Join(filepath.Dir(m.path), "dukascopy-trade-claims", preview.ID+".json"))
	if err != nil || strings.Contains(string(claim), testPassword) || strings.Contains(string(claim), testUsername) {
		t.Fatal("private claim invalid", err)
	}
}
func TestTradeUnknownOutcomeNeverAutomaticallyResubmits(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("CRYPTODESK_JFOREX_PROTOCOL_TRACE", trace)
	m, _ := testManager(t, "trade-hang")
	preview, err := m.PreviewTrade(context.Background(), sampleTrade())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err := m.SubmitTrade(ctx, preview.ID)
	if err != nil || result.Status != "unknown" {
		t.Fatal(result, err)
	}
	again, err := m.SubmitTrade(context.Background(), preview.ID)
	if err != nil || again.Status != "unknown" {
		t.Fatal(again, err)
	}
	after, _ := os.ReadFile(trace)
	if strings.Count(string(after), "tradeSubmit") != 1 {
		t.Fatal("unknown outcome retried", string(after))
	}
}
func TestTradeRequiresMatchingAccountExpiryAndCapableBridge(t *testing.T) {
	m, _ := testManager(t, "")
	if _, err := m.PreviewTrade(context.Background(), sampleTrade()); err == nil || !strings.Contains(err.Error(), "bridge") {
		t.Fatal("legacy bridge allowed trade", err)
	}
	m2, _ := testManager(t, "trade")
	preview, err := m2.PreviewTrade(context.Background(), sampleTrade())
	if err != nil {
		t.Fatal(err)
	}
	m2.mu.Lock()
	m2.tradePreviews[preview.ID].preview.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	m2.mu.Unlock()
	if _, err = m2.SubmitTrade(context.Background(), preview.ID); err == nil {
		t.Fatal("expired confirmation accepted")
	}
	preview, err = m2.PreviewTrade(context.Background(), sampleTrade())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m2.Configure(Settings{Username: testUsername, Password: "different-password", Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err = m2.SubmitTrade(context.Background(), preview.ID); err == nil {
		t.Fatal("confirmation reused for changed account")
	}
}
func TestTradeValidationAndClaimFailurePlaceNoOrder(t *testing.T) {
	m, count := testManager(t, "trade")
	for _, quantity := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		request := sampleTrade()
		request.Quantity = quantity
		if _, err := m.PreviewTrade(context.Background(), request); err == nil {
			t.Fatal("invalid quantity accepted")
		}
	}
	if count.Load() != 0 {
		t.Fatal("invalid requests contacted the broker")
	}
	preview, err := m.PreviewTrade(context.Background(), sampleTrade())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(m.path), "dukascopy-trade-claims"), []byte("preserved original"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.SubmitTrade(context.Background(), preview.ID); err == nil || !strings.Contains(err.Error(), "no order") {
		t.Fatal("claim failure submitted", err)
	}
	if _, err = m.SubmitTrade(context.Background(), preview.ID); err == nil {
		t.Fatal("failed claimed confirmation retried")
	}
}

func TestTradeEstimatesDoNotCreateConfirmationOrClaim(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("CRYPTODESK_JFOREX_PROTOCOL_TRACE", trace)
	m, _ := testManager(t, "trade-estimate")
	for _, side := range []string{"buy", "sell"} {
		estimate, err := m.EstimateTrade(context.Background(), "EURUSD", side)
		if err != nil || estimate.EntryPrice != 1.1002 || estimate.AccountCurrency != "EUR" || estimate.ProfitPerPriceUnit != 0.9 || estimate.ValuationTime <= 0 || estimate.ValuationError != "" {
			t.Fatal(estimate, err)
		}
	}
	m.mu.Lock()
	count := len(m.tradePreviews)
	m.mu.Unlock()
	if count != 0 {
		t.Fatal("valuation allocated confirmation tokens")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.path), "dukascopy-trade-claims")); !os.IsNotExist(err) {
		t.Fatal("valuation created order claims")
	}
	calls, _ := os.ReadFile(trace)
	if strings.Contains(string(calls), "tradePreview") || strings.Contains(string(calls), "tradeSubmit") {
		t.Fatal("valuation used a trading RPC", string(calls))
	}
}
func TestTradeValuationUnavailableKeepsEntryAndDoesNotInventRates(t *testing.T) {
	for _, scenario := range []string{"trade-estimate-stale", "trade-estimate-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := testManager(t, scenario)
			estimate, err := m.EstimateTrade(context.Background(), "EURUSD", "buy")
			if err != nil || estimate.EntryPrice <= 0 || estimate.ProfitPerPriceUnit != 0 || estimate.ValuationError == "" {
				t.Fatal(estimate, err)
			}
		})
	}
	m, _ := testManager(t, "trade")
	if _, err := m.EstimateTrade(context.Background(), "EURUSD", "buy"); err == nil || !strings.Contains(err.Error(), "selected JForex bridge") {
		t.Fatal("missing estimate capability accepted", err)
	}
	preview, err := m.PreviewTrade(context.Background(), sampleTrade())
	if err != nil || preview.ValuationError == "" || preview.ProfitPerPriceUnit != 0 {
		t.Fatal("old bridge preview was blocked or invented valuation", preview, err)
	}
}

func TestOrderMetricsKeepAccountMoneyIndependentOfQuoteAndConversion(t *testing.T) {
	for _, scenario := range []string{"trade-estimate-metrics", "trade-estimate-metrics-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			trace := filepath.Join(t.TempDir(), "trace")
			t.Setenv("CRYPTODESK_JFOREX_PROTOCOL_TRACE", trace)
			m, _ := testManager(t, scenario)
			metrics, err := m.OrderMetrics(context.Background(), TradeRequest{Instrument: "AAPLUSUSD", Side: "buy"})
			if err != nil || metrics.AccountCurrency != "EUR" || metrics.Balance == nil || metrics.Equity == nil || metrics.UsedMargin == nil || metrics.AvailableBalance == nil || *metrics.AvailableBalance != 1000 {
				t.Fatal(metrics, err)
			}
			if scenario == "trade-estimate-metrics-unavailable" && (*metrics.Balance != 0 || metrics.EntryError == "" || metrics.ValuationError != "" || metrics.ProfitPerPriceUnit != 0) {
				t.Fatal("balance lost when quote/conversion unavailable", metrics)
			}
			if metrics.ProfitPerPriceUnit != 0 || metrics.ValuationError != "" || metrics.ValuationTime != 0 {
				t.Fatal("account metrics unexpectedly included FX valuation", metrics)
			}
			if metrics.MarginRequired != nil || metrics.LiquidationPrice != nil || metrics.MarginError == "" || metrics.LiquidationError == "" {
				t.Fatal("unsupported financial values invented", metrics)
			}
			calls, _ := os.ReadFile(trace)
			if strings.Contains(string(calls), "tradeSubmit") || strings.Contains(string(calls), "tradePreview") {
				t.Fatal("read-only account metrics placed/prepared order", string(calls))
			}
			m.mu.Lock()
			tokens := len(m.tradePreviews)
			m.mu.Unlock()
			if tokens != 0 {
				t.Fatal("metrics allocated confirmation")
			}
		})
	}
	legacy, _ := testManager(t, "trade-estimate")
	if _, err := legacy.OrderMetrics(context.Background(), TradeRequest{Instrument: "EURUSD", Side: "buy"}); err == nil || !strings.Contains(err.Error(), "selected JForex bridge") {
		t.Fatal("missing metrics capability not explained", err)
	}
}
