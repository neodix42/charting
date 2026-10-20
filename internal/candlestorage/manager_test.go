package candlestorage

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"cryptodesk/internal/candlecache"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/storage"
)

func managerKey() domain.FeedKey {
	return domain.FeedKey{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1m"}
}
func managerBar(index int) domain.Candle {
	return domain.Candle{Time: 1700000040 + int64(index)*60, Open: 100.12345678912345, High: 102, Low: 99, Close: 100.5, Volume: .000000000123456789, Closed: false, Quality: "provisional", SourceRevision: 1792500000999}
}
func newManager(t *testing.T, profile string, onError func(string)) *Manager {
	t.Helper()
	m := New(profile, onError)
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m
}
func managerInfo(t *testing.T, m *Manager) Info {
	t.Helper()
	info, err := m.Info()
	if err != nil {
		t.Fatal(err)
	}
	return info
}
func managerRange(t *testing.T, m *Manager, key domain.FeedKey) []domain.Candle {
	t.Helper()
	bars, err := m.Range(key, 0, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return bars
}
func configureManager(t *testing.T, m *Manager, enabled bool, directory string) Info {
	t.Helper()
	info, err := m.Configure(enabled, directory)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestDefaultOffNeverCreatesPreferencesOrCandleFiles(t *testing.T) {
	profile := t.TempDir()
	m := newManager(t, profile, nil)
	info := managerInfo(t, m)
	if info.Enabled || info.Directory != filepath.Join(profile, "candles") || info.SizeBytes != 0 || info.Error != "" {
		t.Fatalf("default info: %#v", info)
	}
	m.Put(managerKey(), []domain.Candle{managerBar(0)})
	if bars := managerRange(t, m, managerKey()); len(bars) != 0 {
		t.Fatalf("disabled manager served data: %#v", bars)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(profile)
	if err != nil || len(entries) != 0 {
		t.Fatalf("default/off created local files: %v, %v", entries, err)
	}
}

func TestEnableCorrectCloseAndRestartPreservesPreferencesAndAcceptedExactData(t *testing.T) {
	profile := t.TempDir()
	directory := filepath.Join(t.TempDir(), "candle folder #25% ? + ü")
	m := newManager(t, profile, nil)
	info := configureManager(t, m, true, directory)
	if !info.Enabled || info.Directory != directory || info.Error != "" {
		t.Fatalf("enabled info: %#v", info)
	}
	key := managerKey()
	first, second := managerBar(0), managerBar(1)
	first.Volume = math.Copysign(0, -1)
	m.Put(key, []domain.Candle{second, first})
	corrected := second
	corrected.Close = 100.76543210987654
	corrected.Volume = .000000000000004
	corrected.Closed = true
	corrected.Quality = "authoritative"
	corrected.SourceRevision = 0
	m.Put(key, []domain.Candle{corrected})
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	preferencesBefore, err := os.ReadFile(filepath.Join(profile, "candle-storage.json"))
	if err != nil {
		t.Fatal(err)
	}
	m = newManager(t, profile, nil)
	restored := managerInfo(t, m)
	if !restored.Enabled || restored.Directory != directory || restored.SizeBytes <= 0 || restored.Error != "" {
		t.Fatalf("restart info: %#v", restored)
	}
	bars := managerRange(t, m, key)
	if want := []domain.Candle{first, corrected}; !reflect.DeepEqual(bars, want) {
		t.Fatalf("restored %#v; want %#v", bars, want)
	}
	if !math.Signbit(bars[0].Volume) || math.Float64bits(bars[1].Close) != math.Float64bits(corrected.Close) || math.Float64bits(bars[1].Volume) != math.Float64bits(corrected.Volume) {
		t.Fatalf("changed accepted float representation: %#v", bars)
	}
	other := key
	other.Exchange = "bybit"
	if bars := managerRange(t, m, other); len(bars) != 0 {
		t.Fatalf("mixed provider data: %#v", bars)
	}
	after, err := os.ReadFile(filepath.Join(profile, "candle-storage.json"))
	if err != nil || !bytes.Equal(after, preferencesBefore) {
		t.Fatalf("restart rewrote preferences: %q %v", after, err)
	}
}

func TestFolderChangesAndDisableLeaveOldDataIntactAndDoNotWriteDisabledBars(t *testing.T) {
	profile := t.TempDir()
	firstDirectory, secondDirectory := filepath.Join(t.TempDir(), "original"), filepath.Join(t.TempDir(), "selected")
	m := newManager(t, profile, nil)
	configureManager(t, m, true, firstDirectory)
	key := managerKey()
	original, selected := managerBar(0), managerBar(1)
	m.Put(key, []domain.Candle{original})
	configureManager(t, m, true, secondDirectory)
	if got := managerRange(t, m, key); len(got) != 0 {
		t.Fatalf("implicitly migrated old folder into new folder: %#v", got)
	}
	old, err := candlecache.Open(firstDirectory)
	if err != nil {
		t.Fatal(err)
	}
	got, err := old.Range(key, 0, 0, 10)
	if err != nil || !reflect.DeepEqual(got, []domain.Candle{original}) {
		t.Fatalf("old folder lost data: %#v %v", got, err)
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	m.Put(key, []domain.Candle{selected})
	configureManager(t, m, false, secondDirectory)
	disabledBefore, err := os.ReadFile(filepath.Join(secondDirectory, candlecache.DatabaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	m.Put(key, []domain.Candle{managerBar(2)})
	if got := managerRange(t, m, key); len(got) != 0 {
		t.Fatalf("disabled manager served cached data: %#v", got)
	}
	disabledAfter, err := os.ReadFile(filepath.Join(secondDirectory, candlecache.DatabaseFilename))
	if err != nil || !bytes.Equal(disabledAfter, disabledBefore) {
		t.Fatalf("disabled write changed DB: %v", err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m = newManager(t, profile, nil)
	info := managerInfo(t, m)
	if info.Enabled || info.Directory != secondDirectory {
		t.Fatalf("disabled preference not restored: %#v", info)
	}
	configureManager(t, m, true, secondDirectory)
	if got := managerRange(t, m, key); !reflect.DeepEqual(got, []domain.Candle{selected}) {
		t.Fatalf("disabled data was persisted or selected data lost: %#v", got)
	}
	configureManager(t, m, true, firstDirectory)
	if got := managerRange(t, m, key); !reflect.DeepEqual(got, []domain.Candle{original}) {
		t.Fatalf("reselect old cache: %#v", got)
	}
}

func TestInvalidTargetAndPreferenceCommitFailureRetainAppliedConfiguration(t *testing.T) {
	profile := t.TempDir()
	directory := filepath.Join(t.TempDir(), "active")
	m := newManager(t, profile, nil)
	configureManager(t, m, true, directory)
	key := managerKey()
	m.Put(key, []domain.Candle{managerBar(0)})
	prefsPath := filepath.Join(profile, "candle-storage.json")
	before, err := os.ReadFile(prefsPath)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "not a directory")
	foreignBytes := []byte("existing unrelated contents")
	if err = os.WriteFile(foreign, foreignBytes, 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", "invalid\x00path", foreign} {
		if _, err = m.Configure(true, target); err == nil {
			t.Fatalf("invalid target accepted: %q", target)
		}
		if info := managerInfo(t, m); !info.Enabled || info.Directory != directory {
			t.Fatalf("failed target replaced config: %#v", info)
		}
	}
	after, err := os.ReadFile(prefsPath)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("failed target changed preferences: %q %v", after, err)
	}
	if data, err := os.ReadFile(foreign); err != nil || !bytes.Equal(data, foreignBytes) {
		t.Fatalf("foreign file changed: %q %v", data, err)
	}
	// Fail the final atomic preference rename after opening a valid replacement.
	if err = os.Remove(prefsPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(prefsPath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Configure(true, filepath.Join(t.TempDir(), "valid replacement")); err == nil {
		t.Fatal("failed preference commit accepted")
	}
	if info := managerInfo(t, m); !info.Enabled || info.Directory != directory {
		t.Fatalf("preference failure replaced applied config: %#v", info)
	}
	m.Put(key, []domain.Candle{managerBar(1)})
	if got := managerRange(t, m, key); !reflect.DeepEqual(got, []domain.Candle{managerBar(0), managerBar(1)}) {
		t.Fatalf("previous cache stopped after invalid config: %#v", got)
	}
}

func TestMalformedPreferencesAreReportedAndPreservedWithoutFallbackWrites(t *testing.T) {
	for _, document := range []string{`{"enabled":true,`, `null`, `{"enabled":false,"directory":"","unknown":1}`, `{"enabled":false,"directory":"valid"} {"second":"object"}`} {
		t.Run(document, func(t *testing.T) {
			profile := t.TempDir()
			path := filepath.Join(profile, "candle-storage.json")
			if err := os.WriteFile(path, []byte(document), 0600); err != nil {
				t.Fatal(err)
			}
			m := newManager(t, profile, nil)
			info := managerInfo(t, m)
			if info.Error == "" || info.Enabled {
				t.Fatalf("malformed preferences silently accepted: %#v", info)
			}
			m.Put(managerKey(), []domain.Candle{managerBar(0)})
			if got := managerRange(t, m, managerKey()); len(got) != 0 {
				t.Fatal("malformed preference fallback stored candles")
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != document {
				t.Fatalf("malformed source replaced: %q %v", after, err)
			}
			if _, err = os.Stat(filepath.Join(profile, "candles")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("malformed preference fallback created cache: %v", err)
			}
		})
	}
}

func TestWorkerReportsWriteFailureWithoutDroppingPriorDataAndCanRecover(t *testing.T) {
	var mu sync.Mutex
	var events []string
	m := newManager(t, t.TempDir(), func(message string) { mu.Lock(); events = append(events, message); mu.Unlock() })
	directory := filepath.Join(t.TempDir(), "active")
	configureManager(t, m, true, directory)
	key := managerKey()
	valid := managerBar(0)
	m.Put(key, []domain.Candle{valid})
	invalid := managerBar(1)
	invalid.Time++
	m.Put(key, []domain.Candle{invalid})
	m.Put(key, []domain.Candle{invalid})
	info := managerInfo(t, m)
	if !info.Enabled || !strings.Contains(info.Error, "UTC interval boundary") {
		t.Fatalf("write failure not surfaced: %#v", info)
	}
	mu.Lock()
	count := len(events)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("duplicate identical failure emitted %d events", count)
	}
	if got := managerRange(t, m, key); !reflect.DeepEqual(got, []domain.Candle{valid}) {
		t.Fatalf("failed write lost accepted data: %#v", got)
	}
	info = configureManager(t, m, true, directory)
	if info.Error != "" {
		t.Fatalf("explicit reconfigure did not clear recovered error: %#v", info)
	}
}

func TestCloseDrainsAcceptedCopiesAndConcurrentReadsClosesAreSafe(t *testing.T) {
	profile := t.TempDir()
	m := newManager(t, profile, nil)
	configureManager(t, m, true, filepath.Join(t.TempDir(), "active"))
	key := managerKey()
	entered, release := make(chan struct{}), make(chan struct{})
	if err := m.submit(func() { close(entered); <-release }); err != nil {
		t.Fatal(err)
	}
	<-entered
	want := make([]domain.Candle, 64)
	for i := range want {
		want[i] = managerBar(i)
		input := []domain.Candle{want[i]}
		m.Put(key, input)
		input[0].Close = 101
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 5 {
				if _, err := m.Info(); err != nil && !errors.Is(err, candlecache.ErrClosed) {
					t.Error(err)
				}
				if _, err := m.Range(key, 0, 0, 100); err != nil && !errors.Is(err, candlecache.ErrClosed) {
					t.Error(err)
				}
			}
		})
	}
	for range 4 {
		wg.Go(func() {
			if err := m.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	close(release)
	wg.Wait()
	if _, err := m.Info(); !errors.Is(err, candlecache.ErrClosed) {
		t.Fatalf("info after close=%v", err)
	}
	if _, err := m.Range(key, 0, 0, 10); !errors.Is(err, candlecache.ErrClosed) {
		t.Fatalf("range after close=%v", err)
	}
	if _, err := m.Configure(true, filepath.Join(t.TempDir(), "after close")); !errors.Is(err, candlecache.ErrClosed) {
		t.Fatalf("configure after close=%v", err)
	}
	m.Put(key, []domain.Candle{managerBar(999)})
	restarted := newManager(t, profile, nil)
	if got := managerRange(t, restarted, key); !reflect.DeepEqual(got, want) {
		t.Fatalf("close did not drain accepted copied bars: got%d want%d", len(got), len(want))
	}
}

func TestMachinePreferencesAndCandlesNeverEnterWorkspaceExportOrChangeOnImport(t *testing.T) {
	profile := t.TempDir()
	workspace, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	loaded, err := workspace.Load()
	if err != nil {
		t.Fatal(err)
	}
	before, err := workspace.Export()
	if err != nil {
		t.Fatal(err)
	}
	m := newManager(t, profile, nil)
	directory := filepath.Join(t.TempDir(), "machine-local private cache")
	configureManager(t, m, true, directory)
	m.Put(managerKey(), []domain.Candle{managerBar(0)})
	_ = managerInfo(t, m)
	prefsPath := filepath.Join(profile, "candle-storage.json")
	prefs, err := os.ReadFile(prefsPath)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := workspace.Export()
	if err != nil || !bytes.Equal(exported, before) {
		t.Fatalf("machine preferences changed portable workspace: %q %v", exported, err)
	}
	if bytes.Contains(exported, []byte(directory)) || bytes.Contains(exported, []byte("candle-storage.json")) || bytes.Contains(exported, []byte(candlecache.DatabaseFilename)) {
		t.Fatal("workspace export leaked machine-local storage")
	}
	var replacement domain.Workspace
	if err = json.Unmarshal(exported, &replacement); err != nil {
		t.Fatal(err)
	}
	replacement.Tabs[0].Name = "Imported layout"
	if _, err = workspace.Import(loaded.Revision, replacement); err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(prefsPath); err != nil || !bytes.Equal(after, prefs) {
		t.Fatalf("workspace import altered local preferences: %q %v", after, err)
	}
	if info := managerInfo(t, m); !info.Enabled || info.Directory != directory {
		t.Fatalf("workspace import reconfigured candle storage: %#v", info)
	}
	if bars := managerRange(t, m, managerKey()); !reflect.DeepEqual(bars, []domain.Candle{managerBar(0)}) {
		t.Fatal("workspace import changed local cache data")
	}
}
