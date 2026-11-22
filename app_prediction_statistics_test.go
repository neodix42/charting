//go:build bindings

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/predictions"
	"cryptodesk/internal/screener"
)

func statisticsFacade(t *testing.T) (*App, string) {
	t.Helper()
	profile := t.TempDir()
	a := &App{screener: screener.New(profile)}
	t.Cleanup(a.screener.Close)
	directory := a.screener.Info().Directory
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return a, directory
}

func statisticsFacadeCSV() string {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	var data strings.Builder
	data.WriteString("timestamp,open,high,low,close\n")
	for week := range 3 {
		for day := range 5 {
			if week == 2 && day > 0 {
				break
			}
			fmt.Fprintf(&data, "%d,100,102,99,101\n", start.AddDate(0, 0, week*7+day).UnixMilli())
		}
	}
	return data.String()
}

func TestStatisticsFacadeUsesBothSourcesCandlePeriodsAndNeedsNoPredictionRuntime(t *testing.T) {
	a, directory := statisticsFacade(t)
	text := statisticsFacadeCSV()
	files := map[string]string{
		"eurusd-d1-2024-01-01-2024-01-16.csv":             text,
		"hyperliquid-abc123-d1-2024-01-01-2024-01-16.csv": text,
		"eurusd-h1-2024-01-01-2024-01-16.csv":             "timestamp,open,high,low,close\n1704103200000,100,102,99,101\n1704708000000,100,102,99,101\n1705312800000,100,102,99,101\n",
		"eurusd-w1-2023-12-25-2024-01-16.csv":             "timestamp,open,high,low,close\n1703462400000,100,102,99,101\n1704067200000,100,102,99,101\n1704672000000,100,102,99,101\n",
		"eurusd-tick-2024-01-01-2024-01-16.csv":           "timestamp,bidPrice,askPrice\n1704067200000,1,2\n",
		"audusd-d1-2024-01-01-2024-01-16.csv":             "timestamp,close\n1704067200000,100\n",
		"unrecognized.csv":                                text,
	}
	for filename, content := range files {
		if err := os.WriteFile(filepath.Join(directory, filename), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// a.predictions is deliberately nil: even inspecting its model readiness
	// would panic. Statistics operates only on the actual Screener inventory.
	datasets, err := a.GetPredictionStatisticsDatasets()
	if err != nil || len(datasets) != 4 {
		t.Fatalf("candle source inventory: %+v %v", datasets, err)
	}
	seen := map[string]bool{}
	for _, dataset := range datasets {
		seen[dataset.DataSource+"|"+dataset.Period] = true
		if dataset.DataSource == "dukascopy" && dataset.Market != "forex" {
			t.Fatalf("downloaded forex classification lost: %+v", dataset)
		}
		if dataset.DataSource == "hyperliquid" && dataset.Market != "" {
			t.Fatalf("unknown retained market was guessed: %+v", dataset)
		}
	}
	for _, identity := range []string{"dukascopy|h1", "dukascopy|d1", "dukascopy|w1", "hyperliquid|d1"} {
		if !seen[identity] {
			t.Fatal("downloaded candle timeframe was omitted", identity, datasets)
		}
	}
	encoded, err := json.Marshal(datasets)
	if err != nil || strings.Contains(string(encoded), directory) || strings.Contains(string(encoded), "directory") {
		t.Fatalf("statistics inventory exposed an internal path: %s %v", encoded, err)
	}
	for _, dataset := range datasets {
		if dataset.Period != "d1" {
			result, err := a.GetPredictionStatistics(predictions.StatisticsRequest{DataSource: dataset.DataSource, Asset: dataset.Symbol, Period: dataset.Period})
			if err != nil || result.Period != dataset.Period || result.DataSource != dataset.DataSource || result.Symbol != dataset.Symbol || result.CompanionSourceHash == "" || result.SourceHash == "" || len(result.Scenarios) == 0 {
				t.Fatalf("native-period calculation lost its real D1 companion or invoked model readiness: %+v %v", result, err)
			}
			continue
		}
		// Legacy requests omit Period and remain D1 even when H1 and W1 files
		// are available for the very same asset.
		result, err := a.GetPredictionStatistics(predictions.StatisticsRequest{DataSource: dataset.DataSource, Asset: dataset.Symbol})
		if err != nil || result.DataSource != dataset.DataSource || result.Symbol != dataset.Symbol || result.MatchedWeeks != 2 || result.SourceHash == "" || len(result.Scenarios) != 1 || result.Scenarios[0].Probability != 1 {
			t.Fatalf("source-isolated runtime-free calculation: %+v %v", result, err)
		}
		if data, err := os.ReadFile(filepath.Join(directory, dataset.Filename)); err != nil || string(data) != text {
			t.Fatal("statistics changed downloaded source data", err)
		}
	}
	for _, request := range []predictions.StatisticsRequest{
		{DataSource: "hyperliquid", Asset: "eurusd"},
		{DataSource: "dukascopy", Asset: "hyperliquid-abc123"},
		{DataSource: "binance", Asset: "eurusd"},
		{DataSource: "dukascopy", Asset: "../eurusd"},
	} {
		if _, err := a.GetPredictionStatistics(request); err == nil {
			t.Fatal("statistics used another source or accepted an invalid asset", request)
		}
	}
	for filename, original := range files {
		if after, err := os.ReadFile(filepath.Join(directory, filename)); err != nil || string(after) != original {
			t.Fatal("native statistics changed selected, companion or unrelated candle bytes", filename, err)
		}
	}
}

func TestStatisticsAssetMarketsUseCatalogCategories(t *testing.T) {
	for _, test := range []struct{ source, category, want string }{
		{"dukascopy", "Forex currencies 💱", "forex"},
		{"dukascopy", "Forex major currencies 💶", "forex"},
		{"dukascopy", "Forex metals 🥇", "metal"},
		{"dukascopy", "Metals commodities ⚙️", "cfd"},
		{"dukascopy", "Crypto assets ₿", "cfd"},
		{"dukascopy", "Other", ""},
		{"hyperliquid", "Hyperliquid spot", "spot"},
		{"hyperliquid", "Hyperliquid perpetual", "perpetual"},
		{"hyperliquid", "Hyperliquid", ""},
	} {
		if got := statisticsAssetMarket(test.source, test.category); got != test.want {
			t.Errorf("%s %s: %q, want %q", test.source, test.category, got, test.want)
		}
	}
}

func TestStatisticsFacadeRequiresUniqueSameSourceDailyCompanionForH1AndW1(t *testing.T) {
	for _, period := range []string{"h1", "w1"} {
		t.Run(period, func(t *testing.T) {
			a, directory := statisticsFacade(t)
			text := statisticsFacadeCSV()
			name := "eurusd-" + period + "-2024-01-01-2024-01-16.csv"
			if err := os.WriteFile(filepath.Join(directory, name), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			// Another source's D1 file cannot satisfy this companion requirement.
			if err := os.WriteFile(filepath.Join(directory, "hyperliquid-abc123-d1-2024-01-01-2024-01-16.csv"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			request := predictions.StatisticsRequest{DataSource: "dukascopy", Asset: "eurusd", Period: period}
			if _, err := a.GetPredictionStatistics(request); err == nil || !strings.Contains(err.Error(), "download companion D1 candles") {
				t.Fatal("missing daily candles were synthesized or borrowed from another provider", err)
			}
			for _, name := range []string{"eurusd-d1-2024-01-01-2024-01-16.csv", "eurusd-d1-2023-01-01-2024-01-16.csv"} {
				if err := os.WriteFile(filepath.Join(directory, name), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.GetPredictionStatistics(request); err == nil || !strings.Contains(err.Error(), "multiple companion D1 CSV ranges") {
				t.Fatal("ambiguous daily companion was silently selected", err)
			}
		})
	}
}

func TestStatisticsFacadeRejectsAmbiguousRangesAndPreservesFiles(t *testing.T) {
	a, directory := statisticsFacade(t)
	text := statisticsFacadeCSV()
	for _, name := range []string{"eurusd-d1-2024-01-01-2024-01-16.csv", "eurusd-d1-2023-01-01-2024-01-16.csv"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.GetPredictionStatistics(predictions.StatisticsRequest{DataSource: "dukascopy", Asset: "eurusd"}); err == nil || !strings.Contains(err.Error(), "multiple CSV ranges") {
		t.Fatal("statistics silently selected an arbitrary range", err)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 2 {
		t.Fatal("statistics created state or modified source inventory", files, err)
	}
	for _, file := range files {
		if data, err := os.ReadFile(filepath.Join(directory, file.Name())); err != nil || string(data) != text {
			t.Fatal("ambiguous statistics request changed candle bytes", err)
		}
	}
}

func TestStatisticsFacadeAmbiguityUsesTheRequestedCandlePeriod(t *testing.T) {
	a, directory := statisticsFacade(t)
	text := statisticsFacadeCSV()
	for _, name := range []string{"eurusd-d1-2024-01-01-2024-01-16.csv", "eurusd-h1-2024-01-01-2024-01-16.csv", "eurusd-h1-2023-01-01-2024-01-16.csv"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request := predictions.StatisticsRequest{DataSource: "dukascopy", Asset: "eurusd", Period: "d1"}
	if result, err := a.GetPredictionStatistics(request); err != nil || result.Period != "d1" || result.SourceFile != "eurusd-d1-2024-01-01-2024-01-16.csv" {
		t.Fatal("other timeframe ranges prevented the unique D1 selection", result, err)
	}
	request.Period = "h1"
	if _, err := a.GetPredictionStatistics(request); err == nil || !strings.Contains(err.Error(), "multiple CSV ranges") {
		t.Fatal("ambiguous selected timeframe was silently resolved", err)
	}
	request.Period = "w1"
	if _, err := a.GetPredictionStatistics(request); err == nil || !strings.Contains(err.Error(), "download OHLC candles") {
		t.Fatal("missing selected timeframe fell back to another period", err)
	}
}

func TestStatisticsPatternFacadeUsesOnlyItsNativePeriodWithoutDailyCompanions(t *testing.T) {
	for _, period := range []string{"h1", "d1", "w1"} {
		t.Run(period, func(t *testing.T) {
			a, directory := statisticsFacade(t)
			step := map[string]time.Duration{"h1": time.Hour, "d1": 24 * time.Hour, "w1": 7 * 24 * time.Hour}[period]
			start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			var data strings.Builder
			data.WriteString("timestamp,open,high,low,close\n")
			for index := range 12 {
				fmt.Fprintf(&data, "%d,100,101,98,99\n", start.Add(time.Duration(index)*step).UnixMilli())
			}
			name := "eurusd-" + period + "-2024-01-01-2024-03-25.csv"
			if err := os.WriteFile(filepath.Join(directory, name), []byte(data.String()), 0600); err != nil {
				t.Fatal(err)
			}
			request := predictions.StatisticsRequest{DataSource: "dukascopy", Asset: "eurusd", Period: period, Pattern: []string{"down", "down", "down"}}
			got, err := a.GetPredictionStatistics(request)
			if err != nil || got.Period != period || got.MatchedPatterns != 6 || got.EligiblePatterns != 6 || got.CompanionSourceFile != "" || len(got.Scenarios) != 1 || got.Scenarios[0].ID != "D" {
				t.Fatal("native pattern required a daily source or returned weekday scenarios", got, err)
			}
			currentRequest := request
			currentRequest.PatternMode, currentRequest.PatternLength, currentRequest.Pattern = "current", 3, nil
			current, err := a.GetPredictionStatistics(currentRequest)
			if err != nil || current.PatternMode != "current" || current.PatternLength != 3 || current.MatchedPatterns != got.MatchedPatterns || len(current.Pattern) != 3 || current.Pattern[0] != "down" || current.CompanionSourceFile != "" {
				t.Fatal("current mode required manual directions or a daily companion", current, err)
			}
			// Even ambiguous companion ranges must be irrelevant to native H1/W1.
			if period != "d1" {
				for _, companion := range []string{"eurusd-d1-2024-01-01-2024-01-16.csv", "eurusd-d1-2023-01-01-2024-01-16.csv"} {
					if err := os.WriteFile(filepath.Join(directory, companion), []byte(statisticsFacadeCSV()), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := a.GetPredictionStatistics(request); err != nil {
					t.Fatal("an unrelated daily companion prevented native matching", err)
				}
				if _, err := a.GetPredictionStatistics(currentRequest); err != nil {
					t.Fatal("an unrelated daily companion prevented current native matching", err)
				}
			}
		})
	}
}
