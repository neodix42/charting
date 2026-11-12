package screener

import (
	"context"
	"reflect"
	"testing"
)

func TestPublicAlertEngulfingKijunPlanExcludesUnusedDraftOperands(t *testing.T) {
	// The visual editor retains distance/upper/window defaults even when the
	// chosen comparison does not use them. Only effective rule inputs, the
	// previous OHLC values, and the configured live SL/TP may affect this plan.
	previous := func(field string) AnalysisOperand {
		operand := analysisField(field)
		operand.Offset = 1
		return operand
	}
	saved := publicAlertTestCondition()
	saved.Request = AnalysisRequest{
		Period: "d1", ProfitabilityFilter: 80,
		Conditions: []AnalysisCondition{
			analysisCondition("above", previous("low"), analysisField("low")),
			analysisCondition("above", analysisField("close"), analysisField("open")),
			analysisCondition("below", previous("high"), analysisField("high")),
			analysisCondition("above", analysisField("high"), analysisField("kijun_26")),
		},
		TradeSimulation: &AnalysisTradeSimulation{StopLoss: "low", TakeProfitMultiple: 1, Duration: 20},
	}
	for i := range saved.Request.Conditions {
		saved.Request.Conditions[i].Distance = analysisField("atr_14")
		saved.Request.Conditions[i].Upper = analysisField("unknown_unused_999")
		saved.Request.Conditions[i].Bars = 3
	}
	plans, err := publicAlertPlans([]SavedAnalysisCondition{saved}, map[string][]string{saved.ID: {"d1"}})
	if err != nil {
		t.Fatal(err)
	}
	plan := plans["d1"]
	want := []string{"close", "high", "kijun_26", "low", "open"}
	if len(plans) != 1 || !reflect.DeepEqual(plan.Fields, want) || plan.Extra != 2 {
		t.Fatalf("unused editor/simulation values changed effective live prerequisites: %+v", plans)
	}
	// Exercise the installed recipe recovery/calculator worker rather than
	// duplicating its indicator initialization convention in this assertion.
	m := sourceEnrichmentManager(t, false)
	worker, resolved, err := startPublicAlertWorker(context.Background(), m.runtime, plans)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.close()
	if got := resolved["d1"].Bars; got != 28 {
		t.Fatalf("Kijun26 plus the existing two-bar evaluation guard needs 28 closed candles, got %d", got)
	}
}
