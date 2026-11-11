package screener

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAlertsPauseDownloadWorkersPreserveCheckpointsAndResumeOnlyUnfinished(t *testing.T) {
	m, gates := gatedManager(t)
	configureTestConcurrency(t, m, 1)
	r := validRequest()
	r.Symbols, r.Concurrency = []string{"btcusd", "ethusd", "ltcusd"}, 1
	if _, err := m.Start(r); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	releaseGate(t, gates, "btcusd")
	waitGate(t, gates, "ethusd")
	before := m.Info()
	completed, err := os.ReadFile(before.Jobs[0].OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := filepath.Join(m.jobDirectory(before.Jobs[1]), "checkpoint.json")
	partial := filepath.Join(m.jobDirectory(before.Jobs[1]), "data.part")
	for _, path := range []string{checkpoint, partial} {
		if err := os.WriteFile(path, []byte("retained worker checkpoint"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, release, err := m.reserveAlertPriority(context.Background(), time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	paused := m.Info()
	if ctx.Err() != nil || !paused.Active || !paused.DownloadsPaused || paused.Jobs[0].Status != "completed" || paused.Jobs[1].Status != "queued" || paused.Jobs[2].Status != "queued" {
		t.Fatal(paused, ctx.Err())
	}
	assertNotStarted(t, gates, "ltcusd")
	for _, path := range []string{checkpoint, partial} {
		if raw, err := os.ReadFile(path); err != nil || string(raw) != "retained worker checkpoint" {
			t.Fatal("pausing removed the download checkpoint", path, err)
		}
	}
	if _, err := m.Start(r); err == nil {
		t.Fatal("a new download batch took the alert reservation")
	}
	if _, err := m.Resume(); err == nil {
		t.Fatal("manual resume took the alert reservation")
	}
	if err := os.Remove(filepath.Join(gates, "ethusd.started")); err != nil {
		t.Fatal(err)
	}
	release()
	// Release is idempotent and cannot start another batch.
	release()
	waitGate(t, gates, "ethusd")
	if got := m.Info(); got.DownloadsPaused || got.Jobs[0].ID != before.Jobs[0].ID || got.Jobs[0].Status != "completed" {
		t.Fatal(got)
	}
	releaseGate(t, gates, "ethusd")
	waitGate(t, gates, "ltcusd")
	releaseGate(t, gates, "ltcusd")
	result := waitFinished(t, m)
	for i, job := range result.Jobs {
		if job.Status != "completed" || job.ID != before.Jobs[i].ID {
			t.Fatal("paused queue did not resume its original jobs", result)
		}
	}
	if after, err := os.ReadFile(before.Jobs[0].OutputPath); err != nil || !bytes.Equal(completed, after) {
		t.Fatal("completed download was rewritten", err)
	}
	assertNoPartials(t, result.Directory)
}

func TestCancelDuringAlertPauseDoesNotRestartDownloads(t *testing.T) {
	m, gates := gatedManager(t)
	if _, err := m.Start(validRequest()); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	_, release, err := m.reserveAlertPriority(context.Background(), time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if info := m.Cancel(); info.DownloadsPaused || info.Jobs[0].Status != "cancelled" {
		t.Fatal(info)
	}
	release()
	if info := m.Info(); info.Active || info.DownloadsPaused || info.Jobs[0].Status != "cancelled" {
		t.Fatal("explicitly cancelled jobs resumed", info)
	}
	assertNoPartials(t, m.Info().Directory)
}

func TestShutdownCancelsAlertReservationAndLeavesDownloadQueued(t *testing.T) {
	m, gates := gatedManager(t)
	if _, err := m.Start(validRequest()); err != nil {
		t.Fatal(err)
	}
	waitGate(t, gates, "btcusd")
	ctx, release, err := m.reserveAlertPriority(context.Background(), time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan struct{})
	go func() { m.Close(); close(done) }()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel the alert reservation")
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not drain the reservation")
	}
	if info := m.Info(); info.Active || info.DownloadsPaused || info.Jobs[0].Status != "queued" {
		t.Fatal("shutdown resumed or discarded a queued download", info)
	}
}
