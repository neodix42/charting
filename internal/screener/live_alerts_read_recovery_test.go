package screener

import (
	"bytes"
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type midnightAlertAdapter struct {
	*publicAlertFakeAdapter
	outage              atomic.Bool
	catalogCalls        atomic.Int64
	onFailure           func()
	historyByInstrument map[string]*publicAlertFakeAdapter
	beforeHistory       func()
	historyOnce         sync.Once
}

func (a *midnightAlertAdapter) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	a.historyOnce.Do(func() {
		if a.beforeHistory != nil {
			a.beforeHistory()
		}
	})
	if selected := a.historyByInstrument[key.Instrument]; selected != nil {
		return selected.History(ctx, key, from, to, limit)
	}
	return a.publicAlertFakeAdapter.History(ctx, key, from, to, limit)
}

func (a *midnightAlertAdapter) Instruments(ctx context.Context) ([]domain.Instrument, error) {
	call := a.catalogCalls.Add(1)
	if a.outage.Load() {
		if call == 1 && a.onFailure != nil {
			a.onFailure()
		}
		return nil, &net.DNSError{Name: "api.hyperliquid.xyz", Server: "127.0.0.53:53", Err: "no such host", IsNotFound: true}
	}
	return a.publicAlertFakeAdapter.Instruments(ctx)
}

func midnightAlertFixture(t *testing.T) (*Manager, *LiveAlerts, SavedAnalysisCondition, *midnightAlertAdapter) {
	t.Helper()
	m, l, saved, base := namedPublicLiveFixture(t)
	base.items = []domain.Instrument{{NativeID: "BTC", Market: "perpetual", Availability: "available"}}
	a := &midnightAlertAdapter{publicAlertFakeAdapter: base}
	l.publicData.adapters["hyperliquid"] = a
	return m, l, saved, a
}

func TestMidnightDailyAlertRecoversDNSAfterLongFirstAttemptWithoutDuplicate(t *testing.T) {
	_, l, saved, adapter := midnightAlertFixture(t)
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(day.Add(-time.Minute).UnixMilli())
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	adapter.outage.Store(true)
	adapter.onFailure = func() { clock.Store(day.Add(6 * time.Minute).UnixMilli()) }
	entry := LiveAlertDefinition{ID: strings.Repeat("e", 32), Name: "Midnight daily", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true}
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int64
	l.send = func(_ context.Context, alert LiveAlert) error {
		if alert.Time != day.Add(-24*time.Hour).UnixMilli() {
			t.Errorf("wrong daily candle: %d", alert.Time)
		}
		sends.Add(1)
		return nil
	}
	l.Start(context.Background())
	clock.Store(day.UnixMilli())
	l.signal()
	info := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && info.Status == "Waiting to retry closed-bar check"
	})
	if sends.Load() != 0 || info.NextRun != day.Add(6*time.Minute+liveAlertRetryDelay).UnixMilli() || len(info.Checks) != 1 || info.Checks[0].ProgressState != "queued" || !strings.Contains(info.Checks[0].Phase, "network/provider") {
		t.Fatal("DNS outage discarded the midnight attempt or looked terminal", info, sends.Load())
	}
	l.mu.Lock()
	original := l.retryAsOf
	l.mu.Unlock()
	if original != day.UnixMilli() {
		t.Fatal("scheduled cutoff moved", original)
	}
	adapter.outage.Store(false)
	clock.Store(info.NextRun)
	l.signal()
	info = waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return !info.Running && info.LastRun == clock.Load() && info.Sent == 1 })
	if sends.Load() != 1 || info.Error != "" || info.NextRun != day.Add(24*time.Hour).UnixMilli() || adapter.catalogCalls.Load() != 2 {
		t.Fatal("recovery did not check the original daily close exactly once", info, sends.Load(), adapter.catalogCalls.Load())
	}
	before := len(adapter.historyKeys)
	if err := l.check(context.Background(), config, day.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 1 || len(adapter.historyKeys) != before {
		t.Fatal("completed signal was downloaded or delivered again", sends.Load(), len(adapter.historyKeys), before)
	}
}

func TestPublicMarketCatalogSurvivesRestartAndTransientDiscoveryFailure(t *testing.T) {
	m, l, _, adapter := midnightAlertFixture(t)
	if _, items, err := l.publicAlertCatalog(context.Background(), "hyperliquid"); err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	path := l.publicAlertCatalogPath("hyperliquid")
	var snapshot publicAlertCatalogSnapshot
	if err := readState(path, publicAlertCatalogBytes, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.FetchedAt = time.Now().Add(-time.Hour).UnixMilli()
	if err := writeState(path, snapshot); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	l.publicData.catalogs["hyperliquid"] = publicAlertCatalog{items: snapshot.Items, at: time.UnixMilli(snapshot.FetchedAt)}
	adapter.outage.Store(true)
	if _, items, err := l.publicAlertCatalog(context.Background(), "hyperliquid"); err == nil || len(items) != 1 || !exchange.RetryableReadError(err) {
		t.Fatal("last verified catalog was unavailable", items, err)
	}
	// A cold application can recover the exact same complete catalog. No
	// account connection or folder-wide inventory scan is needed.
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	restored.publicData = &publicAlertData{adapters: map[string]exchange.Adapter{"hyperliquid": adapter}}
	if _, items, err := restored.publicAlertCatalog(context.Background(), "hyperliquid"); err == nil || len(items) != 1 || items[0].NativeID != "BTC" {
		t.Fatal(items, err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(original, after) {
		t.Fatal("failed rediscovery replaced the last verified catalog")
	}
}

func TestInvalidMarketCatalogIsPreservedWhileFreshDiscoveryStillWorks(t *testing.T) {
	_, l, _, _ := midnightAlertFixture(t)
	path := l.publicAlertCatalogPath("hyperliquid")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"version":900,"source":"hyperliquid","items":[]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, items, err := l.publicAlertCatalog(context.Background(), "hyperliquid"); err == nil || len(items) != 1 {
		t.Fatal("fresh discovery should survive an invalid cache with a warning", items, err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(original, after) {
		t.Fatal("invalid original catalog was overwritten")
	}
}

func TestReadCooldownLimitsDNSStormsButDoesNotBlockHealthySourcesOrIndividualTimeouts(t *testing.T) {
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	d := &publicAlertData{now: func() time.Time { return now }}
	base := &publicAlertFakeAdapter{price: 100, fail: &net.DNSError{Name: "api.hyperliquid.xyz", Err: "no such host", IsNotFound: true}}
	a := publicAlertReadAdapter{Adapter: base, data: d, source: "hyperliquid"}
	key := domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1h"}
	from := now.Add(-time.Hour).Unix()
	to := now.Unix()
	for range 4 {
		if _, err := a.History(context.Background(), key, from, to, 1); err == nil {
			t.Fatal("DNS failure disappeared")
		}
	}
	if len(base.historyKeys) != 1 {
		t.Fatal("a known DNS outage repeated requests for every asset", len(base.historyKeys))
	}
	healthy := publicAlertReadAdapter{Adapter: &publicAlertFakeAdapter{price: 200}, data: d, source: "bybit"}
	otherKey := domain.FeedKey{Exchange: "bybit", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1h"}
	if bars, err := healthy.History(context.Background(), otherKey, from, to, 1); err != nil || len(bars) != 1 {
		t.Fatal("independent source was blocked", bars, err)
	}
	now = now.Add(liveAlertRetryDelay)
	base.fail = context.DeadlineExceeded
	for range 2 {
		if _, err := a.History(context.Background(), key, from, to, 1); err == nil {
			t.Fatal("timeout disappeared")
		}
	}
	if len(base.historyKeys) != 3 || d.readProblem("hyperliquid") != nil {
		t.Fatal("one asset timeout suppressed peers", len(base.historyKeys))
	}
	d.recordReadProblem("hyperliquid", &exchange.Error{Kind: "rateLimited", Message: "provider cooldown", RetryAt: now.Add(time.Hour).Unix()})
	now = now.Add(liveAlertRetryDelay)
	d.recordReadProblem("hyperliquid", &net.DNSError{Name: "api.hyperliquid.xyz", Err: "no such host"})
	if problem := d.readProblem("hyperliquid"); problem == nil || exchange.RetryAt(problem) != now.Add(time.Hour-liveAlertRetryDelay).Unix() {
		t.Fatal("a shorter outage replaced provider Retry-After", problem)
	}
	if d.readProblem("bybit") != nil {
		t.Fatal("provider backoff leaked to another source")
	}
}

func TestRetryScopeKeepsDailyPublicationAndNetworkFailuresButNeverRetriesDelivery(t *testing.T) {
	asOf := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	config := LiveAlertsConfig{Enabled: true}
	for i, period := range []string{"d1", "h1", "d1", "d1"} {
		id := strings.Repeat(string(rune('a'+i)), 32)
		config.Alerts = append(config.Alerts, LiveAlertDefinition{ID: id, Name: id, ConditionID: strings.Repeat("f", 32), DataSource: "hyperliquid", Period: period, Enabled: true})
	}
	err := errors.Join(
		&liveAlertWorkIssue{condition: config.Alerts[0].ID, period: "d1", error: errLiveAlertsPendingBar},
		&liveAlertWorkIssue{condition: config.Alerts[1].ID, period: "h1", error: liveAlertReadFailure(context.DeadlineExceeded)},
		&liveAlertWorkIssue{condition: config.Alerts[2].ID, period: "d1", error: &liveAlertDeliveryIssue{context.DeadlineExceeded}},
		&liveAlertWorkIssue{condition: config.Alerts[3].ID, period: "d1", error: &exchange.Error{Kind: "unknownInstrument"}},
	)
	retry, ok := retryLiveAlertConfig(config, err, asOf.UnixMilli(), asOf.Add(6*time.Minute), false)
	if !ok || len(retry.ConditionIDs) != 2 || len(retry.Alerts) != 2 || len(retry.scope) != 2 {
		t.Fatal("retry lost daily data or included permanent/delivery errors", retry, ok)
	}
	retry, ok = retryLiveAlertConfig(config, err, asOf.UnixMilli(), asOf.Add(2*time.Hour), false)
	if !ok || len(retry.scope) != 1 || retry.scope[0].Period != "d1" {
		t.Fatal("hourly recovery did not expire independently", retry, ok)
	}
	if _, ok = retryLiveAlertConfig(config, err, asOf.UnixMilli(), asOf.Add(24*time.Hour), false); ok {
		t.Fatal("daily recovery exceeded its bound")
	}
	if _, ok = retryLiveAlertConfig(config, err, asOf.Add(23*time.Hour).UnixMilli(), asOf.Add(24*time.Hour), false); ok {
		t.Fatal("a later hourly boundary extended yesterday's daily recovery deadline")
	}
	if len(config.Alerts) != 4 {
		t.Fatal("retry mutated the saved list")
	}
	if _, ok := retryLiveAlertConfig(config, err, asOf.UnixMilli(), asOf.Add(6*time.Minute), true); ok {
		t.Fatal("manual retry could hold unrelated automatic schedules for a day")
	}
}

func TestDailyNetworkRetryKeepsNewlyDueNamedHourlyAlertIndependent(t *testing.T) {
	_, l, saved, adapter := midnightAlertFixture(t)
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(day.Add(-time.Minute).UnixMilli())
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	adapter.outage.Store(true)
	l.publicData.adapters["bybit"] = &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTCUSDT", Market: "spot", Availability: "available"}}}
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{
		{ID: strings.Repeat("a", 32), Name: "Daily DNS recovery", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true},
		{ID: strings.Repeat("b", 32), Name: "Hourly independent source", ConditionID: saved.ID, Period: "h1", DataSource: "bybit", Enabled: true},
	}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int64
	l.send = func(_ context.Context, alert LiveAlert) error {
		if alert.DataSource != "bybit" {
			t.Errorf("unavailable source delivered a signal: %s", alert.DataSource)
		}
		sends.Add(1)
		return nil
	}
	l.Start(context.Background())
	clock.Store(day.UnixMilli())
	l.signal()
	waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && info.Sent == 1 && info.Status == "Waiting to retry closed-bar check"
	})
	clock.Store(day.Add(time.Hour).UnixMilli())
	l.signal()
	info := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return !info.Running && info.LastRun == clock.Load() && info.Sent == 2 })
	if sends.Load() != 2 || info.NextRun != day.Add(time.Hour+liveAlertRetryDelay).UnixMilli() {
		t.Fatal("daily retry swallowed the next named hourly schedule", info, sends.Load())
	}
	l.mu.Lock()
	retry := copyLiveAlertsConfig(*l.retryConfig)
	l.mu.Unlock()
	if len(retry.Alerts) != 1 || retry.Alerts[0].ID != config.Alerts[0].ID {
		t.Fatal("successful hourly alert remained in the recovery subset", retry)
	}
}

func TestMidnightDailyCandlePublicationRecoversAfterFiveMinutesAndSkipsCompletedAsset(t *testing.T) {
	_, l, saved, adapter := midnightAlertFixture(t)
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(day.Add(-time.Minute).UnixMilli())
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	btc := &publicAlertFakeAdapter{price: 100}
	eth := &publicAlertFakeAdapter{price: 200, pending: true}
	adapter.items = append(adapter.items, domain.Instrument{NativeID: "ETH", Market: "perpetual", Availability: "available"})
	adapter.historyByInstrument = map[string]*publicAlertFakeAdapter{"BTC": btc, "ETH": eth}
	adapter.beforeHistory = func() { clock.Store(day.Add(6 * time.Minute).UnixMilli()) }
	entry := LiveAlertDefinition{ID: strings.Repeat("d", 32), Name: "Delayed daily candle", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true}
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int64
	l.send = func(_ context.Context, alert LiveAlert) error {
		if alert.Time != day.Add(-24*time.Hour).UnixMilli() {
			t.Errorf("substituted another daily candle: %d", alert.Time)
		}
		sends.Add(1)
		return nil
	}
	l.Start(context.Background())
	clock.Store(day.UnixMilli())
	l.signal()
	info := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && info.Status == "Waiting to retry closed-bar check"
	})
	if info.Sent != 1 || sends.Load() != 1 || info.Checks[0].ProgressState != "queued" {
		t.Fatal("unpublished daily candle was discarded or healthy asset blocked", info, sends.Load())
	}
	before := len(btc.historyKeys)
	eth.pending = false
	clock.Store(info.NextRun)
	l.signal()
	info = waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return !info.Running && info.Sent == 2 && info.LastRun == clock.Load() })
	if sends.Load() != 2 || len(btc.historyKeys) != before || info.Error != "" || info.Checks[0].SentAlerts != 2 {
		t.Fatal("recovery repeated successful history or delivery", info, sends.Load(), len(btc.historyKeys), before)
	}
}
