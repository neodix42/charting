package main

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"cryptodesk/internal/hyperliquid"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/trading"
)

// GetTradeMetrics is read-only. An empty or incomplete amount must not prevent
// the ticket from displaying verified account balances.
func (a *App) GetTradeMetrics(request trading.Request) (trading.Metrics, error) {
	result := trading.Metrics{FeedKey: request.FeedKey, Side: request.Side, Quantity: strings.TrimSpace(request.Quantity), Leverage: request.Leverage}
	if err := request.FeedKey.Validate(); err != nil {
		return result, err
	}
	if request.Side != "buy" && request.Side != "sell" {
		return result, errors.New("choose Buy or Sell")
	}
	if len(request.Quantity) > 128 {
		return result, errors.New("trade quantity exceeds the supported length")
	}
	if err := a.beginTradeWork(); err != nil {
		return result, err
	}
	defer a.tradingWG.Done()
	ctx, cancel := a.tradingContext()
	defer cancel()
	switch request.FeedKey.Exchange {
	case "hyperliquid":
		if a.hyperliquid == nil {
			return result, errors.New("configure Hyperliquid trading in Settings")
		}
		value, err := a.hyperliquid.OrderMetrics(ctx, hyperliquid.OrderRequest{Market: request.FeedKey.Market, Instrument: request.FeedKey.Instrument, Side: request.Side, Quantity: result.Quantity, Leverage: request.Leverage})
		if err != nil {
			return result, err
		}
		result.AccountMetrics, result.EntryPrice = hyperliquidAccountMetrics(value), value.EntryPrice
	case "dukascopy":
		if a.jforex == nil {
			return result, errors.New("configure Dukascopy in Settings")
		}
		value, err := a.jforex.OrderMetrics(ctx, jforex.TradeRequest{Instrument: request.FeedKey.Instrument, Side: request.Side})
		if err != nil {
			return result, err
		}
		result.AccountMetrics = dukascopyAccountMetrics(&value)
		if value.EntryPrice > 0 && !math.IsNaN(value.EntryPrice) && !math.IsInf(value.EntryPrice, 0) {
			result.EntryPrice = strconv.FormatFloat(value.EntryPrice, 'f', -1, 64)
		}
	default:
		return result, errors.New("account metrics are available only for Hyperliquid and Dukascopy charts")
	}
	return result, nil
}

func hyperliquidAccountMetrics(value hyperliquid.Metrics) trading.AccountMetrics {
	return trading.AccountMetrics{
		AvailableBalance: value.AvailableBalance, AvailableBalanceCurrency: value.AvailableBalanceCurrency,
		AvailableBalanceError: value.AvailableBalanceError, AvailableBalanceBasis: value.AvailableBalanceBasis,
		AccountBalance: value.AccountBalance, Equity: value.Equity, UsedMargin: value.UsedMargin,
		MarginRequired: value.MarginRequired, MarginRequiredCurrency: value.MarginRequiredCurrency,
		MarginError: value.MarginError, MarginBasis: value.MarginBasis,
		LiquidationPrice: value.LiquidationPrice, LiquidationError: value.LiquidationError,
		LiquidationBasis: value.LiquidationBasis, LiquidationKind: value.LiquidationKind, MetricsTime: value.MetricsTime,
	}
}

func metricMoney(value *float64) string {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func dukascopyAccountMetrics(value *jforex.TradeMetrics) trading.AccountMetrics {
	if value == nil {
		return trading.AccountMetrics{AvailableBalanceError: "The installed JForex bridge did not return account metrics; install this build's matching runtime and restart", MarginError: "JForex does not expose a pre-trade required-margin quote", LiquidationKind: "not-applicable", LiquidationError: "Dukascopy liquidation is governed by account-wide margin rules"}
	}
	result := trading.AccountMetrics{
		AvailableBalance: metricMoney(value.AvailableBalance), AvailableBalanceCurrency: value.AccountCurrency,
		AvailableBalanceBasis: value.AccountNote,
		AccountBalance:        metricMoney(value.Balance), Equity: metricMoney(value.Equity), UsedMargin: metricMoney(value.UsedMargin),
		MarginRequired: metricMoney(value.MarginRequired), MarginRequiredCurrency: value.AccountCurrency,
		MarginError:     value.MarginError,
		LiquidationKind: "not-applicable", LiquidationError: value.LiquidationError,
		MetricsTime: value.AccountTime,
	}
	// A partial SDK-field or conversion failure must not conceal a confirmed
	// available balance. Show its reason only when that particular value is absent.
	if result.AvailableBalance == "" {
		result.AvailableBalanceError = value.AccountError
		if result.AvailableBalanceError == "" {
			result.AvailableBalanceError = "JForex did not provide current equity and used margin in the account currency"
		}
	}
	return result
}
