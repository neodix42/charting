package screener

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAnalysisPeriodScansOnlySelectedBaseFilesAndRetainsCompanionBars(t *testing.T) {
	m := analysisTestManager(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	dailyName := "btcusd-d1-2024-01-01-2024-01-03.csv"
	hourlyName := "btcusd-h1-2024-01-01-2024-01-03.csv"
	writeDataFile(t, m, dailyName, fmt.Sprintf("timestamp,close\n%d,100\n%d,200\n", base, base+86_400_000))
	writeDataFile(t, m, hourlyName, fmt.Sprintf("timestamp,close\n%d,101\n%d,201\n", base+23*3_600_000, base+47*3_600_000))
	writeDataFile(t, m, "btcusd-m1-2024-01-01-2024-01-03.csv", analysisCSV([]float64{2, 3}))
	// Invalid assets from unselected categories cannot affect a selected scan.
	writeDataFile(t, m, "eurusd-h1-2024-01-01-2024-01-03.csv", "timestamp,close\ninvalid,2\n")
	request := AnalysisRequest{Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(1))}, Categories: []string{categoryForSymbol(t, "btcusd")}}
	for _, period := range []string{"", "all", "d1", "h1"} {
		t.Run("period-"+period, func(t *testing.T) {
			request.Period = period
			if _, err := m.StartAnalysis(request); err != nil {
				t.Fatal(err)
			}
			info := waitAnalysis(t, m)
			wantFiles := 1
			if period == "" || period == "all" {
				wantFiles = 3
			}
			if info.Total != wantFiles || info.Failed != 0 || info.TotalMatches != int64(wantFiles*2) || len(info.Results) != wantFiles {
				t.Fatalf("period %q scanned incorrect files: %+v", period, info)
			}
			for _, result := range info.Results {
				if !includesAnalysisPeriod(period, result.Timeframe) {
					t.Fatalf("unexpected result outside period: %+v", result)
				}
			}
		})
	}

	// A daily operand is still available when the evaluated base is hourly.
	daily := analysisField("close")
	daily.Timeframe = "d1"
	request.Period = "h1"
	request.Conditions = []AnalysisCondition{analysisCondition("above", analysisField("close"), daily)}
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	if info.Total != 1 || info.Failed != 0 || resultNamed(t, info, hourlyName).Matches != 2 {
		t.Fatalf("period filter removed companion closed bars: %+v", info)
	}
	chart, err := m.AnalysisChart(info.Results[0].ID, 0, 0, 0)
	if err != nil || len(chart.Bars) != 1 || chart.Bars[0].Values["d1:close"] != 100 {
		t.Fatalf("period-filtered chart lost companion values: %+v %v", chart, err)
	}
}

func TestAnalysisPeriodPersistsDraftAndSavedConditionAndRejectsUnknownPeriods(t *testing.T) {
	for _, period := range []string{"", "all", "d1", "h1"} {
		t.Run("period-"+period, func(t *testing.T) {
			profile := t.TempDir()
			m := savedConditionManager(t, profile)
			request := defaultAnalysisRequest()
			request.Period = period
			if _, err := m.SaveAnalysisView(request); err != nil {
				t.Fatal(err)
			}
			if _, err := m.SaveAnalysisCondition("Period snapshot", request); err != nil {
				t.Fatal(err)
			}
			m.Close()
			restarted := savedConditionManager(t, profile)
			view, err := restarted.AnalysisView()
			if err != nil || !reflect.DeepEqual(view, request) {
				t.Fatalf("period draft changed on restart: %+v %v", view, err)
			}
			saved, err := restarted.SavedAnalysisConditions()
			if err != nil || len(saved) != 1 || !reflect.DeepEqual(saved[0].Request, request) {
				t.Fatalf("saved period changed on restart: %+v %v", saved, err)
			}
			request.Period = "h4"
			if _, err = restarted.SaveAnalysisView(request); err == nil || !strings.Contains(err.Error(), "analysis period") {
				t.Fatalf("invalid period saved: %v", err)
			}
			if got, _ := restarted.AnalysisView(); !reflect.DeepEqual(got, view) {
				t.Fatal("invalid period replaced the saved draft")
			}
			if _, err = restarted.SaveAnalysisCondition("Invalid period", request); err == nil {
				t.Fatal("invalid period saved into a named condition")
			}
		})
	}
}

func TestAnalysisUnavailableOrInvalidPeriodKeepsPreviousResults(t *testing.T) {
	m := analysisTestManager(t)
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-03.csv", analysisCSV([]float64{2, 3}))
	before := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), analysisNumber(1)))
	request := defaultAnalysisRequest()
	for _, period := range []string{"h1", "h4", "D1", "same"} {
		request.Period = period
		if _, err := m.StartAnalysis(request); err == nil {
			t.Fatalf("unavailable/invalid period %q accepted", period)
		}
		if got, _ := m.Analysis(0, 200); !reflect.DeepEqual(got, before) {
			t.Fatalf("rejected period %q discarded previous results", period)
		}
	}
}
