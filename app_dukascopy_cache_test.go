//go:build bindings

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cryptodesk/internal/candlestorage"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/market"
)

// Only the transport is substituted. The account settings, private SQLite
// storage, application cache routing and market acceptance rules are real.
type cachedForexTransport struct {
	started chan func([]domain.Candle, domain.Health)
}

func (a *cachedForexTransport) Instruments(context.Context) ([]domain.Instrument, error) {
	return nil, errors.New("offline cache test has no instrument discovery")
}

func (a *cachedForexTransport) History(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error) {
	return nil, errors.New("offline cache test has no historical provider")
}

func (a *cachedForexTransport) Stream(ctx context.Context, _ domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
	a.started <- emit
	<-ctx.Done()
	return ctx.Err()
}

func (a *cachedForexTransport) Diagnostics() exchange.Diagnostics { return exchange.Diagnostics{} }

func configureCachedForex(t *testing.T, manager *jforex.Manager, username, environment string) {
	t.Helper()
	if _, err := manager.Configure(jforex.Settings{Username: username, Environment: environment, Password: "explicit-offline-cache-secret"}); err != nil {
		t.Fatal(err)
	}
}

func TestDukascopyChartsRestorePrivateCandlesAndAppendAcceptedUpdatesAcrossRestart(t *testing.T) {
	profile := t.TempDir()
	public := candlestorage.New(profile, nil)
	defer public.Close()
	private := jforex.New(profile)
	defer private.Close()
	configureCachedForex(t, private, "cache-account-a", "demo")
	key := domain.FeedKey{Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Timeframe: "1h"}
	base := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC).Unix()
	bars := []domain.Candle{
		{Time: base, Open: 1.1, High: 1.3, Low: 1, Close: 1.2, Volume: 7, Closed: true, Quality: "authoritative", SourceRevision: 500},
		{Time: base + 3600, Open: 1.2, High: 1.4, Low: 1.1, Close: 1.3, Volume: 8, Closed: true, Quality: "authoritative", SourceRevision: 501},
	}
	cache := chartCandleCache{manager: public, forex: private}
	cache.Put(key, bars)
	if stored, err := cache.Range(key, 0, 0, 100); err != nil || len(stored) != len(bars) {
		t.Fatalf("private chart history was not stored: %+v %v", stored, err)
	}
	private.Close()

	restored := jforex.New(profile)
	defer restored.Close()
	transport := &cachedForexTransport{started: make(chan func([]domain.Candle, domain.Health), 1)}
	service := market.New(market.Options{Cache: chartCandleCache{manager: public, forex: restored}, Factory: func(string) (exchange.Adapter, error) { return transport, nil }})
	defer service.Close()
	app := &App{jforex: restored, market: service}
	snapshot, err := app.AttachChart("restored-forex-chart", 1, key, 0, 0)
	if err != nil || len(snapshot.Bars) != 2 || snapshot.Health.State != "cached" {
		t.Fatalf("opening a chart did not return its local history immediately: %+v %v", snapshot, err)
	}
	if snapshot.Bars[0].Time != bars[0].Time || snapshot.Bars[0].Close != bars[0].Close || snapshot.Bars[0].SourceRevision != 0 {
		t.Fatalf("restored history or session revisions are wrong: %+v", snapshot.Bars)
	}
	var publish func([]domain.Candle, domain.Health)
	select {
	case publish = <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("chart did not subscribe after restoring local history")
	}
	current := domain.Candle{Time: base + 7200, Open: 1.3, High: 1.6, Low: 1.2, Close: 1.5, Volume: 9, Quality: "provisional", SourceRevision: 1}
	publish([]domain.Candle{current}, domain.Health{State: "live"})
	closed := current
	closed.Closed, closed.Quality, closed.SourceRevision, closed.Close = true, "authoritative", 2, 1.6
	publish([]domain.Candle{closed}, domain.Health{})
	// An old live event must neither reopen the closed candle nor corrupt disk.
	publish([]domain.Candle{current}, domain.Health{})
	// A history seed captured before the native close is older even though it
	// contains an authoritative closed bar.
	staleSeed := closed
	staleSeed.SourceRevision, staleSeed.Close, staleSeed.Volume = 1, 1.4, 4
	publish([]domain.Candle{staleSeed}, domain.Health{})
	service.Close()
	restored.Close()

	again := jforex.New(profile)
	defer again.Close()
	stored, err := (chartCandleCache{manager: public, forex: again}).Range(key, 0, 0, 100)
	if err != nil || len(stored) != 3 || stored[0].Close != bars[0].Close || !stored[2].Closed || stored[2].Close != closed.Close || stored[2].Volume != closed.Volume {
		t.Fatalf("restart lost the append or persisted a rejected stale event: %+v %v", stored, err)
	}
	if info, err := public.Info(); err != nil || info.Enabled {
		t.Fatalf("forex persistence unexpectedly enabled public crypto storage: %+v %v", info, err)
	}
	if actual, err := public.Range(key, 0, 0, 100); err != nil || len(actual) != 0 {
		t.Fatalf("forex candles leaked into public storage: %+v %v", actual, err)
	}
	if _, err := os.Stat(filepath.Join(profile, "jforex-runtime")); !os.IsNotExist(err) {
		t.Fatalf("local restoration unexpectedly prepared the SDK: %v", err)
	}
}

func TestDukascopyPrivateChartCacheSeparatesAccountsEnvironmentsAndPublicData(t *testing.T) {
	profile := t.TempDir()
	public := candlestorage.New(profile, nil)
	defer public.Close()
	info, err := public.Info()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = public.Configure(true, info.Directory); err != nil {
		t.Fatal(err)
	}
	private := jforex.New(profile)
	defer private.Close()
	cache := chartCandleCache{manager: public, forex: private}
	forex := domain.FeedKey{Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Timeframe: "1h"}
	crypto := domain.FeedKey{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1h"}
	bar := domain.Candle{Time: 1700002800, Open: 1, High: 2, Low: 1, Close: 1.2, Volume: 3, Closed: true, Quality: "authoritative"}
	configureCachedForex(t, private, "cache-account-a", "demo")
	cache.Put(forex, []domain.Candle{bar})
	cache.Put(crypto, []domain.Candle{bar})
	for _, identity := range []struct {
		username, environment string
		close                 float64
	}{
		{"cache-account-a", "live", 1.3},
		{"cache-account-b", "demo", 1.4},
	} {
		configureCachedForex(t, private, identity.username, identity.environment)
		if stored, err := cache.Range(forex, 0, 0, 100); err != nil || len(stored) != 0 {
			t.Fatalf("account/environment reused another identity's candles: %+v %v", stored, err)
		}
		value := bar
		value.Close = identity.close
		cache.Put(forex, []domain.Candle{value})
	}
	configureCachedForex(t, private, "cache-account-a", "demo")
	if stored, err := cache.Range(forex, 0, 0, 100); err != nil || len(stored) != 1 || stored[0].Close != bar.Close {
		t.Fatalf("returning to the original account lost its cached history: %+v %v", stored, err)
	}
	if stored, err := cache.Range(crypto, 0, 0, 100); err != nil || len(stored) != 1 || stored[0].Close != bar.Close {
		t.Fatalf("private account changes affected public candles: %+v %v", stored, err)
	}
	if stored, err := public.Range(forex, 0, 0, 100); err != nil || len(stored) != 0 {
		t.Fatalf("authenticated candles leaked into the public SQLite cache: %+v %v", stored, err)
	}
}
