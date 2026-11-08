package screener

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cryptodesk/internal/domain"
)

func TestReadableHyperliquidNamesPreserveIdentityAndAvoidCollisions(t *testing.T) {
	directory := t.TempDir()
	assets := []Instrument{
		{ID: "hyperliquid-" + publicAlertSymbol("spot", "@268"), NativeID: "@268", Market: "spot", Name: "AAPL/USDC · @268"},
		{ID: "hyperliquid-" + publicAlertSymbol("spot", "@999"), NativeID: "@999", Market: "spot", Name: "AAPL/USDC · @999"},
		{ID: "hyperliquid-" + publicAlertSymbol("perpetual", "xyz:AAPL"), NativeID: "xyz:AAPL", Market: "perpetual", Name: "xyz:AAPL"},
	}
	names := map[string]bool{}
	for i, asset := range assets {
		if err := writeHyperliquidAsset(directory, asset); err != nil {
			t.Fatal(err)
		}
		name, err := canonicalHistoryName(asset.ID, "d1", "2026-01-26", "2026-10-31", nil, fileMetadata{}, directory)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && name != "hyperliquid_aapl-usdc-d1-2026-01-26-2026-10-31.csv" {
			t.Fatal(name)
		}
		match := parseDownloadFilename(directory, name)
		if names[name] || match == nil || match[1] != asset.ID || fileDataSource(name) != "hyperliquid" {
			t.Fatalf("identity collision: %s %v", name, match)
		}
		names[name] = true
	}
}

func TestReadableHyperliquidDownloadMigratesLegacyHistoryAndRestores(t *testing.T) {
	m := hyperliquidDownloadManager(t)
	id := "hyperliquid-" + publicAlertSymbol("spot", "@268")
	m.hyperliquidAdapter = publicAlertLiveSmokeAdapter{Adapter: m.hyperliquidAdapter, items: []domain.Instrument{{NativeID: "@268", Market: "spot", Availability: "available", ProviderMetadata: map[string]string{"displaySymbol": "AAPL/USDC"}}}}
	legacy := id + "-d1-2024-01-01-2024-01-04.csv"
	writeDataFile(t, m, legacy, refreshCSV)
	if _, err := m.Start(Request{DataSource: "hyperliquid", Symbols: []string{id}, Timeframe: "w1", From: "2024-01-01", To: "2024-01-20"}); err != nil {
		t.Fatal(err)
	}
	info := waitHyperliquidDownload(t, m)
	if _, err := os.Stat(filepath.Join(m.directory, legacy)); !os.IsNotExist(err) {
		t.Fatalf("legacy duplicate remains: %v", err)
	}
	for _, job := range info.Jobs {
		if !strings.HasPrefix(filepath.Base(job.OutputPath), "hyperliquid_aapl-usdc-") {
			t.Fatal(job.OutputPath)
		}
	}
	page, err := m.FilesForSource(0, 20, "all", "hyperliquid")
	if err != nil || page.Total != 2 || page.Files[0].Symbol != id {
		t.Fatalf("source inventory: %+v %v", page, err)
	}
	if _, err := m.StartEnrichment(EnrichmentRequest{DataSource: "hyperliquid", Period: "d1", Indicator: "sma", Parameters: map[string]float64{"period": 3}}); err != nil {
		t.Fatal(err)
	}
	if got := waitEnrichment(t, m, nil); got.Completed != 1 {
		t.Fatal(got)
	}
	m.Close()
	restored := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
	defer restored.Close()
	if got := restored.Info(); got.Error != "" || len(got.Jobs) != 2 || got.Jobs[0].OutputPath != info.Jobs[0].OutputPath {
		t.Fatalf("queue restore: %+v", got)
	}
	// Stable IDs also keep the provider and downloaded history independent of names.
	if _, err := restored.CatalogForSource(context.Background(), "dukascopy"); err != nil {
		t.Fatal(err)
	}
}
