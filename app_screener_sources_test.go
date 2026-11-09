//go:build bindings

package main

import (
	"os"
	"path/filepath"
	"testing"

	"cryptodesk/internal/screener"
)

func TestScreenerAnalysisFacadeRetainsSelectedSource(t *testing.T) {
	profile := t.TempDir()
	manager := screener.New(profile)
	defer manager.Close()
	app := &App{screener: manager}
	directory := manager.Info().Directory
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"btcusd-d1-2024-01-01-2024-01-02.csv":          "timestamp,close,dukas_signal\n1704067200000,100,1\n",
		"hyperliquid_btc-d1-2024-01-01-2024-01-02.csv": "timestamp,close,hyper_signal\n1704067200000,200,2\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"dukascopy", "hyperliquid"} {
		catalog, err := app.GetScreenerAnalysisCatalogForSource(source)
		if err != nil || catalog.DataSource != source || len(catalog.Files) != 1 || catalog.Files[0].DataSource != source {
			t.Fatalf("bound source catalog leaked other inventory: %+v %v", catalog, err)
		}
	}
	if _, err := app.GetScreenerAnalysisCatalogForSource("binance"); err == nil {
		t.Fatal("unsupported analysis source reached catalog")
	}
	request, err := app.GetScreenerAnalysisView()
	if err != nil {
		t.Fatal(err)
	}
	request.DataSource = "hyperliquid"
	if _, err = app.SaveScreenerAnalysisView(request); err != nil {
		t.Fatal(err)
	}
	saved, err := app.SaveScreenerAnalysisCondition("Hyperliquid analysis", request)
	if err != nil || saved.Request.DataSource != "hyperliquid" {
		t.Fatal("bound save lost the selected source", saved, err)
	}
	manager.Close()
	restored := screener.New(profile)
	defer restored.Close()
	app.screener = restored
	draft, err := app.GetScreenerAnalysisView()
	if err != nil || draft.DataSource != "hyperliquid" {
		t.Fatal("bound restore lost the draft source", draft, err)
	}
	conditions, err := app.GetScreenerSavedConditions()
	if err != nil || len(conditions) != 1 || conditions[0].ID != saved.ID || conditions[0].Request.DataSource != "hyperliquid" {
		t.Fatal("bound restore lost the condition source", conditions, err)
	}
}
