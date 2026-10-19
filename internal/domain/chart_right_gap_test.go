package domain

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

func TestChartRightGapDefaultAndLegacyOmission(t *testing.T) {
	w := NewWorkspace()
	if w.ChartRightGap == nil || *w.ChartRightGap != (ChartRightGap{Unit: "bars", Value: 10}) {
		t.Fatal("first launch must have a ten-bar right gap", w.ChartRightGap)
	}
	if w.Tabs[0].Charts[0].View.RightOffset != 10 || NewChart().View.RightOffset != 10 {
		t.Fatal("new chart viewport must use the ten-bar default")
	}
	w.ChartRightGap = nil
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["chartRightGap"]; present {
		t.Fatal("omitted legacy preference must remain omitted")
	}
	decoded, err := DecodeWorkspace(data)
	if err != nil || !reflect.DeepEqual(decoded, w) {
		t.Fatal("legacy workspace changed during decode", err)
	}
}

func TestChartRightGapBoundsAndRoundTrip(t *testing.T) {
	for _, gap := range []ChartRightGap{{"bars", 0}, {"bars", 10}, {"bars", 500}, {"pixels", 0}, {"pixels", 350}, {"pixels", 5000}} {
		t.Run(gap.Unit+"/"+strconv.Itoa(gap.Value), func(t *testing.T) {
			w := NewWorkspace()
			w.ChartRightGap = &gap
			if got := cloneWorkspace(t, w); !reflect.DeepEqual(got, w) {
				t.Fatal("right gap changed during portable roundtrip", got.ChartRightGap)
			}
		})
	}
	for _, gap := range []ChartRightGap{{"bar", 10}, {"", 10}, {"bars", -1}, {"bars", 501}, {"pixels", -1}, {"pixels", 5001}} {
		w := NewWorkspace()
		w.ChartRightGap = &gap
		if err := Validate(w); err == nil {
			t.Fatal("invalid right gap accepted", gap)
		}
	}
}

func TestChartRightGapStrictJSON(t *testing.T) {
	base, err := json.Marshal(NewWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"null preference":  "null",
		"array preference": "[]",
		"empty object":     "{}",
		"missing unit":     `{"value":10}`,
		"missing value":    `{"unit":"bars"}`,
		"null unit":        `{"unit":null,"value":10}`,
		"null value":       `{"unit":"bars","value":null}`,
		"unknown unit":     `{"unit":"seconds","value":10}`,
		"unknown property": `{"unit":"bars","value":10,"script":"bad"}`,
		"case alias":       `{"Unit":"bars","value":10}`,
		"duplicate unit":   `{"unit":"pixels","unit":"bars","value":10}`,
		"fraction":         `{"unit":"bars","value":1.5}`,
		"string value":     `{"unit":"bars","value":"10"}`,
		"boolean value":    `{"unit":"bars","value":true}`,
		"negative bars":    `{"unit":"bars","value":-1}`,
		"excess bars":      `{"unit":"bars","value":501}`,
		"excess pixels":    `{"unit":"pixels","value":5001}`,
	} {
		t.Run(name, func(t *testing.T) {
			var document map[string]json.RawMessage
			if err := json.Unmarshal(base, &document); err != nil {
				t.Fatal(err)
			}
			document["chartRightGap"] = json.RawMessage(raw)
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeWorkspace(data); err == nil {
				t.Fatal("malformed right gap accepted", raw)
			}
		})
	}
}
