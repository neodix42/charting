// Package screener owns the machine-local Dukascopy downloader independently of
// Wails, workspace documents, chart feeds and exchanges.
package screener

import (
	_ "embed"
	"encoding/json"
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

func loadCatalog() Catalog {
	var c Catalog
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		panic("invalid embedded Dukascopy catalog: " + err.Error())
	}
	return c
}

// Catalog returns a new copy; callers cannot mutate manager validation data.
func (m *Manager) Catalog() Catalog { return loadCatalog() }
