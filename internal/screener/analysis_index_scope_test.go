package screener

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func scopedIndexCSV(timeframe string, close float64) string {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	first := start
	switch timeframe {
	case "d1":
		first += 6 * 86_400_000
	case "h1":
		first += 7*86_400_000 - 3_600_000
	}
	return fmt.Sprintf("timestamp,close\n%d,%g\n%d,%g\n", first, close, first+7*86_400_000, close)
}

func scopedIndexFile(t *testing.T, m *Manager, symbol, timeframe string, close float64) string {
	t.Helper()
	name := symbol + "-" + timeframe + "-2024-01-01-2024-01-16.csv"
	writeDataFile(t, m, name, scopedIndexCSV(timeframe, close))
	return name
}

func assertAnalysisIndexedNames(t *testing.T, m *Manager, want ...string) {
	t.Helper()
	sort.Strings(want)
	indexed, series := []string{}, []string{}
	for _, file := range m.analysis.indexFiles {
		indexed = append(indexed, file.Name)
	}
	for _, source := range m.analysis.series {
		series = append(series, source.metadata.Name)
	}
	sort.Strings(indexed)
	sort.Strings(series)
	if !reflect.DeepEqual(indexed, want) || !reflect.DeepEqual(series, want) {
		t.Fatalf("analysis indexed files outside its needed scope: queue=%v series=%v want=%v", indexed, series, want)
	}
}

func TestAnalysisIndexScopeWeeklyOnlySkipsUnusedDraftOperandsAndOtherPeriods(t *testing.T) {
	m := analysisTestManager(t)
	weekly := scopedIndexFile(t, m, "btcusd", "w1", 100)
	scopedIndexFile(t, m, "btcusd", "d1", 200)
	scopedIndexFile(t, m, "btcusd", "h1", 5)
	scopedIndexFile(t, m, "ethusd", "d1", 200)
	scopedIndexFile(t, m, "ethusd", "h1", 5)
	if categoryForSymbol(t, "btcusd") != categoryForSymbol(t, "ethusd") {
		t.Fatal("fixtures must share a category")
	}
	condition := analysisCondition("above", analysisField("close"), analysisNumber(0))
	condition.Right.Timeframe = "h1" // A numeric operand cannot require companion data.
	condition.Upper = analysisField("close")
	condition.Upper.Timeframe = "d1"
	condition.Distance = analysisField("close")
	condition.Distance.Timeframe = "h1"
	request := AnalysisRequest{Period: "w1", Conditions: []AnalysisCondition{condition}}
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	if info.Total != 1 || info.Failed != 0 || info.TotalMatches != 2 {
		t.Fatalf("weekly-only results: %+v", info)
	}
	assertAnalysisIndexedNames(t, m, weekly)
}

func TestAnalysisIndexScopeIncludesOnlyRequestedSameAssetCompanionPeriod(t *testing.T) {
	m := analysisTestManager(t)
	weekly := scopedIndexFile(t, m, "btcusd", "w1", 100)
	daily := scopedIndexFile(t, m, "btcusd", "d1", 200)
	scopedIndexFile(t, m, "btcusd", "h1", 5)
	scopedIndexFile(t, m, "ethusd", "d1", 200)
	dailyOperand := analysisField("close")
	dailyOperand.Timeframe = "d1"
	condition := analysisCondition("below", analysisField("close"), dailyOperand)
	if _, err := m.StartAnalysis(AnalysisRequest{Period: "w1", Conditions: []AnalysisCondition{condition}}); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	if info.Total != 1 || info.Failed != 0 || info.TotalMatches != 2 {
		t.Fatalf("weekly with daily companion: %+v", info)
	}
	assertAnalysisIndexedNames(t, m, weekly, daily)
	chart, err := m.AnalysisChart(info.Results[0].ID, 0, 0, 0)
	if err != nil || len(chart.Bars) != 1 || chart.Bars[0].Values["d1:close"] != 200 {
		t.Fatalf("requested companion lost from chart: %+v %v", chart, err)
	}
}

func TestAnalysisIndexScopeMissingCompanionStillFailsWithoutOtherAssetFallback(t *testing.T) {
	m := analysisTestManager(t)
	weekly := scopedIndexFile(t, m, "btcusd", "w1", 100)
	scopedIndexFile(t, m, "ethusd", "d1", 200)
	daily := analysisField("close")
	daily.Timeframe = "d1"
	if _, err := m.StartAnalysis(AnalysisRequest{Period: "w1", Conditions: []AnalysisCondition{analysisCondition("below", analysisField("close"), daily)}}); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	if info.Total != 1 || info.Failed != 1 || !strings.Contains(info.Results[0].Error, "no usable d1 data for btcusd") {
		t.Fatalf("missing companion silently substituted: %+v", info)
	}
	assertAnalysisIndexedNames(t, m, weekly)
}

func TestAnalysisIndexScopeIncludesEffectiveTemporalUpperAndDistanceCompanions(t *testing.T) {
	for _, comparison := range []string{"between", "withinDistance"} {
		t.Run(comparison, func(t *testing.T) {
			m := analysisTestManager(t)
			weekly := scopedIndexFile(t, m, "btcusd", "w1", 100)
			daily := scopedIndexFile(t, m, "btcusd", "d1", 200)
			hourly := scopedIndexFile(t, m, "btcusd", "h1", 5)
			scopedIndexFile(t, m, "ethusd", "d1", 200)
			scopedIndexFile(t, m, "ethusd", "h1", 5)
			condition := analysisCondition("occurredWithin", analysisField("close"), analysisNumber(0))
			condition.Comparison = comparison
			condition.Bars = 2
			condition.Upper = analysisField("close")
			condition.Upper.Timeframe = "d1"
			condition.Distance = analysisField("close")
			condition.Distance.Timeframe = "h1"
			companion := daily
			if comparison == "withinDistance" {
				condition.Operator = "remainedFor"
				condition.Right = analysisNumber(100)
				condition.Multiplier = 1
				companion = hourly
			}
			if _, err := m.StartAnalysis(AnalysisRequest{Period: "w1", Conditions: []AnalysisCondition{condition}}); err != nil {
				t.Fatal(err)
			}
			info := waitAnalysis(t, m)
			if info.Total != 1 || info.Failed != 0 || info.TotalMatches == 0 {
				t.Fatalf("temporal comparison lost effective operand: %+v", info)
			}
			assertAnalysisIndexedNames(t, m, weekly, companion)
		})
	}
}

func TestAnalysisIndexScopeWeeklyOperandDoesNotNarrowAllBasePeriods(t *testing.T) {
	for _, period := range []string{"", "all"} {
		t.Run("period-"+period, func(t *testing.T) {
			m := analysisTestManager(t)
			weekly := scopedIndexFile(t, m, "btcusd", "w1", 100)
			daily := scopedIndexFile(t, m, "btcusd", "d1", 200)
			hourly := scopedIndexFile(t, m, "btcusd", "h1", 200)
			operand := analysisField("close")
			operand.Timeframe = "w1"
			if _, err := m.StartAnalysis(AnalysisRequest{Period: period, Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), operand)}}); err != nil {
				t.Fatal(err)
			}
			info := waitAnalysis(t, m)
			if info.Total != 3 || info.Failed != 0 {
				t.Fatalf("weekly operand narrowed All-period base results: %+v", info)
			}
			assertAnalysisIndexedNames(t, m, weekly, daily, hourly)
		})
	}
}
