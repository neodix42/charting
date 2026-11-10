package screener

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAnalysisTradeExecutionRecordsFirstExitForLongAndShortOutsideChartWindow(t *testing.T) {
	const start int64 = 1704067200000
	for _, direction := range []string{"long", "short"} {
		matched := simulationBar(100, 105, 95, 100)
		matched.signal = 1
		quiet := simulationBar(100, 105, 95, 100)
		stop, target := simulationBar(100, 105, 85, 100), simulationBar(100, 115, 95, 100)
		gapTarget := simulationBar(115, 120, 80, 100)
		if direction == "short" {
			matched.stop, matched.profit = 110, 90
			stop, target = target, stop
			gapTarget = simulationBar(85, 120, 80, 100)
		}
		for _, test := range []struct {
			name     string
			future   []simulationTestBar
			duration int
			outcome  string
			exitBar  int64
			wins     int64
		}{
			{"stop before later target", []simulationTestBar{quiet, stop, target}, 4, "stopLoss", 2, 0},
			{"target before later stop", []simulationTestBar{quiet, target, stop}, 4, "takeProfit", 2, 1},
			{"both touched stop first", []simulationTestBar{simulationBar(100, 115, 85, 100), target}, 4, "stopLoss", 1, 0},
			{"opening gap precedes intrabar stop", []simulationTestBar{quiet, gapTarget}, 4, "takeProfit", 2, 1},
			{"expiry on entry bar", []simulationTestBar{quiet, target}, 1, "expired", 1, 0},
			{"unresolved tail has no exit", []simulationTestBar{quiet}, 4, "insufficientData", 0, 0},
		} {
			t.Run(direction+"/"+test.name, func(t *testing.T) {
				m := analysisTestManager(t)
				name := "btcusd-m1-2024-01-01-2024-01-02.csv"
				writeDataFile(t, m, name, simulationCSV(append([]simulationTestBar{matched}, test.future...), start))
				request := simulationRequest(test.duration)
				request.TradeSimulation.Direction = direction
				if _, err := m.StartAnalysis(request); err != nil {
					t.Fatal(err)
				}
				result := resultNamed(t, waitAnalysis(t, m), name)
				if result.Status != "completed" || result.Matches != 1 || result.Simulation == nil || result.Simulation.TakeProfitHits != test.wins || simulationSummaryTotal(result.Simulation) != 1 {
					t.Fatalf("wrong first-exit partition: %+v %+v", result, result.Simulation)
				}
				// The selected chart contains only the signal. Entry and exit must
				// come from the applied scan, including an exit outside this window.
				chart, err := m.AnalysisChart(result.ID, 0, 0, 0)
				if err != nil || len(chart.Bars) != 1 || chart.Trade == nil {
					t.Fatalf("missing selected trade: %+v %v", chart, err)
				}
				if chart.Direction != direction {
					t.Fatalf("chart lost applied direction: %q, want %q", chart.Direction, direction)
				}
				// Editing the draft after Apply must not relabel the scanned trade.
				other := copyAnalysisRequest(request)
				if direction == "long" {
					other.TradeSimulation.Direction = "short"
				} else {
					other.TradeSimulation.Direction = "long"
				}
				if _, err = m.SaveAnalysisView(other); err != nil {
					t.Fatal(err)
				}
				unchanged, e := m.AnalysisChart(result.ID, 0, 0, 0)
				if e != nil || unchanged.Direction != direction {
					t.Fatal("draft changed applied chart direction", unchanged.Direction, e)
				}
				want := &AnalysisTradeExecution{EntryTime: start + 60000, Outcome: test.outcome}
				if test.exitBar > 0 {
					want.ExitTime = start + test.exitBar*60000
				}
				if !reflect.DeepEqual(chart.TradeExecution, want) || len(chart.ProfitableMatches) != int(test.wins) {
					t.Fatalf("execution=%+v want=%+v profitable=%+v", chart.TradeExecution, want, chart.ProfitableMatches)
				}
			})
		}
	}
}

func TestAnalysisTradeExecutionJanuaryEighthILSCZKDailyChronology(t *testing.T) {
	// Explicit historical OHLC excerpt reproduces the screenshot without opening
	// or changing a user profile. The Jan 8 signal low precedes Jan 9 entry;
	// Jan 9 reaches TP while remaining above SL, before Jan 10 crosses SL.
	const data = "timestamp,open,high,low,close,signal\n" +
		"1736294400000,6.6222,6.6796,6.619,6.6491,1\n" +
		"1736380800000,6.6369,6.668,6.6257,6.6522,0\n" +
		"1736467200000,6.6525,6.6662,6.5916,6.6203,0\n"
	m := analysisTestManager(t)
	name := "ilsczk-d1-2025-01-08-2025-01-11.csv"
	writeDataFile(t, m, name, data)
	request := simulationRequest(20)
	request.TradeSimulation = &AnalysisTradeSimulation{Direction: "long", StopLoss: "low", TakeProfitMultiple: 1, Duration: 20}
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	result := resultNamed(t, waitAnalysis(t, m), name)
	chart, err := m.AnalysisChart(result.ID, 0, 0, 2)
	entryBar := time.Date(2025, time.January, 9, 0, 0, 0, 0, time.UTC).UnixMilli()
	if err != nil || chart.TradeExecution == nil || *chart.TradeExecution != (AnalysisTradeExecution{EntryTime: entryBar, Outcome: "takeProfit", ExitTime: entryBar}) || len(chart.ProfitableMatches) != 1 {
		t.Fatalf("Jan 9 TP should precede Jan 10 SL: %+v %v", chart, err)
	}
	if chart.Trade == nil || chart.Trade.Entry != 6.6369 || chart.Trade.StopLoss != 6.619 || result.Simulation.TakeProfitHits != 1 || result.Simulation.StopLossHits != 0 {
		t.Fatalf("wrong screenshot trade levels/outcome: %+v %+v", chart.Trade, result.Simulation)
	}
}

func TestAnalysisTradeOutcomeCallbackErrorsPropagate(t *testing.T) {
	for _, outcome := range []string{"stopLoss", "takeProfit", "expired", "insufficientData", "unavailable"} {
		t.Run(outcome, func(t *testing.T) {
			config := simulationRequest(1).TradeSimulation
			if outcome == "insufficientData" || outcome == "unavailable" {
				config.Duration = 5
			}
			simulation := newAnalysisTradeSimulator(simulationTestSeries(), config)
			want := errors.New("execution write failed")
			simulation.onOutcome = func(int64, string, int64) error { return want }
			simulation.match(simulationTestRow(simulationBar(100, 105, 95, 100)))
			bar := simulationBar(100, 105, 95, 100)
			if outcome == "stopLoss" {
				bar.low = 85
			}
			if outcome == "takeProfit" {
				bar.high = 115
			}
			err := simulation.advance(1, simulationTestRow(bar))
			if outcome == "insufficientData" {
				err = simulation.finish()
			}
			if outcome == "unavailable" {
				bar.high = 80
				err = simulation.advance(2, simulationTestRow(bar))
			}
			if !errors.Is(err, want) {
				t.Fatalf("outcome callback error lost: %v", err)
			}
		})
	}
}

func TestAnalysisTradeExecutionRejectsUnreadableOutcomeRecords(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "truncated", true: "unknown outcome"}[corrupt], func(t *testing.T) {
			m := analysisTestManager(t)
			matched := simulationBar(100, 105, 95, 100)
			matched.signal = 1
			name := "btcusd-m1-2024-01-01-2024-01-02.csv"
			writeDataFile(t, m, name, simulationCSV([]simulationTestBar{matched, simulationBar(100, 115, 95, 100)}, 1704067200000))
			if _, err := m.StartAnalysis(simulationRequest(1)); err != nil {
				t.Fatal(err)
			}
			result := resultNamed(t, waitAnalysis(t, m), name)
			path := m.analysis.matchFiles[result.ID] + ".trades"
			if corrupt {
				f, err := os.OpenFile(path, os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteAt([]byte{255}, analysisTradeRecordSize-1)
				_ = f.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else if err := os.Truncate(path, analysisTradeRecordSize-1); err != nil {
				t.Fatal(err)
			}
			_, err := m.AnalysisChart(result.ID, 0, 0, 0)
			if err == nil || corrupt && !strings.Contains(err.Error(), "outcome") {
				t.Fatalf("unreadable execution accepted: %v", err)
			}
		})
	}
}
