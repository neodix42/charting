package jforex

import (
	"context"
	"cryptodesk/internal/domain"
	"errors"
	"time"
)

// DownloadHistory reuses the account's authoritative candle cache while giving
// bounded, closed native daily downloads their own SDK work deadline. Ordinary
// chart requests keep the shorter History deadline.
func (m *Manager) DownloadHistory(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	const day = int64(24 * time.Hour / time.Second)
	native := key.Instrument == "BTCUSD" && key.Market == "cfd" || key.Instrument == "XAUXAG" && key.Market == "metal"
	if !native || key.Timeframe != "1d" || from < 0 || to <= from || from%day != 0 || to%day != 0 || to-from > 366*day || to > time.Now().Unix()/day*day || limit < 1 || limit > 1000 {
		return nil, errors.New("Native JForex downloads require BTCUSD/XAUXAG and at most 366 closed UTC daily candles")
	}
	return m.history(ctx, key, from, to, limit, true)
}
