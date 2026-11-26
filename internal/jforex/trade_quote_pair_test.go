package jforex

import (
	"math"
	"testing"
)

func TestOptionalTradeQuotePairNeverInventsMissingOrInvalidSides(t *testing.T) {
	for _, values := range [][2]float64{{0, 0}, {1, 0}, {0, 1}, {2, 1}, {math.NaN(), 2}, {1, math.Inf(1)}} {
		if bid, ask := validTradePair(values[0], values[1]); bid != 0 || ask != 0 {
			t.Fatalf("invalid optional pair retained: %v %v", bid, ask)
		}
	}
	for _, values := range [][2]float64{{1.1, 1.1002}, {1, 1}, {.00001, .00002}} {
		if bid, ask := validTradePair(values[0], values[1]); bid != values[0] || ask != values[1] {
			t.Fatalf("real optional pair changed: %v %v", bid, ask)
		}
	}
}
