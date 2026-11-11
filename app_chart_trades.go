package main

import (
	"cryptodesk/internal/domain"
	"cryptodesk/internal/trading"
	"errors"
)

// GetChartTrades finds local candidates without logging in or importing external
// orders. RefreshTradeHistory must verify their current executions before display.
func (a *App) GetChartTrades(keys []domain.FeedKey) (trading.HistoryPage, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.HistoryPage{}, err
	}
	defer a.tradingWG.Done()
	if len(keys) > 9 {
		return trading.HistoryPage{}, errors.New("Request trade levels for at most nine active charts")
	}
	wanted := map[string]bool{}
	identity := func(source, market, symbol string) string {
		if source == "dukascopy" {
			market = "jforex"
		}
		return source + "|" + market + "|" + symbol
	}
	sources := map[string]bool{}
	for _, key := range keys {
		if err := key.Validate(); err != nil {
			return trading.HistoryPage{}, err
		}
		if key.Exchange != "dukascopy" && !(key.Exchange == "hyperliquid" && key.Market == "perpetual") {
			continue
		}
		sources[key.Exchange] = true
		wanted[identity(key.Exchange, key.Market, key.Instrument)] = true
	}
	out := trading.HistoryPage{Rows: []trading.Trade{}, Errors: []string{}, Limit: 100}
	add := func(rows []trading.Trade, issues []string, account, environment string, legacy bool) {
		out.Errors = append(out.Errors, issues...)
		for _, row := range rows {
			if !wanted[identity(row.Provider, row.Market, row.Instrument)] || row.Environment != environment || row.Account != account && !(legacy && row.Account == "") {
				continue
			}
			switch row.Status {
			case "closed", "rejected", "cancelled", "filled":
				continue
			}
			out.Rows = append(out.Rows, row)
		}
	}
	if sources["hyperliquid"] && a.hyperliquid != nil {
		info := a.hyperliquid.Info()
		if info.AccountAddress != "" {
			rows, issues := a.hyperliquid.TradeHistory()
			add(rows, issues, info.AccountAddress, info.Network, false)
		}
	}
	if sources["dukascopy"] && a.jforex != nil {
		info := a.jforex.Info()
		if info.Username != "" && info.PasswordConfigured {
			rows, issues := a.jforex.TradeHistory()
			add(rows, issues, info.Username, info.Environment, true)
		}
	}
	if len(out.Rows) > out.Limit {
		return trading.HistoryPage{}, errors.New("More than 100 open platform trades match these charts; trade-level refresh is unavailable")
	}
	out.Total = len(out.Rows)
	return out, nil
}
