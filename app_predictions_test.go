//go:build bindings

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/predictions"
	"cryptodesk/internal/screener"
	"cryptodesk/internal/storage"
)

// The facade uses the real inventory, CSV reader and snapshot store. Only the
// costly model is substituted, so these tests never install a runtime or model.
type facadePredictionRuntime struct{}

func (facadePredictionRuntime) Inspect(predictions.Settings) predictions.RuntimeStatus {
	return predictions.RuntimeStatus{Ready: true, ModelReady: true, RuntimeReady: true}
}
func (facadePredictionRuntime) Prepare(context.Context, predictions.Settings, func(predictions.Event)) error {
	return nil
}
func (facadePredictionRuntime) Predict(_ context.Context, _ predictions.Settings, history []float64, horizon int, _ func(predictions.Event)) (predictions.RuntimePrediction, error) {
	points := make([]predictions.Point, horizon)
	for i := range points {
		value := history[len(history)-1] + float64(i+1)
		points[i] = predictions.Point{Step: i + 1, Lower: value - 2, Median: value, Upper: value + 2}
	}
	return predictions.RuntimePrediction{Revision: "explicit-offline-facade-test", Points: points}, nil
}

func predictionFacade(t *testing.T) (*App, storage.Loaded) {
	t.Helper()
	profile := t.TempDir()
	store, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := &App{store: store, screener: screener.New(profile)}
	a.predictions = predictions.New(profile, a.predictionDatasets, facadePredictionRuntime{})
	t.Cleanup(func() { a.predictions.Close(); a.screener.Close(); _ = store.Close() })
	if err := os.MkdirAll(a.screener.Info().Directory, 0700); err != nil {
		t.Fatal(err)
	}
	return a, loaded
}

func TestPredictionFacadeUsesDownloadedCandleInventory(t *testing.T) {
	a, _ := predictionFacade(t)
	files := map[string]string{
		"btcusd-d1-2024-01-01-2026-10-29.csv":   "timestamp,close\n1704067200000,100\n",
		"btcusd-h1-2024-01-01-2026-10-29.csv":   "timestamp,open,high,low,Close\n1704067200000,100,102,99,101\n",
		"btcusd-tick-2024-01-01-2026-10-29.csv": "timestamp,bidPrice,askPrice\n1704067200000,100,101\n",
		"audusd-d1-2024-01-01-2026-10-29.csv":   "timestamp,high\n1704067200000,101\n",
		"unrecognized.csv":                      "timestamp,close\n1704067200000,100\n",
		".btcusd-m1-2024-01-01-2026-10-29.csv":  "timestamp,close\n1704067200000,100\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(a.screener.Info().Directory, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	datasets, err := a.GetPredictionDatasets()
	if err != nil || len(datasets) != 2 || datasets[0].Symbol != "btcusd" || datasets[0].Period != "d1" || datasets[1].Period != "h1" || datasets[0].Name != "Bitcoin vs US Dollar" {
		t.Fatalf("prediction inventory: %+v %v", datasets, err)
	}
	encoded, err := json.Marshal(datasets)
	if err != nil || strings.Contains(string(encoded), a.store.Directory()) || strings.Contains(string(encoded), "directory") {
		t.Fatalf("webview inventory exposed internal source directory: %s %v", encoded, err)
	}
	if _, err = a.StartPrediction(predictions.Selection{Asset: "btcusd", Period: "tick", ContextLength: 512, Horizon: 20}); err == nil {
		t.Fatal("tick prediction accepted by facade")
	}
}

func TestPredictionFacadeSeparatesSnapshotsAndRestoresPreferences(t *testing.T) {
	a, workspace := predictionFacade(t)
	initial := a.GetPredictions()
	if initial.Settings.Model != predictions.DefaultModel || initial.Selection.ContextLength != 512 || initial.Selection.Horizon != 20 || initial.Settings.ModelDirectory != filepath.Join(a.store.Directory(), "predictions", "models") {
		t.Fatalf("unexpected prediction defaults: %+v", initial)
	}
	path := filepath.Join(a.screener.Info().Directory, "btcusd-d1-2024-01-01-2026-10-29.csv")
	var csv strings.Builder
	csv.WriteString("timestamp,close,sma_20\n")
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&csv, "%d,%d,%d\n", start.AddDate(0, 0, i).UnixMilli(), 1000+i, 990+i)
	}
	if err := os.WriteFile(path, []byte(csv.String()), 0600); err != nil {
		t.Fatal(err)
	}
	settings := predictions.Settings{Model: predictions.DefaultModel, ModelDirectory: filepath.Join(a.store.Directory(), "chosen", "model files")}
	if _, err := a.ConfigurePredictions(settings); err != nil {
		t.Fatal(err)
	}
	selection := predictions.Selection{Asset: "btcusd", Period: "d1", ContextLength: 512, Horizon: 20}
	if _, err := a.SavePredictionSelection(selection); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PreparePredictionModel(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartPrediction(selection); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for a.GetPredictions().Active {
		if time.Now().After(deadline) {
			t.Fatal("prediction did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	info := a.GetPredictions()
	if info.Error != "" || len(info.Forecasts) != 1 {
		t.Fatalf("prediction was not saved: %+v", info)
	}
	forecast, err := a.GetPredictionForecast(info.Forecasts[0].ID)
	if err != nil || len(forecast.History) != 512 || len(forecast.Points) != 20 || forecast.History[0].Close != 1088 || forecast.Points[0].Median != 1600 || forecast.CreatedAt < forecast.AsOf || forecast.ModelRevision != "explicit-offline-facade-test" {
		t.Fatalf("snapshot provenance/results: %+v %v", forecast, err)
	}
	selection.ForecastID = forecast.ID
	if _, err = a.SavePredictionSelection(selection); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != csv.String() {
		t.Fatal("prediction changed original enriched market data", err)
	}
	if after, err := a.store.Load(); err != nil || !reflect.DeepEqual(workspace, after) {
		t.Fatal("prediction preferences changed saved chart workspace", err)
	}
	a.predictions.Close()
	a.predictions = predictions.New(a.store.Directory(), a.predictionDatasets, facadePredictionRuntime{})
	restored := a.GetPredictions()
	if restored.Settings != settings || restored.Selection != selection || len(restored.Forecasts) != 1 {
		t.Fatalf("prediction state did not restore: %+v", restored)
	}
	if _, err = a.GetPredictionForecast("../../screener/private"); err == nil {
		t.Fatal("facade accepted arbitrary forecast path")
	}
}
