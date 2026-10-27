package screener

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCompletedHistoryQueue(t *testing.T, profile, directory, output, status string) Job {
	t.Helper()
	job := Job{ID: strings.Repeat("a", 32), Symbol: "audusd", Timeframe: "d1", From: "2006-01-01", To: "2026-10-25", Status: status, Progress: 100, OutputPath: output, Message: "Downloaded history"}
	if err := writeState(filepath.Join(profile, "screener-downloads.json"), savedDownloads{Version: 1, Directory: directory, Concurrency: 1, Jobs: []Job{job}, Publication: map[string]string{job.ID: filepath.Base(output)}}); err != nil {
		t.Fatal(err)
	}
	return job
}

func reopenHistoryQueue(t *testing.T, profile string) *Manager {
	t.Helper()
	m := newManager(profile, func() (workerRuntime, error) {
		return workerRuntime{}, errors.New("explicit offline queue recovery test")
	})
	t.Cleanup(m.Close)
	return m
}

func TestCompletedHistoryQueueFollowsRecoveredRefreshRenameAndPersists(t *testing.T) {
	fixture := newHistoryFixture(t)
	transaction := publishHistoryFixture(t, fixture, historyUnionName)
	// Crash after staging a complete transaction but before replacement, source
	// removal and updating the previously completed Downloader row.
	rewindHistoryPublication(t, fixture, historyUnionName, transaction, "prepared")
	profile := t.TempDir()
	before := writeCompletedHistoryQueue(t, profile, fixture.directory, fixture.sources[0].Input, "completed")
	m := reopenHistoryQueue(t, profile)
	info := m.Info()
	want := filepath.Join(fixture.directory, historyUnionName)
	if info.Error != "" || len(info.Jobs) != 1 || info.Jobs[0].OutputPath != want || info.Jobs[0].Status != "completed" || info.Jobs[0].ID != before.ID {
		t.Fatalf("completed download did not follow recovered publication: %+v", info)
	}
	assertHistoryPublished(t, fixture, historyUnionName, transaction)
	var saved savedDownloads
	if err := readState(filepath.Join(profile, "screener-downloads.json"), 16*1024*1024, &saved); err != nil || saved.Jobs[0].OutputPath != want || saved.Publication[before.ID] != historyUnionName {
		t.Fatalf("reconciled output path was not persisted: %+v %v", saved, err)
	}
	m.Close()
	restarted := reopenHistoryQueue(t, profile)
	if got := restarted.Info(); got.Error != "" || got.Jobs[0].OutputPath != want {
		t.Fatalf("next restart lost the restored history link: %+v", got)
	}
}

func TestCompletedHistoryQueueReconciliationRequiresUnambiguousMissingSource(t *testing.T) {
	for _, scenario := range []string{"one candidate", "existing source", "ambiguous candidates", "different timeframe", "different asset", "candidate symlink", "old symlink", "failed job", "custom filename"} {
		t.Run(scenario, func(t *testing.T) {
			profile := t.TempDir()
			directory := filepath.Join(profile, "screener")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(directory, historyFirstName)
			name := historyUnionName
			status := "completed"
			switch scenario {
			case "different timeframe":
				name = strings.Replace(name, "-d1-", "-h1-", 1)
			case "different asset":
				name = strings.Replace(name, "audusd-", "eurusd-", 1)
			case "custom filename":
				old = filepath.Join(directory, "audusd-custom.csv")
			case "failed job":
				status = "failed"
			}
			candidate := filepath.Join(directory, name)
			if scenario == "candidate symlink" {
				foreign := filepath.Join(t.TempDir(), "external.csv")
				if err := os.WriteFile(foreign, []byte(historyUnionCSV), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(foreign, candidate); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(candidate, []byte(historyUnionCSV), 0600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "existing source":
				if err := os.WriteFile(old, []byte(historyUnionCSV), 0600); err != nil {
					t.Fatal(err)
				}
			case "old symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing.csv"), old); err != nil {
					t.Fatal(err)
				}
			case "ambiguous candidates":
				if err := os.WriteFile(filepath.Join(directory, historySecondName), []byte(historyUnionCSV), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := writeCompletedHistoryQueue(t, profile, directory, old, status)
			m := reopenHistoryQueue(t, profile)
			want := old
			if scenario == "one candidate" {
				want = candidate
			}
			info := m.Info()
			if info.Error != "" || len(info.Jobs) != 1 || info.Jobs[0].OutputPath != want || info.Jobs[0].Status != before.Status {
				t.Fatalf("unsafe completed-history reconciliation: %+v; want %s", info, want)
			}
		})
	}
}

func TestRetryAndRestartRecoverPublishedDownloadBeforeRepeatingMissingPlan(t *testing.T) {
	for _, mode := range []string{"retry", "restart", "cancelled retry"} {
		for _, boundary := range []string{"prepared", "csv published"} {
			t.Run(mode+"/"+boundary, func(t *testing.T) {
				fixture := newHistoryFixture(t)
				profile := t.TempDir()
				m := newManager(profile, func() (workerRuntime, error) { return workerRuntime{node: "must-not-start-a-worker"}, nil })
				t.Cleanup(m.Close)
				status := "failed"
				if mode == "cancelled retry" {
					status = "cancelled"
				}
				job := Job{ID: strings.Repeat("b", 32), Symbol: "audusd", Timeframe: "d1", From: "2006-01-01", To: "2026-10-25", Status: status, Message: "Publication interrupted"}
				m.directory, m.batchDirectory = fixture.directory, fixture.directory
				m.jobs, m.publication = []Job{job}, map[string]string{job.ID: historyUnionName}
				work, err := m.prepareJob(job)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Link(fixture.staging, filepath.Join(work, "data.part")); err != nil {
					t.Fatal(err)
				}
				if !m.saveDownloadsLocked() {
					t.Fatal(m.stateError)
				}
				transaction := publishHistoryFixture(t, fixture, historyUnionName)
				rewindHistoryPublication(t, fixture, historyUnionName, transaction, boundary)
				var info Info
				if mode == "restart" {
					m.Close()
					m = reopenHistoryQueue(t, profile)
					info = m.Info()
				} else {
					info, err = m.Resume()
					if err != nil {
						t.Fatal(err)
					}
				}
				if info.Active || len(info.Jobs) != 1 {
					t.Fatalf("recovery started another download: %+v", info)
				}
				wantStatus, wantPath := "completed", filepath.Join(fixture.directory, historyUnionName)
				if mode == "cancelled retry" {
					wantStatus, wantPath = "cancelled", ""
				}
				if info.Jobs[0].Status != wantStatus || info.Jobs[0].OutputPath != wantPath {
					t.Fatalf("recovery did not retain the correct terminal result: %+v", info)
				}
				assertHistoryPublished(t, fixture, historyUnionName, transaction)
				var saved savedDownloads
				if err := readState(filepath.Join(profile, "screener-downloads.json"), 16*1024*1024, &saved); err != nil || saved.Jobs[0].Status != wantStatus || saved.Jobs[0].OutputPath != wantPath {
					t.Fatalf("recovered terminal state not persisted: %+v %v", saved, err)
				}
				if wantStatus == "completed" {
					if _, err := os.Stat(work); !os.IsNotExist(err) {
						t.Fatalf("completed recovery retained worker scratch: %v", err)
					}
				}
			})
		}
	}
}
