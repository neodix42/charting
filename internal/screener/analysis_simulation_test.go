package screener

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type simulationTestBar struct {
	open, high, low, close, stop, profit, signal float64
}

func simulationBar(open, high, low, close float64) simulationTestBar {
	return simulationTestBar{open: open, high: high, low: low, close: close, stop: 90, profit: 110}
}

func simulationCSV(bars []simulationTestBar, start int64) string {
	var csv strings.Builder
	csv.WriteString("timestamp,open,high,low,close,stop,profit,signal\n")
	for i, bar := range bars {
		fmt.Fprintf(&csv, "%d,%g,%g,%g,%g,", start+int64(i)*60000, bar.open, bar.high, bar.low, bar.close)
		if !math.IsNaN(bar.stop) {
			fmt.Fprintf(&csv, "%g", bar.stop)
		}
		csv.WriteByte(',')
		if !math.IsNaN(bar.profit) {
			fmt.Fprintf(&csv, "%g", bar.profit)
		}
		fmt.Fprintf(&csv, ",%g\n", bar.signal)
	}
	return csv.String()
}

func simulationRequest(duration int) AnalysisRequest {
	return AnalysisRequest{
		Conditions:      []AnalysisCondition{analysisCondition("equal", analysisField("signal"), analysisNumber(1))},
		TradeSimulation: &AnalysisTradeSimulation{StopLoss: "stop", TakeProfit: "profit", Duration: duration},
	}
}

func simulationSummaryTotal(summary *AnalysisSimulationSummary) int64 {
	return summary.TakeProfitHits + summary.StopLossHits + summary.Expired + summary.InsufficientData + summary.InvalidLevels + summary.Unavailable
}

func TestAnalysisSimulationUsesNextClosedBarFrozenLevelsAndOrderedExits(t *testing.T) {
	tests := []struct {
		name     string
		bars     []simulationTestBar
		duration int
		want     AnalysisSimulationSummary
	}{
		{"next bar target inclusive", []simulationTestBar{simulationBar(100, 120, 80, 100), simulationBar(100, 110, 95, 105)}, 1, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
		{"next bar stop inclusive", []simulationTestBar{simulationBar(100, 120, 80, 100), simulationBar(100, 105, 90, 95)}, 1, AnalysisSimulationSummary{StopLossHits: 1}},
		{"both extremes stop first", []simulationTestBar{simulationBar(100, 105, 95, 100), simulationBar(100, 120, 80, 100)}, 1, AnalysisSimulationSummary{StopLossHits: 1}},
		{"gap target precedes later stop", []simulationTestBar{simulationBar(100, 105, 95, 100), simulationBar(100, 105, 95, 100), simulationBar(115, 120, 80, 100)}, 2, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
		{"gap stop precedes later target", []simulationTestBar{simulationBar(100, 105, 95, 100), simulationBar(100, 105, 95, 100), simulationBar(85, 120, 80, 100)}, 2, AnalysisSimulationSummary{StopLossHits: 1}},
		{"entry gap beyond target invalid", []simulationTestBar{simulationBar(100, 105, 95, 100), simulationBar(115, 120, 95, 100)}, 1, AnalysisSimulationSummary{InvalidLevels: 1}},
		{"entry equals stop invalid", []simulationTestBar{simulationBar(100, 105, 95, 100), simulationBar(90, 120, 85, 100)}, 1, AnalysisSimulationSummary{InvalidLevels: 1}},
		{"entry equals target invalid", []simulationTestBar{simulationBar(100, 105, 95, 100), simulationBar(110, 120, 95, 100)}, 1, AnalysisSimulationSummary{InvalidLevels: 1}},
		{"duration includes entry bar", []simulationTestBar{simulationBar(100, 105, 95, 100), simulationBar(100, 105, 95, 100), simulationBar(100, 120, 95, 110)}, 1, AnalysisSimulationSummary{Expired: 1}},
		{"hit on last allowed bar", []simulationTestBar{simulationBar(100, 105, 95, 100), simulationBar(100, 105, 95, 100), simulationBar(100, 110, 95, 105)}, 2, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
		{"short horizon with target still wins", []simulationTestBar{simulationBar(100, 105, 95, 100), simulationBar(100, 110, 95, 105)}, 10, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
		{"unresolved short horizon", []simulationTestBar{simulationBar(100, 105, 95, 100), simulationBar(100, 105, 95, 100)}, 10, AnalysisSimulationSummary{InsufficientData: 1}},
		{"no next bar", []simulationTestBar{simulationBar(100, 120, 80, 100)}, 1, AnalysisSimulationSummary{InsufficientData: 1}},
		{"missing warmup level", []simulationTestBar{{100, 105, 95, 100, math.NaN(), 110, 0}, simulationBar(100, 120, 95, 110)}, 1, AnalysisSimulationSummary{InvalidLevels: 1}},
		{"reversed levels", []simulationTestBar{{100, 105, 95, 100, 110, 90, 0}, simulationBar(100, 120, 95, 110)}, 1, AnalysisSimulationSummary{InvalidLevels: 1}},
		{"freeze matched field values", []simulationTestBar{simulationBar(100, 105, 95, 100), {100, 112, 95, 100, 99, 120, 0}}, 1, AnalysisSimulationSummary{TakeProfitHits: 1, ProfitablePercent: 100}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := analysisTestManager(t)
			test.bars[0].signal = 1
			name := "btcusd-m1-2024-01-01-2024-01-02.csv"
			writeDataFile(t, m, name, simulationCSV(test.bars, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()))
			request := simulationRequest(test.duration)
			if _, err := m.StartAnalysis(request); err != nil {
				t.Fatal(err)
			}
			info := waitAnalysis(t, m)
			result := resultNamed(t, info, name)
			if result.Status != "completed" || result.Matches != 1 || result.Simulation == nil || *result.Simulation != test.want {
				t.Fatalf("unexpected trade result: %+v summary=%+v want=%+v", result, result.Simulation, test.want)
			}
			if !reflect.DeepEqual(info.TradeSimulation, request.TradeSimulation) {
				t.Fatalf("missing configuration snapshot: %+v", info.TradeSimulation)
			}
		})
	}
}

func TestAnalysisSimulationOverlapsKeepAllMatchesInDenominatorAndClosedBarsOnly(t *testing.T) {
	m := analysisTestManager(t)
	bar := simulationBar(100, 105, 95, 100)
	bar.signal = 1
	bars := []simulationTestBar{bar, bar, simulationBar(100, 112, 95, 105), bar}
	bars[2].signal = 1
	// The final row is a still-open minute bar. Its target touch must not
	// settle the trade from the preceding closed bar or itself create a match.
	// Leave a full minute before it closes, so a clock boundary cannot race
	// fixture creation and StartAnalysis's independently captured as-of time.
	bars[3].high = 120
	now := time.Now().UTC().UnixMilli()
	name := "btcusd-m1-2024-01-01-2030-01-01.csv"
	writeDataFile(t, m, name, simulationCSV(bars, now-3*60000))
	request := simulationRequest(5)
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	// Mutating the caller's object cannot alter the run's levels or duration.
	request.TradeSimulation.TakeProfit = "stop"
	request.TradeSimulation.Duration = 1
	info := waitAnalysis(t, m)
	result := resultNamed(t, info, name)
	if result.Matches != 3 || result.Rows != 3 || result.Simulation == nil || result.Simulation.TakeProfitHits != 2 || result.Simulation.InsufficientData != 1 || result.Simulation.ProfitablePercent != 200.0/3 {
		t.Fatalf("unexpected overlapping closed-bar results: %+v, %+v", result, result.Simulation)
	}
	chart, err := m.AnalysisChart(result.ID, 0, 0, 500)
	if err != nil || len(chart.Bars) != 3 || len(chart.ProfitableMatches) != 2 || chart.ProfitableMatches[0].Index != 0 || chart.ProfitableMatches[1].Index != 1 {
		t.Fatalf("open candle settled or marked a trade: %+v %v", chart, err)
	}
	info.TradeSimulation.StopLoss = "mutated"
	result.Simulation.TakeProfitHits = 999
	copy, err := m.Analysis(0, 10)
	if err != nil || copy.Results[0].Simulation.TakeProfitHits != 2 || copy.TradeSimulation.StopLoss != "stop" {
		t.Fatalf("returned configuration or summary aliases mutable state: %+v, %v", copy, err)
	}
}

func TestAnalysisSimulationUnsupportedFilesKeepMatchingRows(t *testing.T) {
	tests := []struct{ name, data, reason string }{
		{"btcusd-tick-2024-01-01-2024-01-02.csv", "timestamp,bidPrice,askPrice,signal,stop,profit\n1704067200000,100,101,1,90,110\n1704067201000,105,106,1,90,110\n", "tick"},
		{"btcusd-m1-2024-01-01-2024-01-02.csv", "timestamp,close,signal,stop,profit\n1704067200000,100,1,90,110\n1704067260000,105,1,90,110\n", "open"},
		{"btcusd-m1-2024-01-01-2024-01-02.csv", "timestamp,open,high,low,close,signal,profit\n1704067200000,100,105,95,100,1,110\n1704067260000,100,105,95,100,1,110\n", "stop"},
	}
	for _, test := range tests {
		t.Run(test.reason, func(t *testing.T) {
			m := analysisTestManager(t)
			writeDataFile(t, m, test.name, test.data)
			if _, err := m.StartAnalysis(simulationRequest(3)); err != nil {
				t.Fatal(err)
			}
			result := resultNamed(t, waitAnalysis(t, m), test.name)
			if result.Status != "completed" || result.Matches != 2 || result.Simulation == nil || result.Simulation.Unavailable != 2 || result.Simulation.ProfitablePercent != 0 || !strings.Contains(result.Simulation.Reason, test.reason) {
				t.Fatalf("unsupported simulation removed matches or hid reason: %+v, %+v", result, result.Simulation)
			}
		})
	}
}

func TestAnalysisSimulationRequestValidationAndPersistence(t *testing.T) {
	m := analysisTestManager(t)
	request := simulationRequest(3)
	request.TradeSimulation.Direction = "short"
	request.TradeSimulation.StopLossBarsAgo = 1
	request.TradeSimulation.TakeProfitMultiple = 3
	request.TradeSimulation.TakeProfit = ""
	request.Categories = []string{categoryForSymbol(t, "btcusd")}
	if _, err := m.SaveAnalysisView(request); err != nil {
		t.Fatal(err)
	}
	saved, err := m.SaveAnalysisCondition("Simulated entry", request)
	if err != nil {
		t.Fatal(err)
	}
	want := copyAnalysisRequest(request)
	request.TradeSimulation.Duration = 9000
	request.TradeSimulation.Direction = "long"
	request.TradeSimulation.StopLoss = "changed"
	request.TradeSimulation.StopLossBarsAgo = 9999
	request.TradeSimulation.TakeProfitMultiple = 2
	saved.Request.TradeSimulation.StopLoss = "changed return"
	m.Close()
	restored := newManager(m.profile, func() (workerRuntime, error) { return workerRuntime{}, fmt.Errorf("no runtime") })
	t.Cleanup(restored.Close)
	draft, err := restored.AnalysisView()
	if err != nil || !reflect.DeepEqual(draft, want) {
		t.Fatalf("draft restoration: %+v %v", draft, err)
	}
	renamed, err := restored.RenameAnalysisCondition(saved.ID, "Renamed simulation")
	if err != nil || !reflect.DeepEqual(renamed.Request, want) {
		t.Fatalf("rename changed simulation: %+v %v", renamed, err)
	}
	conditions, err := restored.SavedAnalysisConditions()
	if err != nil || len(conditions) != 1 || !reflect.DeepEqual(conditions[0].Request, want) {
		t.Fatalf("saved restoration: %+v %v", conditions, err)
	}
	if err := restored.DeleteAnalysisCondition(saved.ID); err != nil {
		t.Fatal(err)
	}
	if draft, err := restored.AnalysisView(); err != nil || !reflect.DeepEqual(draft, want) {
		t.Fatalf("delete changed draft: %+v %v", draft, err)
	}
	incomplete := copyAnalysisRequest(want)
	incomplete.TradeSimulation.StopLoss, incomplete.TradeSimulation.TakeProfit = "", ""
	if _, err = restored.SaveAnalysisView(incomplete); err != nil {
		t.Fatalf("incomplete draft rejected: %v", err)
	}
	if _, err = restored.SaveAnalysisCondition("Incomplete", incomplete); err == nil {
		t.Fatal("incomplete named condition accepted")
	}
	if _, err = restored.StartAnalysis(incomplete); err == nil {
		t.Fatal("incomplete analysis accepted")
	}
	for _, duration := range []int{-1, 0, 10001} {
		invalid := copyAnalysisRequest(want)
		invalid.TradeSimulation.Duration = duration
		if err = validateAnalysisRequest(invalid, true); err == nil {
			t.Fatalf("invalid draft duration %d", duration)
		}
		if err = validateAnalysisRequest(invalid, false); err == nil {
			t.Fatalf("invalid run duration %d", duration)
		}
	}
	invalid := copyAnalysisRequest(want)
	invalid.TradeSimulation.StopLoss = strings.Repeat("s", 1025)
	if err = validateAnalysisRequest(invalid, true); err == nil {
		t.Fatal("oversized field accepted")
	}
	old := copyAnalysisRequest(want)
	old.TradeSimulation = nil
	if _, err = restored.SaveAnalysisCondition("Existing format", old); err != nil {
		t.Fatalf("old format rejected: %v", err)
	}
}

// This deliberately simple oracle is bounded to small test inputs. Production
// uses a sweep and never rescans a duration-sized horizon for every match.
func simulationOracle(bars []simulationTestBar, config AnalysisTradeSimulation) AnalysisSimulationSummary {
	var result AnalysisSimulationSummary
	var matches int64
	for match, bar := range bars {
		if bar.signal != 1 {
			continue
		}
		matches++
		if match < config.StopLossBarsAgo {
			result.InvalidLevels++
			continue
		}
		stop, profit := bars[match-config.StopLossBarsAgo].stop, bar.profit
		if !analysisFinite(stop) || (config.TakeProfitMultiple == 0 && !analysisFinite(profit)) {
			result.InvalidLevels++
			continue
		}
		entry := match + 1
		if entry >= len(bars) {
			result.InsufficientData++
			continue
		}
		if config.TakeProfitMultiple > 0 {
			if config.Direction == "short" {
				profit = bars[entry].open - float64(config.TakeProfitMultiple)*(stop-bars[entry].open)
			} else {
				profit = bars[entry].open + float64(config.TakeProfitMultiple)*(bars[entry].open-stop)
			}
		}
		valid := stop < bars[entry].open && bars[entry].open < profit
		if config.Direction == "short" {
			valid = profit < bars[entry].open && bars[entry].open < stop
		}
		if !analysisFinite(profit) || !valid {
			result.InvalidLevels++
			continue
		}
		resolved := false
		for next := entry; next < min(len(bars), entry+config.Duration); next++ {
			row := bars[next]
			stopOpen, profitOpen := row.open <= stop, row.open >= profit
			stopExtreme, profitExtreme := row.low <= stop, row.high >= profit
			if config.Direction == "short" {
				stopOpen, profitOpen = row.open >= stop, row.open <= profit
				stopExtreme, profitExtreme = row.high >= stop, row.low <= profit
			}
			switch {
			case stopOpen:
				result.StopLossHits++
				resolved = true
			case profitOpen:
				result.TakeProfitHits++
				resolved = true
			case stopExtreme:
				result.StopLossHits++
				resolved = true
			case profitExtreme:
				result.TakeProfitHits++
				resolved = true
			}
			if resolved {
				break
			}
		}
		if !resolved {
			if entry+config.Duration <= len(bars) {
				result.Expired++
			} else {
				result.InsufficientData++
			}
		}
	}
	if matches > 0 {
		result.ProfitablePercent = 100 * float64(result.TakeProfitHits) / float64(matches)
	}
	return result
}

func simulationTestSeries() *analysisSeries {
	return &analysisSeries{metadata: DataFile{Timeframe: "m1"}, positions: map[string]int{"open": 0, "high": 1, "low": 2, "close": 3, "stop": 4, "profit": 5}}
}

func simulationTestRow(bar simulationTestBar) analysisRow {
	return analysisRow{values: []float64{bar.open, bar.high, bar.low, bar.close, bar.stop, bar.profit}}
}

func TestAnalysisSimulationSweepAgreesWithIndependentOverlappingTradeOracle(t *testing.T) {
	random := rand.New(rand.NewSource(20261027))
	for run := 0; run < 200; run++ {
		duration := 1 + random.Intn(20)
		config := *simulationRequest(duration).TradeSimulation
		config.Direction = []string{"", "long", "short"}[run%3]
		config.StopLossBarsAgo = random.Intn(6)
		config.TakeProfitMultiple = random.Intn(4)
		bars := make([]simulationTestBar, 50+random.Intn(100))
		for i := range bars {
			open := 90 + float64(random.Intn(21))
			bar := simulationBar(open, open+float64(random.Intn(15)), open-float64(random.Intn(15)), open)
			bar.stop, bar.profit = open-float64(1+random.Intn(15)), open+float64(1+random.Intn(15))
			if config.Direction == "short" {
				bar.stop, bar.profit = bar.profit, bar.stop
			}
			if random.Intn(3) != 0 {
				bar.signal = 1
			}
			if random.Intn(30) == 0 {
				bar.stop = math.NaN()
			}
			if random.Intn(30) == 0 {
				bar.profit = math.NaN()
			}
			bars[i] = bar
		}
		simulation := newAnalysisTradeSimulator(simulationTestSeries(), &config)
		var matches int64
		for i, bar := range bars {
			row := simulationTestRow(bar)
			simulation.advance(int64(i), row)
			if bar.signal == 1 {
				simulation.match(row)
				matches++
			}
		}
		simulation.finish()
		got, want := simulation.snapshot(matches), simulationOracle(bars, config)
		if *got != want {
			t.Fatalf("run %d duration %d: got %+v want %+v", run, duration, got, want)
		}
		if simulationSummaryTotal(got) != matches {
			t.Fatalf("run %d lost matches", run)
		}
	}
}

func TestAnalysisSimulationMemoryBoundAndInvalidRowOnlyAffectsOpenTrades(t *testing.T) {
	for _, duration := range []int{1, 7, 10000} {
		simulation := newAnalysisTradeSimulator(simulationTestSeries(), simulationRequest(duration).TradeSimulation)
		bar := simulationTestRow(simulationBar(100, 105, 95, 100))
		for index := int64(0); index < 50000; index++ {
			simulation.advance(index, bar)
			simulation.match(bar)
			if len(simulation.active) > duration || len(simulation.expiration) != duration || simulation.stops.Len() > 2*(duration+1) || simulation.profits.Len() > 2*(duration+1) {
				t.Fatalf("unbounded simulation at duration=%d row=%d: active=%d stopHeap=%d targetHeap=%d", duration, index, len(simulation.active), simulation.stops.Len(), simulation.profits.Len())
			}
		}
		simulation.finish()
		if got := simulation.snapshot(50000); got.Expired != 50000-int64(duration) || got.InsufficientData != int64(duration) || simulationSummaryTotal(got) != 50000 {
			t.Fatalf("dense scan partition: %+v", got)
		}
	}
	simulation := newAnalysisTradeSimulator(simulationTestSeries(), simulationRequest(20).TradeSimulation)
	simulation.match(simulationTestRow(simulationBar(100, 105, 95, 100)))
	simulation.advance(1, simulationTestRow(simulationBar(100, 115, 95, 100)))
	simulation.match(simulationTestRow(simulationBar(100, 105, 95, 100)))
	simulation.advance(2, simulationTestRow(simulationBar(100, 105, 95, 100)))
	simulation.match(simulationTestRow(simulationBar(100, 105, 95, 100)))
	simulation.advance(3, simulationTestRow(simulationBar(100, math.NaN(), 95, 100)))
	simulation.finish()
	if got := simulation.snapshot(3); got.TakeProfitHits != 1 || got.Unavailable != 2 || got.Reason == "" || simulationSummaryTotal(got) != 3 {
		t.Fatalf("invalid candle changed resolved trade: %+v", got)
	}
}

func TestAnalysisSimulationThresholdComparisonsDoNotRequirePositivePrices(t *testing.T) {
	for _, direction := range []string{"", "long", "short"} {
		for _, price := range []float64{0, -50} {
			config := simulationRequest(1).TradeSimulation
			config.Direction = direction
			simulation := newAnalysisTradeSimulator(simulationTestSeries(), config)
			matched := simulationBar(price, price+2, price-2, price)
			matched.stop, matched.profit = price-10, price+10
			entry := simulationBar(price, price+10, price-2, price)
			if direction == "short" {
				matched.stop, matched.profit = matched.profit, matched.stop
				entry = simulationBar(price, price+2, price-10, price)
			}
			simulation.match(simulationTestRow(matched))
			if err := simulation.advance(1, simulationTestRow(entry)); err != nil {
				t.Fatal(err)
			}
			simulation.finish()
			if got := simulation.snapshot(1); got.TakeProfitHits != 1 || got.ProfitablePercent != 100 {
				t.Fatalf("direction %q price %g incorrectly unavailable: %+v", direction, price, got)
			}
		}
	}
}

func TestAnalysisSimulationCancellationDoesNotPublishPartialTrades(t *testing.T) {
	m := analysisTestManager(t)
	name := "btcusd-m1-2024-01-01-2024-01-02.csv"
	bar := simulationBar(100, 105, 95, 100)
	bar.signal = 1
	writeDataFile(t, m, name, simulationCSV([]simulationTestBar{bar, bar, bar}, 1704067200000))
	work := t.TempDir()
	series, err := indexAnalysisCSV(context.Background(), m.Info().Directory, name, filepath.Join(work, "rows"), func(float64) {})
	if err != nil {
		t.Fatal(err)
	}
	series.cache = newAnalysisRowCache()
	a := &analysisState{work: work, asOf: time.Now().UnixMilli(), request: simulationRequest(10000), results: []AnalysisResult{{ID: "test", Name: name}}, series: map[string]*analysisSeries{"test": series}, matchFiles: map[string]string{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = m.scanAnalysisFile(ctx, a, 0); err != context.Canceled {
		t.Fatalf("cancelled scan continued: %v", err)
	}
	if a.results[0].Matches != 0 || a.results[0].Simulation != nil || len(a.matchFiles) != 0 {
		t.Fatalf("partial trade published: %+v", a.results[0])
	}
	for _, suffix := range []string{".matches", ".matches.profits", ".matches.trades"} {
		if _, err = os.Stat(filepath.Join(work, "test"+suffix)); !os.IsNotExist(err) {
			t.Fatalf("cancelled scan retained an outcome index %s: %v", suffix, err)
		}
	}
}

func TestAnalysisSimulationDuplicateAndCategoryPagingKeepPerAssetTotals(t *testing.T) {
	m := analysisTestManager(t)
	bar := simulationBar(100, 105, 95, 100)
	bar.signal = 1
	target := simulationBar(100, 115, 95, 100)
	target.signal = 1
	csv := simulationCSV([]simulationTestBar{bar, target, bar}, 1704067200000)
	for _, symbol := range []string{"btcusd", "ethusd", "eurusd"} {
		for _, from := range []string{"2000", "2001"} {
			writeDataFile(t, m, fmt.Sprintf("%s-m1-%s-01-01-2024-01-02.csv", symbol, from), csv)
		}
	}
	request := simulationRequest(1)
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	all := waitAnalysis(t, m)
	if all.Total != 6 || all.TotalResults != 3 || all.TotalMatches != 9 {
		t.Fatalf("duplicates counted twice: %+v", all)
	}
	crypto := categoryForSymbol(t, "btcusd")
	seen := map[string]bool{}
	for offset := 0; offset < 2; offset++ {
		page, err := m.AnalysisFiltered(offset, 1, AnalysisFilter{Categories: []string{crypto}})
		if err != nil || page.TotalResults != 2 || len(page.Results) != 1 {
			t.Fatalf("filtered simulation page: %+v %v", page, err)
		}
		r := page.Results[0]
		if seen[r.Symbol] || r.Matches != 3 || r.Simulation == nil || r.Simulation.TakeProfitHits != 1 || r.Simulation.Expired != 1 || r.Simulation.InsufficientData != 1 || r.Simulation.ProfitablePercent != 100.0/3 {
			t.Fatalf("category page changed denominator: %+v %+v", r, r.Simulation)
		}
		seen[r.Symbol] = true
		chart, err := m.AnalysisChart(r.ID, 0, 0, 2)
		if err != nil || len(chart.ProfitableMatches) != 1 || chart.ProfitableMatches[0].Index != 0 {
			t.Fatalf("duplicate/category page changed winning markers: %+v %v", chart, err)
		}
	}
	request.Categories = []string{crypto}
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	selected := waitAnalysis(t, m)
	if selected.Total != 4 || selected.TotalResults != 2 || selected.TotalMatches != 6 {
		t.Fatalf("selection ignored for simulation: %+v", selected)
	}
	request.TradeSimulation = nil
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	plain := waitAnalysis(t, m)
	if plain.TotalMatches != selected.TotalMatches || plain.TradeSimulation != nil {
		t.Fatalf("disabling simulation changed matches: %+v", plain)
	}
	for _, r := range plain.Results {
		if r.Simulation != nil {
			t.Fatalf("old simulation leaked: %+v", r)
		}
	}
}

func TestAnalysisSimulationCancellationDuringDenseScanClearsPartialSummary(t *testing.T) {
	m := analysisTestManager(t)
	bar := simulationBar(100, 105, 95, 100)
	bar.signal = 1
	bars := make([]simulationTestBar, 100000)
	for i := range bars {
		bars[i] = bar
	}
	writeDataFile(t, m, "btcusd-m1-2024-01-01-2025-01-01.csv", simulationCSV(bars, 1704067200000))
	if _, err := m.StartAnalysis(simulationRequest(10000)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		info, err := m.Analysis(0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if info.Rows >= 256 && info.Active {
			break
		}
		if !info.Active || time.Now().After(deadline) {
			t.Fatalf("did not observe active scan: %+v", info)
		}
		time.Sleep(time.Millisecond)
	}
	info := m.CancelAnalysis()
	if info.Active || info.Phase != "cancelled" || info.TotalMatches != 0 || len(info.Results) != 1 || info.Results[0].Simulation != nil || info.Results[0].Status != "cancelled" {
		t.Fatalf("cancel published partially simulated trades: %+v", info)
	}
	for _, suffix := range []string{".matches", ".matches.profits", ".matches.trades"} {
		if _, err := os.Stat(filepath.Join(m.analysis.work, info.Results[0].ID+suffix)); !os.IsNotExist(err) {
			t.Fatalf("cancellation retained partial outcome file %s: %v", suffix, err)
		}
	}
}
