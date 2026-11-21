package hyperliquid

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestMaximumQuantityFundsWholeOrderAtSelectedLeverageAndIgnoresEnteredQuantity(t *testing.T) {
	store, mock := metricsStore(t)
	request := baseRequest()
	previous := decimal.Zero
	for _, leverage := range []int{1, 3, 10, 20} {
		request.Leverage = leverage
		// Empty and invalid entered quantities must not disable the maximum label.
		for _, quantity := range []string{"", "invalid", "999999999"} {
			request.Quantity = quantity
			metrics, err := store.OrderMetrics(context.Background(), request)
			if err != nil || metrics.MaximumQuantity == "" || metrics.MaximumQuantityError != "" {
				t.Fatal(metrics, err)
			}
			maximum := decimal.RequireFromString(metrics.MaximumQuantity)
			lot := decimal.RequireFromString("0.001")
			// Assert the full worst-case fill plus fees is funded, and the next
			// supported size would exceed the balance, rather than mirroring output.
			required := func(size decimal.Decimal) decimal.Decimal {
				return size.Mul(decimal.NewFromInt(100)).DivRound(decimal.NewFromInt(int64(leverage)), 24).Add(size).Add(size.Mul(decimal.RequireFromString("0.04545")))
			}
			if required(maximum).GreaterThan(decimal.NewFromInt(1000)) || !required(maximum.Add(lot)).GreaterThan(decimal.NewFromInt(1000)) {
				t.Fatalf("leverage=%d: unsupported maximum %s, cost %s", leverage, maximum, required(maximum))
			}
			if maximum.LessThan(previous) {
				t.Fatal("more leverage reduced quantity", previous, maximum)
			}
			previous = maximum
		}
	}
	if len(mock.actions) != 0 || len(store.previews) != 0 {
		t.Fatal("sizing mutated account/orders")
	}
}

func TestMaximumQuantityUsesMarkMarginAndCurrentAccountFees(t *testing.T) {
	store, mock := metricsStore(t)
	mock.activeResponse = json.RawMessage(`{"user":"` + testAccount + `","coin":"BTC","markPx":"200"}`)
	mock.feeResponse = json.RawMessage(`{"userCrossRate":"0.001"}`)
	request := baseRequest()
	request.Leverage = 10
	value, err := store.OrderMetrics(context.Background(), request)
	if err != nil || value.MaximumQuantityError != "" {
		t.Fatal(value, err)
	}
	size := decimal.RequireFromString(value.MaximumQuantity)
	cost := func(q decimal.Decimal) decimal.Decimal { return q.Mul(decimal.RequireFromString("20.101")) }
	if cost(size).GreaterThan(decimal.NewFromInt(1000)) || !cost(size.Add(decimal.RequireFromString("0.001"))).GreaterThan(decimal.NewFromInt(1000)) {
		t.Fatal("mark or fee ignored", value)
	}
}

func TestMaximumQuantityCapsBothSidesByAllVisibleDepthInsideIOC(t *testing.T) {
	store, mock := metricsStore(t)
	mock.bookLevels = json.RawMessage(`[[{"px":"100","sz":"2"},{"px":"99.5","sz":"3"},{"px":"98","sz":"50"}],[{"px":"100","sz":"1.25"},{"px":"100.5","sz":"2"},{"px":"101","sz":"3"},{"px":"102","sz":"50"}]]`)
	for _, tc := range []struct{ side, want string }{{"buy", "6.25"}, {"sell", "5"}} {
		r := baseRequest()
		r.Side = tc.side
		r.Leverage = 20
		value, err := store.OrderMetrics(context.Background(), r)
		if err != nil || value.MaximumQuantityError != "" || value.MaximumQuantity != tc.want {
			t.Fatal(tc.side, value, err)
		}
	}
}

func TestMaximumQuantityHonorsTierLeverageBeforeLowerLeverageThreshold(t *testing.T) {
	store, mock := metricsStore(t)
	mock.marginTableID = intPointer(50)
	mock.marginTables = json.RawMessage(`[[50,{"marginTiers":[{"lowerBound":"0","maxLeverage":20},{"lowerBound":"100","maxLeverage":5}]}]]`)
	r := baseRequest()
	r.Leverage = 10
	r.Quantity = ""
	value, err := store.OrderMetrics(context.Background(), r)
	if err != nil || value.MaximumQuantity != "0.999" || value.MaximumQuantityError != "" {
		t.Fatal(value, err)
	}
	r.Leverage = 5
	value, err = store.OrderMetrics(context.Background(), r)
	if err != nil || value.MaximumQuantityError != "" || decimal.RequireFromString(value.MaximumQuantity).LessThan(decimal.NewFromInt(1)) {
		t.Fatal("supported tier capped unnecessarily", value, err)
	}
}

func TestMaximumSpotBuyFundsQuoteWhileSellUsesAvailableBase(t *testing.T) {
	store, mock := metricsStore(t)
	if _, err := store.Configure(Settings{AccountAddress: testAccount, PrivateKey: testPrivate, Network: "mainnet"}); err != nil {
		t.Fatal(err)
	}
	mock.spotBalances = json.RawMessage(`[{"token":0,"total":"1000","hold":"100"},{"token":150,"total":"12","hold":"0.5"}]`)
	for _, tc := range []struct{ side, want string }{{"buy", "8.9"}, {"sell", "11.5"}} {
		value, err := store.OrderMetrics(context.Background(), OrderRequest{Market: "spot", Instrument: "@107", Side: tc.side})
		if err != nil || value.MaximumQuantity != tc.want || value.MaximumQuantityError != "" {
			t.Fatal(tc.side, value, err)
		}
	}
}

func TestMaximumQuantityZeroAndMinimumNotionalRemainVerifiedZero(t *testing.T) {
	store, mock := metricsStore(t)
	for _, balance := range []string{"0", "0.001"} {
		mock.withdrawable = balance
		value, err := store.OrderMetrics(context.Background(), baseRequest())
		if err != nil || value.MaximumQuantity != "0" || value.MaximumQuantityError != "" {
			t.Fatal(balance, value, err)
		}
	}
}

func TestMaximumExistingPositionUsesMatchingIsolatedProviderCapacity(t *testing.T) {
	store, mock := metricsStore(t)
	mock.positions = json.RawMessage(`[{"position":{"coin":"BTC","szi":"-2"}}]`)
	for _, side := range []string{"buy", "sell"} {
		r := baseRequest()
		r.Side = side
		value, err := store.OrderMetrics(context.Background(), r)
		if err != nil || value.MaximumQuantityError != "" || value.MaximumQuantity == "" || !strings.Contains(value.MaximumQuantityBasis, "maxTradeSzs") {
			t.Fatal(value, err)
		}
		// The provider capacity already resolves netting and account reservations.
		size := decimal.RequireFromString(value.MaximumQuantity)
		bound := decimal.NewFromInt(30)
		if side == "sell" {
			bound = decimal.NewFromInt(40)
		}
		if !size.IsPositive() || size.GreaterThanOrEqual(bound) {
			t.Fatal("fee reserve missing", value)
		}
	}
	r := baseRequest()
	r.Leverage = 10
	value, err := store.OrderMetrics(context.Background(), r)
	if err != nil || value.MaximumQuantity != "" || !strings.Contains(value.MaximumQuantityError, "selected leverage") {
		t.Fatal("capacity reused for different live leverage", value, err)
	}
}

func TestMaximumQuantityFailuresNeverFabricateFundedCapacity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*tradingMock)
	}{
		{"missing fees", func(m *tradingMock) { m.feeResponse = json.RawMessage(`{}`) }},
		{"invalid fees", func(m *tradingMock) { m.feeResponse = json.RawMessage(`{"userCrossRate":"NaN"}`) }},
		{"missing size", func(m *tradingMock) { m.bookLevels = json.RawMessage(`[[{"px":"100"}],[{"px":"100"}]]`) }},
		{"unsorted depth", func(m *tradingMock) {
			m.bookLevels = json.RawMessage(`[[{"px":"100","sz":"2"}],[{"px":"100","sz":"2"},{"px":"99","sz":"2"}]]`)
		}},
		{"wrong mark identity", func(m *tradingMock) {
			m.activeResponse = json.RawMessage(`{"user":"` + testAccount + `","coin":"ETH","markPx":"100"}`)
		}},
		{"unknown tiers", func(m *tradingMock) { m.marginTableID = nil }},
		{"shared collateral", func(m *tradingMock) { m.accountMode = "unifiedAccount" }},
		{"stale quote", func(m *tradingMock) { old := time.Now().Add(-time.Minute).UnixMilli(); m.quoteTime = &old }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := metricsStore(t)
			tc.setup(mock)
			value, err := store.OrderMetrics(context.Background(), baseRequest())
			if err != nil || value.MaximumQuantity != "" || value.MaximumQuantityError == "" {
				t.Fatal(value, err)
			}
		})
	}
}

func TestMaximumBuilderQuantityReservesDocumentedUpperFeeBound(t *testing.T) {
	store, mock := metricsStore(t)
	request := baseRequest()
	request.Instrument = "xyz:BTC"
	request.Leverage = 10
	value, err := store.OrderMetrics(context.Background(), request)
	if err != nil || value.MaximumQuantityError != "" || !strings.Contains(value.MaximumQuantityBasis, "six times") {
		t.Fatal(value, err)
	}
	size := decimal.RequireFromString(value.MaximumQuantity)
	cost := size.Mul(decimal.RequireFromString("11.2727")) // 100/10 + 1 adverse execution + 101*0.00045*6
	if cost.GreaterThan(decimal.NewFromInt(1000)) || len(mock.actions) != 0 {
		t.Fatal(value, cost)
	}
}

func TestMaximumQuantityHonorsProviderPerOrderNotionalCeiling(t *testing.T) {
	store, mock := metricsStore(t)
	mock.withdrawable = "100000000"
	mock.bookLevels = json.RawMessage(`[[{"px":"100","sz":"100000000"}],[{"px":"100","sz":"100000000"}]]`)
	r := baseRequest()
	r.Leverage = 20
	value, err := store.OrderMetrics(context.Background(), r)
	if err != nil || value.MaximumQuantityError != "" {
		t.Fatal(value, err)
	}
	size := decimal.RequireFromString(value.MaximumQuantity)
	if size.Mul(decimal.NewFromInt(101)).GreaterThan(decimal.NewFromInt(5000000)) || !size.Add(decimal.RequireFromString("0.001")).Mul(decimal.NewFromInt(101)).GreaterThan(decimal.NewFromInt(5000000)) {
		t.Fatal("per-order notional ceiling ignored", value)
	}
}
