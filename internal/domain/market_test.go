package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

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

func TestFeedAndWorkspaceMarketCompatibility(t *testing.T) {
	for _, exchange := range []string{"binance", "bybit", "coinbase", "unknown"} {
		for _, market := range []string{"spot", "perpetual", "futures", "", "Spot"} {
			t.Run(exchange+"/"+market, func(t *testing.T) {
				wantValid := exchange != "unknown" && (market == "spot" || (market == "perpetual" && exchange != "coinbase"))
				key := FeedKey{Exchange: exchange, Market: market, Instrument: "BTCUSDT", Timeframe: "1m"}
				if err := key.Validate(); (err == nil) != wantValid {
					t.Fatalf("feed validity=%v want=%v: %v", err == nil, wantValid, err)
				}
				w := NewWorkspace()
				w.Tabs[0].Charts[0].Exchange, w.Tabs[0].Charts[0].Market = exchange, market
				data, err := json.Marshal(w)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := DecodeWorkspace(data)
				if (err == nil) != wantValid {
					t.Fatalf("portable validity=%v want=%v: %v", err == nil, wantValid, err)
				}
				if wantValid && !reflect.DeepEqual(decoded, w) {
					t.Fatal("market identity changed during roundtrip", decoded)
				}
			})
		}
	}
	spot := FeedKey{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1m"}
	perpetual := spot
	perpetual.Market = "perpetual"
	if spot == perpetual || spot.String() == perpetual.String() {
		t.Fatal("market must be part of feed identity")
	}
	data, err := json.Marshal(Instrument{NativeID: "BTCUSDT", Market: "perpetual"})
	if err != nil || !strings.Contains(string(data), `"market":"perpetual"`) {
		t.Fatal("catalog must expose explicit market identity", string(data), err)
	}
}
