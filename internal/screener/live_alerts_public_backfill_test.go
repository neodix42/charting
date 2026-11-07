package screener

import (
	"bytes"
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type filteredPublicAlertHistory struct {
	exchange.Adapter
	filter func([]domain.Candle, int64, int64) []domain.Candle
}

func (a filteredPublicAlertHistory) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	rows, err := a.Adapter.History(ctx, key, from, to, limit)
	if err != nil {
		return nil, err
	}
	return a.filter(rows, from, to), nil
}

func TestPublicAlertSparseHistoryBackfillsActualBarsBeforeEvaluation(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "xyz:SPARSE", Market: "perpetual", Availability: "available"}}}
	l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	end := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC).Unix()
	l.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func(rows []domain.Candle, _, _ int64) []domain.Candle {
		filtered := rows[:0]
		for _, c := range rows {
			if (end-86400-c.Time)/86400%2 == 0 {
				filtered = append(filtered, c)
			}
		}
		return filtered
	}}
	c := publicAlertTestCondition()
	c.Request.Conditions = c.Request.Conditions[:1]
	c.Request.Conditions[0].Right.Field = "sma_27"
	periods := map[string][]string{c.ID: {"d1"}}
	batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, periods, end*1000)
	if err != nil || len(batch.Files) != 1 || len(batch.Failures) != 0 {
		t.Fatalf("sparse history failed instead of backfilling: %+v %v", batch, err)
	}
	if len(fake.ranges) != 2 || fake.ranges[1][1] != fake.ranges[0][0] {
		t.Fatal("did not fetch older history before enrichment", fake.ranges)
	}
	for _, name := range batch.Files {
		s, err := indexAnalysisCSV(context.Background(), batch.Directory, name, filepath.Join(t.TempDir(), "rows"), func(float64) {})
		if err != nil {
			t.Fatal(err)
		}
		closed, err := s.closedCount(end * 1000)
		if err != nil || closed < 28 {
			t.Fatal("calendar slots counted as actual warm-up", closed, err)
		}
		latest, err := s.row(closed - 1)
		if err != nil || latest.timestamp != (end-86400)*1000 {
			t.Fatal("wrong signal bar", latest, err)
		}
	}
	// Restart must reuse real raw and derived data, including the expanded window.
	restarted := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	_, err = restarted.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, periods, end*1000)
	if err != nil || len(fake.ranges) != 2 {
		t.Fatal("backfilled history was not reusable after restart", fake.ranges, err)
	}
}

func TestPublicAlertNewListingBackfillIsBoundedAndRemembered(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	fake := &publicAlertFakeAdapter{price: 100, short: true, items: []domain.Instrument{{NativeID: "xyz:NEW", Market: "perpetual", Availability: "available"}}}
	l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	c := publicAlertTestCondition()
	c.Request.Conditions = c.Request.Conditions[:1]
	c.Request.Conditions[0].Right.Field = "sma_27"
	periods := map[string][]string{c.ID: {"d1"}}
	asOf := time.Date(2026, 11, 7, 0, 0, 5, 0, time.UTC).UnixMilli()
	for attempt := 0; attempt < 2; attempt++ {
		batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, periods, asOf)
		if err != nil || len(batch.Files) != 0 || len(batch.Failures) != 1 {
			t.Fatal(batch, err)
		}
		for _, e := range batch.Failures {
			if !strings.Contains(e.Error(), "only 1 available after older-history backfill") {
				t.Fatal(e)
			}
		}
		if len(fake.ranges) != 2 {
			t.Fatal("unavailable history refetched for same cutoff", fake.ranges)
		}
	}
}

func TestPublicAlertNarrowLatestBarRepairBeforeEnrichment(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "published-during-check", true: "provider-still-missing"}[missing], func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "@698", Market: "spot", Availability: "available"}}}
			l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
			end := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC).Unix()
			l.publicData.adapters["hyperliquid"] = filteredPublicAlertHistory{Adapter: fake, filter: func(rows []domain.Candle, from, _ int64) []domain.Candle {
				if missing || from != end-86400 {
					return rows[:len(rows)-1]
				}
				return rows
			}}
			c := publicAlertTestCondition()
			c.Request.Conditions = c.Request.Conditions[:1]
			batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, map[string][]string{c.ID: {"d1"}}, end*1000)
			if err != nil {
				t.Fatal(err)
			}
			if missing {
				if len(batch.Files) != 0 || len(batch.Failures) != 1 {
					t.Fatal("stale data became target", batch)
				}
				for _, e := range batch.Failures {
					if !errors.Is(e, errLiveAlertsPendingBar) || !strings.Contains(e.Error(), "2026-11-06 00:00") {
						t.Fatal("missing exact yesterday date/retry cause", e)
					}
				}
			} else if len(batch.Files) != 1 || len(batch.Failures) != 0 {
				t.Fatal("latest publication not repaired", batch)
			}
			last := fake.ranges[len(fake.ranges)-1]
			if last != [2]int64{end - 86400, end} {
				t.Fatal("latest candle not explicitly requested", fake.ranges)
			}
		})
	}
}

func TestPublicAlertEnrichmentReuseChecksOutputPlanAndRuntime(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: "BTC", Market: "perpetual", Availability: "available"}}}
	l := newPublicAlertTestLive(t, m, "hyperliquid", fake)
	c := publicAlertTestCondition()
	c.Request.Conditions = c.Request.Conditions[:1]
	periods := map[string][]string{c.ID: {"d1"}}
	asOf := time.Date(2026, 11, 7, 0, 0, 5, 0, time.UTC).UnixMilli()
	prepare := func() string {
		t.Helper()
		batch, err := l.preparePublicAlertData(context.Background(), "hyperliquid", []SavedAnalysisCondition{c}, periods, asOf)
		if err != nil || len(batch.Files) != 1 || len(batch.Failures) != 0 {
			t.Fatal(batch, err)
		}
		for _, name := range batch.Files {
			return filepath.Join(batch.Directory, name)
		}
		return ""
	}
	output := prepare()
	before, _ := os.ReadFile(output)
	old := time.Unix(100000, 0)
	if err := os.Chtimes(output, old, old); err != nil {
		t.Fatal(err)
	}
	prepare()
	info, _ := os.Stat(output)
	if !info.ModTime().Equal(old) {
		t.Fatal("unchanged series was re-enriched/published")
	}
	if err := os.WriteFile(output, []byte("corrupted output"), 0600); err != nil {
		t.Fatal(err)
	}
	prepare()
	after, _ := os.ReadFile(output)
	if string(before) != string(after) {
		t.Fatal("modified derived CSV was reused")
	}
	c.Request.Conditions[0].Right.Field = "sma_10"
	output = prepare()
	after, _ = os.ReadFile(output)
	if !strings.Contains(string(after), "sma_10") {
		t.Fatal("changed indicator plan reused obsolete enrichment")
	}
	fingerprint := publicAlertRuntimeFingerprint(m.runtime)
	if fingerprint == "" {
		t.Fatal("actual runtime cannot cache derived output")
	}
	plan := publicAlertPlan{Fields: []string{"close"}, Bars: 2}
	if publicAlertEnrichmentKey(before, plan, fingerprint) == publicAlertEnrichmentKey(before, plan, "changed-runtime") {
		t.Fatal("runtime changes did not invalidate enrichment")
	}
}

func TestPublicAlertRuleGuardRejectsEditsCorruptionAndDeletionBeforeClaim(t *testing.T) {
	for _, mode := range []string{"request", "corrupt", "delete", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			m, l, saved, _ := namedPublicLiveFixture(t)
			entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Guarded", ConditionID: saved.ID, Period: "d1", DataSource: "binance", Enabled: true}
			config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
			if _, err := l.Configure(config); err != nil {
				t.Fatal(err)
			}
			conditions, err := l.selectedConditions(config)
			if err != nil {
				t.Fatal(err)
			}
			var guard publicAlertRuleGuard
			claim := func(ts int64) (bool, error) {
				return l.claimCurrentPublicCondition(context.Background(), conditions[0], "binance", livePublicClaimKey(entry.ID, "binance", publicAlertSymbol("spot", "BTCUSDT"), "d1"), ts, nil, &guard)
			}
			if ok, err := claim(1704067200000); err != nil || !ok || !guard.valid {
				t.Fatal("initial valid claim failed", ok, err)
			}
			if ok, err := claim(1704153600000); err != nil || !ok {
				t.Fatal("unchanged cached rule failed", ok, err)
			}
			path := filepath.Join(m.profile, "screener-saved-conditions.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "request":
				after := bytes.Replace(before, []byte(`"value":0`), []byte(`"value":1`), 1)
				if bytes.Equal(before, after) || len(before) != len(after) {
					t.Fatal("test did not produce a same-size rule edit")
				}
				if err = os.WriteFile(path, after, 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err = os.WriteFile(path, append(before, []byte("{}")...), 0600); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				config.Alerts[0].Enabled = false
				if _, err = l.Configure(config); err != nil {
					t.Fatal(err)
				}
			}
			if ok, err := claim(1704240000000); ok || (mode == "corrupt" && err == nil) {
				t.Fatal("obsolete/corrupt rule consumed a claim", ok, err)
			}
			for _, ts := range l.state.Watermarks {
				if ts == 1704240000000 {
					t.Fatal("obsolete claim reached storage")
				}
			}
		})
	}
}
