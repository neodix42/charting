package jforex

import (
	"context"
	"errors"
	"math"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// TradeMetrics is read-only account and valuation information. Nil money fields
// are unavailable, while a nonnil zero is an actual SDK zero balance. Free margin
// is equity minus used margin; it is not a withdrawable-cash promise.
type TradeMetrics struct {
	Instrument           string   `json:"instrument"`
	Side                 string   `json:"side"`
	EntryPrice           float64  `json:"entryPrice"`
	AccountCurrency      string   `json:"accountCurrency"`
	ProfitPerPriceUnit   float64  `json:"profitPerPriceUnit"`
	ValuationTime        int64    `json:"valuationTime"`
	ValuationError       string   `json:"valuationError,omitempty"`
	ValuationSource      string   `json:"valuationSource,omitempty"`
	ValuationNotice      string   `json:"valuationNotice,omitempty"`
	Balance              *float64 `json:"balance,omitempty"`
	Equity               *float64 `json:"equity,omitempty"`
	UsedMargin           *float64 `json:"usedMargin,omitempty"`
	AvailableBalance     *float64 `json:"availableBalance,omitempty"`
	AccountLeverage      float64  `json:"accountLeverage"`
    WeekendLeverage float64 `json:"weekendLeverage,omitempty"`
    InstrumentMaxLeverage float64 `json:"instrumentMaxLeverage,omitempty"`
    EffectiveLeverage float64 `json:"effectiveLeverage,omitempty"`
    MarginCutUsePercent float64 `json:"marginCutUsePercent,omitempty"`
    AccountEquityStopLoss float64 `json:"accountEquityStopLoss,omitempty"`
    MarginPerUnit *float64 `json:"marginPerUnit,omitempty"`
    QuantityStep float64 `json:"quantityStep,omitempty"`
	AccountTime          int64    `json:"accountTime"`
	AccountNote          string   `json:"accountNote,omitempty"`
	AccountError         string   `json:"accountError,omitempty"`
	EntryError           string   `json:"entryError,omitempty"`
	MarginRequired       *float64 `json:"marginRequired,omitempty"`
	MarginError          string   `json:"marginError,omitempty"`
	MarginBasis          string   `json:"marginBasis,omitempty"`
	MaximumQuantity      string   `json:"maximumQuantity,omitempty"`
	MaximumQuantityError string   `json:"maximumQuantityError,omitempty"`
	MaximumQuantityBasis string   `json:"maximumQuantityBasis,omitempty"`
	LiquidationPrice     *float64 `json:"liquidationPrice,omitempty"`
	LiquidationError     string   `json:"liquidationError,omitempty"`
}

var maximumQuantityDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)

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
		return TradeMetrics{}, errors.New("The selected JForex bridge does not support account balance and trade metrics. Install this build's matching jforex-runtime, then restart CryptoDesk")
	}
	m.mu.Lock()
	config := m.config
	m.mu.Unlock()
	var metrics TradeMetrics
	// Empty quantity still reads balances and the independent maximum size.
	quantity := request.Quantity
	if quantity < 0 || math.IsNaN(quantity) || math.IsInf(quantity, 0) {
		quantity = 0
	}
	if err = process.call(ctx, map[string]any{"op": "tradeMetrics", "instrument": request.Instrument, "side": request.Side, "quantity": quantity, "leverage": request.Leverage}, &metrics); err != nil {
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
	if metrics.MarginRequired != nil && (math.IsNaN(*metrics.MarginRequired) || math.IsInf(*metrics.MarginRequired, 0) || *metrics.MarginRequired < 0) {
		metrics.MarginRequired = nil
		metrics.MarginError = "JForex margin estimate is invalid"
	}
	// Legacy bridge snapshots cannot supply a size calculated with instrument rules.
	if metrics.MaximumQuantity != "" {
		if len(metrics.MaximumQuantity) > 100 || !maximumQuantityDecimal.MatchString(metrics.MaximumQuantity) {
			metrics.MaximumQuantity = ""
			metrics.MaximumQuantityError = "JForex maximum quantity is invalid"
		}
		value, ok := new(big.Rat).SetString(metrics.MaximumQuantity)
		if !ok || value.Sign() < 0 {
			metrics.MaximumQuantity = ""
			metrics.MaximumQuantityError = "JForex maximum quantity is invalid"
		}
	}
	if metrics.MaximumQuantity == "" && metrics.MaximumQuantityError == "" {
		metrics.MaximumQuantityError = "The selected JForex bridge does not supply maximum order quantity. Install this build's matching jforex-runtime and restart CryptoDesk"
	}
	// Liquidation remains account-wide, rather than one standalone order price.
	metrics.LiquidationPrice = nil
	if metrics.MarginRequired == nil && metrics.MarginError == "" {
		metrics.MarginError = "JForex does not expose a pre-trade required-margin quote"
	}
	if metrics.LiquidationError == "" {
		metrics.LiquidationError = "Dukascopy liquidation is governed by account-wide margin rules, not one standalone order price"
	}
}
