package predictions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeModelRuntime struct {
	mu           sync.Mutex
	status       RuntimeStatus
	inspections  atomic.Int64
	preparations atomic.Int64
	predictions  atomic.Int64
	prepare      func(context.Context, Settings, func(Event)) error
	predict      func(context.Context, Settings, []float64, int, func(Event)) (RuntimePrediction, error)
}

func (runtime *fakeModelRuntime) Inspect(Settings) RuntimeStatus {
	runtime.inspections.Add(1)
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.status
}
func (runtime *fakeModelRuntime) Prepare(ctx context.Context, settings Settings, emit func(Event)) error {
	runtime.preparations.Add(1)
	if runtime.prepare != nil {
		return runtime.prepare(ctx, settings, emit)
	}
	emit(Event{Phase: "downloading", Progress: 50, Message: "Local test preparation"})
	runtime.mu.Lock()
	runtime.status = RuntimeStatus{Ready: true, ModelReady: true, RuntimeReady: true}
	runtime.mu.Unlock()
	return nil
}
func (runtime *fakeModelRuntime) Predict(ctx context.Context, settings Settings, values []float64, horizon int, emit func(Event)) (RuntimePrediction, error) {
	runtime.predictions.Add(1)
	if runtime.predict != nil {
		return runtime.predict(ctx, settings, values, horizon, emit)
	}
	return testPrediction(horizon), nil
}

func testPrediction(horizon int) RuntimePrediction {
	result := RuntimePrediction{Revision: strings.Repeat("a", 40), Points: []Point{}}
	for i := range horizon {
		result.Points = append(result.Points, Point{Step: i + 1, Lower: float64(i), Median: float64(i + 1), Upper: float64(i + 2)})
	}
	return result
}

func candleCSV(start int64, count int) string {
	var text strings.Builder
	text.WriteString("timestamp,open,high,low,close,volume,sma_200\n")
	for i := range count {
		value := i + 10
		fmt.Fprintf(&text, "%d,%d,%d,%d,%d,1,\n", start+int64(i)*3_600_000, value, value+1, value-1, value)
	}
	return text.String()
}

func managerFixture(t *testing.T, ready bool) (*Manager, *fakeModelRuntime, Dataset, Selection, string) {
	t.Helper()
	directory := t.TempDir()
	dataset := Dataset{ID: "authoritative-downloaded-file", Symbol: "eurusd", Name: "Euro vs US Dollar", Period: "h1", Filename: "eurusd-h1-2024-01-01-2026-10-30.csv", Directory: directory}
	start := time.Now().UTC().Truncate(time.Hour).Add(-600 * time.Hour).UnixMilli()
	text := candleCSV(start, 601)
	if err := os.WriteFile(filepath.Join(directory, dataset.Filename), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeModelRuntime{status: RuntimeStatus{Ready: ready, ModelReady: ready, RuntimeReady: ready}}
	m := New(t.TempDir(), func() ([]Dataset, error) { return []Dataset{dataset}, nil }, runtime)
	t.Cleanup(m.Close)
	return m, runtime, dataset, Selection{Asset: dataset.Symbol, Period: dataset.Period, ContextLength: 512, Horizon: 20}, text
}

func waitManager(t *testing.T, m *Manager) Info {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info := m.Info()
		if !info.Active {
			return info
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("prediction operation did not finish")
	return Info{}
}

func TestManagerForecastSnapshotsOnlyClosedContextAndPersistsImmutableProvenance(t *testing.T) {
	m, runtime, dataset, selection, original := managerFixture(t, true)
	var actual []float64
	runtime.predict = func(ctx context.Context, settings Settings, values []float64, horizon int, emit func(Event)) (RuntimePrediction, error) {
		actual = append([]float64{}, values...)
		emit(Event{Phase: "predicting", Progress: 75})
		return testPrediction(horizon), nil
	}
	if _, err := m.Start(selection); err != nil {
		t.Fatal(err)
	}
	info := waitManager(t, m)
	if info.Error != "" || len(info.Forecasts) != 1 || info.Event.Phase != "completed" || len(actual) != 512 || actual[0] != 98 || actual[511] != 609 {
		t.Fatalf("wrong context or result: %+v values=%v..%v", info, actual[:1], actual[len(actual)-1:])
	}
	forecast, err := m.Forecast(info.Forecasts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256([]byte(original))
	if forecast.SourceHash != hex.EncodeToString(checksum[:]) || forecast.SourceFile != dataset.Filename || forecast.ModelRevision != strings.Repeat("a", 40) || forecast.Model != DefaultModel || forecast.History[511].Close != 609 || forecast.Points[19].Step != 20 || forecast.AsOf > forecast.CreatedAt {
		t.Fatalf("immutable provenance is incomplete: %+v", forecast)
	}
	if data, _ := os.ReadFile(filepath.Join(dataset.Directory, dataset.Filename)); string(data) != original {
		t.Fatal("prediction modified the Screener source")
	}
	forecast.History[0].Close = 999999
	info.Forecasts[0].Symbol = "mutated"
	again, err := m.Forecast(forecast.ID)
	if err != nil || again.History[0].Close == 999999 || m.Info().Forecasts[0].Symbol != dataset.Symbol {
		t.Fatal("caller mutated saved prediction state", err)
	}
	m.Close()
	selection.ForecastID = forecast.ID
	restored := New(m.profile, m.source, runtime)
	defer restored.Close()
	if got := restored.Info(); got.Error != "" || !reflect.DeepEqual(got.Forecasts, m.Info().Forecasts) || got.Selection != selection || got.Settings != m.Info().Settings {
		t.Fatalf("restart lost forecast history or selection: %+v", got)
	}
	if _, err = restored.Forecast("../predictions.json"); err == nil {
		t.Fatal("forecast lookup accepted path traversal")
	}
}

func TestPreparationIsExplicitIdempotentAndInfoDoesNotInspectOrDownload(t *testing.T) {
	m, runtime, _, selection, _ := managerFixture(t, false)
	inspections := runtime.inspections.Load()
	for range 100 {
		m.Info()
	}
	if runtime.inspections.Load() != inspections || runtime.preparations.Load() != 0 {
		t.Fatal("Info inspected or prepared a runtime")
	}
	if _, err := m.Start(selection); err == nil || runtime.predictions.Load() != 0 {
		t.Fatal("prediction started without a ready local model")
	}
	started, release := make(chan struct{}), make(chan struct{})
	runtime.prepare = func(ctx context.Context, settings Settings, emit func(Event)) error {
		close(started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
		}
		runtime.mu.Lock()
		runtime.status = RuntimeStatus{Ready: true, RuntimeReady: true, ModelReady: true}
		runtime.mu.Unlock()
		return nil
	}
	if _, err := m.Prepare(); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := m.Prepare(); err != nil || runtime.preparations.Load() != 1 {
		t.Fatal("repeated Prepare started another operation", err)
	}
	if _, err := m.Configure(m.Info().Settings); err == nil {
		t.Fatal("settings changed during preparation")
	}
	close(release)
	if info := waitManager(t, m); info.Error != "" || !info.Runtime.Ready {
		t.Fatal(info)
	}
	if _, err := m.Prepare(); err != nil || runtime.preparations.Load() != 1 {
		t.Fatal("ready model downloaded again", err)
	}
}

func TestChangedModelSettingsClearOldCancellationWithoutStartingPreparation(t *testing.T) {
	m, runtime, _, _, _ := managerFixture(t, false)
	started := make(chan struct{})
	runtime.prepare = func(ctx context.Context, settings Settings, emit func(Event)) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	if _, err := m.Prepare(); err != nil {
		t.Fatal(err)
	}
	<-started
	if info := m.Cancel(); info.Event.Phase != "cancelled" {
		t.Fatal("preparation cancellation was not retained", info)
	}
	settings := m.Info().Settings
	if info, err := m.Configure(settings); err != nil || info.Event.Phase != "cancelled" {
		t.Fatal("unchanged settings cleared an explicit cancellation", info, err)
	}
	settings.ModelDirectory = filepath.Join(t.TempDir(), "different model folder")
	if info, err := m.Configure(settings); err != nil || info.Event.Phase != "idle" || info.Runtime.Ready || runtime.preparations.Load() != 1 {
		t.Fatal("new configuration inherited cancellation or started preparation itself", info, err)
	}
}

func TestForecastCancellationCloseAndChangedSourceNeverPublish(t *testing.T) {
	for _, action := range []string{"cancel", "close", "source-change", "invalid-quantiles"} {
		t.Run(action, func(t *testing.T) {
			m, runtime, dataset, selection, original := managerFixture(t, true)
			started := make(chan struct{})
			runtime.predict = func(ctx context.Context, settings Settings, values []float64, horizon int, emit func(Event)) (RuntimePrediction, error) {
				close(started)
				if action == "source-change" {
					if err := os.WriteFile(filepath.Join(dataset.Directory, dataset.Filename), []byte(original+"\n"), 0600); err != nil {
						return RuntimePrediction{}, err
					}
				} else if action == "invalid-quantiles" {
					prediction := testPrediction(horizon)
					prediction.Points[0].Lower = prediction.Points[0].Upper + 1
					return prediction, nil
				} else {
					<-ctx.Done()
					return RuntimePrediction{}, ctx.Err()
				}
				return testPrediction(horizon), nil
			}
			if _, err := m.Start(selection); err != nil {
				t.Fatal(err)
			}
			<-started
			if action == "close" {
				m.Close()
			} else if action == "cancel" {
				if _, err := m.SaveSelection(selection); err != nil {
					t.Fatal("active forecast blocked a harmless selection save", err)
				}
				m.Cancel()
			}
			info := waitManager(t, m)
			if len(info.Forecasts) != 0 || (action == "source-change" || action == "invalid-quantiles") && info.Error == "" {
				t.Fatal("cancelled/invalid forecast was published", info)
			}
			entries, err := os.ReadDir(m.forecastDirectory)
			if !errors.Is(err, os.ErrNotExist) && len(entries) > 0 {
				t.Fatal("failed forecast left a published file", entries, err)
			}
		})
	}
}

func TestSelectionCanPersistDuringPreparationAndPredictionWithoutChangingTheRun(t *testing.T) {
	for _, operation := range []string{"prepare", "predict"} {
		t.Run(operation, func(t *testing.T) {
			m, runtime, _, selection, _ := managerFixture(t, operation == "predict")
			started, release := make(chan struct{}), make(chan struct{})
			if operation == "prepare" {
				runtime.prepare = func(ctx context.Context, settings Settings, emit func(Event)) error {
					close(started)
					<-release
					runtime.mu.Lock()
					runtime.status = RuntimeStatus{Ready: true, ModelReady: true, RuntimeReady: true}
					runtime.mu.Unlock()
					return nil
				}
				if _, err := m.Prepare(); err != nil {
					t.Fatal(err)
				}
			} else {
				runtime.predict = func(ctx context.Context, settings Settings, values []float64, horizon int, emit func(Event)) (RuntimePrediction, error) {
					close(started)
					<-release
					if len(values) != 512 || horizon != 20 {
						return RuntimePrediction{}, errors.New("mutable selection changed the active inference request")
					}
					return testPrediction(horizon), nil
				}
				if _, err := m.Start(selection); err != nil {
					t.Fatal(err)
				}
			}
			<-started
			changed := Selection{Asset: "ethusd", Period: "d1", ContextLength: 1024, Horizon: 77}
			if _, err := m.SaveSelection(changed); err != nil {
				t.Fatal("selection could not persist during background work", err)
			}
			if _, err := m.Configure(m.Info().Settings); err == nil {
				t.Fatal("active model settings changed")
			}
			loaded := New(m.profile, m.source, runtime)
			if loaded.Info().Selection != changed {
				t.Fatal("concurrent selection write was not durable")
			}
			loaded.Close()
			close(release)
			info := waitManager(t, m)
			if info.Error != "" || info.Selection.Asset != changed.Asset || info.Selection.Horizon != changed.Horizon || info.Selection.ContextLength != changed.ContextLength {
				t.Fatal("completed operation replaced independently edited inputs", info)
			}
			if operation == "predict" && (len(info.Forecasts) != 1 || info.Forecasts[0].Symbol != selection.Asset || info.Forecasts[0].Horizon != selection.Horizon || info.Selection.ForecastID != info.Forecasts[0].ID) {
				t.Fatal("active forecast changed with the later selection", info)
			}
		})
	}
}

func TestPredictionSettingsPersistAndCorruptStateIsPreserved(t *testing.T) {
	m, runtime, _, selection, _ := managerFixture(t, true)
	settings := Settings{Model: DefaultModel, ModelDirectory: filepath.Join(t.TempDir(), "weights")}
	if _, err := m.Configure(settings); err != nil {
		t.Fatal(err)
	}
	selection.ContextLength, selection.Horizon = 1024, 77
	if _, err := m.SaveSelection(selection); err != nil {
		t.Fatal(err)
	}
	restored := New(m.profile, m.source, runtime)
	defer restored.Close()
	if info := restored.Info(); info.Settings != settings || info.Selection != selection {
		t.Fatal("settings did not survive restart", info)
	}
	for _, bad := range []Settings{{Model: "not-an-approved-model", ModelDirectory: settings.ModelDirectory}, {Model: DefaultModel, ModelDirectory: "relative"}} {
		if _, err := m.Configure(bad); err == nil || m.Info().Settings != settings {
			t.Fatal("invalid settings changed the profile", err)
		}
	}
	original := []byte(`{"version":999,"unknown":"preserve me"}`)
	if err := os.WriteFile(m.settingsPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	broken := New(m.profile, m.source, runtime)
	defer broken.Close()
	if broken.Info().Error == "" {
		t.Fatal("invalid settings accepted")
	}
	if _, err := broken.Configure(settings); err == nil {
		t.Fatal("invalid state overwritten by Configure")
	}
	if _, err := broken.SaveSelection(selection); err == nil {
		t.Fatal("invalid state overwritten by SaveSelection")
	}
	if _, err := broken.Prepare(); err == nil {
		t.Fatal("invalid state started preparation")
	}
	if got, _ := os.ReadFile(m.settingsPath); string(got) != string(original) {
		t.Fatal("invalid saved state was not preserved")
	}
}

func TestDatasetsRejectAmbiguousRangesAndUnavailableSelections(t *testing.T) {
	m, runtime, dataset, selection, _ := managerFixture(t, true)
	duplicate := dataset
	duplicate.ID, duplicate.Filename = "different-downloaded-file", "another-range.csv"
	m.source = func() ([]Dataset, error) { return []Dataset{dataset, duplicate}, nil }
	if _, err := m.Start(selection); err == nil || !strings.Contains(err.Error(), "consolidate") || runtime.predictions.Load() != 0 {
		t.Fatal("ambiguous history silently chosen", err)
	}
	tick := dataset
	tick.Period = "tick"
	m.source = func() ([]Dataset, error) { return []Dataset{tick}, nil }
	if list, err := m.Datasets(); err != nil || len(list) != 0 {
		t.Fatal("tick data exposed to candle prediction", list, err)
	}
	if _, err := m.Start(selection); err == nil {
		t.Fatal("unavailable downloaded selection accepted")
	}
}
