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
	// LineWidth controls SMA/EMA strokes. Omission uses two pixels and preserves
	// the original JSON shape of saved indicators from earlier versions.
	LineWidth *int `json:"lineWidth,omitempty"`
	// PaneStretchFactor is a MACD pane's height relative to the main chart pane.
	// Omission keeps the default layout and the original shape of legacy exports.
	PaneStretchFactor *float64 `json:"paneStretchFactor,omitempty"`
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
	if width := i.LineWidth; width != nil {
		if i.Type != "sma" && i.Type != "ema" {
			return fmt.Errorf("indicator line width is only supported for SMA and EMA")
		}
		if *width < 1 || *width > 4 {
			return fmt.Errorf("indicator line width must be 1–4 pixels")
		}
	}
	if factor := i.PaneStretchFactor; factor != nil {
		if i.Type != "macd" {
			return fmt.Errorf("pane stretch factor is only supported for MACD")
		}
		if !finite(*factor) || *factor < 0.001 || *factor > 1000 {
			return fmt.Errorf("MACD pane stretch factor must be 0.001–1000")
		}
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
