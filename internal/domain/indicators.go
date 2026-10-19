package domain

import (
	"fmt"
	"regexp"
)

// Indicator stores configuration only; series data is calculated from each
// chart's own bounded candle window and is never persisted or exported.
type Indicator struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Parameters map[string]int `json:"parameters"`
	Color      *string        `json:"color,omitempty"`
}

var indicatorColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func validateIndicator(i Indicator) error {
	var names []string
	switch i.Type {
	case "sma", "ema":
		names = []string{"period"}
	case "ichimoku":
		names = []string{"conversion", "base", "spanB", "displacement"}
	case "macd":
		names = []string{"fast", "slow", "signal"}
	default:
		return fmt.Errorf("unsupported indicator %q", i.Type)
	}
	if i.Color != nil && !indicatorColor.MatchString(*i.Color) {
		return fmt.Errorf("indicator color must be a six-digit hex color")
	}
	if len(i.Parameters) != len(names) {
		return fmt.Errorf("invalid %s parameter fields", i.Type)
	}
	for _, name := range names {
		value, ok := i.Parameters[name]
		if !ok {
			return fmt.Errorf("missing %s parameter %s", i.Type, name)
		}
		if name == "displacement" {
			if value < 0 || value > 500 {
				return fmt.Errorf("Ichimoku displacement must be 0–500 bars")
			}
		} else if value < 1 || value > 2000 {
			return fmt.Errorf("indicator periods must be 1–2000 bars")
		}
	}
	if i.Type == "macd" && i.Parameters["fast"] >= i.Parameters["slow"] {
		return fmt.Errorf("MACD fast period must be smaller than slow period")
	}
	return nil
}
