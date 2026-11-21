package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"cryptodesk/internal/predictions"
)

// Statistics needs only existing Screener candle CSVs. It never inspects,
// prepares or invokes the optional LLM runtime.
func (a *App) GetPredictionStatisticsDatasets() ([]predictions.Dataset, error) {
	if err := a.awaitService("screener"); err != nil {
		return nil, err
	}
	items := []predictions.Dataset{}
	for _, source := range []string{"dukascopy", "hyperliquid"} {
		catalog, err := a.screener.AnalysisCatalogForSource(source)
		if err != nil {
			return nil, err
		}
		for _, file := range catalog.Files {
			if file.Error != "" || file.Symbol == "" {
				continue
			}
			switch file.Timeframe {
			case "h1", "d1", "w1":
			default:
				continue
			}
			fields := map[string]bool{}
			for _, field := range file.Fields {
				fields[strings.ToLower(strings.TrimSpace(field))] = true
			}
			if !fields["timestamp"] || !fields["open"] || !fields["high"] || !fields["low"] || !fields["close"] {
				continue
			}
			name := file.Description
			if name == "" {
				name = strings.ToUpper(file.Symbol)
			}
			items = append(items, predictions.Dataset{DataSource: source, Market: statisticsAssetMarket(source, file.Category), ID: file.ID, Symbol: file.Symbol,
				Name: name, Period: file.Timeframe, Filename: file.Name, Directory: catalog.Directory})
		}
	}
	return items, nil
}

// Use the retained catalog category, without connecting chart feeds or guessing
// unknown downloaded assets from their names. Unknown assets remain in All.
func statisticsAssetMarket(source, category string) string {
	category = strings.ToLower(strings.TrimSpace(category))
	if source == "hyperliquid" {
		switch category {
		case "hyperliquid spot":
			return "spot"
		case "hyperliquid perpetual":
			return "perpetual"
		}
		return ""
	}
	if source != "dukascopy" || category == "" || category == "other" {
		return ""
	}
	if strings.HasPrefix(category, "forex metals") {
		return "metal"
	}
	if strings.HasPrefix(category, "forex ") {
		return "forex"
	}
	return "cfd"
}

func (a *App) GetPredictionStatistics(request predictions.StatisticsRequest) (predictions.StatisticsForecast, error) {
	if err := predictions.ValidateStatisticsRequest(request); err != nil {
		return predictions.StatisticsForecast{}, err
	}
	items, err := a.GetPredictionStatisticsDatasets()
	if err != nil {
		return predictions.StatisticsForecast{}, err
	}
	var selected *predictions.Dataset
	for index := range items {
		if items[index].DataSource != request.DataSource || items[index].Symbol != request.Asset || items[index].Period != predictions.StatisticsPeriod(request.Period) {
			continue
		}
		if selected != nil {
			return predictions.StatisticsForecast{}, errors.New("multiple CSV ranges exist for this source, asset and candle timeframe; use Screener Refresh to consolidate them before calculating statistics")
		}
		selected = &items[index]
	}
	if selected == nil {
		return predictions.StatisticsForecast{}, errors.New("download OHLC candles for the selected source, asset and candle timeframe in Screener before calculating statistics")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if predictions.StatisticsPeriod(request.Period) != "d1" {
		var companion *predictions.Dataset
		for index := range items {
			if items[index].DataSource != request.DataSource || items[index].Symbol != request.Asset || items[index].Period != "d1" {
				continue
			}
			if companion != nil {
				return predictions.StatisticsForecast{}, errors.New("multiple companion D1 CSV ranges exist for this source and asset; use Screener Refresh to consolidate them before calculating statistics")
			}
			companion = &items[index]
		}
		if companion == nil {
			return predictions.StatisticsForecast{}, errors.New("download companion D1 candles for the same source and asset in Screener before calculating H1 or W1 statistics")
		}
		return predictions.CalculateStatisticsWithCompanion(ctx, *selected, *companion, request, time.Now().UTC().UnixMilli())
	}
	return predictions.CalculateStatistics(ctx, *selected, request, time.Now().UTC().UnixMilli())
}
