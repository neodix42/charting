package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFontSizeDefaultsLegacyAndPortableRoundTrip(t *testing.T) {
	w := NewWorkspace()
	if w.FontSize == nil || *w.FontSize != 15 {
		t.Fatal("first launch must use the 15px default text size", w.FontSize)
	}
	for _, size := range []int{10, 13, 15, 18, 24} {
		w.FontSize = &size
		if got := cloneWorkspace(t, w); !reflect.DeepEqual(got, w) {
			t.Fatal("font size changed in a portable round trip", got.FontSize)
		}
	}
	w.FontSize = nil
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["fontSize"]; present {
		t.Fatal("legacy omission must remain omitted when exported")
	}
	decoded, err := DecodeWorkspace(data)
	if err != nil || !reflect.DeepEqual(decoded, w) {
		t.Fatal("legacy workspace must remain unchanged", err)
	}
}

func TestFontSizeRejectsInvalidAndAmbiguousValues(t *testing.T) {
	base, err := json.Marshal(NewWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"null", "0", "9", "25", "-1", "13.5", `"18"`, "true", "{}"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(base, &fields); err != nil {
			t.Fatal(err)
		}
		fields["fontSize"] = json.RawMessage(raw)
		data, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = DecodeWorkspace(data); err == nil {
			t.Fatal("invalid font size accepted", raw)
		}
	}
}
