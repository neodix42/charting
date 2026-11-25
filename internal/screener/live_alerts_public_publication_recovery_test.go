package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestManualPublicMissingCloseRecoversAfterExpiryAndRestartWithoutStaleClaims(t *testing.T) {
	for _, period := range []string{"d1", "w1"} {
		t.Run(period, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			end := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC).Unix()
			interval := int64(86400)
			if period == "w1" {
				end = time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC).Unix()
				interval *= 7
			}
			request := publicWarmupCondition(period, "sma_2").Request
			request.Conditions[0].ID = "late-close-rule"
			// A false rule still records one evaluated latest candle. Any actual
			// delivery or premature watermark is therefore a test failure.
			request.Conditions[0].Right = AnalysisOperand{Kind: "number", Value: 1e9}
			saved, err := m.SaveAnalysisCondition("Delayed provider close recovery", request)
			if err != nil {
				t.Fatal(err)
			}
			fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTC", Market: "perpetual", Availability: "available"}}}
			var clock atomic.Int64
			clock.Store((end + 5*3600) * 1000)
			var publication atomic.Int32 // 0=missing, 1=open, 2=provisional, 3=authoritative.
			adapter := filteredPublicAlertHistory{Adapter: fake, filter: func(rows []domain.Candle, _, _ int64) []domain.Candle {
				result := rows[:0]
				for _, row := range rows {
					// Hyperliquid weeks come from actual daily observations. Its
					// final day has to be published before that week can be complete.
					if row.Time == end-86400 {
						switch publication.Load() {
						case 0:
							continue
						case 1:
							row.Closed, row.Quality = false, "provisional"
						case 2:
							row.Quality = "provisional"
						}
					}
					result = append(result, row)
				}
				return result
			}}
			config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Recover latest close", ConditionID: saved.ID, Period: period, DataSource: "hyperliquid", Enabled: true}}}
			newOwner := func() *LiveAlerts {
				owner := newPublicAlertTestLive(t, m, "hyperliquid", fake)
				owner.now = func() time.Time { return time.UnixMilli(clock.Load()) }
				owner.publicData.adapters["hyperliquid"] = adapter
				owner.send = func(context.Context, LiveAlert) error {
					t.Error("publication recovery attempted a real notification")
					return errors.New("delivery forbidden in test")
				}
				t.Cleanup(owner.Close)
				return owner
			}
			owner := newOwner()
			if _, err := owner.Configure(config); err != nil {
				t.Fatal(err)
			}
			pending := func() {
				t.Helper()
				if err := owner.check(context.Background(), owner.state.Config, clock.Load()); !errors.Is(err, errLiveAlertsPendingBar) {
					t.Fatal("missing/open/provisional close lost its pending status", err)
				}
				if len(owner.state.Watermarks) != 0 || owner.Info().Sent != 0 || len(owner.state.Recent) != 0 {
					t.Fatal("unavailable latest bar produced a claim or notification", owner.Info())
				}
				row := owner.Info().Checks[0]
				if row.AssetsAlreadyProcessed != 0 || row.AssetsSkipped != 1 || publication.Load() == 0 && row.AssetsMissingCandle != 1 || publication.Load() == 2 && row.AssetsMissingCandle != 0 {
					t.Fatal("missing and provisional source statuses were conflated", row)
				}
			}
			pending()
			calls := len(fake.ranges)
			if calls == 0 {
				t.Fatal("initial missing close was never inspected")
			}
			clock.Add(20 * time.Second.Milliseconds())
			pending()
			if len(fake.ranges) != calls {
				t.Fatal("manual repeat ignored the bounded absence cooldown")
			}
			owner.Close()
			owner = newOwner() // Exact-cutoff absence is loaded from disk.
			for _, stage := range []int32{1, 2} {
				publication.Store(stage)
				clock.Add(31 * time.Second.Milliseconds())
				pending()
				if len(fake.ranges) <= calls {
					t.Fatal("expired manual memo hid a fresh publication probe after restart")
				}
				if stage == 1 {
					requireNarrowPublicLatestRecheck(t, fake.ranges[calls:], end, interval)
				}
				calls = len(fake.ranges)
			}
			publication.Store(3)
			clock.Add(31 * time.Second.Milliseconds())
			if err := owner.check(context.Background(), owner.state.Config, clock.Load()); err != nil {
				t.Fatal("real authoritative publication did not recover the same UTC cutoff", err)
			}
			info := owner.Info()
			if len(fake.ranges) <= calls || len(info.Checks) != 1 || info.Checks[0].AssetsScanned != 1 || info.Sent != 0 || len(owner.state.Watermarks) != 1 {
				t.Fatal("publication was not evaluated exactly once without delivery", info, owner.state.Watermarks)
			}
			for _, at := range owner.state.Watermarks {
				if at != (end-interval)*1000 {
					t.Fatal("wrong latest closed candle was claimed", at)
				}
			}
			calls = len(fake.ranges)
			clock.Add(time.Minute.Milliseconds())
			if err := owner.check(context.Background(), owner.state.Config, clock.Load()); err != nil || len(fake.ranges) != calls || owner.Info().Sent != 0 {
				t.Fatal("already-checked current candle was downloaded or notified again", err)
			}
			if row := owner.Info().Checks[0]; row.AssetsAlreadyProcessed != 1 || row.AssetsMissingCandle != 0 || row.AssetsScanned != 0 {
				t.Fatal("existing watermark shortcut did not count exactly one already-checked asset", row)
			}
		})
	}
}

func TestManualInactiveSpotMissingMemoUsesExistingThirtyMinuteExpiry(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	end := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC).Unix()
	asset := publicAlertAsset{NativeID: "@698", Market: "spot"}
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", retainedDaily(end-40*86400, 30)); err != nil {
		t.Fatal(err)
	}
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	var clock atomic.Int64
	clock.Store((end + 5*3600) * 1000)
	owner := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	t.Cleanup(owner.Close)
	owner.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	owner.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func([]domain.Candle, int64, int64) []domain.Candle { return nil }}
	condition := publicWarmupCondition("d1", "sma_2")
	ctx := context.WithValue(context.Background(), publicAlertReuseClosedHistoryKey{}, true)
	prepare := func() {
		t.Helper()
		batch, err := owner.preparePublicAlertData(ctx, "hyperliquid", []SavedAnalysisCondition{condition}, map[string][]string{condition.ID: {"d1"}}, clock.Load())
		defer batch.close()
		if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
			t.Fatal("genuinely inactive source became usable or failed globally", batch.Failures, err)
		}
		for _, failure := range batch.Failures {
			if !errors.Is(failure, errLiveAlertsPendingBar) {
				t.Fatal("inactive source lost missing-close protection", failure)
			}
		}
	}
	prepare()
	calls := len(fake.ranges)
	clock.Add(29 * time.Minute.Milliseconds())
	prepare()
	if len(fake.ranges) != calls {
		t.Fatal("inactive spot was polled before its existing thirty-minute budget")
	}
	clock.Add(time.Minute.Milliseconds())
	prepare()
	requireNarrowPublicLatestRecheck(t, fake.ranges[calls:], end, exchange.TimeframeSeconds("1d"))
	if len(owner.state.Watermarks) != 0 || owner.Info().Sent != 0 {
		t.Fatal("fresh empty probe produced a stale claim or notification")
	}
}
