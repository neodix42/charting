package screener

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
)

//go:embed indicators.json
var indicatorJSON []byte

var indicatorDefinitions = sync.OnceValue(func() map[string]Indicator {
	var catalog IndicatorCatalog
	if err := json.Unmarshal(indicatorJSON, &catalog); err != nil {
		panic(err)
	}
	definitions := make(map[string]Indicator, len(catalog.Indicators))
	for _, indicator := range catalog.Indicators {
		definitions[indicator.ID] = indicator
	}
	return definitions
})

type IndicatorParameter struct {
	Key     string  `json:"key"`
	Label   string  `json:"label"`
	Default float64 `json:"default"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Step    float64 `json:"step"`
}

type Indicator struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Category     string               `json:"category"`
	Input        string               `json:"input"`
	Parameters   []IndicatorParameter `json:"parameters"`
	Note         string               `json:"note,omitempty"`
	IntradayOnly bool                 `json:"intradayOnly,omitempty"`
}

type IndicatorCatalog struct {
	Version    string      `json:"version"`
	Indicators []Indicator `json:"indicators"`
}

// Indicators returns independent values; the same reviewed catalog is packaged
// beside the Node worker. Only these indicator constructors, UTC time fields
// and reviewed parameters are accepted.
func (m *Manager) Indicators() IndicatorCatalog {
	var catalog IndicatorCatalog
	if err := json.Unmarshal(indicatorJSON, &catalog); err != nil {
		panic(err) // An invalid embedded catalog is a build error.
	}
	return catalog
}

type EnrichmentRequest struct {
	DataSource string             `json:"dataSource,omitempty"`
	Indicator  string             `json:"indicator"`
	Group      string             `json:"group,omitempty"`
	Parameters map[string]float64 `json:"parameters"`
	Source     string             `json:"source"`
	// Period limits the batch inventory, never the stored indicator recipe.
	// Empty and "all" retain the existing all-files behavior.
	Period string `json:"period,omitempty"`
}

func (m *Manager) validateEnrichment(request EnrichmentRequest) (EnrichmentRequest, error) {
	if request.DataSource != "" && request.DataSource != "all" && request.DataSource != "dukascopy" && request.DataSource != "hyperliquid" {
		return request, errors.New("unsupported enrichment data source")
	}
	if request.Period != "" && request.Period != "all" {
		known := false
		for _, period := range loadCatalog().Timeframes {
			if period == request.Period {
				known = true
				break
			}
		}
		if !known {
			return request, fmt.Errorf("unsupported enrichment period %q", request.Period)
		}
	}
	if request.Group != "" {
		members, err := m.enrichmentMembers(request)
		if err != nil {
			return request, err
		}
		request.Parameters = map[string]float64{}
		request.Source = "auto"
		for _, member := range members {
			if indicatorDefinitions()[member.Indicator].Input == "price" {
				request.Source = member.Source
				break
			}
		}
		return request, nil
	}
	indicator, known := indicatorDefinitions()[request.Indicator]
	if !known {
		return request, errors.New("choose a supported enrichment indicator or time field")
	}
	if indicator.IntradayOnly && request.Period != "" && request.Period != "all" && !intradayEnrichmentPeriod(request.Period) {
		return request, errors.New("day/week range indicators require candle periods below D1 (s1 through h4)")
	}
	switch request.Source {
	case "", "auto":
		request.Source = "auto"
	case "close", "open", "high", "low", "bidprice", "askprice":
	default:
		return request, errors.New("unsupported price source")
	}
	if indicator.Input == "timestamp" || indicator.Input == "low" || indicator.Input == "high" {
		request.Source = "auto"
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

// Groups are expanded to ordinary recipes with reviewed defaults. Files and
// backups persist those individual recipes so replay never depends on a later
// change to group membership or default parameter values.
func (m *Manager) enrichmentMembers(request EnrichmentRequest) ([]EnrichmentRequest, error) {
	if request.Group == "" {
		member, err := m.validateEnrichment(request)
		return []EnrichmentRequest{member}, err
	}
	if request.Indicator != "" || len(request.Parameters) != 0 {
		return nil, errors.New("choose one indicator or one group; groups use each indicator's default parameters")
	}
	members := []EnrichmentRequest{}
	for _, definition := range m.Indicators().Indicators {
		if definition.Category != request.Group {
			continue
		}
		member := request
		member.Group = ""
		member.Indicator = definition.ID
		canonical, err := m.validateEnrichment(member)
		if err != nil {
			return nil, err
		}
		members = append(members, canonical)
	}
	if len(members) == 0 {
		return nil, errors.New("choose a supported indicator group")
	}
	return members, nil
}

func (m *Manager) enrichmentIntradayOnly(request EnrichmentRequest) bool {
	members, _ := m.enrichmentMembers(request)
	for _, member := range members {
		if indicatorDefinitions()[member.Indicator].IntradayOnly {
			return true
		}
	}
	return false
}

func intradayEnrichmentPeriod(period string) bool {
	switch period {
	case "s1", "m1", "m5", "m15", "m30", "h1", "h4":
		return true
	}
	return false
}
