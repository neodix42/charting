package domain

import (
	"encoding/json"
	"testing"
)

func TestCalendarRangeIndicatorsRoundTripAndRejectInapplicableOptions(t *testing.T) {
	ids := []string{"today_low", "today_high", "previous_day_low", "previous_day_high", "week_low", "week_high", "previous_week_low", "previous_week_high", "two_days_ago_low", "two_days_ago_high", "three_days_ago_low", "three_days_ago_high"}
	for _, kind := range ids {
		w := NewWorkspace()
		w.Tabs[0].Charts[0].Indicators = []Indicator{{ID: ID(), Type: kind, Parameters: map[string]int{}}}
		encoded, _ := json.Marshal(w)
		restored, err := DecodeWorkspace(encoded)
		if err != nil || restored.Tabs[0].Charts[0].Indicators[0].Type != kind {
			t.Fatalf("%s: %v", kind, err)
		}
		// Changing chart periods must preserve the setting for returning to intraday.
		w.Tabs[0].Charts[0].Timeframe = "1d"
		encoded, _ = json.Marshal(w)
		if _, err = DecodeWorkspace(encoded); err != nil {
			t.Fatalf("daily retention %s: %v", kind, err)
		}
		for _, parameters := range []map[string]int{nil, {"period": 10}, {"displacement": 0}} {
			if err = validateIndicator(Indicator{Type: kind, Parameters: parameters}); err == nil {
				t.Fatalf("%s accepted %+v", kind, parameters)
			}
		}
		width := 2
		factor := 0.2
		for _, bad := range []Indicator{{Type: kind, Parameters: map[string]int{}, LineWidth: &width}, {Type: kind, Parameters: map[string]int{}, PaneStretchFactor: &factor}, {Type: kind, Parameters: map[string]int{}, Visibility: map[string]bool{"low": false}}} {
			if err = validateIndicator(bad); err == nil {
				t.Fatalf("inapplicable option accepted: %+v", bad)
			}
		}
	}
}
