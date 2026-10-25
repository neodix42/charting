package screener

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

//go:embed indicators.json
var indicatorJSON []byte

type IndicatorParameter struct {
	Key     string  `json:"key"`
	Label   string  `json:"label"`
	Default float64 `json:"default"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Step    float64 `json:"step"`
}

type Indicator struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Category   string               `json:"category"`
	Input      string               `json:"input"`
	Parameters []IndicatorParameter `json:"parameters"`
	Note       string               `json:"note,omitempty"`
}

type IndicatorCatalog struct {
	Version    string      `json:"version"`
	Indicators []Indicator `json:"indicators"`
}

// Indicators returns independent values; the same reviewed catalog is packaged
// beside the Node worker. Only these constructors and parameters are accepted.
func (m *Manager) Indicators() IndicatorCatalog {
	var catalog IndicatorCatalog
	if err := json.Unmarshal(indicatorJSON, &catalog); err != nil {
		panic(err) // An invalid embedded catalog is a build error.
	}
	return catalog
}

type EnrichmentRequest struct {
	Indicator  string             `json:"indicator"`
	Parameters map[string]float64 `json:"parameters"`
	Source     string             `json:"source"`
}

func (m *Manager) validateEnrichment(request EnrichmentRequest) (EnrichmentRequest, error) {
	var indicator *Indicator
	for _, item := range m.Indicators().Indicators {
		if item.ID == request.Indicator {
			indicator = &item
			break
		}
	}
	if indicator == nil {
		return request, errors.New("choose a supported trading-signals indicator")
	}
	switch request.Source {
	case "", "auto":
		request.Source = "auto"
	case "close", "open", "high", "low", "bidprice", "askprice":
	default:
		return request, errors.New("unsupported price source")
	}
	parameters := make(map[string]float64, len(indicator.Parameters))
	for _, param := range indicator.Parameters {
		value, ok := request.Parameters[param.Key]
		if !ok {
			value = param.Default
		}
		if math.IsNaN(value) || math.IsInf(value, 0) || value < param.Min || value > param.Max || (param.Step == 1 && value != math.Trunc(value)) {
			return request, fmt.Errorf("%s must be between %g and %g", param.Label, param.Min, param.Max)
		}
		parameters[param.Key] = value
	}
	for key := range request.Parameters {
		if _, ok := parameters[key]; !ok {
			return request, fmt.Errorf("unsupported indicator parameter %q", key)
		}
	}
	if fast, ok := parameters["fast"]; ok && fast >= parameters["slow"] {
		return request, errors.New("fast period must be smaller than slow period")
	}
	request.Parameters = parameters
	return request, nil
}
