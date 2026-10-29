package screener

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func shortSimulationBar(open, high, low, close float64) simulationTestBar {
	return simulationTestBar{open: open, high: high, low: low, close: close, stop: 110, profit: 90}
}

func TestAnalysisSimulationShortOrderedExitsHistoryRiskAndChartPrices(t *testing.T) {
	matched := shortSimulationBar(100, 120, 80, 100)
	matched.signal = 1
	quiet := shortSimulationBar(100, 105, 95, 100)
	tests := []struct {
		name                       string
		bars                       []simulationTestBar
		duration, offset, multiple int
		want                       AnalysisSimulationSummary
		levels                     *AnalysisTradeLevels
	}{
		{"next bar target inclusive", []simulationTestBar{matched, shortSimulationBar(100, 105, 90, 95)}, 1, 0, 0, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}, &AnalysisTradeLevels{100, 110, 90}},
		{"next bar stop inclusive", []simulationTestBar{matched, shortSimulationBar(100, 110, 95, 105)}, 1, 0, 0, AnalysisSimulationSummary{StopLossHits: 1}, &AnalysisTradeLevels{100, 110, 90}},
		{"both extrema stop first", []simulationTestBar{matched, shortSimulationBar(100, 120, 80, 100)}, 1, 0, 0, AnalysisSimulationSummary{StopLossHits: 1}, &AnalysisTradeLevels{100, 110, 90}},
		{"gap target before later stop", []simulationTestBar{matched, quiet, shortSimulationBar(85, 120, 80, 100)}, 2, 0, 0, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}, &AnalysisTradeLevels{100, 110, 90}},
		{"gap stop before later target", []simulationTestBar{matched, quiet, shortSimulationBar(115, 120, 80, 100)}, 2, 0, 0, AnalysisSimulationSummary{StopLossHits: 1}, &AnalysisTradeLevels{100, 110, 90}},
		{"entry below target invalid", []simulationTestBar{matched, shortSimulationBar(85, 105, 80, 90)}, 1, 0, 0, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"entry above stop invalid", []simulationTestBar{matched, shortSimulationBar(115, 120, 100, 110)}, 1, 0, 0, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"entry equals stop invalid", []simulationTestBar{matched, shortSimulationBar(110, 120, 95, 100)}, 1, 0, 0, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"entry equals target invalid", []simulationTestBar{matched, shortSimulationBar(90, 105, 85, 100)}, 1, 0, 0, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"duration includes entry bar", []simulationTestBar{matched, quiet, shortSimulationBar(100, 105, 80, 90)}, 1, 0, 0, AnalysisSimulationSummary{Expired: 1}, &AnalysisTradeLevels{100, 110, 90}},
		{"last allowed bar target", []simulationTestBar{matched, quiet, shortSimulationBar(100, 105, 90, 95)}, 2, 0, 0, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}, &AnalysisTradeLevels{100, 110, 90}},
		{"incomplete duration", []simulationTestBar{matched, quiet}, 10, 0, 0, AnalysisSimulationSummary{InsufficientData: 1}, &AnalysisTradeLevels{100, 110, 90}},
		{"no next bar", []simulationTestBar{matched}, 1, 0, 0, AnalysisSimulationSummary{InsufficientData: 1}, nil},
		{"missing stop warmup", []simulationTestBar{{100, 105, 95, 100, math.NaN(), 90, 1}, quiet}, 1, 0, 0, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"missing profit warmup", []simulationTestBar{{100, 105, 95, 100, 110, math.NaN(), 1}, quiet}, 1, 0, 0, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"reversed long bracket", []simulationTestBar{{100, 105, 95, 100, 90, 110, 1}, quiet}, 1, 0, 0, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"freeze matched fields", []simulationTestBar{matched, {100, 105, 88, 100, 150, 80, 0}}, 1, 0, 0, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}, &AnalysisTradeLevels{100, 110, 90}},
		{"previous stop outside chart", []simulationTestBar{{100, 120, 90, 100, 120, 80, 0}, matched, shortSimulationBar(100, 115, 85, 90)}, 1, 1, 0, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}, &AnalysisTradeLevels{100, 120, 90}},
		{"missing previous stop", []simulationTestBar{matched, quiet}, 1, 1, 0, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"previous stop warmup", []simulationTestBar{{100, 105, 95, 100, math.NaN(), 90, 0}, matched, quiet}, 1, 1, 0, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"1xSL uses next open", []simulationTestBar{matched, shortSimulationBar(95, 100, 80, 90)}, 1, 0, 1, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}, &AnalysisTradeLevels{95, 110, 80}},
		{"2xSL uses next open", []simulationTestBar{matched, shortSimulationBar(95, 100, 65, 90)}, 1, 0, 2, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}, &AnalysisTradeLevels{95, 110, 65}},
		{"3xSL ignores missing target field", []simulationTestBar{{100, 105, 95, 100, 110, math.NaN(), 1}, shortSimulationBar(95, 100, 50, 90)}, 1, 0, 3, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}, &AnalysisTradeLevels{95, 110, 50}},
		{"risk target both extrema stop first", []simulationTestBar{matched, shortSimulationBar(100, 115, 75, 90)}, 1, 0, 2, AnalysisSimulationSummary{StopLossHits: 1}, &AnalysisTradeLevels{100, 110, 80}},
		{"risk target not based on signal close", []simulationTestBar{matched, shortSimulationBar(95, 100, 85, 90)}, 1, 0, 1, AnalysisSimulationSummary{Expired: 1}, &AnalysisTradeLevels{95, 110, 80}},
		{"zero risk invalid", []simulationTestBar{matched, shortSimulationBar(110, 115, 80, 100)}, 1, 0, 1, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"negative risk invalid", []simulationTestBar{matched, shortSimulationBar(115, 120, 80, 100)}, 1, 0, 1, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
		{"target overflow invalid", []simulationTestBar{{100, 105, 95, 100, math.MaxFloat64, 90, 1}, shortSimulationBar(math.MaxFloat64/8, math.MaxFloat64/8, math.MaxFloat64/8, math.MaxFloat64/8)}, 1, 0, 3, AnalysisSimulationSummary{InvalidLevels: 1}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := analysisTestManager(t)
			name := "btcusd-m1-2024-01-01-2024-01-02.csv"
			writeDataFile(t, m, name, simulationCSV(test.bars, 1704067200000))
			request := simulationRequest(test.duration)
			request.TradeSimulation.Direction = "short"
			request.TradeSimulation.StopLossBarsAgo = test.offset
			request.TradeSimulation.TakeProfitMultiple = test.multiple
			if test.multiple > 0 {
				request.TradeSimulation.TakeProfit = ""
			}
			if _, err := m.StartAnalysis(request); err != nil {
				t.Fatal(err)
			}
			// Applied requests and recorded chart prices survive later draft edits.
			request.TradeSimulation.Direction = "long"
			if _, err := m.SaveAnalysisView(request); err != nil {
				t.Fatal(err)
			}
			info := waitAnalysis(t, m)
			result := resultNamed(t, info, name)
			if result.Status != "completed" || result.Matches != 1 || result.Simulation == nil || *result.Simulation != test.want || info.TradeSimulation.Direction != "short" {
				t.Fatalf("result=%+v summary=%+v want=%+v config=%+v", result, result.Simulation, test.want, info.TradeSimulation)
			}
			chart, err := m.AnalysisChart(result.ID, 0, 0, 0)
			if err != nil || !reflect.DeepEqual(chart.Trade, test.levels) || len(chart.ProfitableMatches) != int(test.want.TakeProfitHits) {
				t.Fatalf("signal chart=%+v want levels=%+v error=%v", chart, test.levels, err)
			}
			if len(chart.ProfitableMatches) > 0 && (chart.ProfitableMatches[0].Time != chart.MatchTime || chart.ProfitableMatches[0].Index != chart.MatchIndex) {
				t.Fatalf("profit marker moved away from the matched bar: %+v", chart)
			}
		})
	}
}

func TestAnalysisSimulationDirectionValidationAndLegacyDefault(t *testing.T) {
	var legacy AnalysisTradeSimulation
	if err := json.Unmarshal([]byte(`{"stopLoss":"stop","takeProfit":"profit","duration":3}`), &legacy); err != nil || legacy.Direction != "" {
		t.Fatalf("legacy direction changed: %+v %v", legacy, err)
	}
	for _, draft := range []bool{false, true} {
		for _, direction := range []string{"", "long", "short"} {
			config := legacy
			config.Direction = direction
			if err := validateAnalysisTradeSimulation(&config, draft); err != nil {
				t.Fatalf("valid direction=%q draft=%v: %v", direction, draft, err)
			}
		}
		for _, direction := range []string{"SHORT", "Long", " short", "long ", "both", strings.Repeat("s", 1025)} {
			request := simulationRequest(3)
			request.TradeSimulation.Direction = direction
			if err := validateAnalysisRequest(request, draft); err == nil {
				t.Fatalf("accepted direction=%q draft=%v", direction, draft)
			}
		}
	}
	encoded, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(encoded), "direction") {
		t.Fatalf("legacy configuration gained a direction: %s %v", encoded, err)
	}
	for _, direction := range []string{"", "long"} {
		config := legacy
		config.Direction = direction
		simulation := newAnalysisTradeSimulator(simulationTestSeries(), &config)
		simulation.match(simulationTestRow(simulationBar(100, 105, 95, 100)))
		if err := simulation.advance(1, simulationTestRow(simulationBar(100, 110, 95, 105))); err != nil {
			t.Fatal(err)
		}
		simulation.finish()
		if got := simulation.snapshot(1); got.TakeProfitHits != 1 || got.ProfitablePercent != 100 {
			t.Fatalf("legacy/explicit long behavior changed: %+v", got)
		}
	}
}

func TestAnalysisSimulationShortRiskOverflow(t *testing.T) {
	config := simulationRequest(1).TradeSimulation
	config.Direction, config.TakeProfitMultiple = "short", 1
	simulation := newAnalysisTradeSimulator(simulationTestSeries(), config)
	matched := shortSimulationBar(100, 105, 95, 100)
	matched.stop = math.MaxFloat64
	simulation.match(simulationTestRow(matched))
	// The core simulator accepts arbitrary finite prices; CSV import separately
	// requires positive OHLC prices. Finite endpoints can overflow the risk.
	if err := simulation.advance(1, simulationTestRow(shortSimulationBar(-math.MaxFloat64, 1, -math.MaxFloat64, -math.MaxFloat64))); err != nil {
		t.Fatal(err)
	}
	simulation.finish()
	if got := simulation.snapshot(1); got.InvalidLevels != 1 || simulationSummaryTotal(got) != 1 {
		t.Fatalf("overflowed short risk was accepted: %+v", got)
	}
}

func TestAnalysisSimulationShortDenseMemoryBound(t *testing.T) {
	for _, duration := range []int{1, 7, 10000} {
		config := simulationRequest(duration).TradeSimulation
		config.Direction = "short"
		simulation := newAnalysisTradeSimulator(simulationTestSeries(), config)
		row := simulationTestRow(shortSimulationBar(100, 105, 95, 100))
		for index := int64(0); index < 50000; index++ {
			if err := simulation.advance(index, row); err != nil {
				t.Fatal(err)
			}
			simulation.match(row)
			if len(simulation.active) > duration || len(simulation.expiration) != duration || simulation.stops.Len() > 2*(duration+1) || simulation.profits.Len() > 2*(duration+1) {
				t.Fatalf("unbounded short simulation duration=%d row=%d: active=%d stopHeap=%d targetHeap=%d", duration, index, len(simulation.active), simulation.stops.Len(), simulation.profits.Len())
			}
		}
		simulation.finish()
		if got := simulation.snapshot(50000); got.Expired != 50000-int64(duration) || got.InsufficientData != int64(duration) || simulationSummaryTotal(got) != 50000 {
			t.Fatalf("dense short scan lost matches: %+v", got)
		}
	}
}

func TestLiveAlertShortIndicativeLevels(t *testing.T) {
	m := analysisTestManager(t)
	s := reviewLiveSeries(t, m, "eurusd-h1-2024-01-01-2024-01-02.csv", "timestamp,open,high,low,close,zero,missing\n1704067200000,12,15,9,12,0,\n1704070800000,10,12,9,11,0,\n")
	row, err := s.row(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, stop, target     string
		offset, multiple       int
		stopValue, targetValue *float64
	}{
		{"field target", "high", "low", 0, 0, floatPointer(12), floatPointer(9)},
		{"zero target", "high", "zero", 0, 0, floatPointer(12), floatPointer(0)},
		{"previous stop signal target", "high", "low", 1, 0, floatPointer(15), floatPointer(9)},
		{"2xSL uses signal close", "high", "", 1, 2, floatPointer(15), floatPointer(3)},
		{"stop below entry", "low", "low", 0, 0, nil, floatPointer(9)},
		{"stop at entry", "close", "low", 0, 0, nil, floatPointer(9)},
		{"target above entry", "high", "high", 0, 0, floatPointer(12), nil},
		{"target at entry", "high", "close", 0, 0, floatPointer(12), nil},
		{"empty levels", "missing", "missing", 0, 0, nil, nil},
		{"risk needs valid stop", "low", "", 0, 2, nil, nil},
		{"risk needs stop history", "high", "", 2, 2, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := SavedAnalysisCondition{ID: strings.Repeat("a", 32), Name: "Short levels", Request: AnalysisRequest{TradeSimulation: &AnalysisTradeSimulation{Direction: "short", StopLoss: tc.stop, StopLossBarsAgo: tc.offset, TakeProfit: tc.target, TakeProfitMultiple: tc.multiple, Duration: 1}}}
			alert, err := liveAlertFromMatch(saved, s, 1, row)
			if err != nil || alert.EntryPrice != 11 || alert.Time != row.timestamp || !reflect.DeepEqual(alert.StopLoss, tc.stopValue) || !reflect.DeepEqual(alert.TakeProfit, tc.targetValue) {
				t.Fatalf("wrong indicative short prices: %+v error=%v", alert, err)
			}
		})
	}
}

func floatPointer(value float64) *float64 { return &value }
