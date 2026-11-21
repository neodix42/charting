package jforex

import (
	"math"
	"strings"
	"testing"
)

func TestNormalizeSizingKeepsDecimalZeroAndEstimatedMargin(t *testing.T) {
	margin := 12.5
	metrics := TradeMetrics{EntryPrice: 100, MaximumQuantity: "0", MaximumQuantityBasis: "SDK rules", MarginRequired: &margin, MarginBasis: "instrument leverage"}
	normalizeTradeMetrics(&metrics)
	if metrics.MaximumQuantity != "0" || metrics.MaximumQuantityError != "" || metrics.MarginRequired == nil || *metrics.MarginRequired != 12.5 || metrics.MarginError != "" {
		t.Fatal("valid zero capacity and real estimate were discarded", metrics)
	}
}

func TestNormalizeSizingRejectsNonDecimalAndInvalidFinancialValues(t *testing.T) {
	for _, value := range []string{"-1", "1/2", "1e2", "+1", "NaN", "Infinity", " 12 ", strings.Repeat("1", 101)} {
		t.Run(value, func(t *testing.T) {
			invalid := math.NaN()
			metrics := TradeMetrics{EntryPrice: 100, MaximumQuantity: value, MarginRequired: &invalid}
			normalizeTradeMetrics(&metrics)
			if metrics.MaximumQuantity != "" || metrics.MaximumQuantityError == "" || metrics.MarginRequired != nil || metrics.MarginError == "" {
				t.Fatal("invalid size or margin accepted", metrics)
			}
		})
	}
	legacy := TradeMetrics{EntryPrice: 100}
	normalizeTradeMetrics(&legacy)
	if legacy.MaximumQuantity != "" || !strings.Contains(legacy.MaximumQuantityError, "matching jforex-runtime") {
		t.Fatal("legacy bridge sizing missing without update advice", legacy)
	}
}
