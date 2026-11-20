package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"cryptodesk/internal/predictions"
	"cryptodesk/internal/screener"
)

// Only an explicit Statistics action starts provider work. Planning reads the
// owned inventory, never a webview-supplied filename or arbitrary path.
func (a *App) DownloadPredictionStatisticsData(request predictions.StatisticsRequest) (screener.Info, error) {
	if err := predictions.ValidateStatisticsRequest(request); err != nil {
		return screener.Info{}, err
	}
	if err := a.awaitService("screener"); err != nil {
		return screener.Info{}, err
	}
	catalog, err := a.screener.AnalysisCatalogForSource(request.DataSource)
	if err != nil {
		return screener.Info{}, err
	}
	download, err := predictionStatisticsDownloadRequest(request, catalog.Files, time.Now().UTC())
	if err != nil {
		return screener.Info{}, err
	}
	return a.screener.StartWithDailyCompanion(download)
}

func predictionStatisticsDownloadRequest(request predictions.StatisticsRequest, files []screener.DataFile, now time.Time) (screener.Request, error) {
	period := predictions.StatisticsPeriod(request.Period)
	var from time.Time
	selected := false
	for _, file := range files {
		if file.Symbol != request.Asset || (file.Timeframe != period && !(period != "d1" && file.Timeframe == "d1")) || file.DataSource != request.DataSource {
			continue
		}
		if file.Error != "" {
			return screener.Request{}, fmt.Errorf("cannot extend %s: %s", file.Name, file.Error)
		}
		fields := map[string]bool{}
		for _, field := range file.Fields {
			fields[strings.ToLower(strings.TrimSpace(field))] = true
		}
		if !fields["timestamp"] || !fields["open"] || !fields["high"] || !fields["low"] || !fields["close"] {
			return screener.Request{}, fmt.Errorf("cannot extend %s: daily statistics requires timestamp and OHLC columns", file.Name)
		}
		// An older D1 companion must not extend an H1 repair backwards before
		// its native source. The incremental worker retains older D1 rows even
		// when they lie outside this shared request interval.
		if file.Timeframe != period {
			continue
		}
		start, err := time.Parse("2006-01-02", file.From)
		if file.FirstTimestamp != nil {
			start, err = time.UnixMilli(*file.FirstTimestamp).UTC().Truncate(24*time.Hour), nil
		}
		if err != nil || start.Year() < 1900 || start.After(now.UTC()) {
			return screener.Request{}, fmt.Errorf("cannot determine the existing history start for %s", file.Name)
		}
		if from.IsZero() || start.Before(from) {
			from = start
		}
		selected = true
	}
	if !selected {
		return screener.Request{}, errors.New("choose a downloaded source, asset and candle timeframe before downloading missing Statistics data")
	}
	return screener.Request{DataSource: request.DataSource, Symbols: []string{request.Asset}, Timeframe: period,
		From: from.Format("2006-01-02"), To: now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour).Format("2006-01-02")}, nil
}

func (a *App) GetPredictionStatisticsDownload(jobIDs []string) (screener.Info, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.Info{}, err
	}
	return a.screener.DownloadBatch(jobIDs)
}

func (a *App) CancelPredictionStatisticsDownload(jobIDs []string) (screener.Info, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.Info{}, err
	}
	return a.screener.CancelDownloadBatch(jobIDs)
}
