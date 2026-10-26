// Package screener owns the machine-local Dukascopy downloader independently of
// Wails, workspace documents, chart feeds and exchanges.
package screener

import (
	_ "embed"
	"encoding/json"
	"strings"
)

type Instrument struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	EarliestDate string `json:"earliestDate"`
}
type Group struct {
	Name        string       `json:"name"`
	Instruments []Instrument `json:"instruments"`
}
type Catalog struct {
	Version    string   `json:"version"`
	Groups     []Group  `json:"groups"`
	Timeframes []string `json:"timeframes"`
}

// Generated from the pinned npm package's README and checked one-to-one against
// its public instrumentMetaData export by screener-runtime/generate-catalog.cjs.
//
//go:embed catalog.json
var catalogJSON []byte

const otherAssetCategory = "Other"

type assetMetadata struct {
	description string
	category    string
}

// Keep the lookup private and immutable: CSV listings can contain thousands of
// files, so resolving each asset must not repeatedly decode the whole catalog.
var catalogAssets = func() map[string]assetMetadata {
	assets := map[string]assetMetadata{}
	for _, group := range loadCatalog().Groups {
		for _, instrument := range group.Instruments {
			assets[strings.ToLower(instrument.ID)] = assetMetadata{description: instrument.Name, category: group.Name}
		}
	}
	return assets
}()

func describeAsset(symbol, fallback string) assetMetadata {
	if metadata, ok := catalogAssets[strings.ToLower(symbol)]; ok {
		return metadata
	}
	if symbol != "" {
		fallback = strings.ToUpper(symbol)
	}
	return assetMetadata{description: fallback, category: otherAssetCategory}
}

func loadCatalog() Catalog {
	var c Catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		panic("invalid embedded Dukascopy catalog: " + err.Error())
	}
	return c
}

// Catalog returns a new copy; callers cannot mutate manager validation data.
func (m *Manager) Catalog() Catalog { return loadCatalog() }
