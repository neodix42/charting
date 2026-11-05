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
	for _, exchange := range []string{"binance", "bybit", "coinbase", "hyperliquid", "unknown"} {
		for _, market := range []string{"spot", "perpetual", "futures", "", "Spot"} {
			t.Run(exchange+"/"+market, func(t *testing.T) {
				wantValid := exchange != "unknown" && (market == "spot" || (market == "perpetual" && exchange != "coinbase"))
				instrument := "BTCUSDT"
				if exchange == "hyperliquid" && market == "spot" {
					instrument = "@107"
				}
				key := FeedKey{Exchange: exchange, Market: market, Instrument: instrument, Timeframe: "1m"}
				if err := key.Validate(); (err == nil) != wantValid {
					t.Fatalf("feed validity=%v want=%v: %v", err == nil, wantValid, err)
				}
				w := NewWorkspace()
				w.Tabs[0].Charts[0].Exchange, w.Tabs[0].Charts[0].Market, w.Tabs[0].Charts[0].Instrument = exchange, market, instrument
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

func TestHyperliquidNativeIDsAreMarketScopedAndUnambiguous(t *testing.T) {
	for _, tc := range []struct {
		market, instrument string
		valid              bool
	}{
		{"spot", "@107", true}, {"spot", "@0", true}, {"spot", "PURR/USDC", true},
		{"perpetual", "BTC", true}, {"perpetual", "xyz:XYZ100", true}, {"perpetual", "cash:USA500", true},
		{"spot", "BTC", false}, {"spot", "xyz:XYZ100", false}, {"perpetual", "@107", false}, {"perpetual", "PURR/USDC", false},
		{"spot", "@-1", false}, {"spot", "@107|1m", false}, {"perpetual", "xyz:BTC:1m", false},
		{"spot", "https://example.com", false}, {"perpetual", "xyz:BTC?url=1", false},
	} {
		key := FeedKey{Exchange: "hyperliquid", Market: tc.market, Instrument: tc.instrument, Timeframe: "1h"}
		if err := key.Validate(); (err == nil) != tc.valid {
			t.Errorf("%#v validity=%v want %v: %v", key, err == nil, tc.valid, err)
		}
		w := NewWorkspace()
		c := &w.Tabs[0].Charts[0]
		c.Exchange, c.Market, c.Instrument = key.Exchange, key.Market, key.Instrument
		data, _ := json.Marshal(w)
		decoded, err := DecodeWorkspace(data)
		if (err == nil) != tc.valid {
			t.Errorf("workspace %#v validity=%v want %v: %v", key, err == nil, tc.valid, err)
		}
		if tc.valid && decoded.Tabs[0].Charts[0].Instrument != tc.instrument {
			t.Fatal("native identity changed")
		}
		key.Exchange = "binance"
		if (strings.ContainsAny(tc.instrument, "@:/|?")) && key.Validate() == nil {
			t.Errorf("Hyperliquid syntax leaked to Binance: %s", tc.instrument)
		}
	}
}
