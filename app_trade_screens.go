package main

import (
	"context"
	"cryptodesk/internal/desktopimage"
	"cryptodesk/internal/trading"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func (a *App) AddTradeScreen(tradeID, fileName, dataURL string) (trading.TradeScreen, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.TradeScreen{}, err
	}
	defer a.tradingWG.Done()
	store, row, err := a.tradeScreenTarget(tradeID)
	if err != nil {
		return trading.TradeScreen{}, err
	}
	return store.AddScreen(row, fileName, dataURL)
}

// Native WebKit clipboard events may omit image files even though GTK can read
// the clipboard. Read only on an explicit paste action, with a bounded lifetime.
func (a *App) PasteTradeScreen(tradeID string) (trading.TradeScreen, error) {
	return a.pasteTradeScreen(tradeID, desktopimage.ReadClipboard)
}

func (a *App) pasteTradeScreen(tradeID string, read func(context.Context) (string, error)) (trading.TradeScreen, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.TradeScreen{}, err
	}
	defer a.tradingWG.Done()
	store, row, err := a.tradeScreenTarget(tradeID)
	if err != nil {
		return trading.TradeScreen{}, err
	}
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	dataURL, err := read(ctx)
	if err != nil {
		return trading.TradeScreen{}, err
	}
	if err := ctx.Err(); err != nil {
		return trading.TradeScreen{}, err
	}
	return store.AddScreen(row, "", dataURL)
}

// Wails supplies filesystem paths for native drops; browser File objects are
// not consistently available on GTK. Validate and retain the original bytes.
func (a *App) AddTradeScreenFile(tradeID, filePath string) (trading.TradeScreen, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.TradeScreen{}, err
	}
	defer a.tradingWG.Done()
	store, row, err := a.tradeScreenTarget(tradeID)
	if err != nil {
		return trading.TradeScreen{}, err
	}
	if !filepath.IsAbs(filePath) {
		return trading.TradeScreen{}, errors.New("Drop an image file from your file manager")
	}
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return trading.TradeScreen{}, errors.New("The dropped image must be a readable regular file")
	}
	if info.Size() <= 0 || info.Size() > trading.MaxTradeScreenBytes {
		return trading.TradeScreen{}, errors.New("Choose an image up to 32 MiB")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return trading.TradeScreen{}, errors.New("The dropped image could not be read")
	}
	defer file.Close()
	if opened, err := file.Stat(); err != nil || !opened.Mode().IsRegular() {
		return trading.TradeScreen{}, errors.New("The dropped image must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, trading.MaxTradeScreenBytes+1))
	if err != nil || len(data) == 0 || len(data) > trading.MaxTradeScreenBytes {
		return trading.TradeScreen{}, errors.New("The image could not be read; choose an image up to 32 MiB")
	}
	dataURL := "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
	return store.AddScreen(row, filepath.Base(filePath), dataURL)
}

func (a *App) tradeScreenTarget(tradeID string) (*trading.EntryConditions, trading.Trade, error) {
	provider, id, err := splitTradeID(tradeID)
	if err != nil {
		return nil, trading.Trade{}, err
	}
	var row trading.Trade
	switch provider {
	case "dukascopy":
		if a.jforex == nil {
			return nil, trading.Trade{}, errors.New("The recorded trade is unavailable")
		}
		row, err = a.jforex.LocalTrade(id)
	case "hyperliquid":
		if a.hyperliquid == nil {
			return nil, trading.Trade{}, errors.New("The recorded trade is unavailable")
		}
		row, err = a.hyperliquid.LocalTrade(id)
	}
	if err != nil || row.ID != tradeID {
		return nil, trading.Trade{}, errors.New("Attach screens only to a locally recorded platform trade")
	}
	store := a.entryConditionStore()
	if store == nil {
		return nil, trading.Trade{}, errors.New("The local trade settings directory is unavailable")
	}
	return store, row, nil
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

func (a *App) RemoveTradeScreen(tradeID, screenID string) (trading.TradeScreenRemoval, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.TradeScreenRemoval{}, err
	}
	defer a.tradingWG.Done()
	if _, _, err := splitTradeID(tradeID); err != nil {
		return trading.TradeScreenRemoval{}, err
	}
	store := a.entryConditionStore()
	if store == nil {
		return trading.TradeScreenRemoval{}, errors.New("The local trade settings directory is unavailable")
	}
	return store.RemoveScreen(tradeID, screenID)
}

// Filtering reads all local platform journals, not the visible history page,
// and never refreshes accounts or makes exchange requests.
func (a *App) GetTradeScreenGallery(query trading.TradeScreenGalleryQuery) (trading.TradeScreenGalleryPage, error) {
	if err := a.beginTradeWork(); err != nil {
		return trading.TradeScreenGalleryPage{}, err
	}
	defer a.tradingWG.Done()
	store := a.entryConditionStore()
	if store == nil {
		return trading.TradeScreenGalleryPage{}, errors.New("The local trade settings directory is unavailable")
	}
	rows := []trading.Trade{}
	issues := []string{}
	if a.hyperliquid != nil {
		trades, errors := a.hyperliquid.TradeHistory()
		rows = append(rows, trades...)
		issues = append(issues, errors...)
	}
	if a.jforex != nil {
		trades, errors := a.jforex.TradeHistory()
		rows = append(rows, trades...)
		issues = append(issues, errors...)
	}
	page, err := store.ScreenGallery(rows, query)
	page.Errors = append(page.Errors, issues...)
	return page, err
}
