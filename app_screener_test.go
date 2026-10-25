//go:build bindings

package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"cryptodesk/internal/screener"
	"cryptodesk/internal/storage"
)

func TestScreenerFolderIsIndependentOfWorkspaceAndRestores(t *testing.T) {
	profile := t.TempDir()
	store, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	manager := screener.New(profile)
	t.Cleanup(manager.Close)
	app := &App{store: store, screener: manager}
	if catalog := app.GetScreenerIndicators(); catalog.Version != "8.3.0" || len(catalog.Indicators) != 42 {
		t.Fatalf("unexpected enrichment catalog: %+v", catalog)
	}
	if files, err := app.GetScreenerFiles(0, 50); err != nil || files.Total != 0 || files.Directory != filepath.Join(profile, "screener") {
		t.Fatalf("unexpected initial CSV inventory: %+v %v", files, err)
	}
	if _, err := app.StartScreenerEnrichment(screener.EnrichmentRequest{Indicator: "unsupported"}); err == nil {
		t.Fatal("facade accepted unsupported enrichment")
	}
	if info := app.GetScreener(); info.Concurrency != 20 || info.MaxConcurrency != 20 {
		t.Fatalf("unexpected parallel download defaults: %+v", info)
	}
	if got := app.GetScreener().Directory; got != filepath.Join(profile, "screener") {
		t.Fatalf("default folder: %q", got)
	}
	destination := filepath.Join(profile, "selected", "market data")
	configured, err := app.ConfigureScreenerDirectory(destination)
	if err != nil || configured.Directory != destination {
		t.Fatalf("configure directory: %+v, %v", configured, err)
	}
	if _, err = app.StartScreenerDownload(screener.Request{Symbols: []string{"not-a-real-instrument"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-02"}); err == nil {
		t.Fatal("facade accepted an unsupported instrument")
	}
	if _, err = app.StartScreenerDownload(screener.Request{Symbols: []string{"btcusd"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-02", Concurrency: 21}); err == nil {
		t.Fatal("facade accepted more than twenty parallel downloads")
	}
	if jobs := app.CancelScreenerDownload().Jobs; len(jobs) != 0 {
		t.Fatalf("invalid request created jobs: %+v", jobs)
	}
	draft, err := app.GetScreenerAnalysisView()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.SaveScreenerAnalysisCondition("My condition", draft); err != nil {
		t.Fatal(err)
	}
	if saved, err := app.GetScreenerSavedConditions(); err != nil || len(saved) != 1 || !reflect.DeepEqual(saved[0].Request, draft) {
		t.Fatalf("facade lost saved condition rows: %+v %v", saved, err)
	}
	after, err := store.Load()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("machine-local configuration changed saved workspace: %v", err)
	}
	reopened := screener.New(profile)
	defer reopened.Close()
	if got := reopened.Info().Directory; got != destination {
		t.Fatalf("preference did not restore: %q", got)
	}
	if saved, err := reopened.SavedAnalysisConditions(); err != nil || len(saved) != 1 || saved[0].Name != "My condition" {
		t.Fatalf("named condition did not restore: %+v %v", saved, err)
	}
	configured, err = app.ConfigureScreenerDirectory("  ")
	if err != nil || configured.Directory != filepath.Join(profile, "screener") {
		t.Fatalf("empty path did not reset to profile default: %+v, %v", configured, err)
	}
}
