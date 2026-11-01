package domain

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestForexWorkspaceRoundtripAndProviderMarketSeparation(t *testing.T) {
	for _, provider := range []string{"binance", "bybit", "coinbase", "dukascopy"} {
		for _, market := range []string{"spot", "perpetual", "forex", "metal", "cfd", "futures", "", "CFD"} {
			key := FeedKey{Exchange: provider, Market: market, Instrument: "EURUSD", Timeframe: "1w"}
			valid := (provider == "dukascopy" && (market == "forex" || market == "metal" || market == "cfd")) || (provider != "dukascopy" && market == "spot") || ((provider == "binance" || provider == "bybit") && market == "perpetual")
			if err := key.Validate(); (err == nil) != valid {
				t.Fatalf("%s/%s validity: %v", provider, market, err)
			}
		}
	}
	w := NewWorkspace()
	chart := &w.Tabs[0].Charts[0]
	chart.Exchange, chart.Market, chart.Instrument = "dukascopy", "forex", "EURUSD"
	chart.Drawings = []Drawing{{ID: ID(), Type: "horizontal", Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Points: []DrawingPoint{{Time: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), Price: 1.05}}}}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeWorkspace(data)
	if err != nil || !reflect.DeepEqual(restored, w) {
		t.Fatalf("forex chart/drawings did not roundtrip: %v", err)
	}
	if len(restored.Tabs) != 1 || len(restored.Tabs[0].Charts) != 1 {
		t.Fatal("adding forex changed starter capacity")
	}
}

func TestDukascopyAssetChartsAndDrawingsRoundtrip(t *testing.T) {
	for _, instrument := range []struct{ market, nativeID string }{
		{"forex", "EURUSD"}, {"metal", "XAUUSD"}, {"metal", "XAGUSD"},
		{"cfd", "AAPLUSUSD"}, {"cfd", "USA30IDXUSD"}, {"cfd", "BRENTCMDUSD"},
		{"cfd", "SPYUSUSD"}, {"cfd", "BTCUSD"},
	} {
		t.Run(instrument.nativeID, func(t *testing.T) {
			key := FeedKey{Exchange: "dukascopy", Market: instrument.market, Instrument: instrument.nativeID, Timeframe: "1d"}
			if err := key.Validate(); err != nil {
				t.Fatal("SDK native ID rejected", err)
			}
			w := NewWorkspace()
			chart := &w.Tabs[0].Charts[0]
			chart.Exchange, chart.Market, chart.Instrument = key.Exchange, key.Market, key.Instrument
			chart.Drawings = []Drawing{{ID: ID(), Type: "horizontal", Exchange: key.Exchange, Market: key.Market, Instrument: key.Instrument, Points: []DrawingPoint{{Time: 1700002800, Price: 100}}}}
			data, err := json.Marshal(w)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := DecodeWorkspace(data)
			if err != nil || !reflect.DeepEqual(restored, w) {
				t.Fatalf("Dukascopy chart/drawing identity changed: %+v %v", restored, err)
			}
		})
	}
}

func TestHistoricalForexSecondsPredateCryptoButMillisecondsRemainInvalid(t *testing.T) {
	date := time.Date(1973, 3, 1, 0, 0, 0, 0, time.UTC)
	bar := Candle{Time: date.Unix(), Open: 1.2, High: 1.3, Low: 1.1, Close: 1.25, Volume: 0, Closed: true}
	if err := bar.Validate(); err != nil {
		t.Fatal("valid pre-2009 forex history rejected", err)
	}
	bar.Time = time.Now().UnixMilli()
	if bar.Validate() == nil {
		t.Fatal("millisecond timestamp accepted as seconds")
	}
	bar.Time = -1
	if bar.Validate() == nil {
		t.Fatal("negative chart time accepted")
	}
}
