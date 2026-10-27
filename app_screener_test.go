//go:build bindings

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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
	if info := app.GetScreener(); info.Concurrency != 1 || info.MaxConcurrency != 1 {
		t.Fatalf("unexpected sequential download defaults: %+v", info)
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
		t.Fatal("facade accepted an invalid legacy concurrency value")
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

func TestScreenerAnalysisFacadeFiltersCategoriesBeforePaging(t *testing.T) {
	manager := screener.New(t.TempDir())
	t.Cleanup(manager.Close)
	app := &App{screener: manager}
	catalog := app.GetScreenerCatalog()
	category := ""
	for _, group := range catalog.Groups {
		for _, instrument := range group.Instruments {
			if instrument.ID == "arkiususd" {
				category = group.Name
			}
		}
	}
	if category == "" {
		t.Fatal("test instrument missing from catalog")
	}
	if err := os.MkdirAll(manager.Info().Directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"arkiususd", "btcusd"} {
		path := filepath.Join(manager.Info().Directory, symbol+"-m1-2024-01-01-2024-01-02.csv")
		if err := os.WriteFile(path, []byte("timestamp,close\n1704067200000,2\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request, err := app.GetScreenerAnalysisView()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.StartScreenerAnalysis(request); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		page, err := app.GetScreenerAnalysisFiltered(0, 10, screener.AnalysisFilter{Categories: []string{category}})
		if err != nil {
			t.Fatal(err)
		}
		if !page.Active {
			if page.Total != 2 || page.TotalResults != 1 || len(page.Results) != 1 || page.Results[0].Description != "ARK Artificial Intelligence & Robotics UCITS ETF" || page.Results[0].Category != category {
				t.Fatalf("filtered facade metadata: %+v", page)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("analysis did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if all, err := app.GetScreenerAnalysis(0, 10); err != nil || all.TotalResults != 2 {
		t.Fatalf("original facade no longer lists all results: %+v %v", all, err)
	}
	if _, err := app.GetScreenerAnalysisFiltered(0, 10, screener.AnalysisFilter{Categories: []string{"invalid"}}); err == nil {
		t.Fatal("facade accepted an invalid category")
	}
}

func TestScreenerDeleteSavedConditionFacadePersists(t *testing.T) {
	profile := t.TempDir()
	manager := screener.New(profile)
	t.Cleanup(manager.Close)
	app := &App{screener: manager}
	draft, err := app.GetScreenerAnalysisView()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := app.SaveScreenerAnalysisCondition("Remove", draft)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.DeleteScreenerAnalysisCondition(saved.ID); err != nil {
		t.Fatal(err)
	}
	if err = app.DeleteScreenerAnalysisCondition(saved.ID); err == nil {
		t.Fatal("facade did not propagate the missing condition error")
	}
	manager.Close()
	reopened := screener.New(profile)
	t.Cleanup(reopened.Close)
	app = &App{screener: reopened}
	if list, err := app.GetScreenerSavedConditions(); err != nil || len(list) != 0 {
		t.Fatalf("facade deletion did not persist: %+v %v", list, err)
	}
	if restored, err := app.GetScreenerAnalysisView(); err != nil || !reflect.DeepEqual(restored, draft) {
		t.Fatalf("facade deletion changed the editable draft: %+v %v", restored, err)
	}
}

func TestScreenerRenameSavedConditionFacadePersists(t *testing.T) {
	profile := t.TempDir()
	manager := screener.New(profile)
	t.Cleanup(manager.Close)
	app := &App{screener: manager}
	draft, err := app.GetScreenerAnalysisView()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := app.SaveScreenerAnalysisCondition("Original", draft)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := app.RenameScreenerAnalysisCondition(saved.ID, "  Renamed  ")
	if err != nil || renamed.ID != saved.ID || renamed.Name != "Renamed" || !reflect.DeepEqual(renamed.Request, saved.Request) {
		t.Fatalf("facade rename changed the snapshot: %+v %v", renamed, err)
	}
	if _, err := app.RenameScreenerAnalysisCondition(saved.ID, ""); err == nil {
		t.Fatal("facade did not propagate invalid name errors")
	}
	if _, err := app.RenameScreenerAnalysisCondition("missing", "Valid"); err == nil {
		t.Fatal("facade did not propagate invalid ID errors")
	}
	manager.Close()
	reopened := screener.New(profile)
	t.Cleanup(reopened.Close)
	app = &App{screener: reopened}
	if list, err := app.GetScreenerSavedConditions(); err != nil || !reflect.DeepEqual(list, []screener.SavedAnalysisCondition{renamed}) {
		t.Fatalf("facade rename did not persist: %+v %v", list, err)
	}
	if restored, err := app.GetScreenerAnalysisView(); err != nil || !reflect.DeepEqual(restored, draft) {
		t.Fatalf("facade rename changed the editable draft: %+v %v", restored, err)
	}
}
