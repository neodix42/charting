package screener

import (
	"fmt"
	"math"
	"reflect"
	"testing"
)

func TestAnalysisProfitableSortBeforePagination(t *testing.T) {
	m := analysisTestManager(t)
	crypto, forex := categoryForSymbol(t, "btcusd"), categoryForSymbol(t, "eurusd")
	var results []AnalysisResult
	for i, percent := range []float64{50, 0, 100, 10, 90, 20, 80, 30, 70, 40, 60, 50} {
		results = append(results, AnalysisResult{ID: fmt.Sprint(i), Symbol: "btcusd", Category: crypto, Status: "completed", Matches: 10, Simulation: &AnalysisSimulationSummary{ProfitablePercent: percent}})
	}
	results = append(results,
		AnalysisResult{ID: "pending", Category: crypto, Status: "running", Matches: 1},
		AnalysisResult{ID: "unavailable", Category: crypto, Status: "completed", Matches: 1, Simulation: &AnalysisSimulationSummary{Unavailable: 1}},
		AnalysisResult{ID: "failed", Category: crypto, Status: "failed"},
		AnalysisResult{ID: "nan", Category: crypto, Matches: 1, Simulation: &AnalysisSimulationSummary{ProfitablePercent: math.NaN()}},
		AnalysisResult{ID: "infinity", Category: crypto, Matches: 1, Simulation: &AnalysisSimulationSummary{ProfitablePercent: math.Inf(1)}},
		AnalysisResult{ID: "empty", Category: crypto, Simulation: &AnalysisSimulationSummary{ProfitablePercent: 100}},
		AnalysisResult{ID: "forex", Category: forex, Matches: 1, Simulation: &AnalysisSimulationSummary{ProfitablePercent: 99}},
		AnalysisResult{ID: "duplicate", Category: crypto, Matches: 10, Simulation: &AnalysisSimulationSummary{ProfitablePercent: 100}},
	)
	m.analysis = &analysisState{phase: "completed", results: results, duplicateOf: map[string]string{"duplicate": "2"}}
	ids := func(results []AnalysisResult) []string {
		var ids []string
		for _, result := range results {
			ids = append(ids, result.ID)
		}
		return ids
	}
	original := ids(m.analysis.results)
	for _, test := range []struct {
		direction string
		want      []string
	}{
		{"desc", []string{"2", "4", "6", "8", "10", "0", "11", "9", "7", "5", "3", "1"}},
		{"asc", []string{"1", "3", "5", "7", "9", "0", "11", "10", "8", "6", "4", "2"}},
	} {
		t.Run(test.direction, func(t *testing.T) {
			filter := AnalysisFilter{Categories: []string{crypto}, IncludeEmpty: true, ProfitableSort: test.direction}
			first, err := m.AnalysisFiltered(0, 10, filter)
			if err != nil || first.TotalResults != 18 || len(first.Results) != 10 {
				t.Fatalf("first sorted page: %+v %v", first, err)
			}
			second, err := m.AnalysisFiltered(10, 10, filter)
			if err != nil || second.TotalResults != 18 || len(second.Results) != 8 {
				t.Fatalf("second sorted page: %+v %v", second, err)
			}
			want := append(append([]string{}, test.want...), "pending", "unavailable", "failed", "nan", "infinity", "empty")
			if got := ids(append(first.Results, second.Results...)); !reflect.DeepEqual(got, want) {
				t.Fatalf("sorted result IDs: got %v, want %v", got, want)
			}
			if !reflect.DeepEqual(ids(m.analysis.results), original) {
				t.Fatal("sorting changed the stored analysis order")
			}
		})
	}
	for _, direction := range []string{"ascending", "DESC", "invalid"} {
		if _, err := m.AnalysisFiltered(0, 10, AnalysisFilter{ProfitableSort: direction}); err == nil {
			t.Fatalf("accepted invalid sort %q", direction)
		}
	}
	page, err := m.AnalysisFiltered(0, 10, AnalysisFilter{IncludeEmpty: true})
	if err != nil || !reflect.DeepEqual(ids(page.Results), original[:10]) {
		t.Fatalf("default result order changed: %+v %v", page, err)
	}
}

func TestAnalysisProfitableSortUsesUnroundedPercentages(t *testing.T) {
	m := analysisTestManager(t)
	m.analysis = &analysisState{results: []AnalysisResult{
		{ID: "lower", Matches: 1, Simulation: &AnalysisSimulationSummary{ProfitablePercent: 51.001}},
		{ID: "higher", Matches: 1, Simulation: &AnalysisSimulationSummary{ProfitablePercent: 51.002}},
	}}
	page, err := m.AnalysisFiltered(0, 10, AnalysisFilter{ProfitableSort: "desc"})
	if err != nil || len(page.Results) != 2 || page.Results[0].ID != "higher" {
		t.Fatalf("percentages were rounded before sorting: %+v %v", page, err)
	}
}
