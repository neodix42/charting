package hyperliquid

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestRiskSizingTargetsHalfVerifiedDexEquityWithBlankQuantityOnBothSides(t *testing.T) {
	for _, tc := range []struct{ side, stop string }{{"buy", "80"}, {"sell", "120"}} {
		t.Run(tc.side, func(t *testing.T) {
			store, mock := metricsStore(t)
			request := baseRequest()
			request.Side = tc.side
			request.StopLoss = tc.stop
			request.Quantity = ""
			out, err := store.OrderMetrics(context.Background(), request)
			if err != nil || out.RiskSizingError != "" || out.RiskSizingQuantity != "25" || out.RiskSizingSafeQuantity != "25" || out.RiskSizingLoss != "500" || !out.RiskSizingCanTarget {
				t.Fatal(out, err)
			}
			liquidation := decimal.RequireFromString(out.RiskSizingLiquidationPrice)
			stop := decimal.RequireFromString(tc.stop)
			if tc.side == "buy" && !stop.GreaterThan(liquidation) || tc.side == "sell" && !stop.LessThan(liquidation) {
				t.Fatal("stop does not precede liquidation", out)
			}
			if !strings.Contains(out.RiskSizingBasis, "entry 100, stop "+tc.stop) || !strings.Contains(out.RiskSizingBasis, "leverage 3×") {
				t.Fatal("sizing quote/leverage basis missing", out)
			}
			if len(mock.actions) != 0 || len(store.previews) != 0 {
				t.Fatal("read-only risk sizing created an action/intent")
			}
			for _, kind := range mock.infos {
				if kind != "meta" && kind != "spotMeta" && kind != "l2Book" && kind != "userAbstraction" && kind != "clearinghouseState" && kind != "userFees" && kind != "activeAssetData" {
					t.Fatal("risk sizing requested another endpoint", kind)
				}
			}
		})
	}
}

func TestRiskSizingUsesFundedMaximumAndLiquidityInsteadOfAccountMarginPercent(t *testing.T) {
	store, mock := metricsStore(t)
	mock.bookLevels = json.RawMessage(`[[{"px":"100","sz":"2.2345"}],[{"px":"100","sz":"2.2345"}]]`)
	request := baseRequest()
	request.Quantity = ""
	request.StopLoss = "80"
	out, err := store.OrderMetrics(context.Background(), request)
	if err != nil || out.RiskSizingError != "" || out.RiskSizingQuantity != "25" || out.RiskSizingSafeQuantity != "2.234" || out.RiskSizingLoss != "44.68" || out.RiskSizingCanTarget {
		t.Fatal("liquidity/funded maximum ignored", out, err)
	}
	if out.RiskSizingSafeQuantity != out.MaximumQuantity {
		t.Fatal(out)
	}
}

func TestRiskSizingFloorsTargetAndGrossLossBelowHalfEquity(t *testing.T) {
	out := Metrics{Equity: "1000", EntryPrice: "100", riskMarginPrice: decimal.NewFromInt(100), MaximumQuantity: "100"}
	request := OrderRequest{Market: "perpetual", Side: "buy", StopLoss: "79", Leverage: 3}
	market := instrument{szDecimals: 3, maxLeverage: 20, marginTiers: []marginTier{{LowerBound: "0", MaxLeverage: 20}}}
	out.sizeRisk(request, market, decimal.NewFromInt(100), false)
	if out.RiskSizingError != "" || out.RiskSizingQuantity != "23.809" || out.RiskSizingSafeQuantity != "23.809" || out.RiskSizingLoss != "499.989" || !out.RiskSizingCanTarget {
		t.Fatal(out)
	}
}

func TestRiskSizingCapsAtTierDependentLiquidationBoundary(t *testing.T) {
	for _, tc := range []struct{ side, stop, want string }{{"buy", "93", "15.151"}, {"sell", "107", "10.638"}} {
		t.Run(tc.side, func(t *testing.T) {
			out := Metrics{Equity: "1000", EntryPrice: "100", riskMarginPrice: decimal.NewFromInt(100), MaximumQuantity: "100"}
			request := OrderRequest{Market: "perpetual", Side: tc.side, StopLoss: tc.stop, Leverage: 10}
			market := instrument{szDecimals: 3, maxLeverage: 20, marginTiers: []marginTier{{LowerBound: "0", MaxLeverage: 20}, {LowerBound: "1000", MaxLeverage: 10}}}
			out.sizeRisk(request, market, decimal.NewFromInt(100), false)
			if out.RiskSizingError != "" || out.RiskSizingSafeQuantity != tc.want || out.RiskSizingCanTarget {
				t.Fatal(out)
			}
			safe := decimal.RequireFromString(out.RiskSizingSafeQuantity)
			stop := decimal.RequireFromString(tc.stop)
			liquidation := decimal.RequireFromString(out.RiskSizingLiquidationPrice)
			if tc.side == "buy" && !stop.GreaterThan(liquidation) || tc.side == "sell" && !stop.LessThan(liquidation) {
				t.Fatal("suggested stop lies beyond liquidation", out)
			}
			larger := safe.Add(decimal.RequireFromString("0.001"))
			price, problem := isolatedLiquidation(larger, decimal.NewFromInt(100), larger.Mul(decimal.NewFromInt(10)), tc.side, 10, market.marginTiers)
			if problem != "" || tc.side == "buy" && stop.GreaterThan(price) || tc.side == "sell" && stop.LessThan(price) {
				t.Fatal("the next lot remains eligible", out, price, problem)
			}
		})
	}
}

func TestRiskSizingRejectsStopAtIsolatedLiquidation(t *testing.T) {
	for _, tc := range []struct{ side, entry, stop string }{{"buy", "39", "38"}, {"sell", "41", "42"}} {
		out := Metrics{Equity: "1000", EntryPrice: tc.entry, riskMarginPrice: decimal.RequireFromString(tc.entry), MaximumQuantity: "1000"}
		request := OrderRequest{Market: "perpetual", Side: tc.side, StopLoss: tc.stop, Leverage: 20}
		market := instrument{szDecimals: 3, maxLeverage: 20, marginTiers: []marginTier{{LowerBound: "0", MaxLeverage: 20}}}
		out.sizeRisk(request, market, decimal.RequireFromString(tc.entry), false)
		if out.RiskSizingSafeQuantity != "0" || out.RiskSizingLoss != "0" || out.RiskSizingCanTarget || !strings.Contains(out.RiskSizingError, "isolated liquidation") {
			t.Fatal(tc.side, out)
		}
	}
}

func TestRiskSizingOneTimesLongHasNoPositiveLiquidationThreshold(t *testing.T) {
	out := Metrics{Equity: "1000", EntryPrice: "100", riskMarginPrice: decimal.NewFromInt(100), MaximumQuantity: "100"}
	request := OrderRequest{Market: "perpetual", Side: "buy", StopLoss: "1", Leverage: 1}
	market := instrument{szDecimals: 3, maxLeverage: 20, marginTiers: []marginTier{{LowerBound: "0", MaxLeverage: 20}}}
	out.sizeRisk(request, market, decimal.NewFromInt(100), false)
	if out.RiskSizingError != "" || out.RiskSizingSafeQuantity != "5.05" || out.RiskSizingLiquidationPrice != "0" || !out.RiskSizingCanTarget {
		t.Fatal(out)
	}
}

func TestRiskSizingEntryTierLeverageLimitAndMinimumNotional(t *testing.T) {
	out := Metrics{Equity: "1000", EntryPrice: "100", riskMarginPrice: decimal.NewFromInt(100), MaximumQuantity: "100"}
	request := OrderRequest{Market: "perpetual", Side: "buy", StopLoss: "99", Leverage: 10}
	market := instrument{szDecimals: 3, maxLeverage: 20, marginTiers: []marginTier{{LowerBound: "0", MaxLeverage: 20}, {LowerBound: "1000", MaxLeverage: 5}}}
	out.sizeRisk(request, market, decimal.NewFromInt(100), false)
	if out.RiskSizingError != "" || out.RiskSizingSafeQuantity != "9.999" {
		t.Fatal("entry tier ignored", out)
	}
	out = Metrics{Equity: "1000", EntryPrice: "100", riskMarginPrice: decimal.NewFromInt(100), MaximumQuantity: "0.01"}
	out.sizeRisk(request, market, decimal.NewFromInt(100), false)
	if out.RiskSizingSafeQuantity != "0" || !strings.Contains(out.RiskSizingError, "minimum order notional") {
		t.Fatal(out)
	}
}

func TestRiskSizingMissingDraftOrInvalidStopPreservesIndependentBalanceAndMaximum(t *testing.T) {
	for _, stop := range []string{"", "invalid", "0", "101", "100", "80.00000001"} {
		store, _ := metricsStore(t)
		request := baseRequest()
		request.Quantity = ""
		request.StopLoss = stop
		out, err := store.OrderMetrics(context.Background(), request)
		if err != nil || out.AvailableBalance != "1000" || out.MaximumQuantity == "" || out.MaximumQuantityError != "" || out.RiskSizingError == "" || out.RiskSizingSafeQuantity != "" {
			t.Fatal(stop, out, err)
		}
	}
}

func TestRiskSizingUnsupportedOrStaleSnapshotsNeverSuggestQuantity(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*tradingMock)
	}{
		{"shared collateral", func(m *tradingMock) { m.accountMode = "unifiedAccount" }},
		{"existing net position", func(m *tradingMock) { m.positions = json.RawMessage(`[{"position":{"coin":"BTC","szi":"1"}}]`) }},
		{"missing maintenance tiers", func(m *tradingMock) { m.marginTableID = nil }},
		{"invalid maintenance tiers", func(m *tradingMock) {
			m.marginTableID = intPointer(50)
			m.marginTables = json.RawMessage(`[[50,{"marginTiers":[{"lowerBound":"0","maxLeverage":5},{"lowerBound":"1000","maxLeverage":20}]}]]`)
		}},
		{"stale account", func(m *tradingMock) { at := time.Now().Add(-time.Minute).UnixMilli(); m.accountTime = &at }},
		{"stale entry", func(m *tradingMock) { at := time.Now().Add(-time.Minute).UnixMilli(); m.quoteTime = &at }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := metricsStore(t)
			tc.set(mock)
			request := baseRequest()
			request.StopLoss = "80"
			request.Quantity = ""
			out, err := store.OrderMetrics(context.Background(), request)
			if err != nil || out.RiskSizingError == "" || out.RiskSizingSafeQuantity != "" || out.RiskSizingCanTarget {
				t.Fatal(out, err)
			}
		})
	}
}

func TestRiskSizingDoesNotOverstateCollateralWhenMarkIsBelowEntry(t *testing.T) {
	store, mock := metricsStore(t)
	mock.activeResponse = json.RawMessage(`{"user":"` + testAccount + `","coin":"BTC","markPx":"90"}`)
	request := baseRequest()
	request.Quantity = ""
	request.Leverage = 10
	request.StopLoss = "93"
	out, err := store.OrderMetrics(context.Background(), request)
	if err != nil || out.MaximumQuantity == "" || out.MaximumQuantityError != "" || out.RiskSizingSafeQuantity != "0" || !strings.Contains(out.RiskSizingError, "isolated liquidation") {
		t.Fatal("entry-based margin overstated isolated safety", out, err)
	}
	// The same stop is safe at a mark equal to entry. This proves the risk
	// check uses the verified mark read rather than a constant entry model.
	mock.activeResponse = nil
	out, err = store.OrderMetrics(context.Background(), request)
	if err != nil || out.RiskSizingError != "" || out.RiskSizingSafeQuantity == "0" {
		t.Fatal(out, err)
	}
}
