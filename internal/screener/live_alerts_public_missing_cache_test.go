package screener

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

func TestPublicAlertMissingLatestDoesNotDownloadUnusableOlderWarmup(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale", true: "empty"}[empty], func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			asset := publicAlertAsset{NativeID: "@42", Market: "spot"}
			end := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC).Unix()
			if !empty {
				bar := domain.Candle{Time: end - 10*86400, Open: 100, High: 102, Low: 99, Close: 101, Volume: 3, Closed: true, Quality: "authoritative"}
				if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", []domain.Candle{bar}); err != nil {
					t.Fatal(err)
				}
			}
			fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
			l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
			l.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func([]domain.Candle, int64, int64) []domain.Candle { return nil }}
			c := publicWarmupCondition("d1", "sma_27")
			batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, end*1000)
			defer batch.close()
			if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
				t.Fatal("missing latest must remain pending", batch, err)
			}
			for _, failure := range batch.Failures {
				if !errors.Is(failure, errLiveAlertsPendingBar) {
					t.Fatal("missing latest lost retry classification", failure)
				}
			}
			wanted := 2 // Cold series: required window, then one publication-race repair.
			if !empty {
				wanted = 1 // Legacy inactive retained series: probe only the missing latest candle.
			}
			if len(fake.ranges) != wanted || fake.ranges[wanted-1] != [2]int64{end - 86400, end} {
				t.Fatal("missing latest triggered older warm-up before it could be evaluated", fake.ranges)
			}
		})
	}
}

func TestPublicAlertMissingLatestMemoSurvivesRestartAndExpiredProbeIsNarrow(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "inactive", true: "empty"}[empty], func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			asset := publicAlertAsset{NativeID: "@42", Market: "spot"}
			end := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC).Unix()
			if !empty {
				bars := []domain.Candle{}
				for day := int64(10); day < 40; day++ {
					bars = append(bars, domain.Candle{Time: end - day*86400, Open: 100, High: 102, Low: 99, Close: 101, Volume: 3, Closed: true, Quality: "authoritative"})
				}
				if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", bars); err != nil {
					t.Fatal(err)
				}
			}
			fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
			var clock atomic.Int64
			clock.Store(end*1000 + 5000)
			c := publicWarmupCondition("d1", "sma_27")
			prepare := func(cutoff int64) publicAlertBatch {
				t.Helper()
				// Every attempt starts without in-memory series state.
				l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
				l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
				l.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func([]domain.Candle, int64, int64) []domain.Candle { return nil }}
				batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, cutoff*1000)
				t.Cleanup(batch.close)
				if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
					t.Fatal("unpublished candle became an evaluation target", batch, err)
				}
				for _, failure := range batch.Failures {
					if !errors.Is(failure, errLiveAlertsPendingBar) {
						t.Fatal("successful absence lost its pending status", failure)
					}
				}
				return batch
			}
			prepare(end)
			firstCalls := len(fake.ranges)
			if !empty && firstCalls != 1 {
				t.Fatal("legacy inactive cache made a broad download before checking actual publication", fake.ranges)
			}
			_, state, _, err := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), asset, "d1")
			if err != nil || state.LatestMissing == nil || state.LatestMissing.Cutoff != end || state.LatestMissing.CheckedAt != clock.Load() {
				t.Fatal("successful empty latest read was not durably attributed", state, err)
			}
			if !empty && state.CheckedThrough != end-9*86400 {
				t.Fatal("a latest-only absence probe invented coverage of the intervening historical gap", state)
			}
			clock.Add(9 * time.Minute.Milliseconds())
			prepare(end)
			if len(fake.ranges) != firstCalls {
				t.Fatal("immediate full-batch repeat refetched an inactive/empty spot", fake.ranges)
			}
			clock.Store(state.LatestMissing.CheckedAt + 30*time.Minute.Milliseconds())
			prepare(end)
			if len(fake.ranges) != firstCalls+1 || fake.ranges[firstCalls] != [2]int64{end - 86400, end} {
				t.Fatal("expired absence repeated broad or older history", fake.ranges[firstCalls:])
			}
			_, renewed, _, err := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), asset, "d1")
			if err != nil || renewed.LatestMissing == nil || renewed.LatestMissing.CheckedAt != clock.Load() || renewed.OlderBackfill != nil {
				t.Fatal("narrow successful absence did not renew only its own evidence", renewed, err)
			}
			clock.Add(time.Minute.Milliseconds())
			prepare(end)
			if len(fake.ranges) != firstCalls+1 {
				t.Fatal("renewed absence was not reusable", fake.ranges)
			}
			clock.Store((end + 86400) * 1000)
			prepare(end + 86400)
			if len(fake.ranges) <= firstCalls+1 {
				t.Fatal("new daily cutoff reused yesterday's negative evidence")
			}
		})
	}
}

func TestPublicAlertMissingLatestPositiveImportBypassesCooldown(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	asset := publicAlertAsset{NativeID: "@42", Market: "spot"}
	end := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC).Unix()
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	l.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func([]domain.Candle, int64, int64) []domain.Candle { return nil }}
	c := publicWarmupCondition("d1", "sma_27")
	prepare := func() publicAlertBatch {
		t.Helper()
		batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, end*1000)
		t.Cleanup(batch.close)
		if err != nil {
			t.Fatal(err)
		}
		return batch
	}
	first := prepare()
	if len(first.Files) != 0 || len(first.Failures) != 1 {
		t.Fatal(first)
	}
	count := len(fake.ranges)
	bars := []domain.Candle{}
	for day := int64(1); day <= 28; day++ {
		bars = append(bars, domain.Candle{Time: end - day*86400, Open: 100, High: 102, Low: 99, Close: 101, Volume: 3, Closed: true, Quality: "authoritative"})
	}
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", bars); err != nil {
		t.Fatal(err)
	}
	next := prepare()
	if len(next.Files) != 1 || len(next.Failures) != 0 || len(fake.ranges) != count {
		t.Fatal("an actual imported current candle remained hidden behind absence cooldown", next, fake.ranges[count:])
	}
	_, state, _, err := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), asset, "d1")
	if err != nil || state.LatestMissing != nil {
		t.Fatal("positive history retained stale absence evidence", state, err)
	}
}

func TestPublicAlertMissingLatestPublicationResumesRequiredWarmup(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	asset := publicAlertAsset{NativeID: "@42", Market: "spot"}
	end := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC).Unix()
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	var available atomic.Bool
	var clock atomic.Int64
	clock.Store(end * 1000)
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	l.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func(rows []domain.Candle, _, _ int64) []domain.Candle {
		if available.Load() {
			return rows
		}
		return nil
	}}
	c := publicWarmupCondition("d1", "sma_27")
	prepare := func() publicAlertBatch {
		t.Helper()
		batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, end*1000)
		t.Cleanup(batch.close)
		if err != nil {
			t.Fatal(err)
		}
		return batch
	}
	first := prepare()
	if len(first.Files) != 0 || len(first.Failures) != 1 {
		t.Fatal("initial missing candle became a target", first)
	}
	calls := len(fake.ranges)
	clock.Add(30 * time.Minute.Milliseconds())
	available.Store(true)
	next := prepare()
	if len(next.Files) != 1 || len(next.Failures) != 0 || len(fake.ranges) != calls+2 || fake.ranges[calls] != [2]int64{end - 86400, end} || fake.ranges[calls+1] != [2]int64{end - 28*86400, end} {
		t.Fatal("latest publication failed to restore actual warm-up coverage", next, fake.ranges[calls:])
	}
	rows, state, _, err := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), asset, "d1")
	if err != nil || len(rows) != 28 || state.LatestMissing != nil || state.CheckedThrough != end {
		t.Fatal("latest-only probe invented a complete indicator window", len(rows), state, err)
	}
	prepare()
	if len(fake.ranges) != calls+2 {
		t.Fatal("fully verified completed cutoff was fetched again", fake.ranges[calls+2:])
	}
}

func TestPublicAlertLegacyInactiveLatestArrivalRepairsGapsAndCorrectionsDespiteEnoughOldBars(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	asset := publicAlertAsset{NativeID: "@42", Market: "spot"}
	end := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC).Unix()
	bars := []domain.Candle{}
	for day := int64(10); day < 50; day++ {
		bars = append(bars, domain.Candle{Time: end - day*86400, Open: 100, High: 102, Low: 99, Close: 101, Volume: 3, Closed: true, Quality: "authoritative"})
	}
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", bars); err != nil {
		t.Fatal(err)
	}
	// Latest publication also makes intervening real candles and an overlap
	// correction available. Merely having >28 old bars cannot skip that repair.
	fake := &publicAlertFakeAdapter{price: 200, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	var clock atomic.Int64
	clock.Store(end * 1000)
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	var available atomic.Bool
	l.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func(rows []domain.Candle, _, _ int64) []domain.Candle {
		if available.Load() {
			return rows
		}
		return nil
	}}
	c := publicWarmupCondition("d1", "sma_27")
	first, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, end*1000)
	defer first.close()
	if err != nil || len(first.Files) != 0 || len(first.Failures) != 1 || len(fake.ranges) != 1 {
		t.Fatal("legacy inactive absence did not use just one latest probe", first, fake.ranges, err)
	}
	clock.Add(30 * time.Minute.Milliseconds())
	available.Store(true)
	batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, end*1000)
	defer batch.close()
	if err != nil || len(batch.Files) != 1 || len(batch.Failures) != 0 || len(fake.ranges) != 3 || fake.ranges[1] != [2]int64{end - 86400, end} || fake.ranges[2] != [2]int64{end - 10*86400, end} {
		t.Fatal("positive latest probe skipped original overlap/gap repair", batch, fake.ranges, err)
	}
	rows, _, _, err := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), asset, "d1")
	if err != nil || len(rows) != 49 {
		t.Fatal("intervening actual candles were replaced by old warm-up history", len(rows), err)
	}
	for _, row := range rows {
		if row.Time == end-10*86400 && row.Close != 201 {
			t.Fatal("old overlap candle correction was ignored", row)
		}
	}
	for _, name := range batch.Files {
		s, err := indexAnalysisCSV(context.Background(), batch.Directory, name, filepath.Join(t.TempDir(), "rows"), func(float64) {})
		if err != nil {
			t.Fatal(err)
		}
		count, err := s.closedCount(end * 1000)
		if err != nil {
			t.Fatal(err)
		}
		latest, err := s.row(count - 1)
		if err != nil {
			t.Fatal(err)
		}
		position, ok := s.fieldIndex("sma_27")
		if !ok || math.Abs(latest.values[position]-(10*201.0+17*101.0)/27) > 1e-9 {
			t.Fatal("indicator used a gap or uncorrected old bars", latest.values, position)
		}
	}
}

func TestPublicAlertMissingLatestExpiredNetworkFailureDoesNotRenewMemo(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	asset := publicAlertAsset{NativeID: "@42", Market: "spot"}
	end := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC).Unix()
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	var clock atomic.Int64
	clock.Store(end * 1000)
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	l.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func([]domain.Candle, int64, int64) []domain.Candle { return nil }}
	c := publicWarmupCondition("d1", "sma_27")
	prepare := func() publicAlertBatch {
		t.Helper()
		batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, end*1000)
		t.Cleanup(batch.close)
		if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
			t.Fatal(batch, err)
		}
		return batch
	}
	prepare()
	clock.Add(30 * time.Minute.Milliseconds())
	fake.mu.Lock()
	fake.fail = &exchange.Error{Kind: "timeout", Message: "actual expired-probe network timeout"}
	fake.mu.Unlock()
	calls := len(fake.ranges)
	for attempt := 0; attempt < 2; attempt++ {
		batch := prepare()
		for _, failure := range batch.Failures {
			if exchange.ErrorKind(failure) != "timeout" || errors.Is(failure, errLiveAlertsPendingBar) {
				t.Fatal("network outage became a successful missing-candle memo", failure)
			}
		}
		_, state, _, err := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), asset, "d1")
		if err != nil || state.LatestMissing == nil || state.LatestMissing.CheckedAt != end*1000 {
			t.Fatal("failed HTTP read advanced absence check time", state, err)
		}
	}
	if len(fake.ranges) != calls+2 {
		t.Fatal("expired network failure suppressed the next genuine retry", fake.ranges[calls:])
	}
}

func TestPublicAlertMissingCandleCooldownAndEvidenceBounds(t *testing.T) {
	end := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC).Unix() // Monday UTC for W1.
	for _, test := range []struct {
		name, source, market string
		interval, ago        int64
		want                 time.Duration
	}{
		{name: "one missing daily spot slot", source: "hyperliquid", market: "spot", interval: 86400, ago: 2, want: 30 * time.Second},
		{name: "two missing daily spot slots", source: "hyperliquid", market: "spot", interval: 86400, ago: 3, want: 30 * time.Minute},
		{name: "empty daily spot", source: "hyperliquid", market: "spot", interval: 86400, want: 30 * time.Minute},
		{name: "inactive daily perpetual", source: "hyperliquid", market: "perpetual", interval: 86400, ago: 10, want: 30 * time.Second},
		{name: "empty hourly spot", source: "hyperliquid", market: "spot", interval: 3600, want: 30 * time.Second},
		{name: "inactive weekly spot", source: "hyperliquid", market: "spot", interval: 7 * 86400, ago: 3, want: 30 * time.Minute},
		{name: "one missing weekly spot slot", source: "hyperliquid", market: "spot", interval: 7 * 86400, ago: 2, want: 30 * time.Second},
		{name: "inactive other source", source: "binance", market: "spot", interval: 86400, ago: 10, want: 30 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			bars := map[int64]domain.Candle{}
			if test.ago > 0 {
				ts := end - test.ago*test.interval
				bars[ts] = domain.Candle{Time: ts, Closed: true, Quality: "authoritative"}
			}
			if got := publicAlertMissingCandleCooldown(test.source, test.market, end, test.interval, bars); got != test.want {
				t.Fatal("incorrect inactivity/publication retry deadline", got, test.want)
			}
		})
	}
	now := time.Unix(end, 0)
	valid := publicAlertMissingCandleEvidence{Cutoff: end, CheckedAt: now.UnixMilli()}
	if !valid.valid(end, 86400, now) || !valid.valid(end, 7*86400, now) {
		t.Fatal("valid exact daily/weekly cutoff was rejected")
	}
	for _, changed := range []publicAlertMissingCandleEvidence{
		{Cutoff: end - 86400, CheckedAt: now.UnixMilli()},
		{Cutoff: end, CheckedAt: now.Add(time.Second).UnixMilli()},
		{Cutoff: end, CheckedAt: 0},
		{Cutoff: 0, CheckedAt: now.UnixMilli()},
	} {
		if changed.valid(end, 86400, now) {
			t.Fatal("foreign/future/corrupt absence evidence reused", changed)
		}
	}
}
