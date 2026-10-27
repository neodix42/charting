package screener

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAnalysisSimulationStopHistoryAndRiskMultiple(t *testing.T) {
	tests := []struct {
		name     string
		bars     []simulationTestBar
		offset   int
		multiple int
		want     AnalysisSimulationSummary
	}{
		{"matched stop field", []simulationTestBar{{100, 105, 90, 100, 90, 200, 0}, {100, 105, 80, 100, 80, 110, 1}, simulationBar(100, 111, 85, 100)}, 0, 0, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
		{"previous stop field", []simulationTestBar{{100, 105, 90, 100, 90, 200, 0}, {100, 105, 80, 100, 80, 110, 1}, simulationBar(100, 111, 85, 100)}, 1, 0, AnalysisSimulationSummary{StopLossHits: 1}},
		{"target still uses matched bar", []simulationTestBar{{100, 105, 90, 100, 90, 200, 0}, {100, 105, 80, 100, 80, 110, 1}, simulationBar(100, 111, 95, 100)}, 1, 0, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
		{"missing preceding row retains match", []simulationTestBar{{100, 105, 90, 100, 90, 110, 1}, simulationBar(100, 120, 95, 100)}, 1, 0, AnalysisSimulationSummary{InvalidLevels: 1}},
		{"previous field warmup is unavailable", []simulationTestBar{{100, 105, 90, 100, math.NaN(), 110, 0}, {100, 105, 90, 100, 90, 110, 1}, simulationBar(100, 120, 95, 100)}, 1, 0, AnalysisSimulationSummary{InvalidLevels: 1}},
		{"1xSL from entry gap", []simulationTestBar{{100, 105, 90, 100, 90, math.NaN(), 1}, simulationBar(105, 120, 95, 105)}, 0, 1, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
		{"2xSL from entry gap", []simulationTestBar{{100, 105, 90, 100, 90, math.NaN(), 1}, simulationBar(105, 135, 95, 105)}, 0, 2, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
		{"3xSL from entry gap", []simulationTestBar{{100, 105, 90, 100, 90, math.NaN(), 1}, simulationBar(105, 150, 95, 105)}, 0, 3, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
		{"target not based on matched close", []simulationTestBar{{100, 105, 90, 100, 90, 110, 1}, simulationBar(105, 119, 95, 105)}, 0, 1, AnalysisSimulationSummary{Expired: 1}},
		{"previous stop and entry gap combined", []simulationTestBar{{100, 105, 80, 100, 80, 110, 0}, {100, 105, 90, 100, 90, 110, 1}, simulationBar(105, 155, 95, 105)}, 1, 2, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
		{"multiple target both extrema still stop first", []simulationTestBar{{100, 105, 90, 100, 90, 110, 1}, simulationBar(100, 130, 85, 100)}, 0, 2, AnalysisSimulationSummary{StopLossHits: 1}},
		{"zero risk", []simulationTestBar{{100, 105, 90, 100, 100, 110, 1}, simulationBar(100, 120, 95, 100)}, 0, 1, AnalysisSimulationSummary{InvalidLevels: 1}},
		{"negative risk", []simulationTestBar{{100, 105, 90, 100, 110, 120, 1}, simulationBar(100, 120, 95, 100)}, 0, 2, AnalysisSimulationSummary{InvalidLevels: 1}},
		{"finite prices but risk overflow", []simulationTestBar{{100, 105, 95, 100, -math.MaxFloat64, 1, 1}, simulationBar(math.MaxFloat64, math.MaxFloat64, 1, math.MaxFloat64)}, 0, 1, AnalysisSimulationSummary{InvalidLevels: 1}},
		{"finite risk but target overflow", []simulationTestBar{{100, 105, 95, 100, math.MaxFloat64 / 2, 1, 1}, simulationBar(math.MaxFloat64*0.75, math.MaxFloat64, math.MaxFloat64/2, math.MaxFloat64*0.75)}, 0, 3, AnalysisSimulationSummary{InvalidLevels: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := analysisTestManager(t)
			name := "btcusd-m1-2024-01-01-2024-01-02.csv"
			writeDataFile(t, m, name, simulationCSV(test.bars, 1704067200000))
			request := simulationRequest(1)
			request.TradeSimulation.StopLossBarsAgo = test.offset
			request.TradeSimulation.TakeProfitMultiple = test.multiple
			if test.multiple != 0 {
				request.TradeSimulation.TakeProfit = ""
			}
			if _, err := m.StartAnalysis(request); err != nil {
				t.Fatal(err)
			}
			result := resultNamed(t, waitAnalysis(t, m), name)
			if result.Status != "completed" || result.Matches != 1 || result.Simulation == nil || *result.Simulation != test.want {
				t.Fatalf("result=%+v summary=%+v want=%+v", result, result.Simulation, test.want)
			}
			chart, err := m.AnalysisChart(result.ID, 0, 0, 0)
			if err != nil || len(chart.ProfitableMatches) != int(test.want.TakeProfitHits) {
				t.Fatalf("winner on signal bar with exit outside chart: %+v %v", chart, err)
			}
			if len(chart.ProfitableMatches) == 1 && (chart.ProfitableMatches[0].Index != chart.MatchIndex || chart.ProfitableMatches[0].Time != chart.MatchTime) {
				t.Fatalf("winner marked an exit instead of the signal: %+v", chart)
			}
		})
	}
}

func TestAnalysisSimulationExtensionValidationLegacyAndBounds(t *testing.T) {
	legacy := `{"stopLoss":"low","takeProfit":"high","duration":3}`
	var config AnalysisTradeSimulation
	if err := json.Unmarshal([]byte(legacy), &config); err != nil || config.StopLossBarsAgo != 0 || config.TakeProfitMultiple != 0 {
		t.Fatalf("legacy defaults changed: %+v %v", config, err)
	}
	for _, draft := range []bool{false, true} {
		for _, value := range []int{-1, 10001} {
			invalid := config
			invalid.StopLossBarsAgo = value
			if validateAnalysisTradeSimulation(&invalid, draft) == nil {
				t.Fatalf("accepted stop offset %d draft=%v", value, draft)
			}
		}
		for _, value := range []int{-1, 4} {
			invalid := config
			invalid.TakeProfitMultiple = value
			if validateAnalysisTradeSimulation(&invalid, draft) == nil {
				t.Fatalf("accepted target multiple %d draft=%v", value, draft)
			}
		}
	}
	config.StopLoss, config.StopLossBarsAgo, config.TakeProfit, config.TakeProfitMultiple = "low", 10000, "", 1
	if err := validateAnalysisTradeSimulation(&config, false); err != nil {
		t.Fatal(err)
	}
	series := simulationTestSeries()
	delete(series.positions, "profit")
	simulation := newAnalysisTradeSimulator(series, &config)
	if simulation.unsupported {
		t.Fatalf("risk multiple required a target column: %s", simulation.summary.Reason)
	}
	for i := int64(0); i <= 10000; i++ {
		row := simulationTestRow(simulationBar(100, 105, 95, 100))
		if err := simulation.advance(i, row); err != nil {
			t.Fatal(err)
		}
		if i == 0 || i == 10000 {
			simulation.match(row)
		}
	}
	if err := simulation.advance(10001, simulationTestRow(simulationBar(100, 105, 96, 100))); err != nil {
		t.Fatal(err)
	}
	simulation.finish()
	if got := simulation.snapshot(2); got.TakeProfitHits != 1 || got.InvalidLevels != 1 || len(simulation.stopHistory) != 10001 {
		t.Fatalf("maximum history bound or unavailable history failed: %+v samples=%d", got, len(simulation.stopHistory))
	}
}

func TestAnalysisProfitableChartMarkersIncludeAdjacentSignalsAndRespectWindows(t *testing.T) {
	m := analysisTestManager(t)
	name := "btcusd-m1-2024-01-01-2024-01-02.csv"
	bar := simulationBar(100, 105, 95, 100)
	bar.signal = 1
	bars := []simulationTestBar{bar, bar, bar, simulationBar(100, 115, 95, 100), bar, simulationBar(100, 105, 85, 100), bar}
	writeDataFile(t, m, name, simulationCSV(bars, 1704067200000))
	if _, err := m.StartAnalysis(simulationRequest(4)); err != nil {
		t.Fatal(err)
	}
	result := resultNamed(t, waitAnalysis(t, m), name)
	if result.Matches != 5 || result.Simulation.TakeProfitHits != 3 {
		t.Fatalf("unexpected fixture summary %+v %+v", result, result.Simulation)
	}
	for _, test := range []struct {
		occurrence, before, after int
		indices                   []int64
	}{{1, 1, 1, []int64{0, 1, 2}}, {1, 0, 0, []int64{1}}, {0, 0, 1, []int64{0, 1}}, {2, 1, 2, []int64{1, 2}}, {3, 0, 2, []int64{}}, {4, 0, 0, []int64{}}} {
		chart, err := m.AnalysisChart(result.ID, test.occurrence, test.before, test.after)
		if err != nil {
			t.Fatal(err)
		}
		indices := []int64{}
		for _, winner := range chart.ProfitableMatches {
			indices = append(indices, winner.Index)
			if winner.Time != 1704067200000+winner.Index*60000 {
				t.Fatalf("wrong signal timestamp %+v", winner)
			}
		}
		if !reflect.DeepEqual(indices, test.indices) {
			t.Fatalf("window %+v: winners %v", test, indices)
		}
	}
	// Outcomes are a snapshot of the exact CSV that was analyzed, never a
	// promise that a later replacement/append contains the same winning bars.
	f, err := os.OpenFile(filepath.Join(m.directory, name), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("1704067620000,100,105,95,100,90,110,0\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err = m.AnalysisChart(result.ID, 0, 0, 0); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale CSV accepted with old winning markers: %v", err)
	}
}

func TestAnalysisProfitableChartMarkersAcrossDensePagesAndLifecycle(t *testing.T) {
	m := analysisTestManager(t)
	bar := simulationBar(100, 115, 95, 100)
	bar.signal = 1
	bars := make([]simulationTestBar, 2051)
	for i := range bars {
		bars[i] = bar
	}
	name := "btcusd-m1-2024-01-01-2024-01-03.csv"
	writeDataFile(t, m, name, simulationCSV(bars, 1704067200000))
	if _, err := m.StartAnalysis(simulationRequest(1)); err != nil {
		t.Fatal(err)
	}
	result := resultNamed(t, waitAnalysis(t, m), name)
	path := m.analysis.matchFiles[result.ID] + ".profits"
	stat, err := os.Stat(path)
	if err != nil || stat.Size() != result.Matches {
		t.Fatalf("outcomes not one byte per occurrence: %+v %v", stat, err)
	}
	for _, occurrence := range []int{0, 256, 1024, 2049, 2050} {
		chart, err := m.AnalysisChart(result.ID, occurrence, 500, 500)
		if err != nil {
			t.Fatal(err)
		}
		want := min(2050, occurrence+501) - max(0, occurrence-500)
		if len(chart.ProfitableMatches) != want {
			t.Fatalf("occurrence=%d markers=%d want=%d", occurrence, len(chart.ProfitableMatches), want)
		}
		for i, winner := range chart.ProfitableMatches {
			if winner.Index != chart.StartIndex+int64(i) {
				t.Fatalf("missing/duplicated winning signal at occurrence=%d: %+v", occurrence, winner)
			}
		}
	}
	oldWork := m.analysis.work
	request := simulationRequest(1)
	request.TradeSimulation = nil
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	result = resultNamed(t, waitAnalysis(t, m), name)
	chart, err := m.AnalysisChart(result.ID, 0, 0, 1)
	if err != nil || len(chart.ProfitableMatches) != 0 {
		t.Fatalf("disabled simulation leaked previous markers: %+v %v", chart, err)
	}
	if _, err := os.Stat(oldWork); !os.IsNotExist(err) {
		t.Fatalf("new analysis retained old outcome files: %v", err)
	}
	work := m.analysis.work
	m.Close()
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("closing manager retained outcome files: %v", err)
	}
}

func TestAnalysisSimulationProfitOutcomeWriteErrorsPropagate(t *testing.T) {
	simulation := newAnalysisTradeSimulator(simulationTestSeries(), simulationRequest(1).TradeSimulation)
	want := errors.New("outcome storage failed")
	simulation.onTakeProfit = func(int64) error { return want }
	simulation.match(simulationTestRow(simulationBar(100, 105, 95, 100)))
	if err := simulation.advance(1, simulationTestRow(simulationBar(100, 115, 95, 100))); !errors.Is(err, want) {
		t.Fatalf("outcome write error ignored: %v", err)
	}
}
