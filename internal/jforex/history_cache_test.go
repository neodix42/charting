package jforex

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/candlecache"
	"cryptodesk/internal/domain"
)

const historyCacheStart int64 = 1700002800

func cacheBar(at int64) domain.Candle {
	return domain.Candle{Time: at, Open: 1, High: 2, Low: 1, Close: 1.5, Volume: 3, Closed: true, Quality: "authoritative", SourceRevision: 42}
}
func cacheBars(from, to int64) []domain.Candle {
	bars := []domain.Candle{}
	for at := from; at < to; at += 3600 {
		bars = append(bars, cacheBar(at))
	}
	return bars
}
func testHistoryCache(t *testing.T, directory string) *chartHistoryCache {
	t.Helper()
	c, err := openChartHistoryCache(directory)
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return time.Unix(historyCacheStart+24*3600, 0) }
	t.Cleanup(func() { _ = c.store.Close() })
	return c
}

func TestHistoryCacheRestartReusesClosedPagesAndFetchesOnlyMissingSuffix(t *testing.T) {
	directory := t.TempDir()
	key := forexKey()
	c := testHistoryCache(t, directory)
	calls := []chartHistorySpan{}
	fetch := func(_ context.Context, from, to int64, limit int) ([]domain.Candle, error) {
		calls = append(calls, chartHistorySpan{from, to})
		return cacheBars(from, to), nil
	}
	if bars, err := c.history(context.Background(), key, historyCacheStart, historyCacheStart+3*3600, 20, fetch); err != nil || len(bars) != 3 {
		t.Fatal(bars, err)
	}
	if err := c.store.Close(); err != nil {
		t.Fatal(err)
	}
	restored := testHistoryCache(t, directory)
	if bars, err := restored.history(context.Background(), key, historyCacheStart, historyCacheStart+3*3600, 20, fetch); err != nil || len(bars) != 3 || len(calls) != 1 {
		t.Fatalf("closed page was downloaded after restart: %+v %v calls=%+v", bars, err, calls)
	}
	bars, err := restored.history(context.Background(), key, historyCacheStart, historyCacheStart+5*3600, 20, fetch)
	if err != nil || len(bars) != 5 || !reflect.DeepEqual(calls, []chartHistorySpan{{historyCacheStart, historyCacheStart + 3*3600}, {historyCacheStart + 3*3600, historyCacheStart + 5*3600}}) {
		t.Fatalf("wrong append or query bounds: %+v %v calls=%+v", bars, err, calls)
	}
	for _, bar := range bars {
		if bar.SourceRevision != 0 {
			t.Fatal("persistent revision escaped into new session", bar)
		}
	}
}

func TestHistoryCacheEmptyWeekendAndSparsePagesRemainCoveredAfterRestart(t *testing.T) {
	for _, empty := range []bool{true, false} {
		t.Run(map[bool]string{true: "empty", false: "sparse"}[empty], func(t *testing.T) {
			directory := t.TempDir()
			c := testHistoryCache(t, directory)
			calls := 0
			fetch := func(context.Context, int64, int64, int) ([]domain.Candle, error) {
				calls++
				if empty {
					return []domain.Candle{}, nil
				}
				return []domain.Candle{cacheBar(historyCacheStart), cacheBar(historyCacheStart + 4*3600)}, nil
			}
			first, err := c.history(context.Background(), forexKey(), historyCacheStart, historyCacheStart+6*3600, 10, fetch)
			if err != nil {
				t.Fatal(err)
			}
			_ = c.store.Close()
			restored := testHistoryCache(t, directory)
			actual, err := restored.history(context.Background(), forexKey(), historyCacheStart, historyCacheStart+6*3600, 10, fetch)
			if err != nil || calls != 1 || !reflect.DeepEqual(first, actual) {
				t.Fatalf("market gaps lost query coverage: %v calls=%d first=%+v actual=%+v", err, calls, first, actual)
			}
		})
	}
}

func TestHistoryCacheLimitedPageDoesNotCertifyUnrequestedEarlierBars(t *testing.T) {
	c := testHistoryCache(t, t.TempDir())
	key := forexKey()
	calls := []chartHistorySpan{}
	fetch := func(_ context.Context, from, to int64, limit int) ([]domain.Candle, error) {
		calls = append(calls, chartHistorySpan{from, to})
		bars := cacheBars(from, to)
		if len(bars) > limit {
			bars = bars[len(bars)-limit:]
		}
		return bars, nil
	}
	for range 2 {
		bars, err := c.history(context.Background(), key, historyCacheStart, historyCacheStart+5*3600, 2, fetch)
		if err != nil || len(bars) != 2 {
			t.Fatal(bars, err)
		}
	}
	if len(calls) != 1 {
		t.Fatal("same limited latest page repeated query", calls)
	}
	bars, err := c.history(context.Background(), key, historyCacheStart, historyCacheStart+5*3600, 5, fetch)
	if err != nil || len(bars) != 5 || len(calls) != 2 || calls[1] != (chartHistorySpan{historyCacheStart, historyCacheStart + 3*3600}) {
		t.Fatalf("truncated prefix falsely covered or fully redownloaded: %v bars=%+v calls=%+v", err, bars, calls)
	}
}

func TestHistoryCacheRecentQueryKeepsUnverifiedPastGapsAndReturnsOpenWithoutPersisting(t *testing.T) {
	c := testHistoryCache(t, t.TempDir())
	key := forexKey()
	now := historyCacheStart + 3*3600 + 20
	c.now = func() time.Time { return time.Unix(now, 0) }
	calls := []chartHistorySpan{}
	fetch := func(_ context.Context, from, to int64, limit int) ([]domain.Candle, error) {
		calls = append(calls, chartHistorySpan{from, to})
		bars := cacheBars(from, to)
		for i := range bars {
			if bars[i].Time >= historyCacheStart+3*3600 {
				bars[i].Closed = false
				bars[i].Quality = "provisional"
			}
		}
		return bars, nil
	}
	result, err := c.history(context.Background(), key, historyCacheStart, now, 10, fetch)
	if err != nil || len(result) != 4 || result[3].Closed || calls[0] != (chartHistorySpan{historyCacheStart, now}) {
		t.Fatalf("first recent query skipped unknown history or open row: %+v %v calls=%+v", result, err, calls)
	}
	saved, err := c.store.Range(key, 0, 0, 10)
	if err != nil || len(saved) != 3 {
		t.Fatalf("open candle persisted: %+v %v", saved, err)
	}
	_, err = c.history(context.Background(), key, historyCacheStart, now, 10, fetch)
	if err != nil || len(calls) != 2 || calls[1] != (chartHistorySpan{historyCacheStart + 2*3600, now}) {
		t.Fatalf("warm current query did not fetch bounded overlap: %v %+v", err, calls)
	}
}

func TestHistoryCacheFailedAndCanceledRefreshPreserveSavedCandlesAndCoverage(t *testing.T) {
	c := testHistoryCache(t, t.TempDir())
	key := forexKey()
	fetch := func(_ context.Context, from, to int64, _ int) ([]domain.Candle, error) {
		return cacheBars(from, to), nil
	}
	if _, err := c.history(context.Background(), key, historyCacheStart, historyCacheStart+2*3600, 10, fetch); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(c.path)
	for _, cancel := range []bool{false, true} {
		ctx, stop := context.WithCancel(context.Background())
		_, err := c.history(ctx, key, historyCacheStart, historyCacheStart+3*3600, 10, func(context.Context, int64, int64, int) ([]domain.Candle, error) {
			if cancel {
				stop()
				return []domain.Candle{cacheBar(historyCacheStart + 2*3600)}, nil
			}
			return nil, errors.New("provider unavailable")
		})
		stop()
		if err == nil {
			t.Fatal("failure accepted")
		}
		after, _ := os.ReadFile(c.path)
		saved, _ := c.store.Range(key, 0, 0, 10)
		if string(before) != string(after) || len(saved) != 2 {
			t.Fatalf("failed refresh changed prior data: %s %+v", after, saved)
		}
	}
}

func TestHistoryCacheMissingRowsInvalidateCoverageAndMissingDatabaseIsPreserved(t *testing.T) {
	directory := t.TempDir()
	c := testHistoryCache(t, directory)
	key := forexKey()
	calls := 0
	fetch := func(_ context.Context, from, to int64, _ int) ([]domain.Candle, error) {
		calls++
		return cacheBars(from, to), nil
	}
	if _, err := c.history(context.Background(), key, historyCacheStart, historyCacheStart+3*3600, 10, fetch); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(directory, candlecache.DatabaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DELETE FROM candles WHERE time=?", historyCacheStart+3600); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if result, err := c.history(context.Background(), key, historyCacheStart, historyCacheStart+3*3600, 10, fetch); err != nil || len(result) != 3 || calls != 2 {
		t.Fatalf("deleted row was claimed covered: %+v %v calls=%d", result, err, calls)
	}
	_ = c.store.Close()
	before, _ := os.ReadFile(c.path)
	if err = os.Rename(filepath.Join(directory, candlecache.DatabaseFilename), filepath.Join(directory, "preserved.sqlite3")); err != nil {
		t.Fatal(err)
	}
	if _, err = openChartHistoryCache(directory); err == nil || !strings.Contains(err.Error(), "preserved") {
		t.Fatal("coverage reopened without its database", err)
	}
	after, _ := os.ReadFile(c.path)
	if string(before) != string(after) {
		t.Fatal("orphaned coverage overwritten")
	}
}

func TestHistoryCacheConcurrentNativeCloseWinsAndNewBackfillRowsStillAppend(t *testing.T) {
	m, _ := testManager(t, "")
	key := forexKey()
	initial := cacheBar(historyCacheStart)
	if err := m.StoreCandles(key, []domain.Candle{initial}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	c := m.chartCache
	m.mu.Unlock()
	c.now = func() time.Time { return time.Unix(historyCacheStart+10*3600, 0) }
	started, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := c.history(context.Background(), key, historyCacheStart, historyCacheStart+2*3600, 10, func(context.Context, int64, int64, int) ([]domain.Candle, error) {
			close(started)
			<-finish
			return cacheBars(historyCacheStart, historyCacheStart+2*3600), nil
		})
		done <- err
	}()
	<-started
	corrected := initial
	corrected.Close = 1.9
	corrected.High = 2.5
	corrected.Volume = 9
	if err := m.StoreCandles(key, []domain.Candle{corrected}); err != nil {
		t.Fatal(err)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	actual, err := m.CachedCandles(key, 0, 0, 10)
	if err != nil || len(actual) != 2 || actual[0].Close != 1.9 || actual[0].Volume != 9 || actual[1].Time != historyCacheStart+3600 {
		t.Fatalf("late history rewound native close or dropped backfill: %+v %v", actual, err)
	}
}

func TestHistoryCacheManagerRestartAccountIsolationAndProvisionalWrites(t *testing.T) {
	m, count := testManager(t, "")
	key := forexKey()
	closed := cacheBar(historyCacheStart)
	open := cacheBar(historyCacheStart + 3600)
	open.Closed = false
	open.Quality = "provisional"
	if err := m.StoreCandles(key, []domain.Candle{closed, open}); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 0 {
		t.Fatal("local writes started Java")
	}
	profile := filepath.Dir(m.path)
	m.Close()
	restored := newManager(profile, bridgeRuntime{}, nil)
	defer restored.Close()
	actual, err := restored.CachedCandles(key, 0, 0, 10)
	if err != nil || len(actual) != 1 || actual[0].SourceRevision != 0 {
		t.Fatal(actual, err)
	}
	for _, settings := range []Settings{{Username: testUsername, Password: testPassword, Environment: "live"}, {Username: "another-user", Password: testPassword, Environment: "demo"}} {
		if _, err = restored.Configure(settings); err != nil {
			t.Fatal(err)
		}
		actual, err = restored.CachedCandles(key, 0, 0, 10)
		if err != nil || len(actual) != 0 {
			t.Fatalf("account/environment cache leaked: %+v %v", actual, err)
		}
	}
	if _, err = restored.Configure(Settings{Username: testUsername, Password: testPassword, Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	actual, err = restored.CachedCandles(key, 0, 0, 10)
	if err != nil || len(actual) != 1 {
		t.Fatal("switching back lost private history", actual, err)
	}
}

func TestStreamHistoryCheckpointDoesNotFollowLaterNativeCandlesAcrossFailedGap(t *testing.T) {
	m, _ := testManager(t, "")
	key := forexKey()
	if err := m.StoreCandles(key, cacheBars(historyCacheStart, historyCacheStart+2*3600)); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordHistoryCoverage(key, historyCacheStart, historyCacheStart+2*3600); err != nil {
		t.Fatal(err)
	}
	want := historyCacheStart + 3600
	if got, err := m.HistoryAnchor(key); err != nil || got != want {
		t.Fatal(got, err)
	}
	if err := m.StoreCandles(key, []domain.Candle{cacheBar(historyCacheStart + 8*3600)}); err != nil {
		t.Fatal(err)
	}
	if got, err := m.HistoryAnchor(key); err != nil || got != want {
		t.Fatal("native close advanced past unchecked gap", got, err)
	}
	if err := m.RecordHistoryCoverage(key, historyCacheStart+7*3600, historyCacheStart+9*3600); err == nil {
		t.Fatal("disjoint successful chunk skipped a failed earlier gap")
	}
	// Retry after the rejected checkpoint first confirms the existing closed
	// rows can still be committed. A storage/metadata failure must not be cleared
	// merely by an empty chunk that did not persist its received bars.
	if err := m.StoreCandles(key, cacheBars(historyCacheStart, historyCacheStart+2*3600)); err != nil {
		t.Fatal(err)
	}
	// Verified empty weekend chunks advance chronology without inventing bars.
	if err := m.RecordHistoryCoverage(key, historyCacheStart+2*3600, historyCacheStart+5*3600); err != nil {
		t.Fatal(err)
	}
	if got, err := m.HistoryAnchor(key); err != nil || got != historyCacheStart+4*3600 {
		t.Fatal(got, err)
	}
	profile := filepath.Dir(m.path)
	m.Close()
	restored := newManager(profile, bridgeRuntime{}, nil)
	defer restored.Close()
	if got, err := restored.HistoryAnchor(key); err != nil || got != historyCacheStart+4*3600 {
		t.Fatal("restart lost empty-range checkpoint", got, err)
	}
	actual, err := restored.CachedCandles(key, 0, 0, 20)
	if err != nil || len(actual) != 3 {
		t.Fatal("empty ranges fabricated bars", actual, err)
	}
}

func TestManagerHistoryReusesSuccessfulPrivateQueryAfterRestartWithoutJava(t *testing.T) {
	m, count := testManager(t, "")
	from, to := int64(1792368000), int64(1792378800)
	first, err := m.History(context.Background(), forexKey(), from, to, 10)
	if err != nil || len(first) != 3 || count.Load() != 1 {
		t.Fatal(first, err, count.Load())
	}
	profile := filepath.Dir(m.path)
	m.Close()
	restored := newManager(profile, bridgeRuntime{}, nil)
	defer restored.Close()
	actual, err := restored.History(context.Background(), forexKey(), from, to, 10)
	if err != nil || !reflect.DeepEqual(first, actual) {
		t.Fatalf("restart required Java or lost candles: %+v %v", actual, err)
	}
}

func TestHistoryCacheCorruptCoverageRemainsUntouchedAndSuccessfulRetryClearsLocalError(t *testing.T) {
	m, _ := testManager(t, "")
	m.mu.Lock()
	path := filepath.Join(m.cachePath(m.config), "chart-candles")
	m.mu.Unlock()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "coverage.json")
	original := []byte(`{"version":999,"entries":[]}`)
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CachedCandles(forexKey(), 0, 0, 10); err == nil || !strings.Contains(m.Info().Error, "local history") {
		t.Fatal("corrupt coverage error hidden", err, m.Info())
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(original) {
		t.Fatal("corrupt coverage replaced")
	}
	// Simulate explicit recovery of the malformed sidecar; ordinary local retry
	// must clear its previous error only after a successful cache open/write.
	if err := os.Rename(file, file+".preserved"); err != nil {
		t.Fatal(err)
	}
	if err := m.StoreCandles(forexKey(), []domain.Candle{cacheBar(historyCacheStart)}); err != nil || strings.Contains(m.Info().Error, "local history") {
		t.Fatal("cache retry failed to clear repaired storage error", err, m.Info())
	}
}
