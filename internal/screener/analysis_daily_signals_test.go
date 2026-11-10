package screener

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestAnalysisOnlyOneSignalPerDayKeepsFirstMatchingBarPerAssetAndPeriod(t *testing.T) {
	m := analysisTestManager(t)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	values := make([]float64, 50)
	for i := range values {
		values[i] = 1
	}
	for _, i := range []int{2, 3, 23, 24, 25, 49} {
		values[i] = 2
	}
	for _, symbol := range []string{"btcusd", "ethusd"} {
		writeDataFile(t, m, symbol+"-h1-2024-01-01-2024-01-04.csv", reviewHourlyCSV(start, values))
	}
	writeDataFile(t, m, "btcusd-h4-2024-01-01-2024-01-02.csv", fmt.Sprintf("timestamp,close\n%d,2\n%d,3\n", start, start+4*3_600_000))
	request := AnalysisRequest{Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(1))}}
	for _, enabled := range []bool{false, true} {
		request.OnlyOneSignalPerDay = enabled
		if _, err := m.StartAnalysis(request); err != nil {
			t.Fatal(err)
		}
		info := waitAnalysis(t, m)
		want := int64(14)
		indices := []int64{2, 3, 23, 24, 25, 49}
		if enabled {
			want, indices = 7, []int64{2, 24, 49}
		}
		if info.Failed != 0 || info.TotalMatches != want || info.Rows != 102 {
			t.Fatalf("enabled=%v: %+v", enabled, info)
		}
		for _, symbol := range []string{"btcusd", "ethusd"} {
			result := resultNamed(t, info, symbol+"-h1-2024-01-01-2024-01-04.csv")
			if result.Matches != int64(len(indices)) {
				t.Fatal(result)
			}
			for occurrence, index := range indices {
				chart, err := m.AnalysisChart(result.ID, occurrence, 0, 0)
				if err != nil || chart.MatchIndex != index || chart.MatchTime != start+index*3_600_000 {
					t.Fatalf("first matching bar: %+v %v", chart, err)
				}
			}
		}
	}
}

func TestAnalysisDailySignalContinuesCrossStateAndSimulationOnSuppressedBars(t *testing.T) {
	m := analysisTestManager(t)
	start := time.Date(2024, 1, 1, 23, 57, 0, 0, time.UTC).UnixMilli()
	bars := make([]simulationTestBar, 6)
	for i := range bars {
		bars[i] = simulationBar(100, 110, 95, 100)
		bars[i].signal = 1
	}
	name := "btcusd-m1-2024-01-01-2024-01-03.csv"
	writeDataFile(t, m, name, simulationCSV(bars, start))
	request := simulationRequest(1)
	request.OnlyOneSignalPerDay = true
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	result := resultNamed(t, info, name)
	if info.Failed != 0 || result.Matches != 2 || result.Simulation == nil || result.Simulation.TakeProfitHits != 2 || result.Simulation.ProfitablePercent != 100 || simulationSummaryTotal(result.Simulation) != 2 {
		t.Fatalf("suppressed bars must still settle the retained trades: %+v", info)
	}
	chart, err := m.AnalysisChart(result.ID, 1, 6, 6)
	if err != nil || chart.MatchIndex != 3 || len(chart.ProfitableMatches) != 2 || chart.ProfitableMatches[0].Index != 0 || chart.ProfitableMatches[1].Index != 3 {
		t.Fatalf("retained chart matches: %+v %v", chart, err)
	}

	// Midnight does not manufacture a cross: the preceding suppressed bar's
	// state must survive the day boundary, and every AND row must advance.
	request.TradeSimulation = nil
	request.Conditions = []AnalysisCondition{analysisCondition("above", analysisField("signal"), analysisNumber(0)), analysisCondition("crossesAbove", analysisField("close"), analysisNumber(1))}
	request.Conditions[1].ID = "cross"
	// Use a proper H1 file for the new fixture.
	request.Conditions[0] = analysisCondition("above", analysisField("close"), analysisNumber(1))
	hourly := "btcusd-h1-2024-01-01-2024-01-03.csv"
	writeDataFile(t, m, hourly, reviewHourlyCSV(time.Date(2024, 1, 1, 22, 0, 0, 0, time.UTC).UnixMilli(), []float64{1, 2, 2, 1, 2}))
	request.Period = "h1"
	if _, err = m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	info = waitAnalysis(t, m)
	result = resultNamed(t, info, hourly)
	if result.Matches != 2 {
		t.Fatal(info)
	}
	for occurrence, index := range []int64{1, 4} {
		chart, err := m.AnalysisChart(result.ID, occurrence, 0, 0)
		if err != nil || chart.MatchIndex != index {
			t.Fatalf("cross state: %+v %v", chart, err)
		}
	}
}

func TestAnalysisDailySignalSelectsEarliestAcrossOverlappingHistories(t *testing.T) {
	m := analysisTestManager(t)
	day := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC).UnixMilli()
	later := "btcusd-h1-2024-01-01-2024-01-03.csv"
	earlier := "btcusd-h1-2024-01-02-2024-01-04.csv"
	writeDataFile(t, m, later, reviewHourlyCSV(day+12*3_600_000, []float64{2, 3}))
	writeDataFile(t, m, earlier, reviewHourlyCSV(day, []float64{1, 2, 3}))
	request := AnalysisRequest{OnlyOneSignalPerDay: true, Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(1))}}
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	if info.Failed != 0 || info.TotalMatches != 1 || resultNamed(t, info, later).Matches != 0 || resultNamed(t, info, earlier).FirstTime != day+3_600_000 {
		t.Fatalf("file ordering overrode earliest signal: %+v", info)
	}
	// Applying the option must leave non-intraday scans untouched, even with
	// overlapping files that contain the same day and different prices.
	for i, end := range []string{"03", "04"} {
		writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-"+end+".csv", fmt.Sprintf("timestamp,close\n%d,%d\n", day, i+2))
	}
	request.Period = "d1"
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	if info = waitAnalysis(t, m); info.Failed != 0 || info.TotalMatches != 2 {
		t.Fatalf("D1 was filtered: %+v", info)
	}
}

func TestDailySignalRulePersistsAndInvalidatesIncompatibleProfitabilityEvidence(t *testing.T) {
	m := analysisTestManager(t)
	request := defaultAnalysisRequest()
	request.Period = "h1"
	m.analysis = &analysisState{phase: "completed", asOf: time.Now().UnixMilli(), request: request, results: []AnalysisResult{profitabilityTestResult("before", 9, 10)}}
	saved, err := m.SaveAnalysisCondition("Once daily", request)
	if err != nil || saved.Analysis == nil {
		t.Fatal(saved, err)
	}
	request.OnlyOneSignalPerDay = true
	updated, err := m.UpdateAnalysisCondition(saved.ID, request)
	if err != nil || updated.Analysis != nil || !updated.Request.OnlyOneSignalPerDay || sameAnalysisProfitabilityRequest(m.analysis.request, request) {
		t.Fatal(updated, err)
	}
	if _, err := m.SaveAnalysisView(request); err != nil {
		t.Fatal(err)
	}
	profile := m.profile
	m.Close()
	restarted := savedConditionManager(t, profile)
	view, err := restarted.AnalysisView()
	if err != nil || !reflect.DeepEqual(view, request) {
		t.Fatal(view, err)
	}
	conditions, err := restarted.SavedAnalysisConditions()
	if err != nil || len(conditions) != 1 || !conditions[0].Request.OnlyOneSignalPerDay {
		t.Fatal(conditions, err)
	}
}

func TestLiveAlertDailySignalUsesWholeUTCdayAndOnlyLatestClosedBar(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, test := range []struct {
		name          string
		values        []float64
		closed        int64
		enabled, want bool
	}{
		{"first-is-not-midnight", []float64{0, 0, 2, 99}, 3, true, true},
		{"suppress-later-after-false-bars", []float64{2, 0, 0, 2, 99}, 4, true, false},
		{"option-off", []float64{2, 0, 0, 2, 99}, 4, false, true},
		{"midnight-resets", append(make([]float64, 23), 2, 2, 99), 25, true, true},
		{"23h-belongs-to-opening-day", append([]float64{2}, append(make([]float64, 22), 2, 99)...), 24, true, false},
		{"only-forming-bar-matches", []float64{0, 0, 99}, 2, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for i, value := range test.values {
				if value == 0 {
					test.values[i] = 1
				}
			}
			m := analysisTestManager(t)
			s := reviewLiveSeries(t, m, "btcusd-h1-2024-01-01-2024-01-03.csv", reviewHourlyCSV(start, test.values))
			a := &analysisState{request: AnalysisRequest{OnlyOneSignalPerDay: test.enabled, Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(1))}}, series: map[string]*analysisSeries{"base": s}}
			for repeat := 0; repeat < 2; repeat++ {
				matched, err := evaluateLiveAlertLatest(context.Background(), a, s, test.closed)
				if err != nil || matched != test.want {
					t.Fatalf("%t, want %t: %v", matched, test.want, err)
				}
			}
		})
	}
	if analysisSignalDay(-1) != -86_400_000 {
		t.Fatal("pre-epoch UTC day rounding")
	}
}

func TestPublicAlertDailySignalPlansFullDayIncludingCompanionWarmup(t *testing.T) {
	saved := SavedAnalysisCondition{ID: "once", Request: AnalysisRequest{OnlyOneSignalPerDay: true, Conditions: []AnalysisCondition{analysisCondition("crossesAbove", analysisField("close"), AnalysisOperand{Kind: "field", Field: "close", Timeframe: "m1"})}}}
	plans, err := publicAlertPlans([]SavedAnalysisCondition{saved}, map[string][]string{"once": {"h1"}})
	if err != nil || plans["h1"].Extra != 25 || plans["m1"].Extra != 26*60 {
		t.Fatalf("full-day dependency coverage: %+v %v", plans, err)
	}
	saved.Request.OnlyOneSignalPerDay = false
	plans, err = publicAlertPlans([]SavedAnalysisCondition{saved}, map[string][]string{"once": {"h1"}})
	if err != nil || plans["h1"].Extra != 1 || plans["m1"].Extra != 120 {
		t.Fatalf("ordinary warmup changed: %+v %v", plans, err)
	}
}

func TestLiveAlertDailySignalChecksEarlierMatchesInOtherDownloadedHistories(t *testing.T) {
	m := analysisTestManager(t)
	start := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC).UnixMilli()
	previous := reviewLiveSeries(t, m, "btcusd-h1-2024-01-01-2024-01-03.csv", reviewHourlyCSV(start, []float64{1, 2, 1}))
	latest := reviewLiveSeries(t, m, "btcusd-h1-2024-01-02-2024-01-04.csv", reviewHourlyCSV(start+12*3_600_000, []float64{2, 99}))
	a := &analysisState{request: AnalysisRequest{OnlyOneSignalPerDay: true, Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(1))}}, series: map[string]*analysisSeries{"previous": previous, "latest": latest}}
	matched, err := evaluateLiveAlertLatest(context.Background(), a, latest, 1)
	if err != nil || matched {
		t.Fatalf("later download repeated today's signal: %t %v", matched, err)
	}
	delete(a.series, "previous")
	matched, err = evaluateLiveAlertLatest(context.Background(), a, latest, 1)
	if err != nil || !matched {
		t.Fatalf("first available matching bar was suppressed: %t %v", matched, err)
	}
}
