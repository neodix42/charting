//go:build bindings

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/predictions"
	"cryptodesk/internal/screener"
)

func statisticsRepairFile(source, symbol, period, from string, first int64) screener.DataFile {
	return screener.DataFile{DataSource: source, Symbol: symbol, Timeframe: period, Name: symbol + "-" + period + ".csv", From: from,
		FirstTimestamp: &first, Fields: []string{"timestamp", "open", "high", "low", "close", "volume", "sma_2"}}
}

func TestStatisticsDownloadPlanningUsesSelectedNativeHistoryAndCurrentUTC(t *testing.T) {
	now := time.Date(2026, 11, 20, 23, 59, 59, 0, time.FixedZone("UTC+4", 4*60*60))
	first := time.Date(2025, 1, 2, 3, 0, 0, 0, time.UTC).UnixMilli()
	older := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, source := range []string{"dukascopy", "hyperliquid"} {
		files := []screener.DataFile{
			statisticsRepairFile(source, "eurusd", "h1", "2024-01-01", first),
			statisticsRepairFile(source, "eurusd", "d1", "2000-01-01", older),
			statisticsRepairFile(source, "ethusd", "h1", "2000-01-01", older),
			statisticsRepairFile("other", "eurusd", "h1", "2000-01-01", older),
		}
		request, err := predictionStatisticsDownloadRequest(predictions.StatisticsRequest{DataSource: source, Asset: "eurusd", Period: "h1"}, files, now)
		if err != nil || request.DataSource != source || request.Timeframe != "h1" || !reflect.DeepEqual(request.Symbols, []string{"eurusd"}) || request.From != "2025-01-02" || request.To != "2026-11-21" {
			t.Fatal("repair expanded native history to an older companion, another asset/source, or the wrong cutoff", request, err)
		}
		missingCompanion, err := predictionStatisticsDownloadRequest(predictions.StatisticsRequest{DataSource: source, Asset: "eurusd", Period: "h1"}, files[:1], now)
		if err != nil || !reflect.DeepEqual(request, missingCompanion) {
			t.Fatal("missing companion could not be seeded from native source", missingCompanion, err)
		}
	}
}

func TestStatisticsDownloadPlanningPreservesMultipleNativeRangesAndRejectsInvalidSources(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	first, earlier := int64(1704153600000), int64(1704067200000)
	files := []screener.DataFile{statisticsRepairFile("dukascopy", "eurusd", "d1", "2024-01-02", first), statisticsRepairFile("dukascopy", "eurusd", "d1", "2024-01-01", earlier)}
	request := predictions.StatisticsRequest{DataSource: "dukascopy", Asset: "eurusd"}
	got, err := predictionStatisticsDownloadRequest(request, files, now)
	if err != nil || got.From != "2024-01-01" || got.Timeframe != "d1" {
		t.Fatal("duplicate native ranges were not sent to normal consolidation", got, err)
	}
	for _, invalid := range []screener.DataFile{
		{DataSource: "dukascopy", Symbol: "eurusd", Timeframe: "d1", Error: "changed CSV"},
		{DataSource: "dukascopy", Symbol: "eurusd", Timeframe: "d1", Fields: []string{"timestamp", "close"}},
		{DataSource: "dukascopy", Symbol: "eurusd", Timeframe: "d1", Fields: files[0].Fields, From: "bad-date"},
	} {
		if _, err := predictionStatisticsDownloadRequest(request, []screener.DataFile{invalid}, now); err == nil {
			t.Fatal("unusable history was replaced instead of reported", invalid)
		}
	}
	if _, err := predictionStatisticsDownloadRequest(request, nil, now); err == nil {
		t.Fatal("repair accepted an asset without owned native history")
	}
}

func TestStatisticsDownloadFacadeRejectsInvalidAndMissingRequestsWithoutChangingFiles(t *testing.T) {
	a, directory := statisticsFacade(t)
	name := "eurusd-d1-2024-01-01-2024-01-16.csv"
	contents := statisticsFacadeCSV()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	for _, request := range []predictions.StatisticsRequest{
		{DataSource: "binance", Asset: "eurusd", Period: "d1"},
		{DataSource: "dukascopy", Asset: "../eurusd", Period: "d1"},
		{DataSource: "dukascopy", Asset: "eurusd", Period: "m1"},
		{DataSource: "dukascopy", Asset: "eurusd", Period: "h1"},
		{DataSource: "hyperliquid", Asset: "eurusd", Period: "d1"},
		{DataSource: "dukascopy", Asset: "eurusd", Period: "d1", PatternMode: "current", PatternLength: 2},
		{DataSource: "dukascopy", Asset: "eurusd", Period: "d1", PatternMode: "custom"},
	} {
		if _, err := a.DownloadPredictionStatisticsData(request); err == nil {
			t.Fatal("invalid or another-source request started a repair", request)
		}
	}
	if len(a.screener.Info().Jobs) != 0 {
		t.Fatal("rejected planning replaced the existing queue")
	}
	data, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil || string(data) != contents {
		t.Fatal("rejected repair changed source data", err)
	}
	for _, ids := range [][]string{nil, {strings.Repeat("a", 32)}} {
		if _, err := a.GetPredictionStatisticsDownload(ids); err == nil {
			t.Fatal("missing batch reported as complete")
		}
		if _, err := a.CancelPredictionStatisticsDownload(ids); err == nil {
			t.Fatal("missing batch accepted cancellation")
		}
	}
}

func TestStatisticsPatternDownloadPlanningIgnoresUnusableDailyCompanion(t *testing.T) {
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	first := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, period := range []string{"h1", "w1"} {
		files := []screener.DataFile{
			statisticsRepairFile("dukascopy", "eurusd", period, "2024-01-01", first),
			{DataSource: "dukascopy", Symbol: "eurusd", Timeframe: "d1", Name: "unrelated-daily.csv", Error: "invalid daily file"},
		}
		request := predictions.StatisticsRequest{DataSource: "dukascopy", Asset: "eurusd", Period: period, Pattern: []string{"up", "down", "up"}}
		got, err := predictionStatisticsDownloadRequest(request, files, now)
		if err != nil || got.Timeframe != period || got.From != "2024-01-01" || !reflect.DeepEqual(got.Symbols, []string{"eurusd"}) {
			t.Fatal("native repair inspected an unnecessary daily companion", got, err)
		}
		request.PatternMode, request.PatternLength, request.Pattern = "current", 3, nil
		current, err := predictionStatisticsDownloadRequest(request, files, now)
		if err != nil || !reflect.DeepEqual(current, got) {
			t.Fatal("current repair inspected an unnecessary daily companion or changed its native scope", current, got, err)
		}
	}
}
