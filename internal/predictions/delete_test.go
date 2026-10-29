package predictions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func saveTestForecast(t *testing.T, m *Manager, selection Selection) string {
	t.Helper()
	if _, err := m.Start(selection); err != nil {
		t.Fatal(err)
	}
	info := waitManager(t, m)
	if info.Error != "" || len(info.Forecasts) == 0 {
		t.Fatal("fixture forecast was not saved", info)
	}
	return info.Selection.ForecastID
}

func assertPredictionRestart(t *testing.T, m *Manager) {
	t.Helper()
	restored := New(m.profile, m.source, m.runtime)
	defer restored.Close()
	got, want := restored.Info(), m.Info()
	if got.Error != "" || got.Selection != want.Selection || !reflect.DeepEqual(got.Forecasts, want.Forecasts) {
		t.Fatalf("forecast removal did not survive restart: got=%+v want=%+v", got, want)
	}
}

func TestDeleteForecastPreservesOtherSnapshotsSourceAndModelsAndRestoresSelection(t *testing.T) {
	m, model, dataset, selection, source := managerFixture(t, true)
	for range 3 {
		saveTestForecast(t, m, selection)
	}
	initial := m.Info()
	latest, middle, oldest := initial.Forecasts[0].ID, initial.Forecasts[1].ID, initial.Forecasts[2].ID
	modelPath := filepath.Join(initial.Settings.ModelDirectory, "verified-weights.bin")
	if err := os.MkdirAll(filepath.Dir(modelPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("existing verified model weights"), 0600); err != nil {
		t.Fatal(err)
	}
	originals := map[string]string{}
	for _, item := range initial.Forecasts {
		data, err := os.ReadFile(filepath.Join(m.forecastDirectory, item.ID+".json"))
		if err != nil {
			t.Fatal(err)
		}
		originals[item.ID] = string(data)
	}
	settingsBefore, _ := os.ReadFile(m.settingsPath)
	for index, id := range []string{oldest, latest, middle} {
		info, err := m.DeleteForecast(id)
		if err != nil || len(info.Forecasts) != 2-index {
			t.Fatalf("delete %s: %+v %v", id, info, err)
		}
		want := middle
		if index == 0 {
			want = latest
			if data, _ := os.ReadFile(m.settingsPath); string(data) != string(settingsBefore) {
				t.Fatal("removing an unselected forecast rewrote selection settings")
			}
		} else if index == 2 {
			want = ""
		}
		if info.Selection.ForecastID != want || info.Selection.Asset != selection.Asset || info.Selection.Period != selection.Period || info.Selection.ContextLength != selection.ContextLength || info.Selection.Horizon != selection.Horizon {
			t.Fatalf("removal changed independent inputs or retained a removed selection: %+v", info.Selection)
		}
		if _, err := m.Forecast(id); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("removed forecast remains readable", err)
		}
		for _, remaining := range info.Forecasts {
			data, err := os.ReadFile(filepath.Join(m.forecastDirectory, remaining.ID+".json"))
			if err != nil || string(data) != originals[remaining.ID] {
				t.Fatalf("removing %s changed another snapshot %s: %v", id, remaining.ID, err)
			}
		}
		assertPredictionRestart(t, m)
	}
	if data, _ := os.ReadFile(filepath.Join(dataset.Directory, dataset.Filename)); string(data) != source {
		t.Fatal("removing forecasts changed the source CSV")
	}
	if data, _ := os.ReadFile(modelPath); string(data) != "existing verified model weights" {
		t.Fatal("removing forecasts changed model files")
	}
	if model.predictions.Load() != 3 || model.preparations.Load() != 0 {
		t.Fatal("removal performed model inference or preparation")
	}
}

func TestDeleteForecastChoosesSuccessorForImplicitDefaultAndRejectsStaleSelectionWrites(t *testing.T) {
	m, _, _, selection, _ := managerFixture(t, true)
	saveTestForecast(t, m, selection)
	saveTestForecast(t, m, selection)
	before := m.Info()
	selection.ForecastID = ""
	if _, err := m.SaveSelection(selection); err != nil {
		t.Fatal(err)
	}
	info, err := m.DeleteForecast(before.Forecasts[0].ID)
	if err != nil || info.Selection.ForecastID != before.Forecasts[1].ID {
		t.Fatal("removing the implicitly displayed forecast did not select its successor", info, err)
	}
	stale := before.Selection
	if _, err = m.SaveSelection(stale); err == nil || !strings.Contains(err.Error(), "no longer available") || m.Info().Selection != info.Selection {
		t.Fatal("stale selection restored a deleted reference", err)
	}
	if _, err = m.Start(stale); err == nil || m.Info().Selection != info.Selection {
		t.Fatal("stale inference request restored a deleted reference", err)
	}
	assertPredictionRestart(t, m)
}

func TestDeleteForecastBackfillsBoundedInventoryAndCanRemoveSelectedOlderSnapshot(t *testing.T) {
	m, _, _, selection, _ := managerFixture(t, true)
	oldest := saveTestForecast(t, m, selection)
	forecast, err := m.Forecast(oldest)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= forecastLimit+1; index++ {
		copy := forecast
		copy.CreatedAt += int64(index)
		copy.ID = fmt.Sprintf("%015d-%032x", copy.CreatedAt, index)
		if err := writeJSON(filepath.Join(m.forecastDirectory, copy.ID+".json"), copy, true); err != nil {
			t.Fatal(err)
		}
	}
	m.Close()
	restored := New(m.profile, m.source, m.runtime)
	defer restored.Close()
	if restored.Info().Selection.ForecastID != oldest {
		t.Fatal("valid selected forecast older than the displayed inventory was cleared during restore")
	}
	selection.ForecastID = oldest
	if _, err := restored.SaveSelection(selection); err != nil {
		t.Fatal("older snapshot outside the displayed inventory could not be selected", err)
	}
	before := restored.Info()
	info, err := restored.DeleteForecast(before.Forecasts[0].ID)
	if err != nil || len(info.Forecasts) != forecastLimit || info.Selection.ForecastID != oldest || info.Forecasts[forecastLimit-1].CreatedAt != forecast.CreatedAt+1 {
		t.Fatal("deletion did not backfill the newest forecast inventory", info, err)
	}
	info, err = restored.DeleteForecast(oldest)
	if err != nil || info.Selection.ForecastID != info.Forecasts[0].ID || len(info.Forecasts) != forecastLimit {
		t.Fatal("selected older snapshot could not be removed consistently", info, err)
	}
	assertPredictionRestart(t, restored)
}

func TestDeleteForecastRejectsInvalidMissingAndNonregularPathsWithoutChangingData(t *testing.T) {
	for _, kind := range []string{"invalid-id", "missing", "symlink-file", "directory-file", "symlink-folder", "symlink-parent"} {
		t.Run(kind, func(t *testing.T) {
			m, _, _, selection, _ := managerFixture(t, true)
			id := saveTestForecast(t, m, selection)
			before := m.Info()
			path := filepath.Join(m.forecastDirectory, id+".json")
			original, _ := os.ReadFile(path)
			settings, _ := os.ReadFile(m.settingsPath)
			target := path
			switch kind {
			case "invalid-id":
				id = "../predictions.json"
			case "missing":
				id = "000000000000000-00000000000000000000000000000000"
			case "symlink-file", "directory-file":
				target = path + ".kept"
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink-file" {
					if err := os.Symlink(target, path); err != nil {
						t.Skip("symlink creation unavailable", err)
					}
				} else if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink-folder", "symlink-parent":
				folder := m.forecastDirectory
				if kind == "symlink-parent" {
					folder = filepath.Dir(folder)
				}
				moved := folder + ".kept"
				if err := os.Rename(folder, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, folder); err != nil {
					t.Skip("symlink creation unavailable", err)
				}
			}
			if _, err := m.DeleteForecast(id); err == nil || !reflect.DeepEqual(m.Info(), before) {
				t.Fatal("invalid removal succeeded or changed manager state", err)
			}
			if data, _ := os.ReadFile(target); string(data) != string(original) {
				t.Fatal("invalid removal changed forecast bytes")
			}
			if data, _ := os.ReadFile(m.settingsPath); string(data) != string(settings) {
				t.Fatal("invalid removal changed selection settings")
			}
		})
	}
}

func TestDeleteForecastSettingsFailurePreservesSnapshotAndSelection(t *testing.T) {
	m, _, _, selection, _ := managerFixture(t, true)
	id := saveTestForecast(t, m, selection)
	before := m.Info()
	settings, _ := os.ReadFile(m.settingsPath)
	if err := os.Remove(m.settingsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.settingsPath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteForecast(id); err == nil || !strings.Contains(err.Error(), "save selection") || !reflect.DeepEqual(m.Info(), before) {
		t.Fatal("failed selection persistence removed a snapshot or changed state", err)
	}
	if _, err := m.Forecast(id); err != nil {
		t.Fatal("settings failure deleted the original forecast", err)
	}
	if err := os.Remove(m.settingsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.settingsPath, settings, 0600); err != nil {
		t.Fatal(err)
	}
	assertPredictionRestart(t, m)
}

func TestDeleteForecastUnlinkFailureRollsBackPersistedSelection(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("requires Unix directory permissions enforced for an unprivileged user")
	}
	m, _, _, selection, _ := managerFixture(t, true)
	id := saveTestForecast(t, m, selection)
	before := m.Info()
	settings, _ := os.ReadFile(m.settingsPath)
	if err := os.Chmod(m.forecastDirectory, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(m.forecastDirectory, 0700) })
	if _, err := m.DeleteForecast(id); err == nil || !strings.Contains(err.Error(), "remove forecast") || !reflect.DeepEqual(m.Info(), before) {
		t.Fatal("failed unlink changed manager state", err)
	}
	if _, err := m.Forecast(id); err != nil {
		t.Fatal("failed unlink lost the saved forecast", err)
	}
	if data, _ := os.ReadFile(m.settingsPath); string(data) != string(settings) {
		t.Fatal("failed unlink did not roll back the persisted selection")
	}
	assertPredictionRestart(t, m)
}

func TestDeleteForecastRejectsActiveModelOperationsAndClosedManager(t *testing.T) {
	for _, operation := range []string{"prepare", "predict"} {
		t.Run(operation, func(t *testing.T) {
			m, model, _, selection, _ := managerFixture(t, true)
			id := saveTestForecast(t, m, selection)
			started := make(chan struct{})
			if operation == "prepare" {
				model.prepare = func(ctx context.Context, _ Settings, _ func(Event)) error {
					close(started)
					<-ctx.Done()
					return ctx.Err()
				}
				m.mu.Lock()
				m.status.Ready = false
				m.mu.Unlock()
				if _, err := m.Prepare(); err != nil {
					t.Fatal(err)
				}
			} else {
				model.predict = func(ctx context.Context, _ Settings, _ []float64, _ int, _ func(Event)) (RuntimePrediction, error) {
					close(started)
					<-ctx.Done()
					return RuntimePrediction{}, ctx.Err()
				}
				if _, err := m.Start(selection); err != nil {
					t.Fatal(err)
				}
			}
			<-started
			before := m.Info()
			if _, err := m.DeleteForecast(id); err == nil || !strings.Contains(err.Error(), "finish or cancel") || !reflect.DeepEqual(m.Info(), before) {
				t.Fatal("active operation allowed snapshot removal", err)
			}
			m.Close()
			if _, err := m.DeleteForecast(id); err == nil || !strings.Contains(err.Error(), "closed") {
				t.Fatal("closed manager allowed snapshot removal", err)
			}
			if _, err := m.Forecast(id); err != nil {
				t.Fatal("rejected removal lost the original snapshot", err)
			}
		})
	}
}

func TestDeleteForecastSerializesConcurrentSelectionSave(t *testing.T) {
	m, _, _, selection, _ := managerFixture(t, true)
	id := saveTestForecast(t, m, selection)
	stale := m.Info().Selection
	start := make(chan struct{})
	var done sync.WaitGroup
	done.Add(2)
	go func() {
		defer done.Done()
		<-start
		_, _ = m.SaveSelection(stale)
	}()
	go func() {
		defer done.Done()
		<-start
		if _, err := m.DeleteForecast(id); err != nil {
			t.Error(err)
		}
	}()
	close(start)
	done.Wait()
	if got := m.Info(); got.Selection.ForecastID != "" || len(got.Forecasts) != 0 {
		t.Fatal("concurrent selection save retained the deleted forecast", got)
	}
	assertPredictionRestart(t, m)
}

func TestRestoreUnavailableSelectedForecastAllowsNewSelectionsAndPredictionsWithoutRewritingOriginalState(t *testing.T) {
	for _, reason := range []string{"missing", "corrupt"} {
		t.Run(reason, func(t *testing.T) {
			m, _, _, selection, _ := managerFixture(t, true)
			id := saveTestForecast(t, m, selection)
			settings, err := os.ReadFile(m.settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			m.Close()
			path := filepath.Join(m.forecastDirectory, id+".json")
			const malformed = `{"id":"preserve this malformed forecast"}`
			if reason == "missing" {
				err = os.Remove(path)
			} else {
				err = os.WriteFile(path, []byte(malformed), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			restored := New(m.profile, m.source, m.runtime)
			defer restored.Close()
			if info := restored.Info(); info.Selection.ForecastID != "" || info.Selection.Asset != selection.Asset || info.Selection.Horizon != selection.Horizon {
				t.Fatal("restore retained an unavailable reference or changed independent inputs", info)
			}
			if data, _ := os.ReadFile(m.settingsPath); string(data) != string(settings) {
				t.Fatal("restore rewrote the original settings")
			}
			changed := restored.Info().Selection
			changed.Horizon++
			if _, err := restored.SaveSelection(changed); err != nil {
				t.Fatal("unavailable restored forecast blocked selection edits", err)
			}
			if _, err := restored.Start(changed); err != nil {
				t.Fatal("unavailable restored forecast blocked a new prediction", err)
			}
			info := waitManager(t, restored)
			if len(info.Forecasts) != 1 || info.Selection.ForecastID == "" || info.Selection.ForecastID == id {
				t.Fatal("a new forecast was not saved after recovery", info)
			}
			if _, err := restored.Forecast(info.Selection.ForecastID); err != nil {
				t.Fatal("recovered prediction produced an unreadable snapshot", err)
			}
			if reason == "corrupt" {
				if data, _ := os.ReadFile(path); string(data) != malformed {
					t.Fatal("recovery changed the malformed original forecast")
				}
			} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("recovery recreated the externally removed snapshot", err)
			}
		})
	}
}
