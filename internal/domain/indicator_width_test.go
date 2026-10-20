package domain

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestMovingAverageLineWidthRoundTripAndLegacyOmission(t *testing.T) {
	for _, kind := range []string{"sma", "ema"} {
		t.Run(kind, func(t *testing.T) {
			w := NewWorkspace()
			w.Tabs[0].Charts[0].Indicators = []Indicator{{ID: ID(), Type: kind, Parameters: map[string]int{"period": 20}}}
			legacy, err := json.Marshal(w)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(legacy), `"lineWidth"`) {
				t.Fatal("default line width must remain omitted in legacy configuration")
			}
			decoded, err := DecodeWorkspace(legacy)
			if err != nil || !reflect.DeepEqual(decoded, w) {
				t.Fatal("legacy moving average changed", err)
			}
			roundTrip, err := json.Marshal(decoded)
			if err != nil || string(roundTrip) != string(legacy) {
				t.Fatal("legacy moving average JSON shape changed", err)
			}
			for width := 1; width <= 4; width++ {
				w.Tabs[0].Charts[0].Indicators[0].LineWidth = &width
				if got := cloneWorkspace(t, w); !reflect.DeepEqual(got, w) {
					t.Fatalf("line width %d changed during roundtrip", width)
				}
			}
		})
	}
}

func TestInvalidMovingAverageLineWidthsRejected(t *testing.T) {
	for _, kind := range []string{"sma", "ema"} {
		for _, width := range []int{-1, 0, 5, 1000} {
			t.Run(kind+"/"+strconv.Itoa(width), func(t *testing.T) {
				w := NewWorkspace()
				w.Tabs[0].Charts[0].Indicators = []Indicator{{ID: ID(), Type: kind, Parameters: map[string]int{"period": 20}, LineWidth: &width}}
				if err := Validate(w); err == nil {
					t.Fatal("invalid moving average width accepted", width)
				}
			})
		}
	}
	width := 2
	for _, indicator := range []Indicator{
		{ID: ID(), Type: "macd", Parameters: map[string]int{"fast": 12, "slow": 26, "signal": 9}, LineWidth: &width},
		{ID: ID(), Type: "ichimoku", Parameters: map[string]int{"conversion": 9, "base": 26, "spanB": 52, "displacement": 26}, LineWidth: &width},
	} {
		w := NewWorkspace()
		w.Tabs[0].Charts[0].Indicators = []Indicator{indicator}
		if err := Validate(w); err == nil {
			t.Fatal("line width accepted on unsupported indicator", indicator.Type)
		}
		data, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeWorkspace(data); err == nil {
			t.Fatal("unsupported indicator line width passed portable validation", indicator.Type)
		}
	}
}

func TestMovingAverageLineWidthJSONStrictness(t *testing.T) {
	for _, kind := range []string{"sma", "ema"} {
		w := NewWorkspace()
		w.Tabs[0].Charts[0].Indicators = []Indicator{{ID: ID(), Type: kind, Parameters: map[string]int{"period": 20}}}
		data, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		for name, field := range map[string]string{
			"null":              `"lineWidth":null`,
			"fractional":        `"lineWidth":2.5`,
			"string":            `"lineWidth":"2"`,
			"boolean":           `"lineWidth":true`,
			"object":            `"lineWidth":{"value":2}`,
			"array":             `"lineWidth":[2]`,
			"negative":          `"lineWidth":-1`,
			"zero":              `"lineWidth":0`,
			"above max":         `"lineWidth":5`,
			"overflow":          `"lineWidth":1e999`,
			"NaN":               `"lineWidth":NaN`,
			"positive infinity": `"lineWidth":Infinity`,
			"negative infinity": `"lineWidth":-Infinity`,
			"case alias":        `"LineWidth":2`,
			"duplicate":         `"lineWidth":2,"lineWidth":3`,
		} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				input := strings.Replace(string(data), `"type":"`+kind+`"`, `"type":"`+kind+`",`+field, 1)
				if _, err := DecodeWorkspace([]byte(input)); err == nil {
					t.Fatal("malformed line width accepted", input)
				}
			})
		}
	}
}
