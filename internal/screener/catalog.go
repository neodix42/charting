// Package screener owns the machine-local Dukascopy downloader independently of
// Wails, workspace documents, chart feeds and exchanges.
package screener

import (
	_ "embed"
	"encoding/json"
	"strings"
)

type Instrument struct {
	NativeID     string `json:"nativeId,omitempty"`
	Market       string `json:"market,omitempty"`
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

// Decode the generated catalog once. Internal lookups only read these values;
// public callers receive independent slices through loadCatalog.
var catalogData = func() Catalog {
	var c Catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		panic("invalid embedded Dukascopy catalog: " + err.Error())
	}
	return c
}()

var catalogTimeframes = func() map[string]bool {
	values := make(map[string]bool, len(catalogData.Timeframes))
	for _, value := range catalogData.Timeframes {
		values[value] = true
	}
	return values
}()

const otherAssetCategory = "Other"

type assetMetadata struct {
	description string
	category    string
}

// Keep the lookup private and immutable: CSV listings can contain thousands of
// files, so resolving each asset must not repeatedly decode the whole catalog.
var catalogAssets = func() map[string]assetMetadata {
	assets := map[string]assetMetadata{}
	for _, group := range catalogData.Groups {
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
	c := catalogData
	c.Timeframes = append([]string{}, catalogData.Timeframes...)
	c.Groups = append([]Group{}, catalogData.Groups...)
	for i := range c.Groups {
		c.Groups[i].Instruments = append([]Instrument{}, catalogData.Groups[i].Instruments...)
	}
	return c
}

// Catalog returns a new copy; callers cannot mutate manager validation data.
func (m *Manager) Catalog() Catalog { return loadCatalog() }
