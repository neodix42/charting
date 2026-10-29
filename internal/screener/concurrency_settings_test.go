package screener

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredDownloadPoolCapsWorkersAndPreservesOrder(t *testing.T) {
	for _, configured := range []int{DefaultConcurrency, MaxConcurrency} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			manager, gates := gatedManager(t)
			if configured != DefaultConcurrency {
				configureTestConcurrency(t, manager, configured)
			}
			request := validRequest()
			request.Concurrency = 1 // A stale Downloader draft is not authoritative.
			request.Symbols = firstSymbols(configured + 2)
			if _, err := manager.Start(request); err != nil {
				t.Fatal(err)
			}
			private := map[string]bool{}
			for _, symbol := range request.Symbols[:configured] {
				child := waitGate(t, gates, symbol)
				if child.SourceBatchSize != 1 || private[filepath.Dir(child.Output)] {
					t.Fatalf("source concurrency or private output changed: %+v", child)
				}
				private[filepath.Dir(child.Output)] = true
			}
			for index, job := range manager.Info().Jobs {
				want := "running"
				if index >= configured {
					want = "queued"
					assertNotStarted(t, gates, job.Symbol)
				}
				if job.Symbol != request.Symbols[index] || job.Status != want {
					t.Fatalf("worker cap or selected order changed: %+v", manager.Info())
				}
			}
			if _, err := manager.ConfigureConcurrency(1); err == nil || manager.Info().Concurrency != configured {
				t.Fatal("an active batch changed its worker snapshot")
			}
			releaseGate(t, gates, request.Symbols[0])
			waitGate(t, gates, request.Symbols[configured])
			assertNotStarted(t, gates, request.Symbols[configured+1])
			for _, symbol := range request.Symbols[1:] {
				releaseGate(t, gates, symbol)
			}
			finished := waitFinished(t, manager)
			for index, job := range finished.Jobs {
				if job.Symbol != request.Symbols[index] || job.Status != "completed" || job.OutputPath == "" {
					t.Fatalf("parallel completion changed job identity/order: %+v", finished)
				}
			}
			assertNoPartials(t, finished.Directory)
		})
	}
}

func TestConcurrencyPreferencesSurviveDirectoryAndIgnoreLegacyViewAndQueue(t *testing.T) {
	m := testManager(t)
	configureTestConcurrency(t, m, 7)
	destination := filepath.Join(t.TempDir(), "chosen history")
	if _, err := m.ConfigureDirectory(destination); err != nil {
		t.Fatal(err)
	}
	view := defaultView()
	view.Concurrency = 1
	if returned, err := m.SaveView(view); err != nil || returned.Concurrency != 7 || m.Info().Concurrency != 7 {
		t.Fatal("UI draft changed the global preference", returned, err)
	}
	if _, err := m.Start(validRequest()); err != nil {
		t.Fatal(err)
	}
	waitFinished(t, m)
	m.Close()
	var saved savedDownloads
	queuePath := filepath.Join(m.profile, "screener-downloads.json")
	if err := readState(queuePath, 16*1024*1024, &saved); err != nil {
		t.Fatal(err)
	}
	saved.Concurrency = 20
	if err := writeState(queuePath, saved); err != nil {
		t.Fatal(err)
	}
	view.Concurrency = 20
	if err := writeState(filepath.Join(m.profile, "screener-view.json"), savedView{1, view}); err != nil {
		t.Fatal(err)
	}
	restored := reopenTestManager(t, m.profile)
	if info := restored.Info(); info.Concurrency != 7 || info.Directory != destination {
		t.Fatalf("legacy batch/view metadata changed the local preferences: %+v", info)
	}
	if returned, err := restored.View(); err != nil || returned.Concurrency != 7 {
		t.Fatal("view did not report the authoritative current preference", returned, err)
	}
	configureTestConcurrency(t, restored, 3)
	if returned, err := restored.View(); err != nil || returned.Concurrency != 3 {
		t.Fatal("existing view stayed stale after settings change", returned, err)
	}
	restored.Close()
	if info := reopenTestManager(t, m.profile).Info(); info.Concurrency != 3 || info.Directory != destination {
		t.Fatalf("changed setting did not persist: %+v", info)
	}
}

func TestLegacyDirectoryOnlyPreferencesDefaultToFiveWorkers(t *testing.T) {
	profile, destination := t.TempDir(), t.TempDir()
	if err := writeState(filepath.Join(profile, "screener.json"), map[string]string{"directory": destination}); err != nil {
		t.Fatal(err)
	}
	m := reopenTestManager(t, profile)
	if info := m.Info(); info.Concurrency != DefaultConcurrency || info.Directory != destination || info.Error != "" {
		t.Fatalf("legacy directory-only settings did not preserve the folder: %+v", info)
	}
}

func TestInvalidConcurrencyPreferencesAndFailedWritesPreserveOriginal(t *testing.T) {
	for _, raw := range []string{"0", "21", "-1", "null", "1.5", `"5"`} {
		t.Run(raw, func(t *testing.T) {
			profile := t.TempDir()
			path := filepath.Join(profile, "screener.json")
			directory, _ := json.Marshal(t.TempDir())
			original := []byte(fmt.Sprintf(`{"directory":%s,"concurrency":%s}`, directory, raw))
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			m := reopenTestManager(t, profile)
			if m.Info().Error == "" || m.Info().Concurrency != DefaultConcurrency {
				t.Fatal("invalid saved setting was accepted", m.Info())
			}
			if _, err := m.ConfigureConcurrency(3); err == nil {
				t.Fatal("overwrote corrupt settings")
			}
			if _, err := m.ConfigureDirectory(t.TempDir()); err == nil {
				t.Fatal("directory save overwrote corrupt settings")
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, original) {
				t.Fatal("invalid settings were not preserved")
			}
		})
	}
	m := testManager(t)
	configureTestConcurrency(t, m, 4)
	path := filepath.Join(m.profile, "screener.json")
	original, _ := os.ReadFile(path)
	for _, invalid := range []int{0, -1, 21} {
		if _, err := m.ConfigureConcurrency(invalid); err == nil || m.Info().Concurrency != 4 {
			t.Fatal("invalid configuration changed memory", invalid, err)
		}
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatal("invalid configuration changed disk")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConfigureConcurrency(9); err == nil || m.Info().Concurrency != 4 {
		t.Fatal("failed write changed memory", err)
	}
}

func TestConcurrencyChangesRejectPendingAndActiveFileOperations(t *testing.T) {
	for _, kind := range []string{"queued", "download", "enrichment", "refresh", "live", "analysis"} {
		t.Run(kind, func(t *testing.T) {
			m := testManager(t)
			m.mu.Lock()
			switch kind {
			case "queued":
				m.jobs = []Job{{ID: strings.Repeat("a", 32), Status: "queued"}}
			case "download":
				m.active = true
			case "enrichment":
				m.enrichmentActive = true
			case "refresh":
				m.refreshActive = true
			case "live":
				m.liveActive = true
			case "analysis":
				m.analysis = &analysisState{active: true}
			}
			m.mu.Unlock()
			if _, err := m.ConfigureConcurrency(9); err == nil || m.Info().Concurrency != DefaultConcurrency {
				t.Fatalf("changed workers while %s was active: %v", kind, err)
			}
			m.mu.Lock()
			m.jobs, m.active, m.enrichmentActive, m.refreshActive, m.liveActive, m.analysis = nil, false, false, false, false, nil
			m.mu.Unlock()
		})
	}
}
