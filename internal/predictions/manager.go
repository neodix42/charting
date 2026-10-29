package predictions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Manager struct {
	mu                                           sync.Mutex
	wg                                           sync.WaitGroup
	profile, settingsPath, defaultModelDirectory string
	forecastDirectory                            string
	settings                                     Settings
	selection                                    Selection
	status                                       RuntimeStatus
	event                                        Event
	forecasts                                    []ForecastSummary
	settingsError, restoreError, errorText       string
	active, closed                               bool
	operation                                    string
	cancel                                       context.CancelFunc
	done                                         chan struct{}
	source                                       func() ([]Dataset, error)
	runtime                                      ModelRuntime
}

func New(profile string, source func() ([]Dataset, error), runtime ModelRuntime) *Manager {
	if absolute, err := filepath.Abs(profile); err == nil {
		profile = absolute
	}
	m := &Manager{profile: profile, settingsPath: filepath.Join(profile, "predictions.json"), defaultModelDirectory: filepath.Join(profile, "predictions", "models"), forecastDirectory: filepath.Join(profile, "predictions", "forecasts"), source: source, runtime: runtime, forecasts: []ForecastSummary{}, selection: Selection{Period: "d1", ContextLength: 512, Horizon: 20}, event: Event{Phase: "idle", Message: "Choose downloaded candle data and prepare a local model"}}
	m.settings = Settings{Model: DefaultModel, ModelDirectory: m.defaultModelDirectory}
	var saved savedSettings
	err := readJSON(m.settingsPath, 64*1024, &saved)
	if err == nil {
		if saved.Version != 1 {
			err = errors.New("unsupported saved prediction settings version")
		} else if err = validateSettings(saved.Settings); err == nil {
			err = validateSelection(saved.Selection, false)
		}
		if err == nil {
			m.settings, m.selection = saved.Settings, saved.Selection
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		m.settingsError = "Restore prediction settings (original state preserved): " + cleanError(err)
	}
	if err = m.loadForecasts(); err != nil {
		m.restoreError = cleanError(err)
	}
	if m.selection.ForecastID != "" {
		// External removal or corruption must not trap every later selection
		// edit or prediction behind an unavailable reference. Keep the saved
		// settings and snapshot bytes intact until the user changes selection.
		if _, err := m.Forecast(m.selection.ForecastID); err != nil {
			m.selection.ForecastID = ""
		}
	}
	if runtime != nil {
		m.status = runtime.Inspect(m.settings)
	} else {
		m.status.Error = "Local prediction runtime is unavailable"
	}
	return m
}

func cleanError(err error) string {
	message := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, err.Error())
	runes := []rune(message)
	if len(runes) > 2000 {
		runes = runes[:2000]
	}
	return string(runes)
}

func validateSettings(settings Settings) error {
	if !ValidModel(settings.Model) {
		return errors.New("choose one of the supported local prediction models")
	}
	if settings.ModelDirectory == "" || !filepath.IsAbs(settings.ModelDirectory) || filepath.Clean(settings.ModelDirectory) != settings.ModelDirectory || strings.ContainsRune(settings.ModelDirectory, 0) {
		return errors.New("model storage must be an absolute folder path")
	}
	return nil
}

func validateSelection(selection Selection, complete bool) error {
	if selection.ContextLength != 512 && selection.ContextLength != 1024 {
		return errors.New("prediction context must contain 512 or 1024 closed bars")
	}
	if selection.Horizon < 1 || selection.Horizon > 256 {
		return errors.New("prediction horizon must be between 1 and 256 bars")
	}
	if len(selection.Asset) > 128 || strings.ContainsFunc(selection.Asset, unicode.IsControl) || strings.TrimSpace(selection.Asset) != selection.Asset || (complete && selection.Asset == "") {
		return errors.New("choose a downloaded prediction asset")
	}
	if (selection.Period != "" || complete) && !validPeriod(selection.Period) {
		return errors.New("choose an available candle period; tick data cannot be predicted")
	}
	if selection.ForecastID != "" && !forecastID.MatchString(selection.ForecastID) {
		return errors.New("invalid selected forecast ID")
	}
	return nil
}

func (m *Manager) infoLocked() Info {
	return Info{Settings: m.settings, Models: Models(), DefaultModelDirectory: m.defaultModelDirectory, ForecastDirectory: m.forecastDirectory, Selection: m.selection, Runtime: m.status, Active: m.active, Event: m.event, Error: strings.TrimSpace(strings.Join([]string{m.settingsError, m.restoreError, m.errorText}, " ")), Forecasts: append([]ForecastSummary{}, m.forecasts...)}
}

func (m *Manager) Info() Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.infoLocked()
}

func (m *Manager) Datasets() ([]Dataset, error) {
	if m.source == nil {
		return []Dataset{}, errors.New("downloaded prediction datasets are unavailable")
	}
	items, err := m.source()
	if err != nil {
		return []Dataset{}, err
	}
	result := []Dataset{}
	seen := map[string]bool{}
	for _, dataset := range items {
		if !validPeriod(dataset.Period) {
			continue
		}
		if dataset.ID == "" || dataset.Symbol == "" || seen[dataset.ID] || !filepath.IsAbs(dataset.Directory) || filepath.Base(dataset.Filename) != dataset.Filename || dataset.Filename == "." || dataset.Filename == "" {
			return []Dataset{}, errors.New("downloaded dataset list has invalid or duplicate identities; refresh the Screener file list")
		}
		seen[dataset.ID] = true
		result = append(result, dataset)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Symbol != result[j].Symbol {
			return result[i].Symbol < result[j].Symbol
		}
		if result[i].Period != result[j].Period {
			return result[i].Period < result[j].Period
		}
		return result[i].Filename < result[j].Filename
	})
	return result, nil
}

func (m *Manager) Configure(settings Settings) (Info, error) {
	settings.Model = strings.TrimSpace(settings.Model)
	settings.ModelDirectory = strings.TrimSpace(settings.ModelDirectory)
	if settings.ModelDirectory == "" {
		settings.ModelDirectory = m.defaultModelDirectory
	}
	if settings.Model == "" {
		settings.Model = DefaultModel
	}
	if err := validateSettings(settings); err != nil {
		return m.Info(), err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.mutableLocked(); err != nil {
		return m.infoLocked(), err
	}
	changed := m.settings != settings
	if err := writeJSON(m.settingsPath, savedSettings{Version: 1, Settings: settings, Selection: m.selection}, false); err != nil {
		return m.infoLocked(), fmt.Errorf("save prediction settings: %w", err)
	}
	m.settings, m.errorText = settings, ""
	if m.runtime != nil {
		m.status = m.runtime.Inspect(settings)
	}
	if changed {
		m.event = Event{Phase: "idle", Message: "Model settings changed; open Predictions to prepare the selected local model"}
	}
	return m.infoLocked(), nil
}

func (m *Manager) mutableLocked() error {
	if m.closed {
		return errors.New("predictions are closed")
	}
	if m.settingsError != "" {
		return errors.New(m.settingsError)
	}
	if m.active {
		return errors.New("finish or cancel the current prediction operation first")
	}
	return nil
}

func (m *Manager) SaveSelection(selection Selection) (Selection, error) {
	if err := validateSelection(selection, false); err != nil {
		return Selection{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.selection, errors.New("predictions are closed")
	}
	if m.settingsError != "" {
		return m.selection, errors.New(m.settingsError)
	}
	if selection.ForecastID != "" {
		if _, err := m.Forecast(selection.ForecastID); err != nil {
			return m.selection, fmt.Errorf("selected forecast is no longer available: %w", err)
		}
	}
	if err := writeJSON(m.settingsPath, savedSettings{Version: 1, Settings: m.settings, Selection: selection}, false); err != nil {
		return m.selection, fmt.Errorf("save prediction selection: %w", err)
	}
	m.selection = selection
	return m.selection, nil
}

func (m *Manager) emit(event Event) {
	if !finite(event.Progress) {
		return
	}
	event.Progress = max(0, min(100, event.Progress))
	event.Downloaded, event.Total = max(0, event.Downloaded), max(0, event.Total)
	event.Phase = cleanError(errors.New(event.Phase))
	event.Message = cleanError(errors.New(event.Message))
	m.mu.Lock()
	if m.active {
		m.event = event
	}
	m.mu.Unlock()
}

func (m *Manager) Prepare() (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.infoLocked(), errors.New("predictions are closed")
	}
	if m.active && m.operation == "prepare" {
		return m.infoLocked(), nil
	}
	if err := m.mutableLocked(); err != nil {
		return m.infoLocked(), err
	}
	if m.runtime == nil {
		return m.infoLocked(), errors.New("local prediction runtime is unavailable")
	}
	if m.status.Ready {
		return m.infoLocked(), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.active, m.operation, m.cancel, m.done, m.errorText = true, "prepare", cancel, make(chan struct{}), ""
	m.event = Event{Phase: "preparing", Message: "Preparing the local prediction runtime and model"}
	settings := m.settings
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		err := m.runtime.Prepare(ctx, settings, m.emit)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		status := m.runtime.Inspect(settings)
		if err == nil && !status.Ready {
			err = errors.New("model preparation did not produce a ready local runtime: " + status.Error)
		}
		m.finish(err, status, "Model and local runtime are ready")
	}()
	return m.infoLocked(), nil
}

func (m *Manager) finish(err error, status RuntimeStatus, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status, m.active, m.cancel, m.operation = status, false, nil, ""
	if err == nil {
		m.event, m.errorText = Event{Phase: "completed", Progress: 100, Message: message}, ""
	} else if errors.Is(err, context.Canceled) {
		m.event, m.errorText = Event{Phase: "cancelled", Message: "Prediction operation cancelled; saved forecasts and source data are retained"}, ""
	} else {
		m.errorText = cleanError(err)
		m.event = Event{Phase: "failed", Message: m.errorText}
	}
	close(m.done)
}

func (m *Manager) Start(selection Selection) (Info, error) {
	asOf := time.Now().UTC().UnixMilli()
	if err := validateSelection(selection, true); err != nil {
		return m.Info(), err
	}
	datasets, err := m.Datasets()
	if err != nil {
		return m.Info(), err
	}
	var selected *Dataset
	for i := range datasets {
		if datasets[i].Symbol == selection.Asset && datasets[i].Period == selection.Period {
			if selected != nil {
				return m.Info(), errors.New("multiple CSV ranges exist for this asset and period; use Screener Refresh to consolidate them before predicting")
			}
			selected = &datasets[i]
		}
	}
	if selected == nil {
		return m.Info(), errors.New("download the selected asset and candle period in Screener before predicting")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = m.mutableLocked(); err != nil {
		return m.infoLocked(), err
	}
	if m.runtime == nil || !m.status.Ready {
		return m.infoLocked(), errors.New("prepare the local model and runtime before starting a prediction")
	}
	if selection.ForecastID != "" {
		if _, err := m.Forecast(selection.ForecastID); err != nil {
			return m.infoLocked(), fmt.Errorf("selected forecast is no longer available: %w", err)
		}
	}
	if err = writeJSON(m.settingsPath, savedSettings{Version: 1, Settings: m.settings, Selection: selection}, false); err != nil {
		return m.infoLocked(), fmt.Errorf("save prediction selection: %w", err)
	}
	m.selection = selection
	ctx, cancel := context.WithCancel(context.Background())
	m.active, m.operation, m.cancel, m.done, m.errorText = true, "predict", cancel, make(chan struct{}), ""
	m.event = Event{Phase: "reading", Message: "Reading the latest fully closed candle history"}
	settings, dataset := m.settings, *selected
	m.wg.Add(1)
	go m.predict(ctx, settings, selection, dataset, asOf)
	return m.infoLocked(), nil
}

func (m *Manager) predict(ctx context.Context, settings Settings, selection Selection, dataset Dataset, asOf int64) {
	defer m.wg.Done()
	var operationError error
	defer func() { m.finish(operationError, m.runtime.Inspect(settings), "Forecast saved") }()
	snapshot, err := readSource(ctx, dataset, selection, asOf)
	if err != nil {
		operationError = err
		return
	}
	values := make([]float64, len(snapshot.history))
	for i, observation := range snapshot.history {
		values[i] = observation.Close
	}
	prediction, err := m.runtime.Predict(ctx, settings, values, selection.Horizon, m.emit)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = validatePoints(prediction.Points, selection.Horizon)
	}
	if err == nil {
		err = snapshot.unchanged()
	}
	if err != nil {
		operationError = err
		return
	}
	created := time.Now().UTC().UnixMilli()
	id, err := newForecastID(created)
	if err != nil {
		operationError = err
		return
	}
	forecast := Forecast{ID: id, CreatedAt: created, AsOf: asOf, Model: settings.Model, ModelRevision: prediction.Revision, Symbol: dataset.Symbol, AssetName: dataset.Name, Period: dataset.Period, ContextLength: selection.ContextLength, Horizon: selection.Horizon, SourceFile: dataset.Filename, SourceHash: snapshot.checksum, History: snapshot.history, Points: append([]Point{}, prediction.Points...)}
	if err = validateForecast(forecast); err != nil {
		operationError = err
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = ctx.Err(); err != nil {
		operationError = err
		return
	}
	if err = writeJSON(filepath.Join(m.forecastDirectory, id+".json"), forecast, true); err != nil {
		operationError = fmt.Errorf("save forecast snapshot: %w", err)
		return
	}
	m.forecasts = append(m.forecasts, summary(forecast))
	sort.Slice(m.forecasts, func(i, j int) bool { return m.forecasts[i].ID > m.forecasts[j].ID })
	if len(m.forecasts) > forecastLimit {
		m.forecasts = m.forecasts[:forecastLimit]
	}
	selectedForecast := m.selection
	selectedForecast.ForecastID = id
	if err = writeJSON(m.settingsPath, savedSettings{Version: 1, Settings: m.settings, Selection: selectedForecast}, false); err != nil {
		operationError = fmt.Errorf("forecast saved, but its selection could not be remembered: %w", err)
		return
	}
	m.selection = selectedForecast
}

func (m *Manager) Forecast(id string) (Forecast, error) {
	if !forecastID.MatchString(id) {
		return Forecast{}, errors.New("invalid stored forecast ID")
	}
	var forecast Forecast
	if err := readJSON(filepath.Join(m.forecastDirectory, id+".json"), forecastBytes, &forecast); err != nil {
		return Forecast{}, fmt.Errorf("read stored forecast: %w", err)
	}
	if forecast.ID != id {
		return Forecast{}, errors.New("stored forecast identity does not match its filename")
	}
	if err := validateForecast(forecast); err != nil {
		return Forecast{}, err
	}
	return forecast, nil
}

func (m *Manager) Cancel() Info {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	done := m.done
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	return m.Info()
}

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}
