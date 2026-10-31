//go:build bindings

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/screener"
)

func TestAnalysisProfitabilitySurvivesFacadeSaveScanRestartAndAlertFormatting(t *testing.T) {
	profile := t.TempDir()
	manager := screener.New(profile)
	defer manager.Close()
	app := &App{screener: manager}
	directory := manager.Info().Directory
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	csv := "timestamp,open,high,low,close,signal,stop,profit\n1704067200000,100,105,95,100,1,90,110\n1704070800000,100,115,95,105,0,90,120\n1704074400000,105,106,100,104,0,90,120\n"
	if err := os.WriteFile(filepath.Join(directory, "eurusd-h1-2024-01-01-2024-01-02.csv"), []byte(csv), 0600); err != nil {
		t.Fatal(err)
	}
	request, err := app.GetScreenerAnalysisView()
	if err != nil || request.ProfitabilityFilter != 85 {
		t.Fatalf("fresh profitability default: %+v %v", request, err)
	}
	request.Period = "h1"
	request.Conditions[0].Left.Field = "signal"
	request.TradeSimulation = &screener.AnalysisTradeSimulation{StopLoss: "stop", TakeProfit: "profit", Duration: 1}
	if _, err = app.SaveScreenerAnalysisView(request); err != nil {
		t.Fatal(err)
	}
	named, err := app.SaveScreenerAnalysisCondition("Qualified signals", request)
	if err != nil || named.Analysis != nil {
		t.Fatalf("save invented historical evidence: %+v %v", named, err)
	}
	if _, ok := screener.SavedConditionProfitabilityFor(named, "eurusd", "h1"); ok {
		t.Fatal("unanalysed filtered rule allowed live targets")
	}
	if _, err = app.StartScreenerAnalysis(request); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, e := app.GetScreenerAnalysisFiltered(0, 10, screener.AnalysisFilter{IncludeEmpty: false})
		if e != nil {
			t.Fatal(e)
		}
		if !info.Active {
			if info.Error != "" || len(info.Results) != 1 || info.Results[0].Matches != 1 || info.Results[0].Simulation == nil || info.Results[0].Simulation.TakeProfitHits != 1 {
				t.Fatalf("qualified production scan: %+v", info)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("analysis did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	manager.Close()
	restarted := screener.New(profile)
	defer restarted.Close()
	app.screener = restarted
	saved, err := app.GetScreenerSavedConditions()
	if err != nil || len(saved) != 1 || saved[0].ID != named.ID || saved[0].Request.ProfitabilityFilter != 85 || saved[0].Analysis == nil {
		t.Fatalf("restart lost rule/filter/evidence: %+v %v", saved, err)
	}
	stats, allowed := screener.SavedConditionProfitabilityFor(saved[0], "eurusd", "h1")
	if !allowed || stats == nil || stats.Matches != 1 || stats.TakeProfitHits != 1 {
		t.Fatalf("recorded alert scope: %+v %v", stats, allowed)
	}
	if _, allowed = screener.SavedConditionProfitabilityFor(saved[0], "eurusd", "d1"); allowed {
		t.Fatal("hourly evidence widened daily alert scope")
	}
	message := formatScreenerLiveAlert(screener.LiveAlert{Symbol: "eurusd", Timeframe: "h1", ConditionName: saved[0].Name, Profitability: stats})
	if !strings.Contains(message, "Profitability: 100.00%, 1 / 1 hit TP") {
		t.Fatal("alert lost recorded historical statistics", message)
	}
}
