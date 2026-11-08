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
