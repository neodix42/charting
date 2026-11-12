package screener

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDukascopyRepeatedManualDailyCheckCountsInspectedAssetsWithoutResending(t *testing.T) {
	m := analysisTestManager(t)
	const history = "timestamp,open,high,low,close,volume\n1704067200000,10,12,9,11,1\n1704153600000,11,14,10,13,1\n1704240000000,13,16,12,15,1\n"
	symbols := []string{"eurusd", "gbpusd", "usdjpy", "audusd", "usdchf", "usdcad"}
	request := AnalysisRequest{Period: "d1", ProfitabilityFilter: 80, Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(1))}}
	saved, err := m.SaveAnalysisCondition("Daily eligible asset inspection", request)
	if err != nil {
		t.Fatal(err)
	}
	saved.Analysis = &SavedConditionAnalysis{AnalyzedAt: 1704153600000}
	for index, symbol := range symbols {
		name := symbol + "-d1-2024-01-01-2024-01-04.csv"
		writeDataFile(t, m, name, history)
		hits := int64(8)
		if index == len(symbols)-1 {
			hits = 7 // Existing historical evidence excludes this sixth asset.
		}
		saved.Analysis.Assets = append(saved.Analysis.Assets, liveProfitRow(symbol, "d1", name, hits, 10))
	}
	if err := writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: []SavedAnalysisCondition{saved}}); err != nil {
		t.Fatal(err)
	}
	if got := len(liveConditionEligiblePairs(saved)); got != 5 {
		t.Fatalf("expected exactly five historically qualified daily assets, got %d", got)
	}
	sends, refreshes := 0, 0
	asOf := time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC)
	newAlerts := func() *LiveAlerts {
		t.Helper()
		l, err := NewLiveAlerts(m, func(context.Context, LiveAlert) error { sends++; return nil })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(l.Close)
		l.now = func() time.Time { return asOf }
		l.refresh = func(ctx context.Context, _ string, groups []historyGroup, _ int64) (map[string]bool, error) {
			pairs := map[string]bool{}
			for _, group := range groups {
				match := downloadFilename.FindStringSubmatch(group.name)
				pairs[match[1]+"/"+match[2]] = true
				if match[1] == "usdcad" {
					t.Fatal("manual check widened historical profitability eligibility")
				}
			}
			if len(pairs) != 5 {
				t.Fatalf("manual check refreshed %d pairs, want five", len(pairs))
			}
			refreshes++
			return pairs, ctx.Err()
		}
		return l
	}
	l := newAlerts()
	config := LiveAlertsConfig{Enabled: true, ConditionIDs: []string{saved.ID}}
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	assertCheck := func(l *LiveAlerts) {
		t.Helper()
		if err := l.check(context.Background(), config, asOf.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		info := l.Info()
		if len(info.Checks) != 1 || info.Checks[0].Status != "completed" || !info.Checks[0].Manual || info.Checks[0].AssetsScanned != 5 || info.Checks[0].AssetsSkipped != 0 || info.Checks[0].Error != "" || sends != 5 || info.Sent != 5 || len(info.Recent) != 5 {
			t.Fatalf("inspected daily assets were miscounted or already-processed signals resent: %+v; sends=%d", info.Checks, sends)
		}
	}
	assertCheck(l)
	watermarks := map[string]int64{}
	for key, value := range l.state.Watermarks {
		watermarks[key] = value
	}
	recent := append([]LiveAlertOutcome{}, l.state.Recent...)
	assertCheck(l)
	if !reflect.DeepEqual(watermarks, l.state.Watermarks) || !reflect.DeepEqual(recent, l.state.Recent) {
		t.Fatal("repeated manual inspection changed processed-bar or delivery history")
	}
	restored := newAlerts()
	assertCheck(restored)
	if !reflect.DeepEqual(watermarks, restored.state.Watermarks) || !reflect.DeepEqual(recent, restored.state.Recent) || refreshes != 3 {
		t.Fatal("restart changed durable claims or skipped actual file refresh")
	}
	for _, symbol := range symbols {
		data, err := os.ReadFile(filepath.Join(m.directory, symbol+"-d1-2024-01-01-2024-01-04.csv"))
		if err != nil || string(data) != history {
			t.Fatal("counting a repeated check changed retained candle data", symbol, err)
		}
	}
}

func TestDukascopyMissingOrFailedHistoryDoesNotCountAsInspected(t *testing.T) {
	for _, name := range []string{"refresh failure", "no closed candle", "scheduled candle pending"} {
		t.Run(name, func(t *testing.T) {
			_, l, config, path := liveAlertsFixture(t)
			asOf := time.Date(2024, 1, 1, 2, 30, 0, 0, time.UTC).UnixMilli()
			scheduled := false
			switch name {
			case "refresh failure":
				l.refresh = func(context.Context, string, []historyGroup, int64) (map[string]bool, error) {
					return nil, errors.New("provider unavailable")
				}
			case "no closed candle":
				if err := os.WriteFile(path, []byte("timestamp,open,high,low,close,volume,sma_2\n1704074400000,13,16,12,15,1,14\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "scheduled candle pending":
				scheduled = true
				asOf = time.Date(2024, 1, 1, 4, 0, 0, 0, time.UTC).UnixMilli()
			}
			_ = l.checkCycle(context.Background(), config, asOf, scheduled)
			if row := l.Info().Checks[0]; row.AssetsScanned != 0 {
				t.Fatal("unavailable candle or failed history inflated inspection count", row)
			}
		})
	}
}
