package jforex

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
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

func TestLegacyCatalogRefreshesMarketHoursOnceAcrossSelectionsAndRestarts(t *testing.T) {
	m, _ := testManager(t, "all-assets")
	want, err := m.Instruments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Dir(m.path)
	path := filepath.Join(m.cachePath(m.config), "instruments.json")
	m.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot savedInstrumentCatalog
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	// A catalog fetched moments ago by an older build must still upgrade once.
	snapshot.MarketHoursVersion = 1
	snapshot.FetchedAt = time.Now().UnixMilli()
	data, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	gate := t.TempDir()
	t.Setenv("CRYPTODESK_JFOREX_CATALOG_GATE", gate)
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("CRYPTODESK_JFOREX_PROTOCOL_TRACE", trace)
	restored, count := restoredCatalogManager(t, profile, "catalog-market-hours-gated")
	if items, err := restored.Instruments(context.Background()); err != nil || !reflect.DeepEqual(items, want) || count.Load() != 0 {
		t.Fatal("legacy metadata blocked the saved catalog", items, err, count.Load())
	}
	results := make(chan error, 8)
	for range 8 {
		go func() {
			items, err := restored.RefreshInstruments(context.Background(), false)
			if err == nil && (len(items) != 2 || items[0].ProviderMetadata["marketHoursStatus"] != "unavailable" || items[1].ProviderMetadata["marketHoursOpen"] != "08:12:34") {
				err = errors.New("regular-session metadata was not returned")
			}
			results <- err
		}()
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(gate, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("legacy catalog did not refresh")
		}
		time.Sleep(time.Millisecond)
	}
	if items, err := restored.Instruments(context.Background()); err != nil || !reflect.DeepEqual(items, want) {
		t.Fatal("refresh hid the legacy catalog", items, err)
	}
	if err := os.WriteFile(filepath.Join(gate, "release"), []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	operations, err := os.ReadFile(trace)
	if err != nil || count.Load() != 1 || strings.Count(string(operations), "instruments\n") != 1 {
		t.Fatal("selections repeated the metadata upgrade", count.Load(), string(operations), err)
	}
	upgraded, err := restored.Instruments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	restored.Close()
	reopened, reopenedCount := restoredCatalogManager(t, profile, "error")
	items, err := reopened.RefreshInstruments(context.Background(), false)
	if err != nil || !reflect.DeepEqual(items, upgraded) || reopenedCount.Load() != 0 {
		t.Fatal("metadata upgrade or unavailable hours were not persisted", items, err, reopenedCount.Load())
	}
}

func TestInstrumentCatalogHoursFreshnessRetriesColdAndExpiredSchedules(t *testing.T) {
	now := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		version   int
		age       time.Duration
		metadata  []map[string]string
		wantFresh bool
	}{
		{"version one upgrades", 1, 0, nil, false},
		{"normal metadata stays fresh", instrumentMarketHoursVersion, 2 * time.Minute, nil, true},
		{"cold unavailable has short grace", instrumentMarketHoursVersion, 30 * time.Second, []map[string]string{{"marketHoursStatus": "unavailable"}}, true},
		{"all cold hours retry early", instrumentMarketHoursVersion, 2 * time.Minute, []map[string]string{{"marketHoursStatus": "unavailable"}}, false},
		{"one unknown does not expire usable hours", instrumentMarketHoursVersion, 2 * time.Minute, []map[string]string{{"marketHoursStatus": "unavailable"}, {"marketHoursStatus": "available"}}, true},
		{"mixed pending metadata retries in five minutes", instrumentMarketHoursVersion, 6 * time.Minute, []map[string]string{{"marketHoursStatus": "unavailable"}, {"marketHoursStatus": "available"}}, false},
		{"complete schedules retain normal hourly freshness", instrumentMarketHoursVersion, 30 * time.Minute, []map[string]string{{"marketHoursStatus": "available", "marketHoursSource": "JForex regular market session", "marketHoursFirstDay": "1", "marketHoursLastDay": "5", "marketHoursOpen": "09:00:00", "marketHoursClose": "17:00:00"}}, true},
		{"retained schedules keep a five minute grace", instrumentMarketHoursVersion, 2 * time.Minute, []map[string]string{{"marketHoursStatus": "available", "marketHoursRetained": "true"}}, true},
		{"retained schedules retry before an hour", instrumentMarketHoursVersion, 6 * time.Minute, []map[string]string{{"marketHoursStatus": "available", "marketHoursRetained": "true"}}, false},
		{"equal-clock regular envelope retries calendar soon", instrumentMarketHoursVersion, 6 * time.Minute, []map[string]string{{"marketHoursStatus": "available", "marketHoursSource": "JForex regular market session", "marketHoursFirstDay": "1", "marketHoursLastDay": "5", "marketHoursOpen": "00:00:00", "marketHoursClose": "00:00:00"}}, false},
		{"wrapped regular envelope retries calendar soon", instrumentMarketHoursVersion, 6 * time.Minute, []map[string]string{{"marketHoursStatus": "available", "marketHoursSource": "JForex regular market session", "marketHoursFirstDay": "7", "marketHoursLastDay": "5", "marketHoursOpen": "09:00:00", "marketHoursClose": "17:00:00"}}, false},
		{"expired calendar retries even when recently fetched", instrumentMarketHoursVersion, 0, []map[string]string{{"marketHoursStatus": "available", "marketHoursSource": "JForex instrument calendar", "marketHoursTo": "1795262400000"}}, false},
		{"calendar without valid horizon retries", instrumentMarketHoursVersion, 0, []map[string]string{{"marketHoursStatus": "available", "marketHoursSource": "JForex instrument calendar", "marketHoursTo": "invalid"}}, false},
		{"upcoming calendar stays fresh", instrumentMarketHoursVersion, 2 * time.Minute, []map[string]string{{"marketHoursStatus": "available", "marketHoursSource": "JForex instrument calendar", "marketHoursTo": "1795953600000"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := &savedInstrumentCatalog{MarketHoursVersion: tc.version, FetchedAt: now.Add(-tc.age).UnixMilli()}
			for _, metadata := range tc.metadata {
				snapshot.Items = append(snapshot.Items, domain.Instrument{ProviderMetadata: metadata})
			}
			if got := instrumentCatalogFresh(snapshot, now); got != tc.wantFresh {
				t.Fatalf("fresh=%v, want %v", got, tc.wantFresh)
			}
		})
	}
}

func regularCatalogHours() map[string]string {
	return map[string]string{
		"marketHoursStatus": "available", "marketHoursSource": "JForex regular market session",
		"marketHoursTimeZone": "America/New_York", "marketHoursFirstDay": "1", "marketHoursLastDay": "5",
		"marketHoursOpen": "09:30:00", "marketHoursClose": "16:00:00",
	}
}

func catalogHoursSnapshot(now time.Time, metadata map[string]string) *savedInstrumentCatalog {
	return &savedInstrumentCatalog{Version: 1, Account: "account-a", Environment: "demo", Capability: bridgeInstrumentCatalog,
		FetchedAt: now.Add(-time.Hour).UnixMilli(), MarketHoursVersion: instrumentMarketHoursVersion,
		Items: []domain.Instrument{{NativeID: "AAPLUSUSD", Market: "cfd", BaseAsset: "AAPL", QuoteAsset: "USD", ProviderMetadata: metadata}}}
}

func TestCatalogHoursRetainsCompleteScheduleWithoutMixingMetadataOrMutatingInputs(t *testing.T) {
	now := time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)
	previous := catalogHoursSnapshot(now, regularCatalogHours())
	previous.Items[0].ProviderMetadata["description"] = "old description"
	fresh := []domain.Instrument{{NativeID: "AAPLUSUSD", Market: "cfd", BaseAsset: "NEW", QuoteAsset: "USD", PriceIncrement: "0.001", SupportedIntervals: []string{"1m"}, ProviderMetadata: map[string]string{
		"description": "new description", "marketHoursStatus": "unavailable", "marketHoursFrom": "partial-new-calendar", "marketHoursNewPartialField": "must not survive",
	}}}
	previousBefore, freshBefore := cloneInstruments(previous.Items), cloneInstruments(fresh)
	got := mergeInstrumentCatalogHours(previous, fresh, "account-a", "demo", now)
	metadata := got[0].ProviderMetadata
	if metadata["marketHoursRetained"] != "true" || metadata["marketHoursNotice"] == "" || metadata["marketHoursFetchedAt"] != strconv.FormatInt(previous.FetchedAt, 10) || metadata["marketHoursOpen"] != "09:30:00" {
		t.Fatal("complete prior hours were lost or not identified as retained", metadata)
	}
	if _, present := metadata["marketHoursFrom"]; present {
		t.Fatal("old and fresh market-hours groups were mixed", metadata)
	}
	if _, present := metadata["marketHoursNewPartialField"]; present {
		t.Fatal("partial new schedule field survived retention", metadata)
	}
	if metadata["description"] != "new description" || got[0].BaseAsset != "NEW" || got[0].PriceIncrement != "0.001" || len(got) != 1 {
		t.Fatal("fresh non-hours metadata was replaced", got)
	}
	metadata["marketHoursOpen"] = "changed"
	got[0].SupportedIntervals[0] = "changed"
	if !reflect.DeepEqual(previous.Items, previousBefore) || !reflect.DeepEqual(fresh, freshBefore) {
		t.Fatal("merge or returned catalog changed an input map or slice")
	}
}

func TestCatalogHoursFreshScheduleReplacesRetainedHoursAndResetsSourceTime(t *testing.T) {
	now := time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)
	previous := catalogHoursSnapshot(now, regularCatalogHours())
	previous.Items[0].ProviderMetadata["marketHoursRetained"] = "true"
	previous.Items[0].ProviderMetadata["marketHoursFetchedAt"] = strconv.FormatInt(now.Add(-24*time.Hour).UnixMilli(), 10)
	fresh := cloneInstruments(previous.Items)
	fresh[0].ProviderMetadata["marketHoursOpen"] = "10:00:00"
	fresh[0].ProviderMetadata["marketHoursNotice"] = "old notice"
	got := mergeInstrumentCatalogHours(previous, fresh, "account-a", "demo", now)[0].ProviderMetadata
	if got["marketHoursOpen"] != "10:00:00" || got["marketHoursFetchedAt"] != strconv.FormatInt(now.UnixMilli(), 10) || got["marketHoursRetained"] != "" || got["marketHoursNotice"] != "" {
		t.Fatal("fresh provider schedule did not replace retained metadata", got)
	}
}

func TestCatalogHoursRetainedCalendarKeepsOriginalCoverageAndSourceTime(t *testing.T) {
	now := time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)
	from, to := now.Add(-time.Hour).UnixMilli(), now.Add(time.Hour).UnixMilli()
	calendar := map[string]string{"marketHoursStatus": "available", "marketHoursSource": "JForex instrument calendar", "marketHoursTimeZone": "UTC",
		"marketHoursFrom": strconv.FormatInt(from, 10), "marketHoursTo": strconv.FormatInt(to, 10),
		"marketHoursClosedIntervals": "[[" + strconv.FormatInt(from, 10) + "," + strconv.FormatInt(now.Add(-time.Minute).UnixMilli(), 10) + "]]"}
	previous := catalogHoursSnapshot(now, calendar)
	fresh := cloneInstruments(previous.Items)
	fresh[0].ProviderMetadata = map[string]string{"marketHoursStatus": "unavailable"}
	first := mergeInstrumentCatalogHours(previous, fresh, "account-a", "demo", now)
	previous.Items, previous.FetchedAt = first, now.UnixMilli()
	second := mergeInstrumentCatalogHours(previous, fresh, "account-a", "demo", now.Add(5*time.Minute))
	if !reflect.DeepEqual(first, second) || second[0].ProviderMetadata["marketHoursTo"] != strconv.FormatInt(to, 10) || second[0].ProviderMetadata["marketHoursFetchedAt"] != strconv.FormatInt(now.Add(-time.Hour).UnixMilli(), 10) {
		t.Fatal("retry extended dated coverage or changed original hours time", second)
	}
	expired := mergeInstrumentCatalogHours(previous, fresh, "account-a", "demo", now.Add(time.Hour))
	if expired[0].ProviderMetadata["marketHoursStatus"] != "unavailable" || expired[0].ProviderMetadata["marketHoursRetained"] != "" {
		t.Fatal("expired calendar was extended", expired)
	}
}

func TestCatalogHoursRejectsIncompleteAmbiguousOrInvalidPriorSchedules(t *testing.T) {
	now := time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, key, value string
	}{
		{"unknown source", "marketHoursSource", "unknown"}, {"missing timezone", "marketHoursTimeZone", ""},
		{"invalid timezone", "marketHoursTimeZone", "Not/AZone"}, {"unknown status", "marketHoursStatus", "unavailable"},
		{"missing day", "marketHoursFirstDay", ""}, {"invalid day", "marketHoursLastDay", "9"},
		{"wrapped week", "marketHoursFirstDay", "7"}, {"equal clocks", "marketHoursClose", "09:30:00"},
		{"overnight", "marketHoursClose", "08:00:00"}, {"malformed clock", "marketHoursOpen", "9:30:00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			prior := regularCatalogHours()
			prior[test.key] = test.value
			previous := catalogHoursSnapshot(now, prior)
			fresh := cloneInstruments(previous.Items)
			fresh[0].ProviderMetadata = map[string]string{"marketHoursStatus": "unavailable"}
			if got := mergeInstrumentCatalogHours(previous, fresh, "account-a", "demo", now); got[0].ProviderMetadata["marketHoursRetained"] != "" {
				t.Fatal("unusable regular schedule was retained", got)
			}
		})
	}
	from, to := strconv.FormatInt(now.Add(-time.Hour).UnixMilli(), 10), strconv.FormatInt(now.Add(time.Hour).UnixMilli(), 10)
	for _, encoded := range []string{"", "[]", "null", "invalid", "[[1]]", "[[2,1]]", "[[1,2]]", "[[1,9007199254740992]]", "[[1,2.5]]"} {
		previous := catalogHoursSnapshot(now, map[string]string{"marketHoursStatus": "available", "marketHoursSource": "JForex instrument calendar", "marketHoursTimeZone": "UTC", "marketHoursFrom": from, "marketHoursTo": to, "marketHoursClosedIntervals": encoded})
		fresh := cloneInstruments(previous.Items)
		fresh[0].ProviderMetadata = map[string]string{"marketHoursStatus": "unavailable"}
		if got := mergeInstrumentCatalogHours(previous, fresh, "account-a", "demo", now); got[0].ProviderMetadata["marketHoursRetained"] != "" {
			t.Fatal("invalid calendar was retained", encoded, got)
		}
	}
	for _, test := range []struct{ key, value string }{
		{"marketHoursFrom", strconv.FormatInt(now.Add(time.Minute).UnixMilli(), 10)},
		{"marketHoursTo", strconv.FormatInt(now.UnixMilli(), 10)},
		{"marketHoursFrom", "invalid"}, {"marketHoursTo", "invalid"}, {"marketHoursTimeZone", "America/New_York"},
	} {
		prior := map[string]string{"marketHoursStatus": "available", "marketHoursSource": "JForex instrument calendar", "marketHoursTimeZone": "UTC", "marketHoursFrom": from, "marketHoursTo": to, "marketHoursClosedIntervals": "[[" + from + "," + to + "]]"}
		prior[test.key] = test.value
		previous := catalogHoursSnapshot(now, prior)
		fresh := cloneInstruments(previous.Items)
		fresh[0].ProviderMetadata = map[string]string{"marketHoursStatus": "unavailable"}
		if got := mergeInstrumentCatalogHours(previous, fresh, "account-a", "demo", now); got[0].ProviderMetadata["marketHoursRetained"] != "" {
			t.Fatal("calendar outside validated current coverage was retained", test, got)
		}
	}
}

func TestCatalogHoursRetentionFencesAccountsEnvironmentMarketAndRemovedInstruments(t *testing.T) {
	now := time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, account, environment, market, nativeID string
	}{
		{"other account", "account-b", "demo", "cfd", "AAPLUSUSD"},
		{"other environment", "account-a", "live", "cfd", "AAPLUSUSD"},
		{"other market", "account-a", "demo", "forex", "AAPLUSUSD"},
		{"removed instrument", "account-a", "demo", "cfd", "NEWUSUSD"},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := catalogHoursSnapshot(now, regularCatalogHours())
			fresh := []domain.Instrument{{NativeID: test.nativeID, Market: test.market, ProviderMetadata: map[string]string{"marketHoursStatus": "unavailable"}}}
			got := mergeInstrumentCatalogHours(previous, fresh, test.account, test.environment, now)
			if !reflect.DeepEqual(got, fresh) {
				t.Fatal("hours crossed an identity boundary or removed instrument was restored", got)
			}
		})
	}
}

func TestSuccessfulDegradedCatalogRefreshPersistsLastKnownHours(t *testing.T) {
	m, _ := testManager(t, "all-assets")
	if _, err := m.Instruments(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	snapshot := *m.catalog
	snapshot.Items = cloneInstruments(snapshot.Items)
	snapshot.FetchedAt = time.Now().Add(-time.Hour).UnixMilli()
	for index := range snapshot.Items {
		if snapshot.Items[index].NativeID == "USDJPY" {
			snapshot.Items[index].ProviderMetadata = regularCatalogHours()
		}
	}
	m.catalog = &snapshot
	if err := m.writeInstrumentCatalogLocked(snapshot); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	m.mu.Unlock()
	items, err := m.RefreshInstruments(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range items {
		if item.NativeID == "USDJPY" {
			found = item.ProviderMetadata["marketHoursRetained"] == "true" && item.ProviderMetadata["marketHoursFetchedAt"] == strconv.FormatInt(snapshot.FetchedAt, 10)
		}
	}
	if !found {
		t.Fatal("successful cold refresh replaced the previous usable schedule", items)
	}
	profile := filepath.Dir(m.path)
	m.Close()
	restored, count := restoredCatalogManager(t, profile, "error")
	reopened, err := restored.RefreshInstruments(context.Background(), false)
	if err != nil || count.Load() != 0 || !reflect.DeepEqual(reopened, items) {
		t.Fatal("retained schedule was not persisted or short grace caused a login", reopened, err, count.Load())
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
