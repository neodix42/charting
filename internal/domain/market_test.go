package domain

import "testing"

func TestNativeInstrumentValidationKeepsProviderIdentity(t *testing.T) {
	for _, instrument := range []string{"BTCUSDT", "BTC-USD", "币安人生USDT", "1INCHUSDT"} {
		k := FeedKey{Exchange: "binance", Market: "spot", Instrument: instrument, Timeframe: "1m"}
		if err := k.Validate(); err != nil {
			t.Fatalf("native instrument %q rejected: %v", instrument, err)
		}
	}
	for _, instrument := range []string{"https://evil.invalid/x", "BTC/USDT", "BTC?token=123", "BTC:USD", ""} {
		k := FeedKey{Exchange: "binance", Market: "spot", Instrument: instrument, Timeframe: "1m"}
		if k.Validate() == nil {
			t.Fatalf("unsafe identity %q accepted", instrument)
		}
	}
}
