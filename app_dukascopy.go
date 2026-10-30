package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"cryptodesk/internal/candlestorage"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/jforex"
)

func (a *App) GetDucascopy() jforex.Info { return a.jforex.Info() }

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
	manager *candlestorage.Manager
	forex   *jforex.Manager
}

func (cache chartCandleCache) Range(key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if key.Exchange == "dukascopy" {
		if cache.forex != nil {
			return cache.forex.CachedCandles(key, from, to, limit)
		}
		return []domain.Candle{}, nil
	}
	return cache.manager.Range(key, from, to, limit)
}

func (cache chartCandleCache) Put(key domain.FeedKey, bars []domain.Candle) {
	if key.Exchange == "dukascopy" {
		if cache.forex != nil {
			cache.forex.StoreCandles(key, bars)
		}
		return
	}
	cache.manager.Put(key, bars)
}
