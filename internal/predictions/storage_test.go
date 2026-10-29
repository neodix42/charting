package predictions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestImmutableForecastPublicationCannotReplaceConcurrentWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forecast.json")
	var completed sync.WaitGroup
	var successes atomic.Int64
	for i := range 20 {
		completed.Add(1)
		go func() {
			defer completed.Done()
			if err := writeJSON(path, struct{ Writer int }{i}, true); err == nil {
				successes.Add(1)
			}
		}()
	}
	completed.Wait()
	if successes.Load() != 1 {
		t.Fatalf("immutable forecast had %d successful competing publishers", successes.Load())
	}
	var saved struct{ Writer int }
	if err := readJSON(path, 1024, &saved); err != nil {
		t.Fatal("winning snapshot is not complete JSON", err)
	}
}

func TestForecastHistoryReloadIsBoundedAndOldSnapshotsStayReadable(t *testing.T) {
	m, runtime, _, selection, _ := managerFixture(t, true)
	if _, err := m.Start(selection); err != nil {
		t.Fatal(err)
	}
	info := waitManager(t, m)
	forecast, err := m.Forecast(info.Forecasts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	oldestID := forecast.ID
	for i := 1; i <= 105; i++ {
		copy := forecast
		copy.CreatedAt += int64(i)
		copy.ID = fmt.Sprintf("%015d-%032x", copy.CreatedAt, i)
		if err = writeJSON(filepath.Join(m.forecastDirectory, copy.ID+".json"), copy, true); err != nil {
			t.Fatal(err)
		}
	}
	m.Close()
	restored := New(m.profile, m.source, runtime)
	defer restored.Close()
	if got := restored.Info(); got.Error != "" || len(got.Forecasts) != 100 || got.Forecasts[0].CreatedAt != forecast.CreatedAt+105 {
		t.Fatal("forecast list was not bounded/latest first", got)
	}
	if _, err = restored.Forecast(oldestID); err != nil {
		t.Fatal("history list limit deleted an older immutable snapshot", err)
	}
	if err = writeJSON(filepath.Join(m.forecastDirectory, oldestID+".json"), forecast, true); err == nil {
		t.Fatal("immutable forecast was replaced")
	}
}

func TestSelectionWriteFailureAndInvalidModelOutputNeverSilentlySucceed(t *testing.T) {
	m, runtime, _, selection, _ := managerFixture(t, true)
	if _, err := m.SaveSelection(selection); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.settingsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.settingsPath, 0700); err != nil {
		t.Fatal(err)
	}
	changed := selection
	changed.Horizon++
	if _, err := m.SaveSelection(changed); err == nil || m.Info().Selection != selection {
		t.Fatal("failed write changed selection", err)
	}
	if _, err := m.Start(selection); err == nil || runtime.predictions.Load() != 0 {
		t.Fatal("started model before its selection was saved", err)
	}
	if err := os.Remove(m.settingsPath); err != nil {
		t.Fatal(err)
	}
	runtime.predict = func(_ context.Context, _ Settings, _ []float64, horizon int, _ func(Event)) (RuntimePrediction, error) {
		result := testPrediction(horizon)
		result.Revision = ""
		return result, nil
	}
	if _, err := m.Start(selection); err != nil {
		t.Fatal(err)
	}
	if info := waitManager(t, m); !strings.Contains(info.Error, "provenance") || len(info.Forecasts) != 0 {
		t.Fatal("model without revision produced a saved forecast", info)
	}
}
