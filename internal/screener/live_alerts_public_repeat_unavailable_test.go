package screener

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

// Keep older warm-up reads reusable, while an unavailable latest candle gets a
// bounded fresh probe rather than being pinned until the next UTC cutoff.
func TestManualPublicUnavailableHistoryReusesWarmupAndRechecksLatest(t *testing.T) {
	for _, scenario := range []string{"daily-missing-provisional-prefix", "weekly-missing-provisional-prefix", "weekly-short-listing"} {
		t.Run(scenario, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			const day = int64(86400)
			end := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC).Unix()
			period, interval := "d1", day
			if scenario != "daily-missing-provisional-prefix" {
				period, interval = "w1", 7*day
			}
			asset := publicAlertAsset{NativeID: "xyz:REPEAT-UNAVAILABLE", Market: "perpetual"}
			count := 40
			if scenario == "weekly-short-listing" {
				count = 13
			}
			daily := retainedDaily(end-int64(count)*day, count)
			if scenario != "weekly-short-listing" {
				daily = daily[:len(daily)-1]
			}
			if scenario == "daily-missing-provisional-prefix" {
				daily[0].Quality = "provisional"
			}
			if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", daily); err != nil {
				t.Fatal(err)
			}
			if period == "w1" {
				if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "w1", hyperliquidWeeklyFromDaily(daily)); err != nil {
					t.Fatal(err)
				}
			}
			available := map[int64]domain.Candle{}
			for _, bar := range daily {
				available[bar.Time] = bar
			}
			fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
			adapter := filteredPublicAlertHistory{Adapter: fake, filter: func(rows []domain.Candle, _, _ int64) []domain.Candle {
				filtered := rows[:0]
				for _, row := range rows {
					if bar, ok := available[row.Time]; ok {
						filtered = append(filtered, bar)
					}
				}
				return filtered
			}}
			var clock atomic.Int64
			clock.Store((end + 10*3600) * 1000)
			condition := publicWarmupCondition(period, "sma_2")
			// This context is set by the manual public-check production path. Automatic
			// publication retries keep their independent freshness/recovery behavior.
			ctx := context.WithValue(context.Background(), publicAlertReuseClosedHistoryKey{}, true)
			newOwner := func() *LiveAlerts {
				owner := newPublicAlertTestLive(t, m, "hyperliquid", fake)
				owner.now = func() time.Time { return time.UnixMilli(clock.Load()) }
				owner.publicData.adapters["hyperliquid"] = adapter
				t.Cleanup(owner.Close)
				return owner
			}
			owner := newOwner()
			prepare := func(owner *LiveAlerts, cutoff int64) string {
				t.Helper()
				batch, err := owner.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{condition}, map[string][]string{condition.ID: {period}}, cutoff*1000)
				defer batch.close()
				if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
					t.Fatalf("unavailable history became usable or failed globally: files=%v failures=%v err=%v", batch.Files, batch.Failures, err)
				}
				var text string
				for _, failure := range batch.Failures {
					text = failure.Error()
					if scenario != "weekly-short-listing" && !errors.Is(failure, errLiveAlertsPendingBar) {
						t.Fatalf("missing current candle lost its status: %v", failure)
					}
				}
				if len(owner.state.Watermarks) != 0 || owner.state.Sent != 0 || len(owner.state.Recent) != 0 {
					t.Fatal("unavailable reuse invented claims or delivery", owner.state)
				}
				return text
			}
			initial := prepare(owner, end)
			calls := len(fake.ranges)
			if calls == 0 {
				t.Fatal("initial unavailable history was never inspected")
			}
			clock.Add(10 * time.Minute.Milliseconds())
			if got := prepare(owner, end); got != initial {
				t.Fatalf("repeat changed the known outcome:\n%s\n%s", initial, got)
			}
			if scenario == "weekly-short-listing" {
				if len(fake.ranges) != calls {
					t.Fatal("complete latest with insufficient older warm-up was downloaded again", fake.ranges[calls:])
				}
			} else {
				requireNarrowPublicLatestRecheck(t, fake.ranges[calls:], end, interval)
			}
			calls = len(fake.ranges)
			owner.Close()
			owner = newOwner()
			clock.Add(2 * time.Hour.Milliseconds())
			if got := prepare(owner, end); got != initial {
				t.Fatalf("restart changed the known outcome:\n%s\n%s", initial, got)
			}
			if scenario == "weekly-short-listing" {
				if len(fake.ranges) != calls {
					t.Fatal("restart discarded successful older-history warm-up evidence", fake.ranges[calls:])
				}
			} else {
				requireNarrowPublicLatestRecheck(t, fake.ranges[calls:], end, interval)
			}
			calls = len(fake.ranges)

			if period == "d1" {
				// A changed retained CSV invalidates exact-cutoff read evidence,
				// even when it does not publish the missing latest candle.
				correction := daily[1]
				correction.Close += .25
				correction.High += .25
				if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", []domain.Candle{correction}); err != nil {
					t.Fatal(err)
				}
				prepare(owner, end)
				if len(fake.ranges) == calls {
					t.Fatal("changed retained bytes reused obsolete data-read evidence")
				}
				calls = len(fake.ranges)
			}

			// A changed UTC close must fetch again rather than carry an old absence or
			// insufficient-history result into a different signal candle.
			next := end + interval
			clock.Store((next + 10*3600) * 1000)
			prepare(owner, next)
			if len(fake.ranges) == calls {
				t.Fatal("next closed candle reused the previous close's data-read evidence")
			}
		})
	}
}

func TestManualWeeklyUnavailableMemoObservesImportedDailyPublication(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	const day = int64(86400)
	end := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC).Unix()
	asset := publicAlertAsset{NativeID: "xyz:WEEK-PUBLICATION", Market: "perpetual"}
	// Start exactly on Monday so that only the missing final Sunday prevents
	// three real retained weeks from being authoritative.
	daily := retainedDaily(end-21*day, 21)
	latest := daily[len(daily)-1]
	daily = daily[:len(daily)-1]
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", daily); err != nil {
		t.Fatal(err)
	}
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	owner := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	t.Cleanup(owner.Close)
	owner.now = func() time.Time { return time.Unix(end+10*3600, 0) }
	owner.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func([]domain.Candle, int64, int64) []domain.Candle { return nil }}
	ctx := context.WithValue(context.Background(), publicAlertReuseClosedHistoryKey{}, true)
	condition := publicWarmupCondition("w1", "sma_2")
	prepare := func() publicAlertBatch {
		t.Helper()
		batch, err := owner.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{condition}, map[string][]string{condition.ID: {"w1"}}, (end+10*3600)*1000)
		if err != nil {
			t.Fatal(err)
		}
		return batch
	}
	first := prepare()
	defer first.close()
	if len(first.Files) != 0 || len(first.Failures) != 1 {
		t.Fatal("incomplete week became usable", first)
	}
	calls := len(fake.ranges)
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", []domain.Candle{latest}); err != nil {
		t.Fatal(err)
	}
	ready := prepare()
	defer ready.close()
	if len(ready.Files) != 1 || len(ready.Failures) != 0 {
		t.Fatal("imported native daily candle did not invalidate weekly unavailability", ready)
	}
	if len(fake.ranges) != calls {
		t.Fatalf("imported complete weeks caused unnecessary provider reads: %s", fmt.Sprint(fake.ranges[calls:]))
	}
	if len(owner.state.Watermarks) != 0 || owner.state.Sent != 0 {
		t.Fatal("preparation invented a delivery claim")
	}
}

func TestManualClosedHistoryMemoLeavesEarlyPublicationRepairAvailable(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	const day = int64(86400)
	end := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC).Unix()
	asset := publicAlertAsset{NativeID: "xyz:EARLY-REPEAT", Market: "perpetual"}
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", retainedDaily(end-40*day, 39)); err != nil {
		t.Fatal(err)
	}
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	owner := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	t.Cleanup(owner.Close)
	var clock atomic.Int64
	clock.Store(end*1000 + 5000)
	owner.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	var available atomic.Bool
	owner.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func(rows []domain.Candle, _, _ int64) []domain.Candle {
		if available.Load() {
			return rows
		}
		out := rows[:0]
		for _, row := range rows {
			if row.Time != end-day {
				out = append(out, row)
			}
		}
		return out
	}}
	condition := publicWarmupCondition("d1", "sma_2")
	ctx := context.WithValue(context.Background(), publicAlertReuseClosedHistoryKey{}, true)
	prepare := func() publicAlertBatch {
		t.Helper()
		batch, err := owner.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{condition}, map[string][]string{condition.ID: {"d1"}}, clock.Load())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(batch.close)
		return batch
	}
	if first := prepare(); len(first.Files) != 0 || len(first.Failures) != 1 {
		t.Fatal(first)
	}
	calls := len(fake.ranges)
	clock.Add(29 * time.Second.Milliseconds())
	if again := prepare(); len(again.Files) != 0 || len(again.Failures) != 1 || len(fake.ranges) != calls {
		t.Fatal("near-close memo did not bound immediate retries", again, fake.ranges)
	}
	clock.Add(time.Second.Milliseconds())
	available.Store(true)
	if ready := prepare(); len(ready.Files) != 1 || len(ready.Failures) != 0 || len(fake.ranges) == calls {
		t.Fatal("near-close memo suppressed later publication", ready, fake.ranges)
	}
}

func TestFailedPublicReadDoesNotCreateManualHistoryMemo(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	const day = int64(86400)
	end := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC).Unix()
	asset := publicAlertAsset{NativeID: "xyz:FAILED-READ", Market: "perpetual"}
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", retainedDaily(end-40*day, 39)); err != nil {
		t.Fatal(err)
	}
	fake := &publicAlertFakeAdapter{price: 100, fail: errors.New("provider read failed"), items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	owner := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	t.Cleanup(owner.Close)
	var clock atomic.Int64
	clock.Store((end + 10*3600) * 1000)
	owner.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	condition := publicWarmupCondition("d1", "sma_2")
	ctx := context.WithValue(context.Background(), publicAlertReuseClosedHistoryKey{}, true)
	prepare := func() publicAlertBatch {
		t.Helper()
		batch, err := owner.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{condition}, map[string][]string{condition.ID: {"d1"}}, clock.Load())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(batch.close)
		return batch
	}
	first := prepare()
	if len(first.Files) != 0 || len(first.Failures) != 1 {
		t.Fatal(first)
	}
	_, state, _, err := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), asset, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if state.HistoryRead != nil {
		t.Fatal("failed provider read was retained as successful evidence", state.HistoryRead)
	}
	calls := len(fake.ranges)
	fake.fail = nil
	clock.Add(10 * time.Minute.Milliseconds())
	if ready := prepare(); len(ready.Files) != 1 || len(ready.Failures) != 0 || len(fake.ranges) == calls {
		t.Fatal("failed read suppressed healthy recovery", ready, fake.ranges)
	}
}

func TestManualCheckCycleRechecksOnlyUnavailableLatestDailyAndWeekly(t *testing.T) {
	for _, period := range []string{"d1", "w1"} {
		t.Run(period, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			end := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC).Unix()
			request := publicWarmupCondition(period, "sma_2").Request
			request.Conditions[0].ID = "manual-closed-repeat"
			saved, err := m.SaveAnalysisCondition("Repeat manual closed check", request)
			if err != nil {
				t.Fatal(err)
			}
			fake := &publicAlertFakeAdapter{price: 100, pending: true, items: []domain.Instrument{{NativeID: "xyz:REAL-MANUAL", Market: "perpetual", Availability: "available"}}}
			owner := newPublicAlertTestLive(t, m, "hyperliquid", fake)
			t.Cleanup(owner.Close)
			var clock atomic.Int64
			clock.Store((end + 10*3600) * 1000)
			owner.now = func() time.Time { return time.UnixMilli(clock.Load()) }
			config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Manual retained unavailable", ConditionID: saved.ID, Period: period, DataSource: "hyperliquid", Enabled: true}}}
			if _, err = owner.Configure(config); err != nil {
				t.Fatal(err)
			}
			check := func() {
				t.Helper()
				if err := owner.check(context.Background(), owner.state.Config, clock.Load()); !errors.Is(err, errLiveAlertsPendingBar) {
					t.Fatal("manual check lost missing-candle result", err)
				}
				info := owner.Info()
				if len(info.Checks) != 1 || info.Checks[0].AssetsSkipped != 1 || info.Sent != 0 || len(owner.state.Watermarks) != 0 {
					t.Fatal("manual unavailability was claimed or hidden", info)
				}
			}
			check()
			calls := len(fake.ranges)
			if calls == 0 {
				t.Fatal("first manual check did not inspect provider history")
			}
			clock.Add(10 * time.Minute.Milliseconds())
			check()
			interval := int64(86400)
			if period == "w1" {
				interval *= 7
			}
			requireNarrowPublicLatestRecheck(t, fake.ranges[calls:], end, interval)
			calls = len(fake.ranges)
			// Scheduled publication retries must retain their provider freshness
			// policy rather than inheriting a manual check's reuse preference.
			clock.Add(liveAlertRetryDelay.Milliseconds())
			if err := owner.checkCycle(context.Background(), owner.state.Config, clock.Load(), true); !errors.Is(err, errLiveAlertsPendingBar) {
				t.Fatal("scheduled publication retry lost its missing-candle result", err)
			}
			if len(fake.ranges) == calls {
				t.Fatal("manual reuse policy suppressed scheduled publication recovery")
			}
		})
	}
}

func TestManualClosedHistoryExpiresLegacyMatureMissingCandleEvidence(t *testing.T) {
	for _, period := range []string{"d1", "w1"} {
		t.Run(period, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			const day = int64(86400)
			end := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC).Unix()
			asset := publicAlertAsset{NativeID: "xyz:LEGACY-MISSING", Market: "perpetual"}
			daily := retainedDaily(end-42*day, 41)
			if period == "w1" {
				daily = retainedDaily(end-42*day, 35)
			}
			if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", daily); err != nil {
				t.Fatal(err)
			}
			if period == "w1" {
				if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "w1", hyperliquidWeeklyFromDaily(daily)); err != nil {
					t.Fatal(err)
				}
			}
			fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
			owner := newPublicAlertTestLive(t, m, "hyperliquid", fake)
			t.Cleanup(owner.Close)
			var clock atomic.Int64
			clock.Store((end + 10*3600) * 1000)
			owner.now = func() time.Time { return time.UnixMilli(clock.Load()) }
			owner.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func([]domain.Candle, int64, int64) []domain.Candle { return nil }}
			condition := publicWarmupCondition(period, "sma_2")
			prepare := func(ctx context.Context) {
				t.Helper()
				batch, err := owner.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{condition}, map[string][]string{condition.ID: {period}}, clock.Load())
				defer batch.close()
				if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
					t.Fatalf("legacy absence did not remain unavailable: %+v %v", batch, err)
				}
				for _, failure := range batch.Failures {
					if !errors.Is(failure, errLiveAlertsPendingBar) {
						t.Fatal("legacy absence lost its missing-candle classification", failure)
					}
				}
			}
			prepare(context.Background())
			root := hyperliquidHistoryRoot(m.profile)
			_, state, raw, err := readHyperliquidRaw(root, asset, period)
			if err != nil {
				t.Fatal(err)
			}
			if state.LatestMissing == nil || state.LatestMissing.CheckedAt != clock.Load() {
				t.Fatal("initial read did not establish mature successful absence evidence", state)
			}
			state.HistoryRead = nil // Simulate persisted metadata from the previous app version.
			stem := publicAlertSymbol(asset.Market, asset.NativeID) + "-" + period
			if err := publicAlertPublishRaw(filepath.Join(root, "raw", stem+".csv"), filepath.Join(root, "raw", stem+".json"), raw, state, state.Checksum); err != nil {
				t.Fatal(err)
			}
			calls := len(fake.ranges)
			clock.Add(2 * time.Hour.Milliseconds())
			prepare(context.WithValue(context.Background(), publicAlertReuseClosedHistoryKey{}, true))
			interval := day
			if period == "w1" {
				interval *= 7
			}
			requireNarrowPublicLatestRecheck(t, fake.ranges[calls:], end, interval)
			if len(owner.state.Watermarks) != 0 || owner.state.Sent != 0 {
				t.Fatal("reusing legacy absence invented processed/delivery claims")
			}
		})
	}
}

func requireNarrowPublicLatestRecheck(t *testing.T, ranges [][2]int64, end, interval int64) {
	t.Helper()
	if len(ranges) != 1 || ranges[0][0] < end-interval || ranges[0][1] != end || ranges[0][0] >= ranges[0][1] {
		t.Fatalf("expired absence must probe only its latest daily/weekly bucket: %v", ranges)
	}
}

func TestManualWeeklyShortListingReusesLegacySuccessfulBackfill(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	const day = int64(86400)
	end := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC).Unix()
	asset := publicAlertAsset{NativeID: "io:CRCL-LEGACY", Market: "perpetual"}
	daily := retainedDaily(end-13*day, 13)
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", daily); err != nil {
		t.Fatal(err)
	}
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "w1", hyperliquidWeeklyFromDaily(daily)); err != nil {
		t.Fatal(err)
	}
	available := map[int64]domain.Candle{}
	for _, bar := range daily {
		available[bar.Time] = bar
	}
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	adapter := filteredPublicAlertHistory{Adapter: fake, filter: func(rows []domain.Candle, _, _ int64) []domain.Candle {
		out := rows[:0]
		for _, row := range rows {
			if bar, ok := available[row.Time]; ok {
				out = append(out, bar)
			}
		}
		return out
	}}
	var clock atomic.Int64
	clock.Store((end + 10*3600) * 1000)
	newOwner := func() *LiveAlerts {
		owner := newPublicAlertTestLive(t, m, "hyperliquid", fake)
		owner.now = func() time.Time { return time.UnixMilli(clock.Load()) }
		owner.publicData.adapters["hyperliquid"] = adapter
		t.Cleanup(owner.Close)
		return owner
	}
	condition := publicWarmupCondition("w1", "sma_2")
	ctx := context.WithValue(context.Background(), publicAlertReuseClosedHistoryKey{}, true)
	prepare := func(owner *LiveAlerts) string {
		t.Helper()
		batch, err := owner.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{condition}, map[string][]string{condition.ID: {"w1"}}, clock.Load())
		defer batch.close()
		if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
			t.Fatalf("short listing became usable or failed globally: %+v %v", batch, err)
		}
		for _, failure := range batch.Failures {
			return failure.Error()
		}
		return ""
	}
	owner := newOwner()
	initial := prepare(owner)
	root := hyperliquidHistoryRoot(m.profile)
	rows, state, raw, err := readHyperliquidRaw(root, asset, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if state.BackfillThrough != end || state.BackfillBars < 3 || len(rows) != 2 || rows[0].Quality != "provisional" || rows[1].Quality != "authoritative" {
		t.Fatal("fixture did not reproduce the retained incomplete listing week", rows, state)
	}
	state.HistoryRead = nil
	state.OlderBackfill = nil
	stem := publicAlertSymbol(asset.Market, asset.NativeID) + "-w1"
	if err := publicAlertPublishRaw(filepath.Join(root, "raw", stem+".csv"), filepath.Join(root, "raw", stem+".json"), raw, state, state.Checksum); err != nil {
		t.Fatal(err)
	}
	calls := len(fake.ranges)
	owner.Close()
	owner = newOwner()
	clock.Add(2 * time.Hour.Milliseconds())
	if actual := prepare(owner); actual != initial {
		t.Fatalf("legacy short listing changed readiness outcome:\n%s\n%s", initial, actual)
	}
	if len(fake.ranges) != calls {
		t.Fatal("manual upgrade recheck repeated legacy successful older-history search", fake.ranges[calls:])
	}
	if len(owner.state.Watermarks) != 0 || owner.state.Sent != 0 {
		t.Fatal("legacy backfill evidence invented a processed/delivery claim")
	}
}
