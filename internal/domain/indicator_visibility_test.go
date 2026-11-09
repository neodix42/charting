package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func visibilityWorkspace(kind string) Workspace {
	w := NewWorkspace()
	parameters := map[string]int{"conversion": 9, "base": 26, "spanB": 52, "displacement": 26}
	if kind == "macd" {
		parameters = map[string]int{"fast": 12, "slow": 26, "signal": 9}
	}
	w.Tabs[0].Charts[0].Indicators = []Indicator{{ID: ID(), Type: kind, Parameters: parameters}}
	return w
}

func TestIndicatorVisibilityRoundTripAndLegacyOmission(t *testing.T) {
	for kind, keys := range map[string][]string{
		"ichimoku": {"conversion", "base", "spanA", "spanB", "lagging", "cloud"},
		"macd":     {"macd", "signal", "histogram", "zero"},
	} {
		t.Run(kind, func(t *testing.T) {
			w := visibilityWorkspace(kind)
			legacy, err := json.Marshal(w)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(legacy), `"visibility"`) {
				t.Fatal("legacy visibility should stay omitted")
			}
			decoded, err := DecodeWorkspace(legacy)
			if err != nil || !reflect.DeepEqual(decoded, w) {
				t.Fatal("legacy indicator changed", err)
			}
			roundTrip, err := json.Marshal(decoded)
			if err != nil || string(roundTrip) != string(legacy) {
				t.Fatal("legacy JSON shape changed", err)
			}
			for _, visible := range []bool{false, true} {
				visibility := map[string]bool{}
				for _, key := range keys {
					visibility[key] = visible
				}
				w.Tabs[0].Charts[0].Indicators[0].Visibility = visibility
				if got := cloneWorkspace(t, w); !reflect.DeepEqual(got, w) {
					t.Fatalf("all %s flags did not survive round-trip", kind)
				}
			}
			// A sparse false map must remain sparse rather than hiding omitted plots.
			w.Tabs[0].Charts[0].Indicators[0].Visibility = map[string]bool{keys[0]: false}
			copy := cloneWorkspace(t, w)
			copy.Tabs[0].Charts[0].Indicators[0].Visibility[keys[0]] = true
			if w.Tabs[0].Charts[0].Indicators[0].Visibility[keys[0]] {
				t.Fatal("round-tripped visibility map aliases its input")
			}
		})
	}
}

func TestIndicatorVisibilityRejectsWrongIndicatorAndUnknownComponents(t *testing.T) {
	for _, test := range []struct{ kind, key string }{
		{"ichimoku", "signal"}, {"ichimoku", "displacement"}, {"ichimoku", "Base"},
		{"macd", "base"}, {"macd", "slow"}, {"macd", "Histogram"},
		{"sma", "period"}, {"ema", "ema"}, {"low", "low"}, {"high", "high"},
	} {
		t.Run(test.kind+"/"+test.key, func(t *testing.T) {
			w := visibilityWorkspace(test.kind)
			indicator := &w.Tabs[0].Charts[0].Indicators[0]
			if test.kind != "ichimoku" && test.kind != "macd" {
				indicator.Parameters = map[string]int{"period": 20}
			}
			indicator.Visibility = map[string]bool{test.key: false}
			if err := Validate(w); err == nil {
				t.Fatal("invalid visibility accepted")
			}
			data, err := json.Marshal(w)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeWorkspace(data); err == nil {
				t.Fatal("invalid visibility passed portable decoding")
			}
		})
	}
}

func TestIndicatorVisibilityJSONStrictness(t *testing.T) {
	w := visibilityWorkspace("macd")
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	for name, field := range map[string]string{
		"null map":       `"visibility":null`,
		"boolean map":    `"visibility":false`,
		"array":          `"visibility":[]`,
		"null flag":      `"visibility":{"signal":null}`,
		"number flag":    `"visibility":{"signal":0}`,
		"string flag":    `"visibility":{"signal":"false"}`,
		"object flag":    `"visibility":{"signal":{"visible":false}}`,
		"unknown flag":   `"visibility":{"script":false}`,
		"case flag":      `"visibility":{"Signal":false}`,
		"case map":       `"Visibility":{"signal":false}`,
		"duplicate map":  `"visibility":{"signal":false},"visibility":{"signal":true}`,
		"duplicate flag": `"visibility":{"signal":false,"signal":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			input := strings.Replace(string(data), `"type":"macd"`, `"type":"macd",`+field, 1)
			if _, err := DecodeWorkspace([]byte(input)); err == nil {
				t.Fatal("malformed visibility accepted", input)
			}
		})
	}
}
