//go:build bindings

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"cryptodesk/internal/candlestorage"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/market"
	"cryptodesk/internal/storage"
)

func TestDucascopyFacadeKeepsCredentialsPrivateAndRestoresWithoutLogin(t *testing.T) {
	profile := t.TempDir()
	store, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Load(); err != nil {
		t.Fatal(err)
	}
	before, err := store.Export()
	if err != nil {
		t.Fatal(err)
	}
	a := &App{store: store, market: market.New(market.Options{}), jforex: jforex.New(profile)}
	defer a.market.Close()
	defer a.jforex.Close()
	info, err := a.ConfigureDucascopy(jforex.Settings{Username: "offline-user", Password: "explicit-offline-secret", Environment: " Demo "})
	if err != nil || info.Username != "offline-user" || info.Environment != "demo" || !info.PasswordConfigured || info.Connected {
		t.Fatalf("configuration: %+v %v", info, err)
	}
	if info, err = a.ConfigureDucascopy(jforex.Settings{Username: "offline-user", Environment: "DEMO"}); err != nil || !info.PasswordConfigured {
		t.Fatalf("retention: %+v %v", info, err)
	}
	if _, err = a.ConfigureDucascopy(jforex.Settings{Username: "another-user", Environment: "demo"}); err == nil {
		t.Fatal("identity changed without a new password")
	}
	after, err := store.Export()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("credentials changed the portable workspace", err)
	}
	for name, value := range map[string]any{"info": a.GetDucascopy(), "diagnostics": a.GetDiagnostics()} {
		encoded, err := json.Marshal(value)
		if err != nil || bytes.Contains(encoded, []byte("explicit-offline-secret")) || bytes.Contains(encoded, []byte(`"password"`)) {
			t.Fatalf("%s exposed password: %s %v", name, encoded, err)
		}
	}
	stat, err := os.Stat(filepath.Join(profile, "dukascopy-jforex.json"))
	if err != nil || (runtime.GOOS != "windows" && stat.Mode().Perm() != 0600) {
		t.Fatal("credentials are not private", stat, err)
	}
	reopened := jforex.New(profile)
	defer reopened.Close()
	if restored := reopened.Info(); restored.Username != info.Username || !restored.PasswordConfigured || restored.Connected {
		t.Fatalf("offline restoration: %+v", restored)
	}
	if _, err := os.Stat(filepath.Join(profile, "jforex-runtime")); !os.IsNotExist(err) {
		t.Fatal("settings started SDK setup without a connection request", err)
	}
}

// This adapter deliberately does not connect to JForex. It checks the facade's
// real settings/market coordination without using a customer account.
type facadeForexAdapter struct{ starts chan context.Context }

func (f *facadeForexAdapter) Instruments(context.Context) ([]domain.Instrument, error) {
	return []domain.Instrument{}, nil
}
func (f *facadeForexAdapter) History(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
	return []domain.Candle{}, nil
}
func (f *facadeForexAdapter) Diagnostics() exchange.Diagnostics { return exchange.Diagnostics{} }
func (f *facadeForexAdapter) Stream(ctx context.Context, _ domain.FeedKey, _ func([]domain.Candle, domain.Health)) error {
	f.starts <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func TestDucascopyFirstSetupRecoversActiveChartAndExistingIdentityCannotChange(t *testing.T) {
	adapter := &facadeForexAdapter{starts: make(chan context.Context, 4)}
	a := &App{jforex: jforex.New(t.TempDir()), market: market.New(market.Options{Factory: func(string) (exchange.Adapter, error) { return adapter, nil }})}
	defer a.jforex.Close()
	defer a.market.Close()
	key := domain.FeedKey{Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Timeframe: "1h"}
	snapshot, err := a.AttachChart("forex", 1, key, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var original context.Context
	select {
	case original = <-adapter.starts:
	case <-time.After(time.Second):
		t.Fatal("initial chart did not start")
	}
	if _, err = a.ConfigureDucascopy(jforex.Settings{Username: "offline-user", Password: "offline-secret", Environment: "demo"}); err != nil {
		t.Fatal("first configuration blocked by unconfigured chart", err)
	}
	select {
	case <-original.Done():
	case <-time.After(time.Second):
		t.Fatal("unconfigured subscription not canceled")
	}
	select {
	case <-adapter.starts:
	case <-time.After(time.Second):
		t.Fatal("existing lease did not restart")
	}
	if _, err = a.GetFeedSnapshot(snapshot.LeaseID, 0); err != nil {
		t.Fatal("first setup expired the chart lease", err)
	}
	if _, err = a.ConfigureDucascopy(jforex.Settings{Username: "offline-user", Environment: " DEMO "}); err != nil {
		t.Fatal("unchanged identity rejected", err)
	}
	if _, err = a.ConfigureDucascopy(jforex.Settings{Username: "other-user", Password: "other-secret", Environment: "live"}); err == nil {
		t.Fatal("credentials changed under an active chart")
	}
	a.DetachChart(snapshot.LeaseID)
	if info, err := a.ConfigureDucascopy(jforex.Settings{Username: "other-user", Password: "other-secret", Environment: "live"}); err != nil || info.Environment != "live" {
		t.Fatalf("released provider could not change: %+v %v", info, err)
	}
}

func TestUnconfiguredDucascopyTestDoesNotInstallRuntimeOrLogin(t *testing.T) {
	profile := t.TempDir()
	a := &App{jforex: jforex.New(profile)}
	defer a.jforex.Close()
	if _, err := a.TestDucascopyConnection(); err == nil || exchange.ErrorKind(err) != "restricted" {
		t.Fatalf("unconfigured test: %v", err)
	}
	if _, err := os.Stat(filepath.Join(profile, "jforex-runtime")); !os.IsNotExist(err) {
		t.Fatal("unconfigured connection downloaded SDK", err)
	}
}

func TestChartCandleCacheExcludesAuthenticatedForexButKeepsPublicCandles(t *testing.T) {
	manager := candlestorage.New(t.TempDir(), nil)
	defer manager.Close()
	info, err := manager.Info()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Configure(true, info.Directory); err != nil {
		t.Fatal(err)
	}
	cache := chartCandleCache{manager: manager}
	forex := domain.FeedKey{Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Timeframe: "1h"}
	public := domain.FeedKey{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1h"}
	bars := []domain.Candle{{Time: 1700002800, Open: 1, High: 2, Low: 1, Close: 2, Volume: 3, Closed: true, Quality: "authoritative"}}
	cache.Put(forex, bars)
	cache.Put(public, bars)
	if actual, err := manager.Range(forex, 0, 0, 100); err != nil || len(actual) != 0 {
		t.Fatal("authenticated candles reached shared disk cache", actual, err)
	}
	if actual, err := cache.Range(public, 0, 0, 100); err != nil || len(actual) != 1 {
		t.Fatal("public cache no longer works", actual, err)
	}
}
