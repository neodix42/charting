package jforex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func restoredCatalogManager(t *testing.T, profile, scenario string) (*Manager, *atomic.Int32) {
	t.Helper()
	count := &atomic.Int32{}
	m := newManager(profile, bridgeRuntime{directory: "fake-bridge", java: "fake-java"}, func(ctx context.Context) (*exec.Cmd, error) {
		count.Add(1)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBridgeHelperProcess$")
		cmd.Env = append(os.Environ(), "CRYPTODESK_JFOREX_TEST_PROCESS=1", "CRYPTODESK_JFOREX_TEST_SCENARIO="+scenario)
		configureProcess(cmd)
		return cmd, nil
	})
	t.Cleanup(m.Close)
	return m, count
}

func TestInstrumentCatalogPersistsAndRestoresWithoutLoginAcrossAccounts(t *testing.T) {
	m, _ := testManager(t, "all-assets")
	want, err := m.Instruments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Dir(m.path)
	path := filepath.Join(m.cachePath(m.config), "instruments.json")
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), testUsername) || strings.Contains(string(data), testPassword) {
		t.Fatal("catalog missing or contains credentials", err)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	m.Close()
	restored, count := restoredCatalogManager(t, profile, "error")
	items, err := restored.Instruments(context.Background())
	if err != nil || !reflect.DeepEqual(items, want) || count.Load() != 0 || restored.Info().Connected {
		t.Fatalf("cached read opened JForex: %v %v starts=%d", items, err, count.Load())
	}
	items[0].NativeID = "changed"
	items[0].SupportedIntervals[0] = "bad"
	if items[0].ProviderMetadata != nil {
		items[0].ProviderMetadata["sdkSymbol"] = "changed"
	}
	items, err = restored.RefreshInstruments(context.Background(), false)
	if err != nil || !reflect.DeepEqual(items, want) || count.Load() != 0 {
		t.Fatal("fresh selection check contacted provider or mutated cache", err)
	}
	for _, settings := range []Settings{{Username: testUsername, Password: testPassword, Environment: "live"}, {Username: "another-user", Password: testPassword, Environment: "demo"}} {
		if _, err = restored.Configure(settings); err != nil {
			t.Fatal(err)
		}
		if items, err = restored.Instruments(context.Background()); err == nil || len(items) != 0 {
			t.Fatal("another account reused catalog", items, err)
		}
	}
	if _, err = restored.Configure(Settings{Username: testUsername, Password: testPassword, Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	if items, err = restored.Instruments(context.Background()); err != nil || !reflect.DeepEqual(items, want) {
		t.Fatal("original account cache lost", err)
	}
}

func TestStaleCatalogRefreshSharesWorkKeepsPickerImmediateAndSurvivesCallerCancel(t *testing.T) {
	m, _ := testManager(t, "all-assets")
	want, err := m.Instruments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Dir(m.path)
	path := filepath.Join(m.cachePath(m.config), "instruments.json")
	m.Close()
	data, _ := os.ReadFile(path)
	var snapshot savedInstrumentCatalog
	_ = json.Unmarshal(data, &snapshot)
	snapshot.FetchedAt = time.Now().Add(-2 * time.Hour).UnixMilli()
	data, _ = json.Marshal(snapshot)
	_ = os.WriteFile(path, data, 0600)
	gate := t.TempDir()
	t.Setenv("CRYPTODESK_JFOREX_CATALOG_GATE", gate)
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("CRYPTODESK_JFOREX_PROTOCOL_TRACE", trace)
	restored, count := restoredCatalogManager(t, profile, "catalog-gated")
	if items, err := restored.Instruments(context.Background()); err != nil || !reflect.DeepEqual(items, want) || count.Load() != 0 {
		t.Fatal("stale cache blocked picker", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := restored.RefreshInstruments(ctx, false); first <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(gate, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if items, err := restored.Instruments(context.Background()); err != nil || !reflect.DeepEqual(items, want) {
		t.Fatal("refresh hid saved list", err)
	}
	results := make(chan error, 8)
	for range 8 {
		go func() {
			items, err := restored.RefreshInstruments(context.Background(), false)
			if err == nil && len(items) != 2 {
				err = errors.New("fresh catalog not returned")
			}
			results <- err
		}()
	}
	if err := os.WriteFile(filepath.Join(gate, "release"), []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	operations, _ := os.ReadFile(trace)
	if count.Load() != 1 || strings.Count(string(operations), "instruments\n") != 1 {
		t.Fatal("concurrent selections repeated discovery", count.Load(), string(operations))
	}
	if items, err := restored.Instruments(context.Background()); err != nil || len(items) != 2 {
		t.Fatal("new list not published", items, err)
	}
}

func TestCatalogRefreshFailureAndCorruptionPreserveSavedData(t *testing.T) {
	m, _ := testManager(t, "all-assets")
	want, err := m.Instruments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Dir(m.path)
	path := filepath.Join(m.cachePath(m.config), "instruments.json")
	m.Close()
	original, _ := os.ReadFile(path)
	restored, _ := restoredCatalogManager(t, profile, "catalog-wrong-market")
	if _, err := restored.RefreshInstruments(context.Background(), true); err == nil {
		t.Fatal("invalid live catalog accepted")
	}
	if items, err := restored.Instruments(context.Background()); err != nil || !reflect.DeepEqual(items, want) {
		t.Fatal("failed check removed saved list", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(original) {
		t.Fatal("failed check changed disk cache")
	}
	restored.Close()
	corrupt := []byte(`{"version":999}`)
	_ = os.WriteFile(path, corrupt, 0600)
	repaired, count := restoredCatalogManager(t, profile, "all-assets")
	if _, err := repaired.Instruments(context.Background()); err == nil || count.Load() != 0 {
		t.Fatal("corrupt cache was used or silently replaced", err)
	}
	if _, err := repaired.RefreshInstruments(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(path + ".invalid-*")
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	if got, _ := os.ReadFile(backups[0]); string(got) != string(corrupt) {
		t.Fatal("corrupt original not retained")
	}
	if items, err := repaired.Instruments(context.Background()); err != nil || !reflect.DeepEqual(items, want) {
		t.Fatal("explicit repair failed", err)
	}
}

func TestCloseCancelsOwnedCatalogRefreshAndKeepsCommittedSnapshot(t *testing.T) {
	m, _ := testManager(t, "all-assets")
	if _, err := m.Instruments(context.Background()); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Dir(m.path)
	path := filepath.Join(m.cachePath(m.config), "instruments.json")
	m.Close()
	original, _ := os.ReadFile(path)
	gate := t.TempDir()
	t.Setenv("CRYPTODESK_JFOREX_CATALOG_GATE", gate)
	restored, _ := restoredCatalogManager(t, profile, "catalog-gated")
	done := make(chan error, 1)
	go func() { _, err := restored.RefreshInstruments(context.Background(), true); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(gate, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh did not start")
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { restored.Close(); close(closed) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled refresh succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("refresh caller did not stop")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("catalog retained a worker after close")
	}
	if data, _ := os.ReadFile(path); string(data) != string(original) {
		t.Fatal("cancelled refresh overwrote snapshot")
	}
}
