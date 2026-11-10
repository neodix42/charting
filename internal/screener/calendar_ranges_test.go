package screener

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

var calendarRangeIDs = []string{"today_low", "today_high", "previous_day_low", "previous_day_high", "week_low", "week_high", "previous_week_low", "previous_week_high", "two_days_ago_low", "two_days_ago_high", "three_days_ago_low", "three_days_ago_high"}

func TestDayWeekRangeCatalogAndScope(t *testing.T) {
	m := &Manager{}
	for _, id := range calendarRangeIDs {
		definition := indicatorDefinitions()[id]
		if !definition.IntradayOnly || len(definition.Parameters) != 0 || definition.Category != "Day / Week ranges" {
			t.Fatalf("range definition: %+v", definition)
		}
		for _, period := range []string{"", "all", "s1", "m1", "m5", "m15", "m30", "h1", "h4"} {
			request, err := m.validateEnrichment(EnrichmentRequest{Indicator: id, Period: period, Source: "askprice"})
			if err != nil || request.Source != "auto" || len(request.Parameters) != 0 {
				t.Fatalf("%s/%s: %+v %v", id, period, request, err)
			}
		}
		for _, period := range []string{"tick", "d1", "w1", "mn1"} {
			if _, err := m.validateEnrichment(EnrichmentRequest{Indicator: id, Period: period}); err == nil {
				t.Fatalf("%s accepted %s", id, period)
			}
		}
		if _, err := m.validateEnrichment(EnrichmentRequest{Indicator: id, Parameters: map[string]float64{"period": 10}}); err == nil {
			t.Fatal("parameterless range accepted lookback")
		}
	}
}

func TestDayWeekEnrichmentFeedsIntradayAnalysisWithoutDailyLookahead(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	name := "eurusd-h1-2024-01-01-2024-01-16.csv"
	var text strings.Builder
	text.WriteString("timestamp,open,high,low,close,volume\n")
	dates := []string{"2024-01-01T00:00:00Z", "2024-01-01T23:00:00Z", "2024-01-02T00:00:00Z", "2024-01-02T12:00:00Z", "2024-01-05T23:00:00Z", "2024-01-08T00:00:00Z", "2024-01-08T23:00:00Z", "2024-01-09T00:00:00Z", "2024-01-15T00:00:00Z"}
	lows := []int{8, 6, 9, 5, 7, 10, 4, 6, 3}
	highs := []int{12, 15, 11, 20, 19, 18, 25, 10, 13}
	for i, date := range dates {
		stamp, _ := time.Parse(time.RFC3339, date)
		mid := float64(lows[i]+highs[i]) / 2
		fmt.Fprintf(&text, "%d,%g,%d,%d,%g,1\n", stamp.UnixMilli(), mid, highs[i], lows[i], mid)
	}
	source := writeDataFile(t, m, name, text.String())
	untouched := map[string]string{}
	for _, excluded := range []string{"eurusd-d1-2024-01-01-2024-01-16.csv", "eurusd-w1-2024-01-01-2024-01-16.csv", "eurusd-mn1-2024-01-01-2024-01-16.csv", "eurusd-tick-2024-01-01-2024-01-16.csv", "custom.csv"} {
		untouched[writeDataFile(t, m, excluded, "timestamp,close\nnot-a-valid-candle,1\n")] = "timestamp,close\nnot-a-valid-candle,1\n"
	}
	id := dataFileID(m.directory, name)
	for _, indicator := range append(append([]string{}, calendarRangeIDs...), "today_low") {
		started, err := m.StartEnrichment(EnrichmentRequest{Indicator: indicator})
		if err != nil || started.Total != 1 {
			t.Fatalf("intraday-only batch: %+v %v", started, err)
		}
		done := waitEnrichment(t, m, []string{id})
		if done.Completed != 1 || done.Failed != 0 || len(done.Jobs) != 1 {
			t.Fatalf("enrichment: %+v", done)
		}
	}
	for path, want := range untouched {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("excluded CSV changed %s: %s %v", path, got, err)
		}
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records[0]) != 6+len(calendarRangeIDs) {
		t.Fatalf("duplicate/lost columns: %v", records[0])
	}
	metadata, err := readFileMetadata(m.directory, name)
	if err != nil || len(metadata.Recipes) != len(calendarRangeIDs) {
		t.Fatalf("recipes: %+v %v", metadata, err)
	}
	condition := analysisCondition("below", analysisField("today_low"), analysisField("previous_day_low"))
	request := AnalysisRequest{Period: "h1", Conditions: []AnalysisCondition{condition}}
	if _, err = m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	if info.Total != 1 || info.Failed != 0 || info.TotalMatches != 3 {
		t.Fatalf("intraday matches: %+v", info)
	}
	result := resultNamed(t, info, name)
	for occurrence, wantIndex := range []int64{3, 6, 8} {
		chart, err := m.AnalysisChart(result.ID, occurrence, 0, 0)
		if err != nil || chart.MatchIndex != wantIndex {
			t.Fatalf("match %d: %+v %v", occurrence, chart, err)
		}
		if occurrence == 0 && (!reflect.DeepEqual(chart.Bars[0].Values["today_low"], float64(5)) || chart.Bars[0].Values["previous_day_low"] != 6) {
			t.Fatalf("midday signal lost running/previous lows: %+v", chart.Bars)
		}
	}
	saved, err := m.SaveAnalysisCondition("Today below previous day", request)
	if err != nil || saved.Request.Conditions[0].Left.Field != "today_low" {
		t.Fatalf("save: %+v %v", saved, err)
	}
}

func TestPublicAlertDayWeekRangeWarmupAndEnrichment(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	worker, plans, err := startPublicAlertWorker(context.Background(), m.runtime, map[string]publicAlertPlan{"h1": {Fields: calendarRangeIDs, Extra: 1}, "m1": {Fields: []string{"previous_week_low"}, Extra: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.close()
	if plans["h1"].Bars != 338 || plans["m1"].Bars != 20162 {
		t.Fatalf("calendar warmup: %+v", plans)
	}
	var bars []domain.Candle
	for i := 0; i < plans["h1"].Bars; i++ {
		bars = append(bars, domain.Candle{Time: 1704067200 + int64(i)*3600, Open: 10, High: 12, Low: 8, Close: 11, Volume: 1})
	}
	data, _ := publicAlertRawCSV(bars, false)
	folder := t.TempDir()
	input, output := filepath.Join(folder, "raw.csv"), filepath.Join(folder, "enriched.csv")
	if err = os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = worker.enrich("h1", input, output); err != nil {
		t.Fatal(err)
	}
	enriched, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(enriched))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]int{}
	for index, field := range rows[0] {
		fields[field] = index
	}
	for _, id := range calendarRangeIDs {
		if rows[len(rows)-1][fields[id]] == "" {
			t.Fatalf("missing calendar range %s", id)
		}
	}
	encoded, _ := json.Marshal(plans)
	if !strings.Contains(string(encoded), "20162") {
		t.Fatal("warmup plan lost")
	}
}
