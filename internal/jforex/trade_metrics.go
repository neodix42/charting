package jforex

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
)

// TradeMetrics is read-only account and valuation information. Nil money fields
// are unavailable, while a nonnil zero is an actual SDK zero balance. Free margin
// is equity minus used margin; it is not a withdrawable-cash promise.
type TradeMetrics struct {
	Instrument         string   `json:"instrument"`
	Side               string   `json:"side"`
	EntryPrice         float64  `json:"entryPrice"`
	AccountCurrency    string   `json:"accountCurrency"`
	ProfitPerPriceUnit float64  `json:"profitPerPriceUnit"`
	ValuationTime      int64    `json:"valuationTime"`
	ValuationError     string   `json:"valuationError,omitempty"`
	ValuationSource    string   `json:"valuationSource,omitempty"`
	ValuationNotice    string   `json:"valuationNotice,omitempty"`
	Balance            *float64 `json:"balance,omitempty"`
	Equity             *float64 `json:"equity,omitempty"`
	UsedMargin         *float64 `json:"usedMargin,omitempty"`
	AvailableBalance   *float64 `json:"availableBalance,omitempty"`
	AccountLeverage    float64  `json:"accountLeverage"`
	AccountTime        int64    `json:"accountTime"`
	AccountNote        string   `json:"accountNote,omitempty"`
	AccountError       string   `json:"accountError,omitempty"`
	EntryError         string   `json:"entryError,omitempty"`
	MarginRequired     *float64 `json:"marginRequired,omitempty"`
	MarginError        string   `json:"marginError,omitempty"`
	LiquidationPrice   *float64 `json:"liquidationPrice,omitempty"`
	LiquidationError   string   `json:"liquidationError,omitempty"`
}

func (m *Manager) OrderMetrics(ctx context.Context, request TradeRequest) (TradeMetrics, error) {
	if err := validateTradeRequest(TradeRequest{Instrument: request.Instrument, Side: request.Side, Quantity: 1}); err != nil {
		return TradeMetrics{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	process, release, err := m.begin(ctx, true, "")
	if err != nil {
		return TradeMetrics{}, err
	}
	defer release()
	var version bridgeVersion
	if err = process.call(ctx, map[string]any{"op": "version"}, &version); err != nil {
		return TradeMetrics{}, err
	}
	if version.TradeMetrics != "account-metrics-v1" {
		return TradeMetrics{}, errors.New("The installed JForex bridge does not support account balance and trade metrics. Install this build's matching jforex-runtime, then restart CryptoDesk")
	}
	m.mu.Lock()
	config := m.config
	m.mu.Unlock()
	var metrics TradeMetrics
	// Amount/levels are intentionally absent: account balances and per-unit
	// valuation remain readable while the user edits an empty or invalid amount.
	if err = process.call(ctx, map[string]any{"op": "tradeMetrics", "instrument": request.Instrument, "side": request.Side}, &metrics); err != nil {
		return TradeMetrics{}, err
	}
	if metrics.Instrument != request.Instrument || metrics.Side != request.Side {
		return TradeMetrics{}, errors.New("JForex account metrics do not match the selected instrument")
	}
	normalizeTradeMetrics(&metrics)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.config != config {
		return TradeMetrics{}, errors.New("Dukascopy account changed while reading balances; refresh the metrics")
	}
	return metrics, nil
}
func normalizeTradeMetrics(metrics *TradeMetrics) {
	// Standalone account metrics do not request FX valuation. Preview snapshots
	// can still carry valuation metadata, which must retain its validation.
	if metrics.ProfitPerPriceUnit != 0 || metrics.ValuationTime != 0 || metrics.ValuationError != "" {
		normalizeValuation(&metrics.AccountCurrency, &metrics.ProfitPerPriceUnit, &metrics.ValuationTime, &metrics.ValuationError)
	}
	for _, field := range []struct {
		name   string
		number **float64
	}{{"balance", &metrics.Balance}, {"equity", &metrics.Equity}, {"used margin", &metrics.UsedMargin}, {"available balance", &metrics.AvailableBalance}} {
		if *field.number != nil && (math.IsNaN(**field.number) || math.IsInf(**field.number, 0)) {
			*field.number = nil
			metrics.AccountError = strings.TrimSpace(metrics.AccountError + " JForex " + field.name + " is unavailable")
		}
	}
	if metrics.EntryPrice <= 0 || math.IsNaN(metrics.EntryPrice) || math.IsInf(metrics.EntryPrice, 0) {
		metrics.EntryPrice = 0
		if metrics.EntryError == "" {
			metrics.EntryError = "No current executable JForex entry quote"
		}
	}
	// No supported SDK pre-trade-margin or standalone liquidation-price API is
	// available. Do not invent either value from account leverage or one position.
	metrics.MarginRequired = nil
	metrics.LiquidationPrice = nil
	if metrics.MarginError == "" {
		metrics.MarginError = "JForex does not expose a pre-trade required-margin quote"
	}
	if metrics.LiquidationError == "" {
		metrics.LiquidationError = "Dukascopy liquidation is governed by account-wide margin rules, not one standalone order price"
	}
}
