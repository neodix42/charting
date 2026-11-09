package main

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/hyperliquid"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/trading"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) OpenHyperliquidHelp(kind string) error {
	links := map[string]string{
		"api":        "https://app.hyperliquid.xyz/API",
		"testnetApi": "https://app.hyperliquid-testnet.xyz/API",
		"docs":       "https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/nonces-and-api-wallets",
	}
	url, ok := links[kind]
	if !ok {
		return errors.New("unknown Hyperliquid help page")
	}
	if a.ctx == nil {
		return errors.New("desktop browser integration is unavailable")
	}
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}

func (a *App) GetHyperliquid() hyperliquid.Info { return a.hyperliquid.Info() }
func (a *App) ConfigureHyperliquid(settings hyperliquid.Settings) (hyperliquid.Info, error) {
	return a.hyperliquid.Configure(settings)
}
func (a *App) tradingContext() (context.Context, context.CancelFunc) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, 90*time.Second)
}
func (a *App) TestHyperliquidConnection() (hyperliquid.Info, error) {
	if err := a.beginTradeWork(); err != nil {
		return hyperliquid.Info{}, err
	}
	defer a.tradingWG.Done()
	ctx, cancel := a.tradingContext()
	defer cancel()
	return a.hyperliquid.Test(ctx)
}

func (a *App) GetTradingStatus(key domain.FeedKey) (trading.Status, error) {
	status := trading.Status{QuantityLabel: "base units"}
	if err := key.Validate(); err != nil {
		return status, err
	}
	switch key.Exchange {
	case "hyperliquid":
		if a.hyperliquid == nil {
			return status, nil
		}
		info := a.hyperliquid.Info()
		status.Configured = info.PrivateKeyConfigured && info.AccountAddress != ""
		status.Enabled, status.CanBuy, status.CanSell = status.Configured, status.Configured, status.Configured
		status.Environment, status.Error = info.Network, info.Error
		status.LeverageSupported = key.Market == "perpetual"
		status.ProtectiveOrdersSupported = key.Market == "perpetual"
		if key.Market == "spot" {
			status.MaxLeverage = 1
			if info.Network == "testnet" {
				status.Enabled = false
				status.CanBuy = false
				status.CanSell = false
				status.Error = "Spot chart IDs are mainnet IDs; testnet spot trading is unavailable to prevent trading a different token"
			}
		}
	case "dukascopy":
		if a.jforex == nil {
			return status, nil
		}
		info := a.jforex.Info()
		status.Configured = info.PasswordConfigured && info.Username != ""
		status.Enabled = status.Configured && info.RuntimeAvailable
		status.CanBuy, status.CanSell = status.Enabled, status.Enabled
		status.Environment, status.Error = info.Environment, info.Error
		status.ProtectiveOrdersSupported = true
	}
	return status, nil
}

func tradeNumber(value string, optional bool) (float64, error) {
	if optional && strings.TrimSpace(value) == "" {
		return 0, nil
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || (!optional && number == 0) {
		return 0, errors.New("trade quantities and prices must be finite positive numbers")
	}
	return number, nil
}

func (a *App) PreviewTrade(request trading.Request) (trading.Preview, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.Preview{}, err
	}
	defer a.tradingWG.Done()
	result := trading.Preview{FeedKey: request.FeedKey, Side: request.Side, Quantity: request.Quantity, StopLoss: request.StopLoss, TakeProfit: request.TakeProfit, Warnings: []string{}}
	if err := request.FeedKey.Validate(); err != nil {
		return result, err
	}
	if len(request.Quantity) > 128 || len(request.StopLoss) > 128 || len(request.TakeProfit) > 128 {
		return result, errors.New("trade numbers exceed the supported length")
	}
	if request.Side != "buy" && request.Side != "sell" {
		return result, errors.New("choose Buy or Sell")
	}
	ctx, cancel := a.tradingContext()
	defer cancel()
	switch request.FeedKey.Exchange {
	case "hyperliquid":
		if a.hyperliquid == nil {
			return result, errors.New("configure Hyperliquid trading in Settings")
		}
		preview, err := a.hyperliquid.Preview(ctx, hyperliquid.OrderRequest{Market: request.FeedKey.Market, Instrument: request.FeedKey.Instrument, Side: request.Side, Quantity: request.Quantity, StopLoss: request.StopLoss, TakeProfit: request.TakeProfit, Leverage: request.Leverage})
		if err != nil {
			return result, err
		}
		result.ID, result.EntryPrice, result.Quantity = "hyperliquid:"+preview.ID, preview.ReferencePrice, preview.Request.Quantity
		result.Leverage, result.Environment, result.ExpiresAt = float64(preview.Request.Leverage), preview.Network, preview.ExpiresAt
		result.Warnings = append(result.Warnings, preview.Warnings...)
		result.MaxLeverage = preview.MaxLeverage
		result.AccountMetrics = hyperliquidAccountMetrics(preview.Metrics)
		result.AccountCurrency, result.ProfitPerPriceUnit, result.ValuationTime, result.ValuationError = preview.AccountCurrency, preview.ProfitPerPriceUnit, preview.ValuationTime, preview.ValuationError
	case "dukascopy":
		if a.jforex == nil {
			return result, errors.New("configure Dukascopy in Settings")
		}
		quantity, err := tradeNumber(request.Quantity, false)
		if err != nil {
			return result, err
		}
		stop, err := tradeNumber(request.StopLoss, true)
		if err != nil {
			return result, err
		}
		target, err := tradeNumber(request.TakeProfit, true)
		if err != nil {
			return result, err
		}
		preview, err := a.jforex.PreviewTrade(ctx, jforex.TradeRequest{Instrument: request.FeedKey.Instrument, Side: request.Side, Quantity: quantity, StopLoss: stop, TakeProfit: target, Leverage: float64(request.Leverage)})
		if err != nil {
			return result, err
		}
		result.ID, result.EntryPrice = "dukascopy:"+preview.ID, strconv.FormatFloat(preview.EntryPrice, 'f', -1, 64)
		result.Quantity = strconv.FormatFloat(preview.Quantity, 'f', -1, 64)
		result.Leverage, result.Environment, result.ExpiresAt = preview.AccountLeverage, preview.Environment, preview.ExpiresAt
		result.Warnings = append(result.Warnings, preview.Message)
		result.AccountCurrency, result.ValuationTime, result.ValuationError = preview.AccountCurrency, preview.ValuationTime, preview.ValuationError
		result.ValuationSource, result.ValuationNotice = preview.ValuationSource, preview.ValuationNotice
		result.AccountMetrics = dukascopyAccountMetrics(preview.Metrics)
		if preview.ProfitPerPriceUnit > 0 && !math.IsNaN(preview.ProfitPerPriceUnit) && !math.IsInf(preview.ProfitPerPriceUnit, 0) {
			result.ProfitPerPriceUnit = strconv.FormatFloat(preview.ProfitPerPriceUnit, 'f', -1, 64)
		}
	default:
		return result, errors.New("trading is available only on Hyperliquid and Dukascopy charts")
	}
	return result, nil
}

func (a *App) SubmitTrade(previewID string) (trading.Result, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.Result{}, err
	}
	defer a.tradingWG.Done()
	result := trading.Result{OrderIDs: []string{}}
	provider, id, valid := strings.Cut(previewID, ":")
	if !valid || id == "" || len(previewID) > 256 {
		return result, errors.New("invalid trade confirmation")
	}
	ctx, cancel := a.tradingContext()
	defer cancel()
	switch provider {
	case "hyperliquid":
		if a.hyperliquid == nil {
			return result, errors.New("Hyperliquid trading is unavailable")
		}
		order, err := a.hyperliquid.Submit(ctx, id)
		if err != nil {
			return result, err
		}
		result = hyperliquidTradeResult(order)
	case "dukascopy":
		if a.jforex == nil {
			return result, errors.New("Dukascopy trading is unavailable")
		}
		order, err := a.jforex.SubmitTrade(ctx, id)
		if err != nil {
			return result, err
		}
		result.Status, result.Message = order.Status, order.Message
		if order.OrderID != "" {
			result.OrderIDs = append(result.OrderIDs, order.OrderID)
		}
		if result.Status == "unknown" {
			result.Status = "uncertain"
		}
		if result.Status == "filled" {
			result.Status = "submitted"
			if !order.ProtectionConfirmed {
				result.Status = "partial"
			}
		}
	default:
		return result, errors.New("invalid trade provider")
	}
	return result, nil
}

func (a *App) beginTradeWork() error {
	a.tradingMu.Lock()
	defer a.tradingMu.Unlock()
	if a.tradingClosed {
		return errors.New("Trading is closing; inspect your exchange account for outstanding orders")
	}
	a.tradingWG.Add(1)
	return nil
}
func (a *App) closeTrading() {
	a.tradingMu.Lock()
	a.tradingClosed = true
	a.tradingMu.Unlock()
	a.tradingWG.Wait()
}

func hyperliquidTradeResult(order hyperliquid.OrderResult) trading.Result {
	result := trading.Result{OrderIDs: []string{}, Message: order.Message, Status: order.Status}
	switch result.Status {
	case "filled":
		result.Status = "submitted"
	case "failed":
		result.Status = "rejected"
	case "partial", "submitted", "rejected", "uncertain":
	default:
		result.Status = "uncertain"
	}
	for _, id := range order.OrderIDs {
		result.OrderIDs = append(result.OrderIDs, strconv.FormatInt(id, 10))
	}
	return result
}

// EstimateTrade retrieves current provider quote, settlement and leverage metadata.
// It neither creates a confirmation token nor submits an order.
func (a *App) EstimateTrade(key domain.FeedKey, side string) (trading.Estimate, error) {
	result := trading.Estimate{FeedKey: key, Side: side}
	if err := key.Validate(); err != nil {
		return result, err
	}
	if key.Exchange != "dukascopy" && key.Exchange != "hyperliquid" {
		return result, errors.New("trade estimates are available only for Hyperliquid and Dukascopy charts")
	}
	if side != "buy" && side != "sell" {
		return result, errors.New("choose Buy or Sell")
	}
	if err := a.beginTradeWork(); err != nil {
		return result, err
	}
	defer a.tradingWG.Done()
	ctx, cancel := a.tradingContext()
	defer cancel()
	if key.Exchange == "hyperliquid" {
		if a.hyperliquid == nil {
			return result, errors.New("Hyperliquid trading is unavailable")
		}
		value, err := a.hyperliquid.Estimate(ctx, key.Market, key.Instrument, side)
		if err != nil {
			return result, err
		}
		result.EntryPrice, result.MaxLeverage = value.ReferencePrice, value.MaxLeverage
		result.AccountCurrency, result.ProfitPerPriceUnit, result.ValuationTime, result.ValuationError = value.AccountCurrency, value.ProfitPerPriceUnit, value.ValuationTime, value.ValuationError
		return result, nil
	}
	if a.jforex == nil {
		return result, errors.New("Dukascopy trading is unavailable")
	}
	value, err := a.jforex.EstimateTrade(ctx, key.Instrument, side)
	if err != nil {
		return result, err
	}
	result.EntryPrice = strconv.FormatFloat(value.EntryPrice, 'f', -1, 64)
	result.AccountCurrency, result.ValuationTime, result.ValuationError = value.AccountCurrency, value.ValuationTime, value.ValuationError
	result.ValuationSource, result.ValuationNotice = value.ValuationSource, value.ValuationNotice
	if value.ProfitPerPriceUnit > 0 && !math.IsNaN(value.ProfitPerPriceUnit) && !math.IsInf(value.ProfitPerPriceUnit, 0) {
		result.ProfitPerPriceUnit = strconv.FormatFloat(value.ProfitPerPriceUnit, 'f', -1, 64)
	}
	return result, nil
}
