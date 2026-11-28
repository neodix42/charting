package main

import (
	"context"
	"errors"

	"cryptodesk/internal/comparison"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/market"
)

type comparisonMarketSource struct {
	market   *market.Service
	services *serviceStartup
}

func (source comparisonMarketSource) Instruments(ctx context.Context, provider string) ([]domain.Instrument, error) {
	return source.market.Instruments(ctx, provider)
}
func (source comparisonMarketSource) History(ctx context.Context, id string, key domain.FeedKey, from, to int64, limit int) (market.History, error) {
	if key.Exchange == "hyperliquid" && source.services != nil {
		if err := source.services.wait(ctx, "screener"); err != nil {
			return market.History{}, err
		}
	}
	return source.market.HistorySnapshot(ctx, id, key, from, to, limit)
}

// Comparison is initialized only when its screen is requested. The existing
// market service owns provider discovery, pacing, retained candles and cleanup.
func (a *App) comparisonService() (*comparison.Manager, context.Context, error) {
	if err := a.awaitBootstrap(false); err != nil {
		return nil, nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if a.comparisonClosed || ctx.Err() != nil {
		return nil, nil, context.Canceled
	}
	if a.store == nil {
		return nil, nil, errors.New("Comparison profile is unavailable")
	}
	if a.market == nil {
		return nil, nil, errors.New("Comparison market data is unavailable")
	}
	if a.comparison == nil {
		a.comparison = comparison.New(a.store.Directory(), comparisonMarketSource{market: a.market, services: a.services})
	}
	return a.comparison, ctx, nil
}
func (a *App) GetComparisonView() (comparison.View, error) {
	manager, _, err := a.comparisonService()
	if err != nil {
		return comparison.View{}, err
	}
	return manager.View()
}
func (a *App) SaveComparisonView(view comparison.View) (comparison.View, error) {
	manager, _, err := a.comparisonService()
	if err != nil {
		return comparison.View{}, err
	}
	return manager.SaveView(view)
}
func (a *App) GetComparisonCatalog() (comparison.Catalog, error) {
	manager, ctx, err := a.comparisonService()
	if err != nil {
		return comparison.Catalog{}, err
	}
	return manager.Catalog(ctx)
}
func (a *App) GetComparisonDailyChanges(requestID string, view comparison.View) (comparison.Result, error) {
	manager, ctx, err := a.comparisonService()
	if err != nil {
		return comparison.Result{}, err
	}
	return manager.DailyChanges(ctx, requestID, view)
}
func (a *App) GetComparisonOverview(requestID string, view comparison.View) (comparison.Overview, error) {
	manager, ctx, err := a.comparisonService()
	if err != nil {
		return comparison.Overview{}, err
	}
	return manager.Overview(ctx, requestID, view)
}
func (a *App) CancelComparisonRequest(requestID string) {
	a.mu.Lock()
	manager := a.comparison
	a.mu.Unlock()
	if manager != nil {
		manager.Cancel(requestID)
	}
}
