// Package predictions owns local model preparation and immutable forecast
// snapshots independently of Wails and the market-data files it reads.
package predictions

import "context"

const DefaultModel = "Datadog/Toto-2.0-313m"

type Settings struct {
	Model          string `json:"model"`
	ModelDirectory string `json:"modelDirectory"`
}

type ModelOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Selection struct {
	Asset         string `json:"asset"`
	Period        string `json:"period"`
	ContextLength int    `json:"contextLength"`
	Horizon       int    `json:"horizon"`
	ForecastID    string `json:"forecastId"`
}

type Dataset struct {
	ID        string `json:"id"`
	Symbol    string `json:"symbol"`
	Name      string `json:"name"`
	Period    string `json:"period"`
	Filename  string `json:"filename"`
	Directory string `json:"-"`
}

type Observation struct {
	Time  int64   `json:"time"`
	Close float64 `json:"close"`
}

type Point struct {
	Step   int     `json:"step"`
	Lower  float64 `json:"lower"`
	Median float64 `json:"median"`
	Upper  float64 `json:"upper"`
}

type Forecast struct {
	ID            string        `json:"id"`
	CreatedAt     int64         `json:"createdAt"`
	AsOf          int64         `json:"asOf"`
	Model         string        `json:"model"`
	ModelRevision string        `json:"modelRevision"`
	Symbol        string        `json:"symbol"`
	AssetName     string        `json:"assetName"`
	Period        string        `json:"period"`
	ContextLength int           `json:"contextLength"`
	Horizon       int           `json:"horizon"`
	SourceFile    string        `json:"sourceFile"`
	SourceHash    string        `json:"sourceHash"`
	History       []Observation `json:"history"`
	Points        []Point       `json:"points"`
}

type RuntimeStatus struct {
	Ready        bool   `json:"ready"`
	ModelReady   bool   `json:"modelReady"`
	RuntimeReady bool   `json:"runtimeReady"`
	Error        string `json:"error,omitempty"`
}

type ForecastSummary struct {
	ID            string `json:"id"`
	CreatedAt     int64  `json:"createdAt"`
	AsOf          int64  `json:"asOf"`
	Model         string `json:"model"`
	Symbol        string `json:"symbol"`
	AssetName     string `json:"assetName"`
	Period        string `json:"period"`
	ContextLength int    `json:"contextLength"`
	Horizon       int    `json:"horizon"`
}

type Event struct {
	Phase      string  `json:"phase"`
	Progress   float64 `json:"progress"`
	Message    string  `json:"message"`
	Downloaded int64   `json:"downloaded,omitempty"`
	Total      int64   `json:"total,omitempty"`
}

type RuntimePrediction struct {
	Revision string  `json:"revision"`
	Points   []Point `json:"points"`
}

// ModelRuntime is injectable for isolated offline production-service tests.
// Normal application operation always uses the real pinned implementation.
type ModelRuntime interface {
	Inspect(Settings) RuntimeStatus
	Prepare(context.Context, Settings, func(Event)) error
	Predict(context.Context, Settings, []float64, int, func(Event)) (RuntimePrediction, error)
}

type Info struct {
	Settings              Settings          `json:"settings"`
	Models                []ModelOption     `json:"models"`
	DefaultModelDirectory string            `json:"defaultModelDirectory"`
	ForecastDirectory     string            `json:"forecastDirectory"`
	Selection             Selection         `json:"selection"`
	Runtime               RuntimeStatus     `json:"runtime"`
	Active                bool              `json:"active"`
	Event                 Event             `json:"event"`
	Error                 string            `json:"error,omitempty"`
	Forecasts             []ForecastSummary `json:"forecasts"`
}
