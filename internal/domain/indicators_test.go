package domain

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestIndicatorConfigurationRoundTripAndLegacyCompatibility(t *testing.T) {
	legacy := NewWorkspace()
	data, _ := json.Marshal(legacy)
	if strings.Contains(string(data), `"indicators"`) {
		t.Fatal("empty legacy configuration should keep its original shape")
	}
	if _, err := DecodeWorkspace(data); err != nil {
		t.Fatal(err)
	}
	color := "#26a69a"
	w := NewWorkspace()
	w.Tabs[0].Charts[0].Timeframe = "1w"
	w.Tabs[0].Charts[0].Indicators = []Indicator{
		{ID: ID(), Type: "sma", Parameters: map[string]int{"period": 20}, Color: &color},
		{ID: ID(), Type: "ema", Parameters: map[string]int{"period": 1}},
		{ID: ID(), Type: "ichimoku", Parameters: map[string]int{"conversion": 9, "base": 26, "spanB": 52, "displacement": 26}},
		{ID: ID(), Type: "macd", Parameters: map[string]int{"fast": 12, "slow": 26, "signal": 9}},
	}
	decoded := cloneWorkspace(t, w)
	if len(decoded.Tabs[0].Charts[0].Indicators) != 4 || decoded.Tabs[0].Charts[0].Indicators[2].Parameters["spanB"] != 52 {
		t.Fatal(decoded)
	}
}

func TestInvalidIndicatorConfigurationsRejected(t *testing.T) {
	cases := map[string]func(*Workspace){
		"unknown kind":         func(w *Workspace) { w.Tabs[0].Charts[0].Indicators[0].Type = "script" },
		"zero period":          func(w *Workspace) { w.Tabs[0].Charts[0].Indicators[0].Parameters["period"] = 0 },
		"huge period":          func(w *Workspace) { w.Tabs[0].Charts[0].Indicators[0].Parameters["period"] = 2001 },
		"missing parameters":   func(w *Workspace) { w.Tabs[0].Charts[0].Indicators[0].Parameters = nil },
		"irrelevant parameter": func(w *Workspace) { w.Tabs[0].Charts[0].Indicators[0].Parameters["signal"] = 9 },
		"duplicate global ID":  func(w *Workspace) { w.Tabs[0].Charts[0].Indicators[0].ID = w.ID },
		"duplicate indicator": func(w *Workspace) {
			w.Tabs[0].Charts[0].Indicators = append(w.Tabs[0].Charts[0].Indicators, w.Tabs[0].Charts[0].Indicators[0])
		},
		"unsafe color": func(w *Workspace) { c := "url(https://example.invalid)"; w.Tabs[0].Charts[0].Indicators[0].Color = &c },
		"empty color":  func(w *Workspace) { c := ""; w.Tabs[0].Charts[0].Indicators[0].Color = &c },
		"MACD reversed": func(w *Workspace) {
			w.Tabs[0].Charts[0].Indicators[0] = Indicator{ID: ID(), Type: "macd", Parameters: map[string]int{"fast": 26, "slow": 12, "signal": 9}}
		},
		"negative displacement": func(w *Workspace) {
			w.Tabs[0].Charts[0].Indicators[0] = Indicator{ID: ID(), Type: "ichimoku", Parameters: map[string]int{"conversion": 9, "base": 26, "spanB": 52, "displacement": -1}}
		},
		"too many": func(w *Workspace) {
			for len(w.Tabs[0].Charts[0].Indicators) <= MaxIndicators {
				w.Tabs[0].Charts[0].Indicators = append(w.Tabs[0].Charts[0].Indicators, Indicator{ID: ID(), Type: "sma", Parameters: map[string]int{"period": 20}})
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			w := NewWorkspace()
			w.Tabs[0].Charts[0].Indicators = []Indicator{{ID: ID(), Type: "sma", Parameters: map[string]int{"period": 20}}}
			mutate(&w)
			if err := Validate(w); err == nil {
				t.Fatal("accepted invalid indicator")
			}
		})
	}
}

func TestIndicatorJSONStrictness(t *testing.T) {
	w := NewWorkspace()
	w.Tabs[0].Charts[0].Indicators = []Indicator{{ID: ID(), Type: "sma", Parameters: map[string]int{"period": 20}}}
	data, _ := json.Marshal(w)
	indicatorData, _ := json.Marshal(w.Tabs[0].Charts[0].Indicators)
	cases := map[string]string{
		"null indicators":    strings.Replace(string(data), `"indicators":`+string(indicatorData), `"indicators":null`, 1),
		"null color":         strings.Replace(string(data), `"type":"sma"`, `"type":"sma","color":null`, 1),
		"null period":        strings.Replace(string(data), `"period":20`, `"period":null`, 1),
		"fractional period":  strings.Replace(string(data), `"period":20`, `"period":2.5`, 1),
		"string period":      strings.Replace(string(data), `"period":20`, `"period":"20"`, 1),
		"case alias":         strings.Replace(string(data), `"period":20`, `"Period":20`, 1),
		"extra properties":   strings.Replace(string(data), `"type":"sma"`, `"type":"sma","script":"evil"`, 1),
		"missing parameters": strings.Replace(string(data), `,"parameters":{"period":20}`, ``, 1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeWorkspace([]byte(input)); err == nil {
				t.Fatal("accepted malformed indicator", input)
			}
		})
	}
}

func TestMACDPaneStretchFactorRoundTripAndLegacyCompatibility(t *testing.T) {
	w := NewWorkspace()
	w.Tabs[0].Charts[0].Indicators = []Indicator{{ID: ID(), Type: "macd", Parameters: map[string]int{"fast": 12, "slow": 26, "signal": 9}}}
	legacy, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), `"paneStretchFactor"`) {
		t.Fatal("omitted pane height must not change legacy exports")
	}
	decoded, err := DecodeWorkspace(legacy)
	if err != nil || !reflect.DeepEqual(w, decoded) {
		t.Fatal("legacy MACD configuration changed", err)
	}
	roundTrip, err := json.Marshal(decoded)
	if err != nil || string(legacy) != string(roundTrip) {
		t.Fatal("legacy MACD JSON shape changed", err)
	}
	for _, factor := range []float64{0.001, 0.3, 0.625, 1, 1000} {
		w.Tabs[0].Charts[0].Indicators[0].PaneStretchFactor = &factor
		if err := Validate(w); err != nil {
			t.Fatalf("valid pane factor %v: %v", factor, err)
		}
		if actual := cloneWorkspace(t, w); !reflect.DeepEqual(w, actual) {
			t.Fatalf("pane factor %v changed during roundtrip", factor)
		}
	}
}

func TestInvalidMACDPaneStretchFactorsRejected(t *testing.T) {
	for _, factor := range []float64{-1, 0, 0.0009, 1000.0001, math.NaN(), math.Inf(1), math.Inf(-1)} {
		w := NewWorkspace()
		w.Tabs[0].Charts[0].Indicators = []Indicator{{ID: ID(), Type: "macd", Parameters: map[string]int{"fast": 12, "slow": 26, "signal": 9}, PaneStretchFactor: &factor}}
		if err := Validate(w); err == nil {
			t.Fatalf("accepted invalid pane factor %v", factor)
		}
	}
	factor := 0.5
	for _, indicator := range []Indicator{
		{ID: ID(), Type: "sma", Parameters: map[string]int{"period": 20}, PaneStretchFactor: &factor},
		{ID: ID(), Type: "ema", Parameters: map[string]int{"period": 20}, PaneStretchFactor: &factor},
		{ID: ID(), Type: "ichimoku", Parameters: map[string]int{"conversion": 9, "base": 26, "spanB": 52, "displacement": 26}, PaneStretchFactor: &factor},
	} {
		w := NewWorkspace()
		w.Tabs[0].Charts[0].Indicators = []Indicator{indicator}
		if err := Validate(w); err == nil {
			t.Fatalf("accepted pane factor on %s overlay", indicator.Type)
		}
		data, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeWorkspace(data); err == nil {
			t.Fatalf("decoded pane factor on %s overlay", indicator.Type)
		}
	}
}

func TestMACDPaneStretchFactorJSONStrictness(t *testing.T) {
	w := NewWorkspace()
	w.Tabs[0].Charts[0].Indicators = []Indicator{{ID: ID(), Type: "macd", Parameters: map[string]int{"fast": 12, "slow": 26, "signal": 9}}}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	for name, field := range map[string]string{
		"null":       `"paneStretchFactor":null`,
		"string":     `"paneStretchFactor":"0.5"`,
		"boolean":    `"paneStretchFactor":true`,
		"object":     `"paneStretchFactor":{"value":0.5}`,
		"array":      `"paneStretchFactor":[0.5]`,
		"negative":   `"paneStretchFactor":-0.5`,
		"zero":       `"paneStretchFactor":0`,
		"below min":  `"paneStretchFactor":0.0001`,
		"above max":  `"paneStretchFactor":1001`,
		"overflow":   `"paneStretchFactor":1e999`,
		"case alias": `"PaneStretchFactor":0.5`,
		"duplicate":  `"paneStretchFactor":0.5,"paneStretchFactor":0.6`,
	} {
		t.Run(name, func(t *testing.T) {
			input := strings.Replace(string(data), `"type":"macd"`, `"type":"macd",`+field, 1)
			if _, err := DecodeWorkspace([]byte(input)); err == nil {
				t.Fatal("accepted malformed pane factor", input)
			}
		})
	}
}
