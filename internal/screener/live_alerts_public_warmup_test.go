package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func publicWarmupCondition(period, field string) SavedAnalysisCondition {
	c := publicAlertTestCondition()
	c.Request.Period = period
	c.Request.Conditions = c.Request.Conditions[:1]
	c.Request.Conditions[0].Right.Field = field
	return c
}

func requireWarmupBatch(t *testing.T, batch publicAlertBatch, err error, required, available int) {
	t.Helper()
	defer batch.close()
	if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
		t.Fatalf("short real history became a target or failed globally: files=%v failures=%v error=%v", batch.Files, batch.Failures, err)
	}
	for _, err := range batch.Failures {
		var warmup *publicAlertInsufficientHistory
		if !errors.As(err, &warmup) || warmup.Required != required || warmup.Available != available || warmup.Period != "d1" {
			t.Fatalf("missing typed actual-bar shortfall: %v (%+v)", err, warmup)
		}
	}
}

func TestPublicAlertShortHistoryAddsNewBarsAcrossRestartAndBecomesReady(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	fake := &publicAlertFakeAdapter{price: 100, short: true, items: []domain.Instrument{{NativeID: "xyz:GROW", Market: "perpetual", Availability: "available"}}}
	c := publicWarmupCondition("d1", "sma_3")
	end := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC).Unix()
	const day = int64(86400)
	for offset := 0; offset < 4; offset++ {
		// Each owner is new: the cross-close evidence must survive a restart.
		l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
		calls := len(fake.ranges)
		cutoff := end + int64(offset)*day
		batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, cutoff*1000)
		if offset < 3 {
			requireWarmupBatch(t, batch, err, 4, offset+1)
		} else {
			if err != nil || len(batch.Files) != 1 || len(batch.Failures) != 0 {
				t.Fatalf("enough genuine bars did not become eligible: %+v %v", batch, err)
			}
			batch.close()
		}
		if offset == 0 {
			if len(fake.ranges)-calls != 2 {
				t.Fatal("initial short history must prove an older empty page", fake.ranges)
			}
		} else {
			newCalls := fake.ranges[calls:]
			if len(newCalls) != 1 || newCalls[0] != [2]int64{cutoff - 2*day, cutoff} {
				t.Fatal("new close repeated old-history probes instead of overlap/new bars", offset, newCalls)
			}
		}
	}
	asset := publicAlertAsset{NativeID: "xyz:GROW", Market: "perpetual"}
	rows, state, _, err := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), asset, "d1")
	if err != nil || len(rows) != 4 || state.OlderBackfill == nil || state.OlderBackfill.Cutoff != end || rows[0].Time != end-day {
		t.Fatal("new bars replaced original history or exhausted-span evidence", rows, state, err)
	}
}

func TestPublicAlertOlderEmptyEvidenceExpiresAndLargerPlansReprobe(t *testing.T) {
	for _, mode := range []string{"expiry", "larger-plan", "earlier-import", "future-evidence"} {
		t.Run(mode, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			fake := &publicAlertFakeAdapter{price: 100, short: true, items: []domain.Instrument{{NativeID: "para:NEW", Market: "perpetual", Availability: "available"}}}
			c := publicWarmupCondition("d1", "sma_27")
			end := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC).Unix()
			prepare := func(at int64) (publicAlertBatch, error) {
				l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
				return l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, at*1000)
			}
			batch, err := prepare(end)
			requireWarmupBatch(t, batch, err, 28, 1)
			calls := len(fake.ranges)
			at, need, available := end, 28, 1
			asset := publicAlertAsset{NativeID: "para:NEW", Market: "perpetual"}
			switch mode {
			case "expiry":
				at += 7 * 86400
				available = 8
			case "larger-plan":
				c.Request.Conditions[0].Right.Field = "sma_55"
				need = 56
			case "earlier-import":
				bar := domain.Candle{Time: end - 2*86400, Open: 100, High: 102, Low: 99, Close: 101, Volume: 3, Closed: true, Quality: "authoritative"}
				if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", []domain.Candle{bar}); err != nil {
					t.Fatal(err)
				}
				available = 2
			case "future-evidence":
				_, state, _, err := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), asset, "d1")
				if err != nil {
					t.Fatal(err)
				}
				state.OlderBackfill.Cutoff = end + 86400
				state.BackfillThrough = end + 86400
				encoded, _ := json.Marshal(state)
				path := filepath.Join(hyperliquidHistoryRoot(m.profile), "raw", publicAlertSymbol(asset.Market, asset.NativeID)+"-d1.json")
				if err := os.WriteFile(path, encoded, 0600); err != nil {
					t.Fatal(err)
				}
			}
			batch, err = prepare(at)
			requireWarmupBatch(t, batch, err, need, available)
			olderProbe := false
			for _, r := range fake.ranges[calls:] {
				olderProbe = olderProbe || r[1]-r[0] >= 100*86400
			}
			if !olderProbe {
				t.Fatal("changed/expired evidence suppressed an older-history recheck", fake.ranges[calls:])
			}
		})
	}
}

func TestPublicAlertOlderImportCannotSkipRecentWarmupGap(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	end := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC).Unix() // Monday
	const day = int64(86400)
	asset := publicAlertAsset{NativeID: "xyz:GAPPED", Market: "perpetual"}
	old := domain.Candle{Time: end - 800*day, Open: 100, High: 102, Low: 99, Close: 101, Volume: 3, Closed: true, Quality: "authoritative"}
	if err := mergeHyperliquidHistory(context.Background(), m.profile, asset, "d1", []domain.Candle{old}); err != nil {
		t.Fatal(err)
	}
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	l.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func(rows []domain.Candle, _, _ int64) []domain.Candle {
		result := rows[:0]
		for _, bar := range rows {
			weekday := time.Unix(bar.Time, 0).UTC().Weekday()
			if bar.Time >= end-90*day && weekday != time.Saturday && weekday != time.Sunday {
				result = append(result, bar)
			}
		}
		return result
	}}
	// End on Saturday: the latest Friday bar exists and the last28calendar
	// days contain only20 genuine daily bars, plus the isolated old download.
	end -= 2 * day
	c := publicWarmupCondition("d1", "sma_27")
	batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, end*1000)
	defer batch.close()
	if err != nil || len(batch.Files) != 1 || len(batch.Failures) != 0 {
		t.Fatalf("isolated old history caused false warm-up waiting: %+v %v", batch, err)
	}
	if len(fake.ranges) != 2 || fake.ranges[1][1] != end-28*day {
		t.Fatal("backfill skipped the unqueried recent gap", fake.ranges)
	}
	for _, name := range batch.Files {
		s, err := indexAnalysisCSV(context.Background(), batch.Directory, name, filepath.Join(t.TempDir(), "rows"), func(float64) {})
		if err != nil {
			t.Fatal(err)
		}
		count, err := s.closedCount(end * 1000)
		if err != nil || count < 28 {
			t.Fatal("backfilled actual bars incomplete", count, err)
		}
		first, err := s.row(count - 28)
		if err != nil || first.timestamp < (end-60*day)*1000 {
			t.Fatal("ancient cache island substituted for available recent bars", first, err)
		}
	}
}

type alteredOlderHistory struct {
	exchange.Adapter
	before int64
	mode   string
}

func (a alteredOlderHistory) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	rows, err := a.Adapter.History(ctx, key, from, to, limit)
	if err != nil || to > a.before {
		return rows, err
	}
	if a.mode == "transport" {
		return nil, &exchange.Error{Kind: "timeout", Message: "temporary older-page timeout"}
	}
	bar := domain.Candle{Time: to - 86400, Open: 100, High: 102, Low: 99, Close: 101, Volume: 3, Closed: true, Quality: "authoritative"}
	switch a.mode {
	case "provisional":
		bar.Quality = "provisional"
	case "unclosed":
		bar.Closed = false
	case "outside":
		bar.Time = to
	}
	return []domain.Candle{bar}, nil
}

func TestPublicAlertOlderFailuresAndIncompletePagesAreNotWarmupExhaustion(t *testing.T) {
	for _, mode := range []string{"transport", "provisional", "unclosed", "outside"} {
		t.Run(mode, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			fake := &publicAlertFakeAdapter{price: 100, short: true, items: []domain.Instrument{{NativeID: "xyz:UNVERIFIED", Market: "perpetual", Availability: "available"}}}
			c := publicWarmupCondition("d1", "sma_27")
			end := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC).Unix()
			l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
			l.publicData.adapters["hyperliquid"] = alteredOlderHistory{Adapter: fake, before: end - 28*86400, mode: mode}
			batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, end*1000)
			defer batch.close()
			if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
				t.Fatal("bad page did not remain an isolated failure", batch, err)
			}
			for _, failure := range batch.Failures {
				var warmup *publicAlertInsufficientHistory
				if errors.As(failure, &warmup) {
					t.Fatal("failed/incomplete provider data became benign new-listing status", failure)
				}
			}
			_, state, _, readErr := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), publicAlertAsset{NativeID: "xyz:UNVERIFIED", Market: "perpetual"}, "d1")
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) || state.OlderBackfill != nil {
				t.Fatal("unverified older page acquired durable exhaustion evidence", state, readErr)
			}
		})
	}
}

func TestPublicAlertEmptyEvidenceBoundsAndWeeklyCutoff(t *testing.T) {
	day := int64(86400)
	end := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC).Unix() // Monday
	first := end - 7*day
	valid := publicAlertBackfillEvidence{Cutoff: end, Bars: 28, First: first, EmptyFrom: end - 1000*7*day, EmptyTo: end - 28*7*day}
	if !valid.reusable(first, end-28*7*day, end, 7*day, 28, true) {
		t.Fatal("Monday-UTC weekly evidence rejected")
	}
	// A weekly horizon reached with real data but no empty page is only useful
	// at its exact checked cutoff, never a claim of source inception.
	withoutEmpty := valid
	withoutEmpty.EmptyFrom, withoutEmpty.EmptyTo = 0, 0
	if !withoutEmpty.reusable(first, end-28*7*day, end, 7*day, 28, true) || withoutEmpty.reusable(first, end-27*7*day, end+7*day, 7*day, 28, true) {
		t.Fatal("bounded horizon mistaken for permanent absence")
	}
	for _, mutate := range []func(*publicAlertBackfillEvidence){
		func(e *publicAlertBackfillEvidence) { e.Cutoff += day },
		func(e *publicAlertBackfillEvidence) { e.First -= 7 * day },
		func(e *publicAlertBackfillEvidence) { e.EmptyFrom = -1 },
		func(e *publicAlertBackfillEvidence) { e.EmptyTo = e.First + 7*day },
		func(e *publicAlertBackfillEvidence) { e.EmptyTo = e.EmptyFrom },
		func(e *publicAlertBackfillEvidence) { e.EmptyFrom -= 1000 * 7 * day },
		func(e *publicAlertBackfillEvidence) { e.Bars = 27 },
	} {
		changed := valid
		mutate(&changed)
		if changed.reusable(first, end-28*7*day, end, 7*day, 28, true) {
			t.Fatal("invalid/foreign plan evidence used", changed)
		}
	}
}
