package main

import (
	"cryptodesk/internal/domain"
	"testing"
)

func TestSelectionKeepsMarketWhenNativeIDsCollide(t *testing.T) {
	items := []domain.Instrument{
		{NativeID: "BTCUSDT", Market: "perpetual", Availability: "Trading"},
		{NativeID: "XAGUSDT", Market: "perpetual", Availability: "Trading"},
		{NativeID: "BTCUSDT", Market: "spot", Availability: "TRADING"},
	}
	for _, market := range []string{"spot", "perpetual"} {
		selected, err := selectInstrument(items, "binance", market, "")
		if err != nil || selected.NativeID != "BTCUSDT" || selected.Market != market {
			t.Fatalf("%s default selected %#v, %v", market, selected, err)
		}
	}
	selected, err := selectInstrument(items, "binance", "perpetual", "XAGUSDT")
	if err != nil || selected.NativeID != "XAGUSDT" || selected.Market != "perpetual" {
		t.Fatalf("exact selection = %#v, %v", selected, err)
	}
	if selected, err = selectInstrument(items, "binance", "spot", "XAGUSDT"); err == nil {
		t.Fatalf("missing spot product silently selected %#v", selected)
	}
}

func TestExactSelectionDoesNotFallbackFromUnavailableInstrument(t *testing.T) {
	items := []domain.Instrument{
		{NativeID: "XAGUSDT", Market: "perpetual", Availability: "Settling"},
		{NativeID: "BTCUSDT", Market: "perpetual", Availability: "Trading"},
	}
	if selected, err := selectInstrument(items, "bybit", "perpetual", "XAGUSDT"); err == nil {
		t.Fatalf("unavailable product silently selected %#v", selected)
	}
}
