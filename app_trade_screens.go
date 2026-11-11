package main

import (
	"cryptodesk/internal/trading"
	"errors"
)

func (a *App) AddTradeScreen(tradeID, fileName, dataURL string) (trading.TradeScreen, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.TradeScreen{}, err
	}
	defer a.tradingWG.Done()
	provider, id, err := splitTradeID(tradeID)
	if err != nil {
		return trading.TradeScreen{}, err
	}
	var row trading.Trade
	switch provider {
	case "dukascopy":
		if a.jforex == nil {
			return trading.TradeScreen{}, errors.New("The recorded trade is unavailable")
		}
		row, err = a.jforex.LocalTrade(id)
	case "hyperliquid":
		if a.hyperliquid == nil {
			return trading.TradeScreen{}, errors.New("The recorded trade is unavailable")
		}
		row, err = a.hyperliquid.LocalTrade(id)
	}
	if err != nil || row.ID != tradeID {
		return trading.TradeScreen{}, errors.New("Attach screens only to a locally recorded platform trade")
	}
	store := a.entryConditionStore()
	if store == nil {
		return trading.TradeScreen{}, errors.New("The local trade settings directory is unavailable")
	}
	return store.AddScreen(row, fileName, dataURL)
}

func (a *App) GetTradeScreen(tradeID, screenID string) (string, error) {
	if err := a.beginTradeWork(); err != nil {
		return "", err
	}
	defer a.tradingWG.Done()
	store := a.entryConditionStore()
	if store == nil {
		return "", errors.New("The local trade settings directory is unavailable")
	}
	return store.ReadScreen(tradeID, screenID)
}

func (a *App) QueryTradeScreens(query trading.TradeScreenQuery) (trading.TradeScreenPage, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.TradeScreenPage{}, err
	}
	defer a.tradingWG.Done()
	store := a.entryConditionStore()
	if store == nil {
		return trading.TradeScreenPage{}, errors.New("The local trade settings directory is unavailable")
	}
	return store.QueryScreens(query)
}
