package candlecache

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"cryptodesk/internal/domain"
)

func testKey() domain.FeedKey {
	return domain.FeedKey{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1m"}
}

func testCandle(index int) domain.Candle {
	return domain.Candle{Time: 1700000040 + int64(index)*60, Open: 100.12345678912345, High: 102, Low: 99, Close: 101.25, Volume: .000000001234567890123, Closed: true, Quality: "authoritative", SourceRevision: int64(index)}
}

func openTest(t *testing.T, directory string) *Store {
	t.Helper()
	s, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func readTest(t *testing.T, s *Store, key domain.FeedKey, from, to int64, limit int) []domain.Candle {
	t.Helper()
	bars, err := s.Range(key, from, to, limit)
	if err != nil {
		t.Fatal(err)
	}
	return bars
}

func TestRestartPreservesExactCandlesAndCorrection(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private #25% ? + ü")
	s := openTest(t, directory)
	key := testKey()
	first, second := testCandle(0), testCandle(1)
	first.Volume = math.Copysign(0, -1)
	second.Closed, second.Quality, second.SourceRevision = false, "provisional", 1792500000999
	if err := s.Upsert(key, []domain.Candle{second, first}); err != nil {
		t.Fatal(err)
	}
	// An authoritative reconciliation with revision zero must replace a higher
	// socket revision: service ordering has already accepted this correction.
	corrected := second
	corrected.Close, corrected.Volume = 100.75, .000000004
	corrected.Closed, corrected.Quality, corrected.SourceRevision = true, "authoritative", 0
	if err := s.Upsert(key, []domain.Candle{corrected}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, directory)
	got := readTest(t, s, key, 0, 0, 100)
	if want := []domain.Candle{first, corrected}; !reflect.DeepEqual(got, want) {
		t.Fatalf("restored %#v; want %#v", got, want)
	}
	if !math.Signbit(got[0].Volume) {
		t.Fatal("decimal text did not preserve negative zero's sign")
	}
	var openType, openText string
	if err := s.db.QueryRow("SELECT typeof(open),open FROM candles ORDER BY time LIMIT 1").Scan(&openType, &openText); err != nil {
		t.Fatal(err)
	}
	if openType != "text" || openText != "100.12345678912345" {
		t.Fatalf("stored open: type=%s text=%s", openType, openText)
	}
	var journal string
	var synchronous int
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("journal=%s err=%v", journal, err)
	}
	if err := s.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil || synchronous != 2 {
		t.Fatalf("synchronous=%d err=%v", synchronous, err)
	}
}

func TestRangeBoundsAndProviderIsolation(t *testing.T) {
	s := openTest(t, t.TempDir())
	key := testKey()
	keys := []domain.FeedKey{key, {Exchange: "bybit", Market: "spot", Instrument: key.Instrument, Timeframe: key.Timeframe}, {Exchange: "coinbase", Market: "spot", Instrument: "BTC-USDT", Timeframe: key.Timeframe}, {Exchange: key.Exchange, Market: key.Market, Instrument: "ETHUSDT", Timeframe: key.Timeframe}, {Exchange: key.Exchange, Market: key.Market, Instrument: key.Instrument, Timeframe: "5m"}}
	for i, key := range keys {
		c := testCandle(1)
		c.Time = 1700000100 // Aligned to both 1m and 5m.
		c.Close += float64(i) / 100
		if err := s.Upsert(key, []domain.Candle{c}); err != nil {
			t.Fatal(err)
		}
		if got := readTest(t, s, key, 0, 0, 1); len(got) != 1 || got[0] != c {
			t.Fatalf("provider/key %s collided: %#v", key, got)
		}
	}
	bars := []domain.Candle{testCandle(4), testCandle(2), testCandle(0), testCandle(3), testCandle(1)}
	if err := s.Upsert(key, bars); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		from, to int64
		limit    int
		indices  []int
	}{
		{"latest", 0, 0, 2, []int{3, 4}},
		{"inclusive", testCandle(1).Time, testCandle(3).Time, 9, []int{1, 2, 3}},
		{"latest within bounds", testCandle(1).Time, testCandle(3).Time, 2, []int{2, 3}},
		{"single exact", testCandle(2).Time, testCandle(2).Time, 2, []int{2}},
		{"upper only", 0, testCandle(1).Time, 9, []int{0, 1}},
		{"lower only", testCandle(3).Time, 0, 9, []int{3, 4}},
		{"empty", testCandle(4).Time + 1, 0, 2, []int{}},
		{"default", 0, 0, 0, []int{0, 1, 2, 3, 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readTest(t, s, key, tc.from, tc.to, tc.limit)
			want := make([]domain.Candle, 0, len(tc.indices))
			for _, i := range tc.indices {
				want = append(want, testCandle(i))
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v want %#v", got, want)
			}
		})
	}
}

func TestSameSymbolSpotAndPerpetualRemainIsolatedAfterRestart(t *testing.T) {
	directory := t.TempDir()
	s := openTest(t, directory)
	spot, perpetual := testKey(), testKey()
	perpetual.Market = "perpetual"
	spotBar, perpetualBar := testCandle(0), testCandle(0)
	spotBar.Close, spotBar.Volume = 100.5, 3
	perpetualBar.Close, perpetualBar.Volume = 101.5, 7
	for _, entry := range []struct {
		key domain.FeedKey
		bar domain.Candle
	}{{spot, spotBar}, {perpetual, perpetualBar}} {
		if err := s.Upsert(entry.key, []domain.Candle{entry.bar}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, directory)
	corrected := spotBar
	corrected.Volume = 2
	if err := s.Upsert(spot, []domain.Candle{corrected}); err != nil {
		t.Fatal(err)
	}
	if got := readTest(t, s, spot, 0, 0, 10); !reflect.DeepEqual(got, []domain.Candle{corrected}) {
		t.Fatal("spot candle was overwritten by perpetual", got)
	}
	if got := readTest(t, s, perpetual, 0, 0, 10); !reflect.DeepEqual(got, []domain.Candle{perpetualBar}) {
		t.Fatal("spot correction changed perpetual candle", got)
	}
}

func TestInvalidBatchesAreAtomic(t *testing.T) {
	s := openTest(t, t.TempDir())
	key := testKey()
	valid := testCandle(0)
	mutations := map[string]func(*domain.Candle){
		"NaN":                  func(c *domain.Candle) { c.Close = math.NaN() },
		"infinite volume":      func(c *domain.Candle) { c.Volume = math.Inf(1) },
		"negative price":       func(c *domain.Candle) { c.Open = -1 },
		"bad OHLC":             func(c *domain.Candle) { c.Low = 101 },
		"negative volume":      func(c *domain.Candle) { c.Volume = -1 },
		"milliseconds":         func(c *domain.Candle) { c.Time *= 1000 },
		"unaligned":            func(c *domain.Candle) { c.Time++ },
		"negative revision":    func(c *domain.Candle) { c.SourceRevision = -1 },
		"oversized quality":    func(c *domain.Candle) { c.Quality = strings.Repeat("a", 65) },
		"NUL quality":          func(c *domain.Candle) { c.Quality = "live\x00" },
		"invalid UTF8 quality": func(c *domain.Candle) { c.Quality = string([]byte{255}) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			invalid := testCandle(1)
			mutate(&invalid)
			if err := s.Upsert(key, []domain.Candle{valid, invalid}); err == nil {
				t.Fatal("invalid batch accepted")
			}
			if got := readTest(t, s, key, 0, 0, 9); len(got) != 0 {
				t.Fatalf("partial invalid batch written: %#v", got)
			}
		})
	}
	invalidKey := key
	invalidKey.Market = "futures"
	if err := s.Upsert(invalidKey, []domain.Candle{valid}); err == nil {
		t.Fatal("unsupported market accepted")
	}
	if err := s.Upsert(key, make([]domain.Candle, maxBatch+1)); err == nil {
		t.Fatal("unbounded batch accepted")
	}
	if err := s.Upsert(key, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMondayWeeklyAlignment(t *testing.T) {
	s := openTest(t, t.TempDir())
	key := testKey()
	key.Timeframe = "1w"
	c := testCandle(0)
	c.Time = 1792368000 // Monday, 2026-10-19 UTC.
	if err := s.Upsert(key, []domain.Candle{c}); err != nil {
		t.Fatal(err)
	}
	c.Time += 86400
	if err := s.Upsert(key, []domain.Candle{c}); err == nil {
		t.Fatal("non-Monday weekly bar accepted")
	}
}

func TestDatabaseWriteFailureRollsBackTheWholeBatch(t *testing.T) {
	s := openTest(t, t.TempDir())
	// Force a real SQLite error after the first valid insert. Prevalidation alone
	// must not be responsible for the package's all-or-nothing write guarantee.
	if _, err := s.db.Exec(fmt.Sprintf(`CREATE TRIGGER reject_second_bar BEFORE INSERT ON candles
 WHEN NEW.time=%d BEGIN SELECT RAISE(ABORT,'simulated disk write rejection'); END`, testCandle(1).Time)); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(testKey(), []domain.Candle{testCandle(0), testCandle(1)}); err == nil {
		t.Fatal("SQLite failure was not returned")
	}
	if got := readTest(t, s, testKey(), 0, 0, 10); len(got) != 0 {
		t.Fatalf("failed transaction committed an earlier row: %#v", got)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_second_bar"); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(testKey(), []domain.Candle{testCandle(0)}); err != nil {
		t.Fatal("store could not recover after a rolled back write:", err)
	}
}

func TestInvalidRangesDoNotQuery(t *testing.T) {
	s := openTest(t, t.TempDir())
	key := testKey()
	for _, r := range []struct {
		from, to int64
		limit    int
	}{{-1, 0, 1}, {0, -1, 1}, {lastTimestamp + 1, 0, 1}, {0, lastTimestamp + 1, 1}, {200, 100, 1}, {0, 0, -1}, {0, 0, MaxRangeLimit + 1}} {
		if _, err := s.Range(key, r.from, r.to, r.limit); err == nil {
			t.Fatalf("invalid range accepted: %#v", r)
		}
	}
	key.Exchange = "unknown"
	if _, err := s.Range(key, 0, 0, 1); err == nil {
		t.Fatal("invalid feed key accepted")
	}
}

func TestCorruptForeignAndFutureDatabasesArePreserved(t *testing.T) {
	for _, kind := range []string{"corrupt", "empty", "foreign", "future", "checksum", "schema"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, DatabaseFilename)
			switch kind {
			case "corrupt":
				if err := os.WriteFile(path, []byte("precious original bytes, not SQLite"), 0640); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.WriteFile(path, nil, 0640); err != nil {
					t.Fatal(err)
				}
			default:
				if kind != "foreign" {
					s := openTest(t, directory)
					if err := s.Upsert(testKey(), []domain.Candle{testCandle(0)}); err != nil {
						t.Fatal(err)
					}
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
				}
				db, err := sql.Open("sqlite", sqliteURI(path, "rwc"))
				if err != nil {
					t.Fatal(err)
				}
				statements := map[string]string{
					"foreign":  "CREATE TABLE precious(value TEXT); INSERT INTO precious VALUES('user data')",
					"future":   "PRAGMA user_version=99",
					"checksum": "UPDATE candle_cache_metadata SET schema_checksum='bad'",
					"schema":   "ALTER TABLE candles ADD COLUMN unexpected TEXT",
				}
				if _, err = db.Exec(statements[kind]); err != nil {
					db.Close()
					t.Fatal(err)
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
				if err = os.Chmod(path, 0640); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			s, err := Open(directory)
			if err == nil {
				s.Close()
				t.Fatal("unsupported database was adopted")
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(before, after) {
				t.Fatalf("original changed, read err=%v", readErr)
			}
			if runtime.GOOS != "windows" {
				info, statErr := os.Stat(path)
				if statErr != nil || info.Mode().Perm() != 0640 {
					t.Fatalf("foreign permissions changed: %v %v", info, statErr)
				}
			}
		})
	}
}

func TestReadDetectsModifiedStoredData(t *testing.T) {
	for _, kind := range []string{"checksum", "nonnumeric", "nonfinite", "invalid candle"} {
		t.Run(kind, func(t *testing.T) {
			s := openTest(t, t.TempDir())
			key, c := testKey(), testCandle(0)
			if err := s.Upsert(key, []domain.Candle{c}); err != nil {
				t.Fatal(err)
			}
			values := candleStrings(c)
			value := "102"
			switch kind {
			case "nonnumeric":
				value = "oops"
			case "nonfinite":
				value = "NaN"
			case "invalid candle":
				value = "0"
			}
			values[0] = value
			savedChecksum := "tampered"
			if kind != "checksum" {
				savedChecksum = checksum(key, c.Time, values, 1, c.Quality, c.SourceRevision)
			}
			if _, err := s.db.Exec("UPDATE candles SET open=?,checksum=?", value, savedChecksum); err != nil {
				t.Fatal(err)
			}
			if bars, err := s.Range(key, 0, 0, 1); err == nil || bars != nil {
				t.Fatalf("corrupt stored rows served: %#v %v", bars, err)
			}
		})
	}
}

func TestPrivatePermissionsAndUnrelatedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	directory := filepath.Join(t.TempDir(), "new cache")
	s := openTest(t, directory)
	if err := s.Upsert(testKey(), []domain.Candle{testCandle(0)}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("folder mode: %v %v", info, err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err = os.Stat(filepath.Join(directory, DatabaseFilename+suffix))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode: %v %v", suffix, info, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0750); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(directory, "workspace-export.json")
	if err := os.WriteFile(unrelated, []byte("do not change"), 0640); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, directory)
	info, err = os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0750 {
		t.Fatalf("existing folder mode changed: %v %v", info, err)
	}
	info, err = os.Stat(unrelated)
	data, readErr := os.ReadFile(unrelated)
	if err != nil || readErr != nil || info.Mode().Perm() != 0640 || string(data) != "do not change" {
		t.Fatalf("unrelated file changed: %v %v %q", err, readErr, data)
	}
}

func TestFolderSizeCountsFilesWithoutFollowingSymlinks(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"one": "123", "nested/two": "45678"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside"), make([]byte, 100), 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for name, target := range map[string]string{"linked-directory": outside, "linked-file": filepath.Join(outside, "outside"), "loop": directory} {
			if err := os.Symlink(target, filepath.Join(directory, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if size, err := SizeBytes(directory); err != nil || size != 8 {
		t.Fatalf("size=%d err=%v; want8", size, err)
	}
	if size, err := SizeBytes(filepath.Join(directory, "not-yet-created")); err != nil || size != 0 {
		t.Fatalf("absent size=%d err=%v", size, err)
	}
	if _, err := SizeBytes(filepath.Join(directory, "one")); err == nil {
		t.Fatal("regular file accepted as folder")
	}
}

func TestSymlinkAndOrphanSidecarsArePreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need Windows privileges")
	}
	for _, suffix := range []string{"directory", "", "-wal", "-shm", "-journal", "orphan-wal"} {
		t.Run(fmt.Sprintf("suffix=%s", suffix), func(t *testing.T) {
			directory := t.TempDir()
			outside := filepath.Join(t.TempDir(), "precious")
			if err := os.WriteFile(outside, []byte("precious data"), 0640); err != nil {
				t.Fatal(err)
			}
			if suffix == "directory" {
				link := filepath.Join(directory, "linked")
				if err := os.Symlink(t.TempDir(), link); err != nil {
					t.Fatal(err)
				}
				directory = link
			} else if suffix == "orphan-wal" {
				if err := os.WriteFile(filepath.Join(directory, DatabaseFilename+"-wal"), []byte("precious orphaned journal"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(outside, filepath.Join(directory, DatabaseFilename+suffix)); err != nil {
				t.Fatal(err)
			}
			if s, err := Open(directory); err == nil {
				s.Close()
				t.Fatal("unsafe path accepted")
			}
			if data, err := os.ReadFile(outside); err != nil || string(data) != "precious data" {
				t.Fatalf("symlink target changed: %q %v", data, err)
			}
		})
	}
}

func TestConcurrentReadersWritersAndClose(t *testing.T) {
	s := openTest(t, t.TempDir())
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 10; j++ {
				var err error
				if i%2 == 0 {
					err = s.Upsert(testKey(), []domain.Candle{testCandle(i*10 + j)})
				} else {
					_, err = s.Range(testKey(), 0, 0, 10)
				}
				if err != nil && !errors.Is(err, ErrClosed) {
					t.Error(err)
				}
			}
		}(i)
	}
	close(start)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(testKey(), []domain.Candle{testCandle(0)}); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close=%v", err)
	}
	if _, err := s.Range(testKey(), 0, 0, 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after close=%v", err)
	}
}

func TestMultipleConnectionsUseDurableSerializedWrites(t *testing.T) {
	directory := t.TempDir()
	a, b := openTest(t, directory), openTest(t, directory)
	var wg sync.WaitGroup
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := s.Upsert(testKey(), []domain.Candle{testCandle(10*i + j)}); err != nil {
					t.Error(err)
				}
			}
		}(i, s)
	}
	wg.Wait()
	if got := readTest(t, a, testKey(), 0, 0, 100); len(got) != 20 {
		t.Fatalf("lost concurrent commits: %d", len(got))
	}
}

func TestCommittedWALSurvivesProcessExitWithoutClose(t *testing.T) {
	const childFolder = "CRYPTODESK_CANDLE_CACHE_TEST_CHILD_FOLDER"
	if directory := os.Getenv(childFolder); directory != "" {
		s, err := Open(directory)
		if err == nil {
			err = s.Upsert(testKey(), []domain.Candle{testCandle(0)})
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		// Do not close or checkpoint: verify successful FULL commits alone.
		os.Exit(0)
	}
	directory := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCommittedWALSurvivesProcessExitWithoutClose$")
	cmd.Env = append(os.Environ(), childFolder+"="+directory)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v %s", err, output)
	}
	s := openTest(t, directory)
	if got := readTest(t, s, testKey(), 0, 0, 1); len(got) != 1 || got[0] != testCandle(0) {
		t.Fatalf("committed WAL did not recover: %#v", got)
	}
}

func TestNormalizeDirectory(t *testing.T) {
	for _, directory := range []string{"", "  ", "bad\x00path"} {
		if _, err := NormalizeDirectory(directory); err == nil {
			t.Fatalf("invalid directory accepted: %q", directory)
		}
	}
	directory := filepath.Join(t.TempDir(), "nested", "..", "clean")
	got, err := NormalizeDirectory(directory)
	if err != nil || !filepath.IsAbs(got) || got != filepath.Clean(directory) {
		t.Fatalf("normalized=%s err=%v", got, err)
	}
}
