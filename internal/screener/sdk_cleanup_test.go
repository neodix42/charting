package screener

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitSDKCleanupBatch(t *testing.T, m *Manager) Info {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		info := m.Info()
		if !info.Active {
			return info
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("SDK cleanup integration batch did not finish")
	return Info{}
}

// The real production loop still owns session creation, permits and cleanup.
// Keep this offline integration fast after its first normally paced export.
func accelerateSDKCleanupFixture(ctx context.Context) {
	if session := sessionFromContext(ctx); session != nil {
		session.limiter.mu.Lock()
		session.limiter.interval = 0
		session.limiter.state.NextAt = 0
		session.limiter.notifyLocked()
		session.limiter.mu.Unlock()
	}
}

func TestSDKFailedNativeUpgradeRetainsArchiveAndCheckpointThroughManagerResume(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	preload := filepath.Join(t.TempDir(), "public-native-partial.cjs")
	script := `global.fetch=async function(url){const year=Number(new URL(url).pathname.split('/').at(-1));
if(year!==2024)throw Error('Explicit public native interruption');
return new Response(JSON.stringify({timestamp:Date.UTC(2024,0,1),shift:86400000,multiplier:1,
open:100,high:120,low:90,close:110,times:[0],opens:[0],highs:[0],lows:[0],closes:[0],volumes:[2]}),{headers:{'content-type':'application/json'}});};`
	if err := os.WriteFile(preload, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	if _, err := m.Start(Request{Symbols: []string{"btcusd"}, Timeframe: "d1", From: "2024-01-01", To: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	failed := waitSDKCleanupBatch(t, m)
	if len(failed.Jobs) != 1 || failed.Jobs[0].Status != "failed" || !strings.Contains(failed.Jobs[0].Message, "Explicit public native interruption") {
		t.Fatal("public worker did not stop after committing a native partial", failed)
	}
	job := failed.Jobs[0]
	directory := m.jobDirectory(job)
	partial := filepath.Join(directory, "data.part")
	file, err := os.OpenFile(partial, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString("uncommitted public tail"); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(partial)
	if err != nil {
		t.Fatal(err)
	}
	publicCheckpoint, err := os.ReadFile(filepath.Join(directory, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	disallowSDKTestHTTP(t)
	provider := newFakeSDKHistory()
	var fail atomic.Bool
	fail.Store(true)
	provider.export = func(ctx context.Context, call sdkHistoryCall) (int64, error) {
		accelerateSDKCleanupFixture(ctx)
		if fail.Load() {
			return 0, errors.New("Explicit first native SDK export failure")
		}
		return sdkExportFixture(ctx, call)
	}
	m.dukascopyHistory = provider
	if _, err = m.Resume(); err != nil {
		t.Fatal(err)
	}
	failed = waitSDKCleanupBatch(t, m)
	if failed.Jobs[0].Status != "failed" || !strings.Contains(failed.Jobs[0].Message, "Explicit first native SDK export failure") {
		t.Fatal("SDK upgrade did not exercise first-export failure", failed)
	}
	if !ownedJob(directory, job) {
		t.Fatal("failed SDK upgrade cleanup discarded the owned partial/archive directory")
	}
	var checkpoint struct{ Rows, Bytes, Next int64 }
	checkpointData, err := os.ReadFile(filepath.Join(directory, "sdk-checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(checkpointData, &checkpoint); err != nil || checkpoint.Rows != 0 || checkpoint.Bytes != 0 || checkpoint.Next != 1704067200000 {
		t.Fatal("manager discarded the separate SDK checkpoint", checkpoint, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	archives := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".public-partial-") && !strings.HasSuffix(entry.Name(), ".pending") {
			archives = append(archives, entry.Name())
		}
	}
	if len(archives) != 1 {
		t.Fatal("manager discarded the native public migration archive", archives)
	}
	archive := filepath.Join(directory, archives[0])
	if data, err := os.ReadFile(filepath.Join(archive, "public.csv")); err != nil || !bytes.Equal(data, before) {
		t.Fatal("failed manager cleanup changed archived public bytes", string(data), err)
	}
	if data, err := os.ReadFile(filepath.Join(archive, "checkpoint.json")); err != nil || !bytes.Equal(data, publicCheckpoint) {
		t.Fatal("failed manager cleanup changed archived public checkpoint", err)
	}
	if names, err := csvNames(m.directory); err != nil || len(names) != 0 {
		t.Fatal("failed SDK upgrade published incomplete data", names, err)
	}
	fail.Store(false)
	if _, err = m.Resume(); err != nil {
		t.Fatal(err)
	}
	completed := waitSDKCleanupBatch(t, m)
	if completed.Jobs[0].Status != "completed" || completed.Jobs[0].ID != job.ID || completed.Jobs[0].AsOf != job.AsOf {
		t.Fatal("Resume lost queue identity or frozen cutoff", completed)
	}
	data, err := os.ReadFile(completed.Jobs[0].OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil || len(rows) != 732 || strings.Join(rows[0], ",") != "timestamp,open,high,low,close" || rows[1][1] != "10" {
		t.Fatal("native SDK Resume did not publish its complete no-volume history", len(rows), err)
	}
}

func TestSDKFailedSyntheticDayCheckpointSurvivesManagerCleanupAndResume(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	disallowSDKTestHTTP(t)
	provider := newFakeSDKHistory()
	var fail atomic.Bool
	fail.Store(true)
	provider.export = func(ctx context.Context, call sdkHistoryCall) (int64, error) {
		accelerateSDKCleanupFixture(ctx)
		if fail.Load() && call.symbol == "btcusd" && call.from >= 1704153600000+sdkHistoryHour {
			return 0, errors.New("Explicit SDK tick interruption after committed day")
		}
		return sdkExportFixture(ctx, call)
	}
	m.dukascopyHistory = provider
	if _, err := m.Start(Request{Symbols: []string{"btcxau"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-03"}); err != nil {
		t.Fatal(err)
	}
	failed := waitSDKCleanupBatch(t, m)
	if len(failed.Jobs) != 1 || failed.Jobs[0].Status != "failed" || !strings.Contains(failed.Jobs[0].Message, "Explicit SDK tick interruption") {
		t.Fatal("SDK source did not fail after its committed day", failed)
	}
	job := failed.Jobs[0]
	directory := m.jobDirectory(job)
	var checkpoint struct{ Rows, Bytes, NextDay, End int64 }
	checkpointData, err := os.ReadFile(filepath.Join(directory, "synthetic-checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(checkpointData, &checkpoint); err != nil || checkpoint.Rows != 1 || checkpoint.Bytes <= 0 || checkpoint.NextDay != 1704153600000 || checkpoint.End != 1704240000000 {
		t.Fatal("failed manager cleanup discarded its committed synthetic day", checkpoint, err)
	}
	before, err := os.ReadFile(filepath.Join(directory, "data.part"))
	if err != nil || !bytes.Contains(before, []byte("1704067200000,5,6,5,6")) {
		t.Fatal("committed synthetic partial was lost", string(before), err)
	}
	provider.mu.Lock()
	beforeCalls := len(provider.calls)
	provider.mu.Unlock()
	fail.Store(false)
	if _, err = m.Resume(); err != nil {
		t.Fatal(err)
	}
	completed := waitSDKCleanupBatch(t, m)
	if completed.Jobs[0].Status != "completed" || completed.Jobs[0].ID != job.ID || completed.Jobs[0].AsOf != job.AsOf {
		t.Fatal("synthetic Resume lost identity or frozen cutoff", completed)
	}
	data, err := os.ReadFile(completed.Jobs[0].OutputPath)
	if err != nil || string(data) != "timestamp,open,high,low,close\n1704067200000,5,6,5,6\n1704153600000,5,6,5,6\n" {
		t.Fatal("synthetic Resume lost or duplicated committed candles", string(data), err)
	}
	provider.mu.Lock()
	resumed := append([]sdkHistoryCall(nil), provider.calls[beforeCalls:]...)
	provider.mu.Unlock()
	if len(resumed) == 0 {
		t.Fatal("resume did not finish the missing SDK sources")
	}
	for _, call := range resumed {
		if call.from < 1704153600000 {
			t.Fatal("Resume redownloaded the already committed SDK day", call)
		}
	}
}

func TestSDKCleanupRetainsEveryCheckpointJournalArchiveAndUnknownPartial(t *testing.T) {
	for _, name := range []string{"checkpoint.json", "ranges.json", "synthetic-checkpoint.json", "sdk-checkpoint.json", "public-sdk-upgrade.json", ".public-partial-archived", ".public-partial-interrupted.pending", "nonempty data.part", "unexplained directory data.part", "empty data.part", "no recovery state"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			expected := true
			switch {
			case strings.HasPrefix(name, ".public-partial-"):
				if err := os.Mkdir(filepath.Join(directory, name), 0700); err != nil {
					t.Fatal(err)
				}
			case name == "unexplained directory data.part":
				if err := os.Mkdir(filepath.Join(directory, "data.part"), 0700); err != nil {
					t.Fatal(err)
				}
			case name == "nonempty data.part":
				if err := os.WriteFile(filepath.Join(directory, "data.part"), []byte("uncommitted source"), 0600); err != nil {
					t.Fatal(err)
				}
			case name == "empty data.part":
				if err := os.WriteFile(filepath.Join(directory, "data.part"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				expected = false
			case name == "no recovery state":
				expected = false
			default:
				if err := os.WriteFile(filepath.Join(directory, name), []byte("worker validates recovery content"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := retainedDownloadWork(directory); got != expected {
				t.Fatal("incorrect failed-job cleanup decision", name, got, expected)
			}
		})
	}
}
