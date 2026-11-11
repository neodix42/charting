package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"cryptodesk/internal/candlestorage"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/screener"
)

func (a *App) GetDucascopy() jforex.Info { return a.jforex.Info() }

func (a *App) RefreshDucascopyInstruments(force bool) ([]domain.Instrument, error) {
	ctx, cancel := a.ducascopyContext()
	defer cancel()
	items, err := a.jforex.RefreshInstruments(ctx, force)
	if err == nil && a.market != nil {
		a.market.InvalidateInstruments("dukascopy")
	}
	return items, err
}

func (a *App) ConfigureDucascopy(settings jforex.Settings) (jforex.Info, error) {
	a.jforexMu.Lock()
	defer a.jforexMu.Unlock()
	before := a.jforex.Info()
	environment := strings.ToLower(strings.TrimSpace(settings.Environment))
	if environment == "" {
		environment = "demo"
	}
	changed := before.Username != strings.TrimSpace(settings.Username) || before.Environment != environment || settings.Password != "" || settings.ClearPassword
	firstSetup := !before.PasswordConfigured
	if changed && !firstSetup && a.market != nil && a.market.ProviderActive("dukascopy") {
		return before, errors.New("Close or reselect active Dukascopy charts before changing JForex credentials")
	}
	info, err := a.jforex.Configure(settings)
	if err == nil && changed && a.market != nil {
		if firstSetup {
			a.market.RestartUnconfiguredProvider("dukascopy")
		} else {
			a.market.ForgetProvider("dukascopy")
		}
	}
	return info, err
}

func (a *App) ducascopyContext() (context.Context, context.CancelFunc) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// First connection may download the verified SDK dependencies before login.
	return context.WithTimeout(ctx, 5*time.Minute)
}

func (a *App) TestDucascopyConnection() (jforex.Info, error) {
	return a.TestDucascopyConnectionWithPIN("")
}

func (a *App) TestDucascopyConnectionWithPIN(pin string) (jforex.Info, error) {
	ctx, cancel := a.ducascopyContext()
	defer cancel()
	info, err := a.jforex.TestConnectionWithPIN(ctx, pin)
	if err == nil && a.market != nil {
		a.market.InvalidateInstruments("dukascopy")
	}
	return info, err
}

func (a *App) GetDucascopyLoginChallenge() (jforex.LoginChallenge, error) {
	ctx, cancel := a.ducascopyContext()
	defer cancel()
	return a.jforex.Challenge(ctx)
}

func (a *App) CancelDucascopyConnectionTest() jforex.Info { return a.jforex.CancelConnectionTest() }

// Public-provider candles keep their existing SQLite cache. JForex uses its
// private chart and SDK caches, scoped to the configured server/account, so
// demo/live sessions cannot reuse one another's chart snapshots.
type chartCandleCache struct {
	manager      *candlestorage.Manager
	forex        *jforex.Manager
	screener     *screener.Manager
	loadScreener func() (*screener.Manager, error)
}

func (cache chartCandleCache) Range(key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if key.Exchange == "dukascopy" {
		if cache.forex != nil {
			return cache.forex.CachedCandles(key, from, to, limit)
		}
		return []domain.Candle{}, nil
	}
	var retained []domain.Candle
	if key.Exchange == "hyperliquid" && (cache.screener != nil || cache.loadScreener != nil) {
		var err error
		scanner := cache.screener
		if cache.loadScreener != nil {
			scanner, err = cache.loadScreener()
			if err != nil {
				return nil, err
			}
		}
		if scanner != nil {
			retained, err = scanner.HyperliquidHistory(key, from, to, limit)
		}
		if err != nil {
			return nil, err
		}
	}
	var cached []domain.Candle
	if cache.manager != nil {
		var err error
		cached, err = cache.manager.Range(key, from, to, limit)
		if err != nil {
			return nil, err
		}
	}
	if len(retained) == 0 {
		return cached, nil
	}
	byTime := map[int64]domain.Candle{}
	for _, bar := range retained {
		byTime[bar.Time] = bar
	}
	for _, bar := range cached {
		old, ok := byTime[bar.Time]
		if !ok || old.Quality != "authoritative" || bar.Quality == "authoritative" {
			byTime[bar.Time] = bar
		}
	}
	merged := make([]domain.Candle, 0, len(byTime))
	for _, bar := range byTime {
		merged = append(merged, bar)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Time < merged[j].Time })
	if limit > 0 && len(merged) > limit {
		merged = merged[len(merged)-limit:]
	}
	return merged, nil
}

func (cache chartCandleCache) Put(key domain.FeedKey, bars []domain.Candle) {
	if key.Exchange == "dukascopy" {
		if cache.forex != nil {
			cache.forex.StoreCandles(key, bars)
		}
		return
	}
	if cache.manager != nil {
		cache.manager.Put(key, bars)
	}
}
