package jforex

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

const streamCacheStart int64 = 1700002800

func streamCacheFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "subscribe-anchors.txt")
	t.Setenv("CRYPTODESK_JFOREX_CACHE_TRACE", trace)
	m, _ := testManager(t, "stream-cache")
	key := forexKey()
	if err := m.StoreCandles(key, cacheBars(streamCacheStart, streamCacheStart+2*3600)); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordHistoryCoverage(key, streamCacheStart, streamCacheStart+2*3600); err != nil {
		t.Fatal(err)
	}
	return m, trace
}

func awaitCachedStream(t *testing.T, ready <-chan struct{}, done <-chan error) {
	t.Helper()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("stream ended before the cache outcome: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not deliver its explicit cache fixture")
	}
}

func stopCachedStream(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not release its account operation")
	}
}

func TestStreamAcceptedCacheCommitAdvancesDurableResumeAnchorBeforeRestart(t *testing.T) {
	m, trace := streamCacheFixture(t)
	key := forexKey()
	start := func(manager *Manager) (context.CancelFunc, <-chan error) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		ready := make(chan struct{}, 1)
		done := make(chan error, 1)
		go func() {
			done <- manager.Stream(ctx, key, func(bars []domain.Candle, health domain.Health) {
				if len(bars) > 0 {
					if err := manager.StoreCandles(key, bars); err != nil {
						t.Error(err)
					}
				}
				if health.State == "live" {
					ready <- struct{}{}
				}
			})
		}()
		awaitCachedStream(t, ready, done)
		return cancel, done
	}
	cancel, done := start(m)
	stopCachedStream(t, cancel, done)
	if anchor, err := m.HistoryAnchor(key); err != nil || anchor != streamCacheStart+2*3600 {
		t.Fatalf("accepted seed did not advance its coverage checkpoint: %d %v", anchor, err)
	}
	m.Close()
	restored := newManager(filepath.Dir(m.path), m.runtime, m.factory)
	defer restored.Close()
	if anchor, err := restored.HistoryAnchor(key); err != nil || anchor != streamCacheStart+2*3600 {
		t.Fatalf("restart lost the successful append checkpoint: %d %v", anchor, err)
	}
	cancel, done = start(restored)
	stopCachedStream(t, cancel, done)
	data, err := os.ReadFile(trace)
	want := strconv.FormatInt(streamCacheStart+3600, 10) + "\n" + strconv.FormatInt(streamCacheStart+2*3600, 10) + "\n"
	if err != nil || string(data) != want {
		t.Fatalf("subscribe did not send the persisted incremental anchors: %q want %q %v", data, want, err)
	}
	if bars, err := restored.CachedCandles(key, 0, 0, 100); err != nil || len(bars) != 3 || bars[2].Time != streamCacheStart+2*3600 {
		t.Fatalf("successful coverage was not backed by durable closed rows: %+v %v", bars, err)
	}
}

func TestStreamCanceledEmitDoesNotAdvanceResumeCoverage(t *testing.T) {
	m, _ := streamCacheFixture(t)
	key := forexKey()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.Stream(ctx, key, func(bars []domain.Candle, _ domain.Health) {
			if len(bars) > 0 {
				// A detached market feed ignores this event before its disk write.
				cancel()
			}
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled emit retained the stream")
	}
	if anchor, err := m.HistoryAnchor(key); err != nil || anchor != streamCacheStart+3600 {
		t.Fatalf("ignored event advanced a nonexistent candle commit: %d %v", anchor, err)
	}
	if bars, err := m.CachedCandles(key, 0, 0, 100); err != nil || len(bars) != 2 {
		t.Fatalf("canceled emit wrote new candles: %+v %v", bars, err)
	}
}

func TestStreamFailedAcceptedWritePreservesPriorDurableCoverage(t *testing.T) {
	m, _ := streamCacheFixture(t)
	key := forexKey()
	m.mu.Lock()
	cache := m.chartCache
	m.mu.Unlock()
	before, err := os.ReadFile(cache.path)
	if err != nil {
		t.Fatal(err)
	}
	// Reject writes while leaving reads working. Without the stream's failure
	// guard, coverage could otherwise certify the absent appended row as empty.
	failureDB, err := sql.Open("sqlite", filepath.Join(filepath.Dir(cache.path), "candles.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer failureDB.Close()
	if _, err = failureDB.Exec("CREATE TRIGGER reject_stream_cache_insert BEFORE INSERT ON candles BEGIN SELECT RAISE(ABORT, 'explicit cache write failure'); END"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = failureDB.Exec("DROP TRIGGER IF EXISTS reject_stream_cache_insert") }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	writeErrors := make(chan error, 1)
	go func() {
		done <- m.Stream(ctx, key, func(bars []domain.Candle, health domain.Health) {
			if len(bars) > 0 {
				writeErrors <- m.StoreCandles(key, bars)
				// A second active feed can successfully commit between this
				// failure and Stream's checkpoint decision, clearing its UI error.
				// The failed write must still prevent a false empty-range proof.
				if _, err := failureDB.Exec("DROP TRIGGER reject_stream_cache_insert"); err != nil {
					t.Error(err)
				}
				other := key
				other.Instrument = "GBPUSD"
				if err := m.StoreCandles(other, bars); err != nil {
					t.Error(err)
				}
			}
			if health.State == "error" {
				ready <- struct{}{}
			}
		})
	}()
	awaitCachedStream(t, ready, done)
	stopCachedStream(t, cancel, done)
	if err := <-writeErrors; err == nil {
		t.Fatal("fixture did not fail the accepted candle write")
	}
	after, err := os.ReadFile(cache.path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("failed candle write published false coverage: %s %v", after, err)
	}
	if info := m.Info(); !strings.Contains(info.Error, "local history") {
		t.Fatalf("cache write failure was not visible: %+v", info)
	}
	cache.mu.Lock()
	rows, readErr := cache.store.Range(key, 0, 0, 100)
	cache.mu.Unlock()
	if readErr != nil || len(rows) != 2 {
		t.Fatalf("fixture incorrectly failed reads or changed prior candles: %+v %v", rows, readErr)
	}
	if _, err = failureDB.Exec("DROP TRIGGER IF EXISTS reject_stream_cache_insert"); err != nil {
		t.Fatal(err)
	}
	m.Close()
	restored := newManager(filepath.Dir(m.path), m.runtime, m.factory)
	defer restored.Close()
	if anchor, err := restored.HistoryAnchor(key); err != nil || anchor != streamCacheStart+3600 {
		t.Fatalf("restart accepted nonexistent fetched rows as empty coverage: %d %v", anchor, err)
	}
}

func TestStreamIgnoredClosedRowsCannotBecomeSuccessfulEmptyCoverage(t *testing.T) {
	m, _ := streamCacheFixture(t)
	key := forexKey()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- m.Stream(ctx, key, func(_ []domain.Candle, health domain.Health) {
			// A previous failed write can leave this row only in the market's memory;
			// an ignored duplicate is not evidence of a durable commit.
			if health.State == "error" {
				ready <- struct{}{}
			}
		})
	}()
	awaitCachedStream(t, ready, done)
	stopCachedStream(t, cancel, done)
	if anchor, err := m.HistoryAnchor(key); err != nil || anchor != streamCacheStart+3600 {
		t.Fatalf("missing source rows were claimed as empty provider coverage: %d %v", anchor, err)
	}
}
