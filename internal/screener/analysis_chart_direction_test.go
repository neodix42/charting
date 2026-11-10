package screener

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLiveAlertChartUsesSavedTradeDirectionAndSignalClose(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, test := range []struct {
		name, direction, want string
		simulation            bool
	}{
		{"Long", "long", "long", true},
		{"Short", "short", "short", true},
		{"legacy Long", "", "long", true},
		{"no simulated side", "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := AnalysisRequest{Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(0))}}
			if test.simulation {
				request.TradeSimulation = &AnalysisTradeSimulation{Direction: test.direction, StopLoss: "low", TakeProfit: "high", Duration: 1}
				if test.direction == "short" {
					request.TradeSimulation.StopLoss, request.TradeSimulation.TakeProfit = "high", "low"
				}
			}
			name := "eurusd-h1-2026-01-01-2026-01-02.csv"
			a, series := liveAlertChartIndexedState(t, map[string]string{name: fmt.Sprintf("timestamp,open,high,low,close\n%d,99,110,90,100\n", start)}, request, start+3600000)
			s := series[name]
			row, err := s.row(0)
			if err != nil {
				t.Fatal(err)
			}
			alert, err := liveAlertFromMatch(SavedAnalysisCondition{Request: request}, s, 0, row)
			if err != nil {
				t.Fatal(err)
			}
			chart, err := prepareLiveAlertChart(context.Background(), a, s, 0, alert)
			if err != nil {
				t.Fatal(err)
			}
			if chart.Direction != test.want || chart.Signal == nil || chart.Signal.Entry != 100 || len(chart.Bars) != 1 || chart.Trade != nil || chart.TradeExecution != nil {
				t.Fatal("chart direction/entry differs from evaluated live signal", chart)
			}
			encoded, err := json.Marshal(chart)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if strings.Contains(string(encoded), `"direction"`) {
					t.Fatal("invented a direction", string(encoded))
				}
			} else if !strings.Contains(string(encoded), `"direction":"`+test.want+`"`) {
				t.Fatal("direction missing from chart transport", string(encoded))
			}
		})
	}
}
