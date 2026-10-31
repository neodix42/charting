package screener

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func liveProfitRow(symbol, period, file string, hits, matches int64) SavedConditionProfitability {
	return SavedConditionProfitability{Symbol: symbol, Timeframe: period, FileName: file, Matches: matches, TakeProfitHits: hits, ProfitablePercent: 100 * float64(hits) / float64(matches)}
}

func TestLiveProfitabilityUsesExactBoundaryPeriodAndStableBestFile(t *testing.T) {
	condition := SavedAnalysisCondition{Request: AnalysisRequest{ProfitabilityFilter: 85}, Analysis: &SavedConditionAnalysis{AnalyzedAt: 1, Assets: []SavedConditionProfitability{
		liveProfitRow("eurusd", "h1", "z-file.csv", 17, 20),
		liveProfitRow("eurusd", "h1", "a-file.csv", 17, 20),
		liveProfitRow("eurusd", "d1", "perfect-daily.csv", 20, 20),
		liveProfitRow("gbpusd", "h1", "other-symbol.csv", 20, 20),
	}}}
	result, allowed := SavedConditionProfitabilityFor(condition, "eurusd", "h1")
	if !allowed || result == nil || result.FileName != "a-file.csv" || result.ProfitablePercent != 85 {
		t.Fatalf("threshold equality or deterministic per-period choice failed: %+v %t", result, allowed)
	}
	result.Matches = 1
	if condition.Analysis.Assets[1].Matches != 20 {
		t.Fatal("profitability lookup exposed mutable saved evidence")
	}
	condition.Analysis.Assets = append(condition.Analysis.Assets, liveProfitRow("eurusd", "h1", "better.csv", 10, 11))
	result, allowed = SavedConditionProfitabilityFor(condition, "eurusd", "h1")
	if !allowed || result.TakeProfitHits != 10 || result.Matches != 11 {
		t.Fatal("did not retain highest actual ratio", result, allowed)
	}
	condition.Request.ProfitabilityFilter = 100
	if _, allowed = SavedConditionProfitabilityFor(condition, "eurusd", "h1"); allowed {
		t.Fatal("hourly condition borrowed perfect daily evidence")
	}
	if result, allowed = SavedConditionProfitabilityFor(condition, "eurusd", "d1"); !allowed || result.ProfitablePercent != 100 {
		t.Fatal("100 should retain a perfect historical file", result, allowed)
	}
	condition.Request.ProfitabilityFilter = 0
	condition.Analysis = nil
	if result, allowed = SavedConditionProfitabilityFor(condition, "eurusd", "h1"); allowed || result != nil {
		t.Fatal("zero filter without matching evidence must remain excluded")
	}
	for _, threshold := range []int{5, 85, 100} {
		row := liveProfitRow("eurusd", "h1", "huge.csv", math.MaxInt64, math.MaxInt64)
		if !liveProfitabilityAtLeast(row, threshold) {
			t.Fatalf("large valid counts overflowed threshold %d", threshold)
		}
	}
	// The rounded display is 85.00%, but the exact ratio remains below 85%.
	if liveProfitabilityAtLeast(liveProfitRow("eurusd", "h1", "rounded.csv", 849999, 1000000), 85) {
		t.Fatal("display rounding widened the qualified set")
	}
}

func TestLiveAlertsProfitabilityUnionDoesNotWidenEachConditionOrRefreshUnrelatedHistories(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	names := map[string]string{
		"eurusd/h1": "eurusd-h1-2024-01-01-2024-01-03.csv",
		"eurusd/d1": "eurusd-d1-2023-12-31-2024-01-02.csv",
		"gbpusd/h1": "gbpusd-h1-2024-01-01-2024-01-03.csv",
		"gbpusd/d1": "gbpusd-d1-2023-12-31-2024-01-02.csv",
		"audusd/h1": "audusd-h1-2024-01-01-2024-01-03.csv",
	}
	for pair, name := range names {
		if pair == "eurusd/h1" {
			continue
		}
		csv := reviewHourlyCSV(1704067200000, []float64{11, 13, 15})
		if strings.HasSuffix(pair, "/d1") {
			csv = "timestamp,open,high,low,close\n1703980800000,1,2,1,1\n"
		}
		writeDataFile(t, m, name, csv)
	}
	conditions, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	first := conditions[0]
	first.Request.ProfitabilityFilter = 85
	daily := analysisField("close")
	daily.Timeframe = "d1"
	companion := analysisCondition("above", analysisField("close"), daily)
	companion.ID = "required-daily"
	first.Request.Conditions = append(first.Request.Conditions, companion)
	first.Analysis = &SavedConditionAnalysis{AnalyzedAt: 1704067200000, Assets: []SavedConditionProfitability{
		liveProfitRow("eurusd", "h1", names["eurusd/h1"], 10, 11),
		liveProfitRow("gbpusd", "h1", names["gbpusd/h1"], 2, 10),
	}}
	second, err := saveLiveCondition(t, m, "GBP qualifier", conditions[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	second.Request.ProfitabilityFilter = 85
	second.Analysis = &SavedConditionAnalysis{AnalyzedAt: 1704067200000, Assets: []SavedConditionProfitability{
		liveProfitRow("eurusd", "h1", names["eurusd/h1"], 2, 10),
		liveProfitRow("gbpusd", "h1", names["gbpusd/h1"], 9, 10),
	}}
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: []SavedAnalysisCondition{first, second}}); err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = []string{first.ID, second.ID}
	if _, err = l.Configure(config); err != nil {
		t.Fatal(err)
	}
	var refreshed []string
	refresh := l.refresh
	l.refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
		for _, group := range groups {
			match := downloadFilename.FindStringSubmatch(group.name)
			refreshed = append(refreshed, match[1]+"/"+match[2])
		}
		return refresh(ctx, directory, groups, asOf)
	}
	var alerts []LiveAlert
	l.send = func(_ context.Context, alert LiveAlert) error { alerts = append(alerts, alert); return nil }
	asOf := time.Date(2024, 1, 1, 3, 30, 0, 0, time.UTC).UnixMilli()
	if err = l.check(context.Background(), config, asOf); err != nil {
		t.Fatal(err)
	}
	sort.Strings(refreshed)
	if !reflect.DeepEqual(refreshed, []string{"eurusd/d1", "eurusd/h1", "gbpusd/h1"}) {
		t.Fatalf("profitability/period scopes refreshed unrelated base or companion files: %v", refreshed)
	}
	if len(alerts) != 2 {
		t.Fatalf("conditions were cross-multiplied over the asset union: %+v", alerts)
	}
	for _, alert := range alerts {
		wanted := "eurusd"
		hits, matches := int64(10), int64(11)
		if alert.ConditionID == second.ID {
			wanted = "gbpusd"
			hits, matches = 9, 10
		}
		if alert.Symbol != wanted || alert.Timeframe != "h1" || alert.Profitability == nil || alert.Profitability.TakeProfitHits != hits || alert.Profitability.Matches != matches {
			t.Fatalf("wrong historical profitability/asset scope: %+v", alert)
		}
	}
	info := l.Info()
	if len(info.Recent) != 2 || info.Recent[0].Profitability == nil {
		t.Fatal("delivery did not retain recorded profitability", info.Recent)
	}
	info.Recent[0].Profitability.TakeProfitHits = 0
	if l.Info().Recent[0].Profitability.TakeProfitHits == 0 {
		t.Fatal("Info returned mutable profitability history")
	}
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.Info().Recent[0].Profitability == nil || restored.Info().Recent[0].Profitability.TakeProfitHits == 0 {
		t.Fatal("restart lost recorded signal profitability")
	}
	if _, err = restored.Configure(config); err != nil {
		t.Fatal("saved condition evidence did not survive restart", err)
	}
	// Obsolete public choices cannot alter the inherited condition/asset scope.
	wrong := copyLiveAlertsConfig(config)
	wrong.Symbols = []string{"audusd"}
	wrong.Periods = []string{"d1"}
	if _, err = restored.Configure(wrong); err != nil {
		t.Fatal("obsolete selections changed inherited eligibility", err)
	}
	for _, scope := range restored.Info().ConditionScopes {
		if len(scope.Targets) != 1 || scope.Targets[0].Period != "h1" || scope.Targets[0].Symbol == "audusd" {
			t.Fatal("obsolete selectors widened inherited targets", scope)
		}
	}

}

func TestLiveAlertsFilteredConditionWithoutEvidenceDoesNotContactRefreshOrSend(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	saved, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	saved[0].Request.ProfitabilityFilter = 85
	saved[0].Analysis = nil
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: saved}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	l.refresh = func(context.Context, string, []historyGroup, int64) (map[string]bool, error) {
		calls++
		return nil, fmt.Errorf("unexpected refresh")
	}
	l.send = func(context.Context, LiveAlert) error { calls++; return nil }
	if _, err = l.Configure(config); err == nil || !strings.Contains(err.Error(), "recorded Analysis") {
		t.Fatal("filtered condition silently fell back to every asset", err)
	}
	err = l.check(context.Background(), config, time.Date(2024, 1, 1, 3, 30, 0, 0, time.UTC).UnixMilli())
	if err == nil || calls != 0 || len(l.state.Watermarks) != 0 {
		t.Fatal("missing evidence reached refresh/send or claimed bars", err, calls, l.state.Watermarks)
	}
	// A completed analysis with zero qualifiers remains restricted, too.
	saved[0].Analysis = &SavedConditionAnalysis{AnalyzedAt: 1, Assets: []SavedConditionProfitability{}}
	if err = os.Remove(filepath.Join(m.profile, "screener-saved-conditions.json")); err != nil {
		t.Fatal(err)
	}
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: saved}); err != nil {
		t.Fatal(err)
	}
	if _, err = l.Configure(config); err == nil || calls != 0 {
		t.Fatal("empty qualifying set widened to all assets", err, calls)
	}
}

func TestLiveAlertsInconsistentRecordedProfitabilityStateIsPreservedAndBlocked(t *testing.T) {
	m, l, _, _ := liveAlertsFixture(t)
	profit := liveProfitRow("eurusd", "h1", "recorded.csv", 10, 11)
	profit.ProfitablePercent = 99
	state := l.state
	state.Recent = []LiveAlertOutcome{{ConditionName: "Recorded", Symbol: "eurusd", Timeframe: "h1", Time: 1704070800000, Status: "sent", Profitability: &profit}}
	if err := writeState(l.path, state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewLiveAlerts(m, nil)
	if err == nil || restored == nil || !restored.blocked {
		t.Fatal("inconsistent historical counts were accepted", err)
	}
	defer restored.Close()
	restored.Start(context.Background())
	after, readErr := os.ReadFile(l.path)
	if readErr != nil || string(before) != string(after) || restored.started {
		t.Fatal("invalid recorded metrics were rewritten or started delivery", readErr)
	}
}
