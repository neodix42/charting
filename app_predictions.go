package main

import (
	"os"
	"strings"

	"cryptodesk/internal/predictions"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) GetPredictions() predictions.Info {
	if err := a.awaitService("predictions"); err != nil {
		return predictions.Info{Error: err.Error()}
	}
	return a.predictions.Info()
}

func (a *App) GetPredictionDatasets() ([]predictions.Dataset, error) {
	if err := a.awaitService("predictions"); err != nil {
		return nil, err
	}

	return a.predictions.Datasets()
}

func (a *App) ConfigurePredictions(settings predictions.Settings) (predictions.Info, error) {
	if err := a.awaitService("predictions"); err != nil {
		return predictions.Info{}, err
	}

	return a.predictions.Configure(settings)
}

func (a *App) SavePredictionSelection(selection predictions.Selection) (predictions.Selection, error) {
	if err := a.awaitService("predictions"); err != nil {
		return predictions.Selection{}, err
	}

	return a.predictions.SaveSelection(selection)
}

func (a *App) PreparePredictionModel() (predictions.Info, error) {
	if err := a.awaitService("predictions"); err != nil {
		return predictions.Info{}, err
	}
	return a.predictions.Prepare()
}

func (a *App) StartPrediction(selection predictions.Selection) (predictions.Info, error) {
	if err := a.awaitService("predictions"); err != nil {
		return predictions.Info{}, err
	}

	return a.predictions.Start(selection)
}

func (a *App) CancelPrediction() predictions.Info {
	if err := a.awaitService("predictions"); err != nil {
		return predictions.Info{Error: err.Error()}
	}
	return a.predictions.Cancel()
}

func (a *App) GetPredictionForecast(id string) (predictions.Forecast, error) {
	if err := a.awaitService("predictions"); err != nil {
		return predictions.Forecast{}, err
	}

	return a.predictions.Forecast(id)
}

func (a *App) DeletePredictionForecast(id string) (predictions.Info, error) {
	if err := a.awaitService("predictions"); err != nil {
		return predictions.Info{}, err
	}

	return a.predictions.DeleteForecast(id)
}

func (a *App) ChoosePredictionModelDirectory() (string, error) {
	if err := a.awaitService("predictions"); err != nil {
		return "", err
	}

	initial := a.predictions.Info().Settings.ModelDirectory
	if st, err := os.Stat(initial); err != nil || !st.IsDir() {
		initial = a.store.Directory()
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose prediction model folder", DefaultDirectory: initial, CanCreateDirectories: true,
	})
}

// The Screener remains the owner of its CSV inventory. Only authoritative,
// downloaded candle files enter Predictions; webview requests never supply a
// path and inference never writes back to market data.
func (a *App) predictionDatasets() ([]predictions.Dataset, error) {
	if err := a.awaitService("screener"); err != nil {
		return nil, err
	}

	catalog, err := a.screener.AnalysisCatalog()
	if err != nil {
		return nil, err
	}
	files := make([]predictions.Dataset, 0, len(catalog.Files))
	for _, file := range catalog.Files {
		if file.Error != "" || file.Symbol == "" || !predictionPeriod(file.Timeframe) {
			continue
		}
		hasClose := false
		for _, field := range file.Fields {
			if strings.EqualFold(strings.TrimSpace(field), "close") {
				hasClose = true
				break
			}
		}
		if !hasClose {
			continue
		}
		name := file.Description
		if name == "" {
			name = strings.ToUpper(file.Symbol)
		}
		files = append(files, predictions.Dataset{
			ID: file.ID, Symbol: file.Symbol, Name: name, Period: file.Timeframe,
			Filename: file.Name, Directory: catalog.Directory,
		})
	}
	return files, nil
}

func predictionPeriod(period string) bool {
	switch period {
	case "s1", "m1", "m5", "m15", "m30", "h1", "h4", "d1", "mn1":
		return true
	default:
		return false
	}
}
