package hyperliquid

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func metricsStore(t *testing.T) (*Store, *tradingMock) {
	s, m := mockStore(t)
	m.collateralToken = intPointer(0)
	return s, m
}

func TestOrderMetricsBalancesAndIndependentProposedIsolatedProjection(t *testing.T) {
	store, mock := metricsStore(t)
	mock.withdrawable = "0" // A verified zero must not disappear as unavailable.
	r := baseRequest()
	r.Quantity = "2"
	r.Leverage = 10
	for _, side := range []string{"buy", "sell"} {
		r.Side = side
		metrics, err := store.OrderMetrics(context.Background(), r)
		if err != nil || metrics.AvailableBalance != "0" || metrics.AvailableBalanceCurrency != "USDC" || metrics.AvailableBalanceError != "" || metrics.MarginRequired != "20" || metrics.MarginRequiredCurrency != "USDC" || metrics.MarginError != "" || metrics.LiquidationKind != "estimate" || metrics.LiquidationPrice == "" || metrics.MetricsTime <= 0 {
			t.Fatal(metrics, err)
		}
		want := decimal.RequireFromString("90").Div(decimal.RequireFromString("0.975"))
		if side == "sell" {
			want = decimal.RequireFromString("110").Div(decimal.RequireFromString("1.025"))
		}
		actual := decimal.RequireFromString(metrics.LiquidationPrice)
		if actual.Sub(want).Abs().GreaterThan(decimal.RequireFromString("0.00000000000001")) {
			t.Fatal("incorrect isolated maintenance-based estimate", metrics)
		}
		if metrics.AccountBalance != "1000" || metrics.Equity != "1000" || metrics.UsedMargin != "0" || !strings.Contains(metrics.LiquidationBasis, "verified flat") {
			t.Fatal(metrics)
		}
	}
	if len(store.previews) != 0 || len(mock.actions) != 0 {
		t.Fatal("metrics created an intent or exchange action")
	}
	files, err := os.ReadDir(store.profile)
	if err != nil || len(files) != 1 || files[0].Name() != "hyperliquid.json" {
		t.Fatal("metrics wrote signing/claim state", files, err)
	}
}

func TestInvalidQuantityAndUnavailableQuoteStillReturnVerifiedBalance(t *testing.T) {
	store, mock := metricsStore(t)
	for _, quantity := range []string{"", "-1", "invalid", "1.0001"} {
		r := baseRequest()
		r.Quantity = quantity
		metrics, err := store.OrderMetrics(context.Background(), r)
		if err != nil || metrics.AvailableBalance != "1000" || metrics.MarginRequired != "" || metrics.MarginError == "" || metrics.LiquidationPrice != "" || metrics.LiquidationError == "" {
			t.Fatal(metrics, err)
		}
	}
	stale := time.Now().Add(-time.Minute).UnixMilli()
	mock.quoteTime = &stale
	metrics, err := store.OrderMetrics(context.Background(), baseRequest())
	if err != nil || metrics.AvailableBalance != "1000" || metrics.EntryPrice != "" || metrics.MarginRequired != "" || metrics.MarginError == "" || metrics.LiquidationError == "" {
		t.Fatal("stale quote suppressed independent account balance", metrics, err)
	}
}

func TestExistingPositionLiquidationIsNeverRelabelledAsProposedOrder(t *testing.T) {
	store, mock := metricsStore(t)
	mock.positions = json.RawMessage(`[{"position":{"coin":"BTC","szi":"-2","liquidationPx":"130","marginUsed":"20"}}]`)
	metrics, err := store.OrderMetrics(context.Background(), baseRequest())
	if err != nil || metrics.AvailableBalance != "1000" || metrics.LiquidationPrice != "" || metrics.LiquidationKind != "unavailable" || !strings.Contains(metrics.LiquidationError, "existing position") || metrics.MarginRequired != "" || !strings.Contains(metrics.MarginError, "netted") {
		t.Fatal(metrics, err)
	}
}

func TestMaintenanceTiersUseLiquidationNotionalAndCumulativeDeduction(t *testing.T) {
	tiers := []marginTier{{LowerBound: "0", MaxLeverage: 20}, {LowerBound: "100", MaxLeverage: 10}}
	for _, tc := range []struct{ entry, margin, side, want string }{{"101", "10.1", "buy", "93.23076923076923076923076923076923"}, {"95", "9.5", "sell", "101.90476190476190476190476190476190"}} {
		price, err := isolatedLiquidation(decimal.NewFromInt(1), decimal.RequireFromString(tc.entry), decimal.RequireFromString(tc.margin), tc.side, 10, tiers)
		if err != "" || !price.Equal(decimal.RequireFromString(tc.want)) {
			t.Fatal("tier boundary/deduction was ignored", price, err)
		}
	}
	for _, bad := range [][]marginTier{nil, {{LowerBound: "100", MaxLeverage: 20}}, {{LowerBound: "0", MaxLeverage: 10}, {LowerBound: "100", MaxLeverage: 20}}, {{LowerBound: "0", MaxLeverage: 0}}} {
		if _, problem := isolatedLiquidation(decimal.NewFromInt(1), decimal.NewFromInt(100), decimal.NewFromInt(10), "buy", 10, bad); problem == "" {
			t.Fatal("invalid maintenance table accepted", bad)
		}
	}
}

func TestSharedCollateralHoldingsAreNotPresentedAsFreeTradingMargin(t *testing.T) {
	for _, mode := range []string{"unifiedAccount", "portfolioMargin", "dexAbstraction"} {
		t.Run(mode, func(t *testing.T) {
			store, mock := metricsStore(t)
			mock.accountMode = mode
			mock.collateralToken = intPointer(0)
			metrics, err := store.OrderMetrics(context.Background(), baseRequest())
			if err != nil || metrics.AvailableBalance != "" || metrics.AvailableBalanceError == "" || metrics.AccountBalance != "0" || metrics.AvailableBalanceCurrency != "USDC" || metrics.MarginRequired != "" || metrics.MarginError == "" || metrics.LiquidationPrice != "" || metrics.LiquidationError == "" {
				t.Fatal(metrics, err)
			}
		})
	}
}

func TestSpotBalanceCurrencyDependsOnBuyOrSellAndHasNoLiquidation(t *testing.T) {
	store, _ := metricsStore(t)
	if _, err := store.Configure(Settings{AccountAddress: testAccount, PrivateKey: testPrivate, Network: "mainnet"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ side, balance, currency string }{{"buy", "0", "USDC"}, {"sell", "1.5", "HYPE"}} {
		r := OrderRequest{Market: "spot", Instrument: "@107", Side: tc.side}
		metrics, err := store.OrderMetrics(context.Background(), r)
		if err != nil || metrics.AvailableBalance != tc.balance || metrics.AvailableBalanceCurrency != tc.currency || metrics.LiquidationKind != "not-applicable" || metrics.LiquidationPrice != "" || metrics.MarginRequired != "" {
			t.Fatal(metrics, err)
		}
	}
}

func TestUnknownAccountOrRiskMetadataPreservesIndependentFields(t *testing.T) {
	store, mock := metricsStore(t)
	mock.accountMode = "future-mode"
	metrics, err := store.OrderMetrics(context.Background(), baseRequest())
	if err != nil || metrics.AvailableBalance != "" || metrics.AvailableBalanceError == "" || metrics.MarginRequired != "" || metrics.LiquidationPrice != "" {
		t.Fatal(metrics, err)
	}
	mock.accountMode = "disabled"
	mock.marginTableID = nil
	metrics, err = store.OrderMetrics(context.Background(), baseRequest())
	if err != nil || metrics.AvailableBalance != "1000" || metrics.MarginRequired == "" || metrics.LiquidationPrice != "" || metrics.LiquidationError == "" {
		t.Fatal("unknown maintenance rate fabricated a liquidation price or lost independent fields", metrics, err)
	}
	mock.positions = json.RawMessage(`null`)
	metrics, err = store.OrderMetrics(context.Background(), baseRequest())
	if err != nil || metrics.AvailableBalance != "1000" || metrics.LiquidationPrice != "" || metrics.LiquidationError == "" {
		t.Fatal(metrics, err)
	}
}

func TestMetricsRejectsPositionTierLeverageAndIncompleteBalanceIdentities(t *testing.T) {
	store, mock := metricsStore(t)
	mock.marginTableID = intPointer(50)
	mock.marginTables = json.RawMessage(`[[50,{"marginTiers":[{"lowerBound":"0","maxLeverage":20},{"lowerBound":"100","maxLeverage":5}]}]]`)
	r := baseRequest()
	r.Quantity = "2"
	r.Leverage = 10
	value, err := store.OrderMetrics(context.Background(), r)
	if err != nil || value.AvailableBalance != "1000" || value.MarginRequired != "" || value.MarginError == "" || value.LiquidationPrice != "" || value.LiquidationError == "" {
		t.Fatal("understated margin shown for rejected position-size leverage", value, err)
	}
	if _, err = store.Configure(Settings{Network: "mainnet", AccountAddress: testAccount, PrivateKey: testPrivate}); err != nil {
		t.Fatal(err)
	}
	for _, rows := range []string{`[{"coin":"USDC","total":"30","hold":"0"}]`, `[{"token":-1,"coin":"USDC","total":"30","hold":"0"}]`} {
		mock.spotBalances = json.RawMessage(rows)
		value, err = store.OrderMetrics(context.Background(), OrderRequest{Market: "spot", Instrument: "@107", Side: "buy"})
		if err != nil || value.AvailableBalance != "" || value.AccountBalance != "" || value.AvailableBalanceError == "" {
			t.Fatal("partial balance row fabricated zero funds", value, err)
		}
	}
}

func TestDefaultAccountModeDoesNotGuessClassicMargin(t *testing.T) {
	store, mock := metricsStore(t)
	mock.accountMode = "default"
	metrics, err := store.OrderMetrics(context.Background(), baseRequest())
	if err != nil || metrics.AvailableBalance != "" || metrics.MarginRequired != "" || metrics.LiquidationPrice != "" || metrics.AvailableBalanceError == "" {
		t.Fatal("account default was guessed as classic", metrics, err)
	}
}

func TestQuoteAgeAfterAccountReadPreservesBalanceButSuppressesProjection(t *testing.T) {
	for _, preview := range []bool{false, true} {
		t.Run(map[bool]string{false: "metrics", true: "confirmation"}[preview], func(t *testing.T) {
			store, mock := metricsStore(t)
			base := time.Now()
			var elapsed atomic.Int64
			store.now = func() time.Time { return base.Add(time.Duration(elapsed.Load()) * time.Millisecond) }
			observed := base.UnixMilli()
			mock.quoteTime = &observed
			freshAccount := base.Add(20 * time.Second).UnixMilli()
			mock.accountTime = &freshAccount
			mock.beforeAccount = func() { elapsed.Store(20000) }
			if preview {
				_, err := store.Preview(context.Background(), baseRequest())
				if err == nil || !strings.Contains(err.Error(), "entry quote became stale") || len(store.previews) != 0 {
					t.Fatal("stale quote produced a confirmation", err)
				}
			} else {
				value, err := store.OrderMetrics(context.Background(), baseRequest())
				if err != nil || value.AvailableBalance != "1000" || value.MetricsTime != freshAccount || value.EntryPrice != "" || value.MarginRequired != "" || value.LiquidationPrice != "" || !strings.Contains(value.MarginError, "aged") || !strings.Contains(value.LiquidationError, "aged") {
					t.Fatal("fresh account timestamp hid an aged quote", value, err)
				}
			}
			if len(mock.actions) != 0 {
				t.Fatal("read-only projections submitted orders")
			}
		})
	}
}

func TestMetricsUseConfiguredAccountAndExactBuilderDex(t *testing.T) {
	store, mock := metricsStore(t)
	mock.collateralToken = intPointer(360)
	r := baseRequest()
	r.Instrument = "xyz:BTC"
	value, err := store.OrderMetrics(context.Background(), r)
	if err != nil || value.AvailableBalanceCurrency != "USDH" || value.MarginRequiredCurrency != "USDH" || value.MarginRequired == "" {
		t.Fatal(value, err)
	}
	found := false
	for _, request := range mock.requests {
		var kind, user, dex string
		_ = json.Unmarshal(request["type"], &kind)
		if kind != "userAbstraction" && kind != "clearinghouseState" {
			continue
		}
		_ = json.Unmarshal(request["user"], &user)
		if user != testAccount {
			t.Fatal("agent wallet used as balance owner", user)
		}
		if kind == "clearinghouseState" {
			_ = json.Unmarshal(request["dex"], &dex)
			if dex != "xyz" {
				t.Fatal("wrong DEX ledger selected", dex)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no correct DEX ledger read")
	}
}

func TestPublicEstimateDoesNotReadConfiguredAccountMetrics(t *testing.T) {
	store, mock := metricsStore(t)
	value, err := store.Estimate(context.Background(), "perpetual", "BTC", "buy")
	if err != nil || value.ReferencePrice != "100" {
		t.Fatal(value, err)
	}
	for _, kind := range mock.infos {
		if kind == "userAbstraction" || kind == "clearinghouseState" || kind == "spotClearinghouseState" {
			t.Fatal("public quote estimate unexpectedly read account balances", mock.infos)
		}
	}
}
