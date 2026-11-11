package main

import (
	"cryptodesk/internal/trading"
	"errors"
	"sort"
	"strings"
)

func splitTradeID(value string) (string, string, error) {
	provider, id, ok := strings.Cut(value, ":")
	if !ok || !trading.ValidIntent(id) || provider != "dukascopy" && provider != "hyperliquid" {
		return "", "", errors.New("Select a locally recorded CryptoDesk trade")
	}
	return provider, id, nil
}

func (a *App) entryConditionStore() *trading.EntryConditions {
	a.tradingMu.Lock()
	defer a.tradingMu.Unlock()
	if a.entryConditions == nil && a.store != nil {
		a.entryConditions = trading.NewEntryConditions(a.store.Directory())
	}
	return a.entryConditions
}

func (a *App) annotateTradeHistory(rows []trading.Trade) error {
	store := a.entryConditionStore()
	if store == nil {
		return nil
	}
	notes, err := store.MetadataSnapshot()
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].EntryCondition = notes[rows[i].ID].EntryCondition
		rows[i].Screens = notes[rows[i].ID].Screens
	}
	return nil
}

func (a *App) SetTradeEntryCondition(id, value string) (string, error) {
	if err := a.beginTradeWork(); err != nil {
		return "", err
	}
	defer a.tradingWG.Done()
	if _, _, err := splitTradeID(id); err != nil {
		return "", err
	}
	store := a.entryConditionStore()
	if store == nil {
		return "", errors.New("The local trade settings directory is unavailable")
	}
	return store.Set(id, value)
}
func (a *App) GetTradeHistory(offset, limit int) (trading.HistoryPage, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.HistoryPage{}, err
	}
	defer a.tradingWG.Done()
	if offset < 0 || offset > 20000 || limit < 1 || limit > 100 {
		return trading.HistoryPage{}, errors.New("Invalid trade history page")
	}
	out := trading.HistoryPage{Rows: []trading.Trade{}, Errors: []string{}, Offset: offset, Limit: limit}
	rows := []trading.Trade{}
	if a.hyperliquid != nil {
		items, issues := a.hyperliquid.TradeHistory()
		rows = append(rows, items...)
		out.Errors = append(out.Errors, issues...)
	}
	if a.jforex != nil {
		items, issues := a.jforex.TradeHistory()
		rows = append(rows, items...)
		out.Errors = append(out.Errors, issues...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].SubmittedAt != rows[j].SubmittedAt {
			return rows[i].SubmittedAt > rows[j].SubmittedAt
		}
		return rows[i].ID < rows[j].ID
	})
	out.Total = len(rows)
	start := min(offset, len(rows))
	out.Rows = append(out.Rows, rows[start:min(start+limit, len(rows))]...)
	if err := a.annotateTradeHistory(out.Rows); err != nil {
		out.Errors = append(out.Errors, "Trade annotations: "+err.Error())
	}
	return out, nil
}
func (a *App) RefreshTradeHistory(ids []string) ([]trading.Trade, error) {
	if err := a.beginTradeWork(); err != nil {
		return nil, err
	}
	defer a.tradingWG.Done()
	if len(ids) > 100 {
		return nil, errors.New("Refresh at most 100 platform trades at a time")
	}
	ctx, cancel := a.tradingContext()
	defer cancel()
	rows := []trading.Trade{}
	seen := map[string]bool{}
	for _, value := range ids {
		provider, id, err := splitTradeID(value)
		if err != nil {
			return nil, err
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		var row trading.Trade
		switch provider {
		case "hyperliquid":
			if a.hyperliquid == nil {
				return nil, errors.New("Hyperliquid is unavailable")
			}
			row, err = a.hyperliquid.RefreshTrade(ctx, id)
		case "dukascopy":
			if a.jforex == nil {
				return nil, errors.New("JForex is unavailable")
			}
			row, err = a.jforex.RefreshTrade(ctx, id)
		}
		if err != nil {
			if row.ID == "" {
				row = trading.Trade{ID: value, Provider: provider}
			}
			row.Error = err.Error()
			row.CanClose = false
			row.CloseReason = err.Error()
		}
		rows = append(rows, row)
	}
	if err := a.annotateTradeHistory(rows); err != nil {
		return nil, errors.New("Trade annotations: " + err.Error())
	}
	return rows, nil
}
func (a *App) PreviewTradeClose(value string) (trading.ClosePreview, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.ClosePreview{}, err
	}
	defer a.tradingWG.Done()
	provider, id, err := splitTradeID(value)
	if err != nil {
		return trading.ClosePreview{}, err
	}
	ctx, cancel := a.tradingContext()
	defer cancel()
	var preview trading.ClosePreview
	switch provider {
	case "hyperliquid":
		if a.hyperliquid == nil {
			return preview, errors.New("Hyperliquid is unavailable")
		}
		preview, err = a.hyperliquid.PreviewClose(ctx, id)
	case "dukascopy":
		if a.jforex == nil {
			return preview, errors.New("JForex is unavailable")
		}
		preview, err = a.jforex.PreviewClose(ctx, id)
	}
	if err == nil {
		preview.ID = provider + ":" + preview.ID
	}
	return preview, err
}
func (a *App) ConfirmTradeClose(value string) (trading.Trade, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.Trade{}, err
	}
	defer a.tradingWG.Done()
	provider, id, err := splitTradeID(value)
	if err != nil {
		return trading.Trade{}, err
	}
	ctx, cancel := a.tradingContext()
	defer cancel()
	switch provider {
	case "hyperliquid":
		if a.hyperliquid != nil {
			return a.hyperliquid.ConfirmClose(ctx, id)
		}
	case "dukascopy":
		if a.jforex != nil {
			return a.jforex.ConfirmClose(ctx, id)
		}
	}
	return trading.Trade{}, errors.New("Trade provider is unavailable")
}
