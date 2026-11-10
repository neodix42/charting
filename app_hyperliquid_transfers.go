package main

import (
	"errors"

	"cryptodesk/internal/hyperliquid"
)

// Balances and transfers share the trading lifecycle, so closing the app
// cancels pending reads and drains any explicitly confirmed signed action.
func (a *App) GetHyperliquidBalances() (hyperliquid.AccountBalances, error) {
	if err := a.awaitBootstrap(false); err != nil {
		return hyperliquid.AccountBalances{}, err
	}
	if a.hyperliquid == nil {
		return hyperliquid.AccountBalances{}, errors.New("Hyperliquid is unavailable")
	}
	if err := a.beginTradeWork(); err != nil {
		return hyperliquid.AccountBalances{}, err
	}
	defer a.tradingWG.Done()
	ctx, cancel := a.tradingContext()
	defer cancel()
	return a.hyperliquid.AccountBalances(ctx)
}

func (a *App) PreviewHyperliquidTransfer(request hyperliquid.TransferRequest) (hyperliquid.TransferPreview, error) {
	if err := a.awaitBootstrap(false); err != nil {
		return hyperliquid.TransferPreview{}, err
	}
	if a.hyperliquid == nil {
		return hyperliquid.TransferPreview{}, errors.New("Hyperliquid is unavailable")
	}
	if err := a.beginTradeWork(); err != nil {
		return hyperliquid.TransferPreview{}, err
	}
	defer a.tradingWG.Done()
	ctx, cancel := a.tradingContext()
	defer cancel()
	return a.hyperliquid.PreviewTransfer(ctx, request)
}

func (a *App) SubmitHyperliquidTransfer(previewID string) (hyperliquid.TransferResult, error) {
	if err := a.awaitBootstrap(false); err != nil {
		return hyperliquid.TransferResult{}, err
	}
	if a.hyperliquid == nil {
		return hyperliquid.TransferResult{}, errors.New("Hyperliquid is unavailable")
	}
	if err := a.beginTradeWork(); err != nil {
		return hyperliquid.TransferResult{}, err
	}
	defer a.tradingWG.Done()
	ctx, cancel := a.tradingContext()
	defer cancel()
	return a.hyperliquid.SubmitTransfer(ctx, previewID)
}
