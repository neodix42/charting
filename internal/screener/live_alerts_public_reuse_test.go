package screener

import (
	"bytes"
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHyperliquidManualCheckReusesDownloadedHistoryAndClaimsAcrossRestart(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	ctx := context.Background()
	cutoff := time.Date(2026, 11, 12, 0, 0, 5, 0, time.UTC)
	end := cutoff.Truncate(24 * time.Hour).Unix()
	const day = int64(86400)
	asset := publicAlertAsset{NativeID: "xyz:REUSE", Market: "perpetual"}
	// Use the actual Downloader-to-Live-Alerts history publication path. A
	// matching indicator rule must enrich these retained bars without HTTP.
	if err := mergeHyperliquidHistory(ctx, m.profile, asset, "d1", retainedDaily(end-40*day, 40)); err != nil {
		t.Fatal(err)
	}
	saved, err := m.SaveAnalysisCondition("Retained Hyperliquid SMA", AnalysisRequest{
		DataSource: "hyperliquid", Period: "d1", Conditions: []AnalysisCondition{{
			ID: "positive-sma", Operator: "above", Left: AnalysisOperand{Kind: "field", Field: "sma_3"}, Right: AnalysisOperand{Kind: "number", Value: 0},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Daily retained rule", ConditionID: saved.ID, DataSource: "hyperliquid", Period: "d1", Enabled: true}
	config := LiveAlertsConfig{Enabled: true, Alerts: []LiveAlertDefinition{entry}}
	fake := &publicAlertFakeAdapter{price: 100, items: []domain.Instrument{{NativeID: asset.NativeID, Market: asset.Market, Availability: "available"}}}
	var delivered []LiveAlert
	newOwner := func() *LiveAlerts {
		t.Helper()
		owner, err := NewLiveAlerts(m, func(_ context.Context, alert LiveAlert) error {
			delivered = append(delivered, alert)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(owner.Close)
		owner.now = func() time.Time { return cutoff }
		owner.publicData = &publicAlertData{adapters: map[string]exchange.Adapter{"hyperliquid": fake}}
		return owner
	}
	owner := newOwner()
	if _, err := owner.Configure(config); err != nil {
		t.Fatal(err)
	}
	check := func(owner *LiveAlerts, wantSent int64, wantTotal int) {
		t.Helper()
		if err := owner.check(ctx, owner.state.Config, cutoff.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		info := owner.Info()
		if len(info.Checks) != 1 || info.Checks[0].Status != "completed" || info.Checks[0].Error != "" || info.Checks[0].AssetsSkipped != 0 || !info.Checks[0].SentAlertsKnown || info.Checks[0].SentAlerts != wantSent || info.Sent != int64(wantTotal) || len(info.Recent) != wantTotal || len(delivered) != wantTotal {
			t.Fatalf("repeat changed delivery or check outcome: checks=%+v lifetime=%d recent=%d delivered=%d", info.Checks, info.Sent, len(info.Recent), len(delivered))
		}
	}
	check(owner, 1, 1)
	if len(fake.ranges) != 0 {
		t.Fatal("imported Downloader history was fetched again", fake.ranges)
	}
	stem := publicAlertSymbol(asset.Market, asset.NativeID) + "-d1"
	root := hyperliquidHistoryRoot(m.profile)
	outputs, err := filepath.Glob(filepath.Join(root, "data", stem+"-*.csv"))
	if err != nil || len(outputs) != 1 {
		t.Fatal("missing reusable enriched history", outputs, err)
	}
	// Preserve a distinctive mtime as well as bytes: a recalculation that writes
	// identical values must still be detected on the immediate and restarted runs.
	paths := []string{outputs[0], filepath.Join(root, "raw", stem+".csv"), filepath.Join(root, "raw", stem+".json"), filepath.Join(root, "raw", stem+".enriched.json")}
	before := map[string][]byte{}
	stamp := time.Unix(100000, 0)
	for _, path := range paths {
		before[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	marks := map[string]int64{}
	for key, value := range owner.state.Watermarks {
		marks[key] = value
	}
	recent := append([]LiveAlertOutcome{}, owner.state.Recent...)
	assertReuse := func(owner *LiveAlerts) {
		t.Helper()
		check(owner, 0, 1)
		if len(fake.ranges) != 0 || !reflect.DeepEqual(marks, owner.state.Watermarks) || !reflect.DeepEqual(recent, owner.state.Recent) {
			t.Fatal("same daily close repeated history requests or delivery claims", fake.ranges, owner.state.Watermarks)
		}
		for _, path := range paths {
			after, err := os.ReadFile(path)
			stat, statErr := os.Stat(path)
			if err != nil || statErr != nil || !bytes.Equal(before[path], after) || !stat.ModTime().Equal(stamp) {
				t.Fatal("same daily close rewrote raw or recalculated enriched history", path, err, statErr)
			}
		}
	}
	// A fully processed cutoff needs no indicator subprocess at all.
	// Keep the runtime files valid but make starting Node impossible.
	node := m.runtime.node
	m.runtime.node = filepath.Join(t.TempDir(), "node-must-not-start-for-completed-bars")
	assertReuse(owner)
	owner.Close()
	owner = newOwner()
	assertReuse(owner)
	// At the next close the durable claim no longer covers the new bar. Only
	// the saved reconciliation overlap plus the new day may be requested.
	m.runtime.node = node
	cutoff = cutoff.Add(24 * time.Hour)
	check(owner, 1, 2)
	if len(fake.ranges) != 1 || fake.ranges[0] != [2]int64{end - day, end + day} {
		t.Fatal("new daily close refetched warm-up instead of its delta", fake.ranges)
	}
	key := livePublicClaimKey(entry.ID, "hyperliquid", publicAlertSymbol(asset.Market, asset.NativeID), "d1")
	if owner.state.Watermarks[key] != end*1000 || delivered[0].Time != (end-day)*1000 || delivered[1].Time != end*1000 {
		t.Fatal("repeat skipped the new candle or replayed the previous signal", owner.state.Watermarks, delivered)
	}
	check(owner, 0, 2)
	if len(fake.ranges) != 1 {
		t.Fatal("newly checked daily delta was downloaded a second time", fake.ranges)
	}
}
