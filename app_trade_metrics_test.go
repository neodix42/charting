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
	value := hyperliquid.Metrics{AvailableBalance: "0", AvailableBalanceCurrency: "BTC", MaximumQuantity: "0.00000123", MaximumQuantityBasis: "Current owned balance and order book", MarginRequired: "2", MarginRequiredCurrency: "USDT", LiquidationPrice: "73.25", LiquidationKind: "estimate", LiquidationBasis: "isolated", MetricsTime: 456,
		RiskSizingQuantity: "0.00000123", RiskSizingSafeQuantity: "0.00000120", RiskSizingLoss: "0.015", RiskSizingLiquidationPrice: "73.25", RiskSizingCanTarget: true, RiskSizingBasis: "Verified selected DEX equity and isolated maintenance tiers"}
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
	if decoded["maximumQuantity"] != "0.00000123" || decoded["maximumQuantityBasis"] != value.MaximumQuantityBasis {
		t.Fatalf("maximum base quantity lost or rounded in transport: %s", encoded)
	}
	if decoded["riskSizingQuantity"] != value.RiskSizingQuantity || decoded["riskSizingSafeQuantity"] != value.RiskSizingSafeQuantity || decoded["riskSizingLoss"] != value.RiskSizingLoss || decoded["riskSizingLiquidationPrice"] != value.RiskSizingLiquidationPrice || decoded["riskSizingCanTarget"] != true || decoded["riskSizingBasis"] != value.RiskSizingBasis {
		t.Fatalf("reviewed risk sizing lost precision or its calculation basis: %s", encoded)
	}
	duka := dukascopyAccountMetrics(&jforex.TradeMetrics{MaximumQuantity: "0", MaximumQuantityBasis: "No free margin"})
	if duka.MaximumQuantity != "0" || duka.MaximumQuantityBasis != "No free margin" {
		t.Fatalf("confirmed zero maximum discarded: %+v", duka)
	}
}

func TestDukascopyLeverageTransportPreservesProviderValues(t *testing.T) {
	for _, test := range []struct {
		name string
		wire string
		want *float64
	}{
		{name: "provider instrument limit", wire: `{"accountLeverage":180,"instrumentMaxLeverage":10,"effectiveLeverage":10}`, want: leverageValue(10)},
		{name: "provider instrument overrides account default", wire: `{"accountLeverage":180,"instrumentMaxLeverage":250,"effectiveLeverage":250}`, want: leverageValue(250)},
		{name: "SDK zero uses account", wire: `{"accountLeverage":180,"instrumentMaxLeverage":0,"effectiveLeverage":180}`, want: leverageValue(0)},
		{name: "unavailable is not SDK zero", wire: `{"accountLeverage":180}`, want: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			var provider jforex.TradeMetrics
			if err := json.Unmarshal([]byte(test.wire), &provider); err != nil {
				t.Fatal(err)
			}
			got := dukascopyAccountMetrics(&provider)
			encoded, err := json.Marshal(trading.Metrics{AccountMetrics: got})
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err = json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			value, present := decoded["instrumentMaxLeverage"]
			if test.want == nil {
				if present {
					t.Fatalf("missing SDK value became a reported limit: %s", encoded)
				}
			} else if !present || value != *test.want {
				t.Fatalf("provider instrument limit changed in transport: %s", encoded)
			}
			if got.AccountLeverage != provider.AccountLeverage || got.EffectiveLeverage != provider.EffectiveLeverage {
				t.Fatalf("provider account or effective leverage changed: %+v", got)
			}
		})
	}
}

func leverageValue(value float64) *float64 { return &value }

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
