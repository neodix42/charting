package screener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func liveAlertsFixture(t *testing.T) (*Manager, *LiveAlerts, LiveAlertsConfig, string) {
	t.Helper()
	m := newManager(t.TempDir(), func() (workerRuntime, error) { return workerRuntime{}, errors.New("test runtime unavailable") })
	t.Cleanup(m.Close)
	if err := os.MkdirAll(m.directory, 0700); err != nil {
		t.Fatal(err)
	}
	name := "eurusd-h1-2024-01-01-2024-01-03.csv"
	path := filepath.Join(m.directory, name)
	if err := os.WriteFile(path, []byte("timestamp,open,high,low,close,volume,sma_2\n1704067200000,10,12,9,11,1,\n1704070800000,11,14,10,13,1,12\n1704074400000,13,16,12,15,1,14\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request := defaultAnalysisRequest()
	request.TradeSimulation = &AnalysisTradeSimulation{StopLoss: "low", StopLossBarsAgo: 1, TakeProfitMultiple: 2, Duration: 3}
	condition, err := m.SaveAnalysisCondition("Latest close positive", request)
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	l.refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
		out := map[string]bool{}
		for _, group := range groups {
			match := downloadFilename.FindStringSubmatch(group.name)
			out[match[1]+"/"+match[2]] = true
		}
		return out, ctx.Err()
	}
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{condition.ID}, Periods: []string{"h1"}, Symbols: []string{"eurusd"}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	return m, l, config, path
}

func TestLiveAlertsLatestClosedAndDurableDedup(t *testing.T) {
	m, l, config, path := liveAlertsFixture(t)
	var alerts []LiveAlert
	l.send = func(ctx context.Context, alert LiveAlert) error { alerts = append(alerts, alert); return nil }
	// The third row is still open. Only the second row may become a signal.
	asOf := time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()
	if err := l.check(context.Background(), config, asOf); err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].Time != 1704070800000 || alerts[0].EntryPrice != 13 || alerts[0].StopLoss == nil || *alerts[0].StopLoss != 9 || alerts[0].TakeProfit == nil || *alerts[0].TakeProfit != 21 {
		t.Fatalf("wrong latest closed alert: %+v", alerts)
	}
	if err := l.check(context.Background(), config, asOf); err != nil || len(alerts) != 1 {
		t.Fatalf("same bar repeated: %v, %+v", err, alerts)
	}
	restored, err := NewLiveAlerts(m, l.send)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.refresh = l.refresh
	if err = restored.check(context.Background(), config, asOf); err != nil || len(alerts) != 1 {
		t.Fatalf("restart repeated the claimed bar: %v, %+v", err, alerts)
	}
	if err = restored.check(context.Background(), config, asOf+time.Hour.Milliseconds()); err != nil || len(alerts) != 2 || alerts[1].Time != 1704074400000 {
		t.Fatalf("next close not delivered once: %v, %+v", err, alerts)
	}
	info := restored.Info()
	if info.Sent != 2 || len(info.Recent) != 2 || info.Recent[0].Status != "sent" || info.Assets[0].Symbol != "eurusd" {
		t.Fatalf("wrong persisted info: %+v", info)
	}
	if info.Config.ConditionIDs[0] != config.ConditionIDs[0] || info.Config.Periods[0] != "h1" {
		t.Fatal("selection did not survive restart")
	}
	if st, err := os.Stat(path); err != nil || st.Size() == 0 {
		t.Fatal("signal evaluation changed the source file", err)
	}
}

func TestLiveAlertsOnlyNewestMatchNoHistoricalReplay(t *testing.T) {
	m, l, config, path := liveAlertsFixture(t)
	request := defaultAnalysisRequest()
	request.Conditions[0].Right.Value = 14
	saved, err := m.SaveAnalysisCondition("Above fourteen", request)
	if err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = []string{saved.ID}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	calls := 0
	l.send = func(context.Context, LiveAlert) error { calls++; return nil }
	// First observe a non-match. Later correcting the same historical bar must
	// not retrospectively generate an alert for its already-processed time.
	asOf := time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()
	if err = l.check(context.Background(), config, asOf); err != nil || calls != 0 {
		t.Fatal(err, calls)
	}
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), "11,14,10,13,1,12", "11,16,10,15,1,12", 1))
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = l.check(context.Background(), config, asOf); err != nil || calls != 0 {
		t.Fatal("old non-match was replayed", err, calls)
	}
	if err = l.check(context.Background(), config, asOf+time.Hour.Milliseconds()); err != nil || calls != 1 {
		t.Fatal("next new bar did not match", err, calls)
	}
}

func TestLiveAlertsAmbiguousSendClaimSurvivesRestart(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	calls := 0
	l.send = func(ctx context.Context, alert LiveAlert) error {
		calls++
		// The durable claim is visible even before the callback returns.
		var saved liveAlertsState
		if err := readState(l.path, liveAlertsStateBytes, &saved); err != nil || len(saved.Watermarks) != 1 || saved.Recent[0].Status != "claimed" {
			t.Fatal("send ran before durable claim", err, saved)
		}
		return errors.New("Telegram delivery is uncertain")
	}
	asOf := time.Date(2024, 1, 1, 3, 30, 0, 0, time.UTC).UnixMilli()
	if err := l.check(context.Background(), config, asOf); err == nil || calls != 1 {
		t.Fatal("uncertain delivery not surfaced", err, calls)
	}
	restored, err := NewLiveAlerts(m, l.send)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restored.refresh = l.refresh
	if err = restored.check(context.Background(), config, asOf); err != nil || calls != 1 {
		t.Fatal("uncertain delivery repeated after restart", err, calls)
	}
	if restored.Info().Recent[0].Status != "uncertain" {
		t.Fatal("uncertain outcome disappeared")
	}
}

func TestLiveAlertsDeliveryFailureStopsFurtherClaimsUntilNextCheck(t *testing.T) {
	for _, failOn := range []int{1, 2} {
		t.Run(fmt.Sprintf("failure-on-delivery-%d", failOn), func(t *testing.T) {
			m, l, config, path := liveAlertsFixture(t)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeDataFile(t, m, "btcusd-h1-2024-01-01-2024-01-03.csv", string(data))
			second, err := m.SaveAnalysisCondition("Second matching condition", defaultAnalysisRequest())
			if err != nil {
				t.Fatal(err)
			}
			config.ConditionIDs = append(config.ConditionIDs, second.ID)
			config.Symbols = []string{"btcusd", "eurusd"}
			if _, err = l.Configure(config); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("Telegram operation failed (rateLimit)")
			attempted := map[string]bool{}
			calls := 0
			l.send = func(_ context.Context, alert LiveAlert) error {
				calls++
				attempted[alert.ConditionID+"/"+alert.Symbol+"/"+alert.Timeframe] = true
				if calls == failOn {
					return failure
				}
				return nil
			}
			asOf := time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()
			if err = l.check(context.Background(), config, asOf); !errors.Is(err, failure) || calls != failOn {
				t.Fatalf("delivery failure did not stop further requests: %v calls=%d", err, calls)
			}
			if len(l.state.Watermarks) != failOn || l.state.Sent != int64(failOn-1) || len(l.state.Recent) != failOn {
				t.Fatalf("remaining signals were claimed or previous successes lost: %+v", l.state)
			}
			for key := range l.state.Watermarks {
				if !attempted[key] {
					t.Fatalf("unattempted signal was claimed: %s", key)
				}
			}
			// The failed attempt remains claimed across restart, while later
			// conditions and assets can still alert for this same latest bar.
			retryCalls := 0
			restored, err := NewLiveAlerts(m, func(_ context.Context, alert LiveAlert) error {
				retryCalls++
				if attempted[alert.ConditionID+"/"+alert.Symbol+"/"+alert.Timeframe] {
					t.Fatal("retry repeated an already attempted signal")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			restored.refresh = l.refresh
			if err = restored.check(context.Background(), config, asOf); err != nil || retryCalls != 4-failOn || len(restored.state.Watermarks) != 4 || restored.state.Sent != 3 {
				t.Fatalf("remaining signals were not retained for a later check: %v calls=%d state=%+v", err, retryCalls, restored.state)
			}
		})
	}
}

func TestLiveAlertsClaimWriteFailurePreventsSend(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	calls := 0
	l.send = func(context.Context, LiveAlert) error { calls++; return nil }
	l.path = filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(l.path, 0700); err != nil {
		t.Fatal(err)
	}
	err := l.check(context.Background(), config, time.Date(2024, 1, 1, 3, 30, 0, 0, time.UTC).UnixMilli())
	if err == nil || calls != 0 || len(l.state.Watermarks) != 0 {
		t.Fatalf("claim failure sent notification or changed memory: %v, %d, %+v", err, calls, l.state.Watermarks)
	}
}

func TestLiveAlertsFailedNonMatchFlushRetriesWithoutChangingBar(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	request := defaultAnalysisRequest()
	request.Conditions[0].Right.Value = 1000
	saved, err := m.SaveAnalysisCondition("No match", request)
	if err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = []string{saved.ID}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	originalPath := l.path
	l.path = t.TempDir()
	asOf := time.Date(2024, 1, 1, 3, 30, 0, 0, time.UTC).UnixMilli()
	if err = l.check(context.Background(), config, asOf); err == nil || !l.watermarksDirty {
		t.Fatal("unsaved no-match watermarks were discarded", err)
	}
	l.path = originalPath
	if err = l.check(context.Background(), config, asOf); err != nil || l.watermarksDirty {
		t.Fatal("unchanged bar did not retry its pending flush", err)
	}
	var persisted liveAlertsState
	if err = readState(l.path, liveAlertsStateBytes, &persisted); err != nil || len(persisted.Watermarks) != 1 {
		t.Fatal("retry did not save no-match watermark", err, persisted)
	}
}

func TestLiveAlertsInvalidStatePreservedAndDoesNotRun(t *testing.T) {
	m := newManager(t.TempDir(), func() (workerRuntime, error) { return workerRuntime{}, errors.New("offline") })
	defer m.Close()
	path := filepath.Join(m.profile, "screener-live-alerts.json")
	original := []byte(`{"version":999,"config":{"enabled":true},"future":"keep"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	l, err := NewLiveAlerts(m, nil)
	if err == nil || l == nil || l.Info().Error == "" {
		t.Fatal("invalid config did not produce usable blocked service", err)
	}
	defer l.Close()
	l.Start(context.Background())
	if _, err = l.Configure(LiveAlertsConfig{}); err == nil {
		t.Fatal("overwrote invalid state")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) || l.started {
		t.Fatal("corrupt/future state was changed or started")
	}
}

func TestLiveAlertsClosedLifecycleAndSerializedMutations(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	started, released := make(chan struct{}), make(chan struct{})
	var sends atomic.Int64
	l.send = func(context.Context, LiveAlert) error { sends.Add(1); return nil }
	l.refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
		close(started)
		<-ctx.Done()
		close(released)
		return nil, ctx.Err()
	}
	l.Start(context.Background())
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("enabled schedule did not start")
	}
	if _, err := m.StartAnalysis(defaultAnalysisRequest()); err == nil {
		t.Fatal("historical Analysis overlapped Live Alerts")
	}
	if _, err := m.ConfigureDirectory(t.TempDir()); err == nil {
		t.Fatal("storage change overlapped Live Alerts")
	}
	if _, err := m.StartRefresh(); err == nil {
		t.Fatal("manual refresh overlapped Live Alerts")
	}
	if _, err := l.CheckNow(); err == nil {
		t.Fatal("duplicate live check accepted")
	}
	config.Enabled = false
	if _, err := l.Configure(config); err != nil {
		t.Fatal("could not stop enabled check", err)
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("disable did not cancel source work")
	}
	l.Close()
	if sends.Load() != 0 || m.liveActive {
		t.Fatal("cancelled check sent or retained source reservation")
	}
	if _, err := l.CheckNow(); err == nil {
		t.Fatal("closed scheduler accepted work")
	}
}

func TestLiveAlertsScheduleUTCAndProviderDelay(t *testing.T) {
	zone := time.FixedZone("Dubai", 4*3600)
	for _, tc := range []struct {
		now     time.Time
		periods []string
		want    string
	}{
		{time.Date(2026, 10, 28, 4, 0, 0, 0, zone), []string{"d1"}, "2026-10-28T00:00:30Z"},
		{time.Date(2026, 10, 28, 4, 0, 30, 0, zone), []string{"d1"}, "2026-10-29T00:00:30Z"},
		{time.Date(2026, 10, 28, 9, 59, 59, 0, zone), []string{"h1", "d1"}, "2026-10-28T06:00:30Z"},
	} {
		if got := nextLiveAlertCheck(tc.now, tc.periods).Format(time.RFC3339); got != tc.want {
			t.Errorf("next check %s != %s", got, tc.want)
		}
	}
}

func TestLiveAlertsDefaultsDraftSelectionAndCopiedInfo(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	config.Enabled = false
	config.Periods = []string{"d1", "h1"}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	// Only downloaded intersections are evaluated; a d1 choice does not create
	// a fresh un-enriched history for this h1-only asset.
	config.Enabled = true
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	info := l.Info()
	info.Config.Symbols[0] = "btcusd"
	info.Assets[0].Periods[0] = "bad"
	if l.Info().Config.Symbols[0] != "eurusd" || !reflect.DeepEqual(l.Info().Assets[0].Periods, []string{"h1"}) {
		t.Fatal("Info returned mutable manager configuration")
	}
	if _, err := l.CheckNow(); err == nil {
		t.Fatal("queued check allowed duplicate trigger")
	}
	fresh, err := NewLiveAlerts(newManager(t.TempDir(), func() (workerRuntime, error) { return workerRuntime{}, errors.New("offline") }), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.manager.Close()
	defer fresh.Close()
	if fresh.Info().Config.Enabled || len(fresh.Info().Config.ConditionIDs) != 0 || len(fresh.Info().Config.Symbols) != 0 {
		t.Fatal("new profile enabled alerts by default")
	}
	_ = m
}

func TestLiveAlertsRestartInterruptedClaimShowsUncertain(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	state := l.state
	state.Recent = []LiveAlertOutcome{{Symbol: "eurusd", AssetName: "EUR/USD", ConditionName: "saved", Timeframe: "h1", Time: 1, Status: "claimed"}}
	if err := writeState(l.path, state); err != nil {
		t.Fatal(err)
	}
	copy, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if copy.Info().Recent[0].Status != "uncertain" || !copy.Info().Config.Enabled || !reflect.DeepEqual(copy.Info().Config, config) {
		t.Fatal("restart did not preserve claim/config")
	}
	encoded, err := json.Marshal(copy.Info())
	if err != nil || !strings.Contains(string(encoded), "conditionIds") {
		t.Fatal("public JSON contract", err)
	}
}

func TestLiveAlertsProductionRefreshConsolidatesAndEnrichesBeforeLatestCheck(t *testing.T) {
	m, trace, _ := refreshManager(t, false)
	seedDuplicateHistories(t, m)
	unselectedName := "audusd-d1-2024-01-01-2024-01-04.csv"
	unselectedPath := writeDataFile(t, m, unselectedName, refreshCSV)
	request := defaultAnalysisRequest()
	request.Conditions[0].Left.Field = "sma_2"
	condition, err := m.SaveAnalysisCondition("Moving average positive", request)
	if err != nil {
		t.Fatal(err)
	}
	var alerts []LiveAlert
	l, err := NewLiveAlerts(m, func(ctx context.Context, alert LiveAlert) error { alerts = append(alerts, alert); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	config := LiveAlertsConfig{Enabled: true, Symbols: []string{"eurusd"}, Periods: []string{"d1"}, ConditionIDs: []string{condition.ID}}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2024, 1, 6, 0, 0, 30, 0, time.UTC).UnixMilli()
	if err = l.check(context.Background(), config, asOf); err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].Time != 1704326400000 || alerts[0].EntryPrice != 40 {
		t.Fatalf("did not inspect the newest appended/enriched canonical bar: %+v", alerts)
	}
	files, err := csvNames(m.directory)
	if err != nil || len(files) != 2 {
		t.Fatalf("did not consolidate selected duplicate histories: %v %v", files, err)
	}
	for _, name := range files {
		if name == unselectedName {
			continue
		}
		file := describeFile(m.directory, name)
		if !containsLiveValue(file.Fields, "sma_2") || !containsLiveValue(file.Fields, "ema_2") || file.LastTimestamp == nil || *file.LastTimestamp != alerts[0].Time {
			t.Fatalf("lost enriched columns or actual UTC period: %+v", file)
		}
		metadata, err := readFileMetadata(m.directory, name)
		if err != nil || !metadata.Verified || metadata.CheckedThrough < asOf || len(metadata.Recipes) != 2 {
			t.Fatalf("recipes or provider snapshot not retained: %+v %v", metadata, err)
		}
	}
	unselected, err := os.ReadFile(unselectedPath)
	if err != nil || string(unselected) != refreshCSV {
		t.Fatal("unselected asset was changed", err)
	}
	requests, _ := os.ReadFile(trace)
	if strings.Contains(string(requests), "AUD") || !strings.Contains(string(requests), "EUR") {
		t.Fatalf("wrong provider selection: %s", requests)
	}
	assertNoRefreshScratch(t, m.directory)
	if err = l.check(context.Background(), config, asOf); err != nil || len(alerts) != 1 {
		t.Fatal("unchanged latest candle repeated", err, alerts)
	}
}

func TestLiveAlertInvalidLongTradeLevelsAreUnavailable(t *testing.T) {
	m := analysisTestManager(t)
	s := reviewLiveSeries(t, m, "eurusd-h1-2024-01-01-2024-01-02.csv", "timestamp,open,high,low,close,zero,missing\n1704067200000,10,12,9,11,0,\n")
	row, err := s.row(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, stop, target string
		multiple           int
		hasStop, hasTarget bool
	}{
		{"valid-zero-stop", "zero", "high", 0, true, true},
		{"stop-above-entry", "high", "high", 0, false, true},
		{"stop-at-entry", "close", "high", 0, false, true},
		{"target-below-entry", "low", "low", 0, true, false},
		{"target-at-entry", "low", "close", 0, true, false},
		{"empty-levels", "missing", "missing", 0, false, false},
		{"risk-needs-valid-stop", "high", "", 2, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := SavedAnalysisCondition{ID: strings.Repeat("a", 32), Name: "levels", Request: AnalysisRequest{TradeSimulation: &AnalysisTradeSimulation{StopLoss: tc.stop, TakeProfit: tc.target, TakeProfitMultiple: tc.multiple, Duration: 1}}}
			alert, err := liveAlertFromMatch(saved, s, 0, row)
			if err != nil || (alert.StopLoss != nil) != tc.hasStop || (alert.TakeProfit != nil) != tc.hasTarget {
				t.Fatalf("invalid or missing level was exposed: %+v %v", alert, err)
			}
			if tc.name == "valid-zero-stop" && *alert.StopLoss != 0 {
				t.Fatal("zero-valued valid stop was dropped")
			}
		})
	}
}
