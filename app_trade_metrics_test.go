package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/hyperliquid"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/trading"
)

func TestDukascopyAccountMetricsPreserveZeroAndIndependentBalance(t *testing.T) {
	zero, equity, used, free := 0.0, 1500.0, 400.0, 1100.0
	value := jforex.TradeMetrics{Balance: &zero, Equity: &equity, UsedMargin: &used, AvailableBalance: &free, AccountCurrency: "EUR", AccountTime: 123, AccountError: "Unrelated balance field failed", ValuationError: "No fresh EURUSD conversion", AccountNote: "Free margin is equity minus used margin", MarginError: "No SDK pre-trade quote", LiquidationError: "Account-wide stop-out"}
	got := dukascopyAccountMetrics(&value)
	if got.AccountBalance != "0" || got.AvailableBalance != "1100" || got.Equity != "1500" || got.UsedMargin != "400" || got.AvailableBalanceCurrency != "EUR" || got.MetricsTime != 123 || got.AvailableBalanceError != "" {
		t.Fatalf("balance hidden by unrelated valuation failure: %+v", got)
	}
	if got.MarginRequired != "" || got.MarginError == "" || got.LiquidationPrice != "" || got.LiquidationKind != "not-applicable" {
		t.Fatalf("unsupported per-order fields fabricated: %+v", got)
	}
	value.AvailableBalance = nil
	got = dukascopyAccountMetrics(&value)
	if got.AvailableBalance != "" || got.AvailableBalanceError != value.AccountError || got.AccountBalance != "0" {
		t.Fatalf("partial account failure discarded independent fields: %+v", got)
	}
	invalid := math.NaN()
	if metricMoney(&invalid) != "" || metricMoney(nil) != "" || metricMoney(&zero) != "0" {
		t.Fatal("unknown money must differ from a confirmed zero")
	}
}

func TestAccountMetricsTransportIsFlatAndCurrenciesIndependent(t *testing.T) {
	value := hyperliquid.Metrics{AvailableBalance: "0", AvailableBalanceCurrency: "BTC", MarginRequired: "2", MarginRequiredCurrency: "USDT", LiquidationPrice: "73.25", LiquidationKind: "estimate", LiquidationBasis: "isolated", MetricsTime: 456}
	got := trading.Metrics{AccountMetrics: hyperliquidAccountMetrics(value), Quantity: "1", Leverage: 2}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["availableBalance"] != "0" || decoded["availableBalanceCurrency"] != "BTC" || decoded["marginRequiredCurrency"] != "USDT" || decoded["liquidationKind"] != "estimate" || decoded["AccountMetrics"] != nil {
		t.Fatalf("metrics not flattened or token identities lost: %s", encoded)
	}
}

func TestTradeMetricsReadValidatesBeforeProviderAndEchoesDraft(t *testing.T) {
	a := &App{}
	request := trading.Request{FeedKey: domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1h"}, Side: "buy", Quantity: " 0.0100 ", Leverage: 7}
	got, err := a.GetTradeMetrics(request)
	if err == nil || got.Quantity != "0.0100" || got.Leverage != 7 || got.FeedKey != request.FeedKey || got.Side != "buy" {
		t.Fatal(got, err)
	}
	request.Side = "hold"
	if _, err = a.GetTradeMetrics(request); err == nil || !strings.Contains(err.Error(), "Buy or Sell") {
		t.Fatal("invalid direction reached provider", err)
	}
	request.Side, request.Quantity = "sell", strings.Repeat("1", 129)
	if _, err = a.GetTradeMetrics(request); err == nil || !strings.Contains(err.Error(), "length") {
		t.Fatal("oversized quantity reached provider", err)
	}
	a.closeTrading()
	request.Quantity = ""
	if _, err = a.GetTradeMetrics(request); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatal("balance read outlived trading shutdown", err)
	}
	legacy := dukascopyAccountMetrics(nil)
	if legacy.AvailableBalance != "" || !strings.Contains(legacy.AvailableBalanceError, "matching runtime") {
		t.Fatal("legacy bridge manufactured account data", legacy)
	}
}
