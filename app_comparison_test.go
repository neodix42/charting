package main

import (
	"context"
	"path/filepath"
	"testing"

	"cryptodesk/internal/comparison"
	"cryptodesk/internal/market"
	"cryptodesk/internal/storage"
)

func TestComparisonFacadeKeepsEmptySavedViewWithoutStartingProviders(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// An empty selection must never invoke the real adapter factory.
	service := market.New(market.Options{})
	defer service.Close()
	app := &App{store: store, market: service, ctx: context.Background()}
	initial, err := app.GetComparisonView()
	if err != nil || initial.Weeks != 4 || len(initial.Assets) != 7 {
		t.Fatalf("initial view %+v %v", initial, err)
	}
	view := comparison.View{Weeks: 12, Assets: []string{}}
	if _, err = app.SaveComparisonView(view); err != nil {
		t.Fatal(err)
	}
	result, err := app.GetComparisonDailyChanges("empty-facade", view)
	if err != nil || result.Weeks != 12 || len(result.Series) != 0 || service.Diagnostics().HistoryRequests != 0 {
		t.Fatalf("empty Comparison fetched data %+v %v", result, err)
	}
	app.CancelComparisonRequest("empty-facade")
	app.comparison.Close()
	app.comparison = nil
	restored, err := app.GetComparisonView()
	if err != nil || restored.Weeks != 12 || len(restored.Assets) != 0 {
		t.Fatalf("empty view not durable %+v %v", restored, err)
	}
	app.comparison.Close()
	app.comparisonClosed = true
	if _, err = app.GetComparisonView(); err == nil {
		t.Fatal("Comparison reopened after app shutdown")
	}
}
