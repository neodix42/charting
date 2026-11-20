package predictions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func statisticsTestWeek(start int64, pattern string, closes []float64) []StatisticsCandle {
	result := []StatisticsCandle{}
	for index, body := range pattern {
		close := closes[index]
		open := close + 1
		if body == 'U' {
			open = close - 1
		} else if body == '=' {
			open = close
		}
		result = append(result, StatisticsCandle{Time: start + int64(index)*statisticsDay, Open: open, High: max(open, close) + 1, Low: min(open, close) - 1, Close: close})
	}
	return result
}

func statisticsCSV(candles []StatisticsCandle) string {
	var data strings.Builder
	data.WriteString("timestamp,open,high,low,close\n")
	for _, candle := range candles {
		fmt.Fprintf(&data, "%d,%g,%g,%g,%g\n", candle.Time, candle.Open, candle.High, candle.Low, candle.Close)
	}
	return data.String()
}

func statisticsTestDataset(t *testing.T, candles []StatisticsCandle) (Dataset, StatisticsRequest, string) {
	t.Helper()
	dataset := Dataset{DataSource: "dukascopy", ID: "fixture", Symbol: "eurusd", Name: "EUR/USD", Period: "d1", Directory: t.TempDir(), Filename: "daily.csv"}
	text := statisticsCSV(candles)
	if err := os.WriteFile(filepath.Join(dataset.Directory, dataset.Filename), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return dataset, StatisticsRequest{DataSource: "dukascopy", Asset: "eurusd"}, text
}

func statisticsTestHistory() ([]StatisticsCandle, int64) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	candles := []StatisticsCandle{}
	for index, week := range []struct {
		pattern string
		closes  []float64
	}{
		{"UUDDU", []float64{100, 110, 108, 99, 105}},
		{"UUDDU", []float64{200, 220, 216, 198, 210}},
		{"UDUUD", []float64{50, 48, 49, 55, 52}},
		{"DDDDD", []float64{100, 110, 108, 99, 105}},
	} {
		candles = append(candles, statisticsTestWeek(start+int64(index)*7*statisticsDay, week.pattern, week.closes)...)
	}
	anchor := start + 4*7*statisticsDay
	candles = append(candles, statisticsTestWeek(anchor, "UUDDU", []float64{300, 330, 324, 297, 315})...)
	return candles, anchor
}

func TestStatisticsConditionsJointSequencesOnOwnDailyBodiesAndNormalizesHistoricalPrices(t *testing.T) {
	candles, anchor := statisticsTestHistory()
	dataset, request, original := statisticsTestDataset(t, candles)
	got, err := CalculateStatistics(context.Background(), dataset, request, anchor+statisticsDay)
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchedWeeks != 3 || got.EligibleWeeks != 4 || got.TheoreticalScenarios != 16 || len(got.Scenarios) != 2 || len(got.Prefix) != 1 || got.Prefix[0].Direction != "up" || got.Anchor.Time != anchor || got.Anchor.Close != 300 {
		t.Fatalf("wrong prefix/sample/anchor: %+v", got)
	}
	first, second := got.Scenarios[0], got.Scenarios[1]
	if first.ID != "UDDU" || first.Count != 2 || first.Probability != 2.0/3 || second.ID != "DUUD" || second.Count != 1 || second.Probability != 1.0/3 {
		t.Fatalf("joint sequences were flattened to independent/equal probabilities: %+v", got.Scenarios)
	}
	wants := [][]float64{{330, 324, 297, 315}, {288, 294, 330, 312}}
	for index, scenario := range got.Scenarios {
		for day, point := range scenario.Points {
			if point.Time != anchor+int64(day+1)*statisticsDay || math.Abs(point.Close-wants[index][day]) > 1e-10 {
				t.Fatalf("scenario path is not normalized to the current anchor: %+v", scenario)
			}
		}
	}
	if len(got.History) != 21 || got.History[len(got.History)-1] != got.Anchor {
		t.Fatalf("unclosed future candles entered chart history: %+v", got.History)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "Only 3 matching historical weeks") || !strings.Contains(got.Warnings[0], "small sample") {
		t.Fatal("small matching sample was presented without its limitation", got.Warnings)
	}
	checksum := sha256.Sum256([]byte(original))
	if got.SourceHash != hex.EncodeToString(checksum[:]) || got.SourceFile != dataset.Filename || got.WeekStart != anchor || got.AsOf != anchor+statisticsDay || got.DataSource != request.DataSource || got.Symbol != request.Asset {
		t.Fatalf("source/cutoff identity lost: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), dataset.Directory) {
		t.Fatalf("statistics exposed a path or nonfinite value: %s %v", encoded, err)
	}
	if after, _ := os.ReadFile(filepath.Join(dataset.Directory, dataset.Filename)); string(after) != original {
		t.Fatal("statistics changed downloaded candle bytes")
	}
}

func TestStatisticsWeekdayHorizonsRespectExactClosedBarCutoffsThroughFriday(t *testing.T) {
	candles, anchor := statisticsTestHistory()
	dataset, request, _ := statisticsTestDataset(t, candles)
	for day, scenarios := range []int{16, 8, 4, 2, 0} {
		cutoff := anchor + int64(day+1)*statisticsDay
		got, err := CalculateStatistics(context.Background(), dataset, request, cutoff)
		if err != nil || got.Anchor.Time != anchor+int64(day)*statisticsDay || got.TheoreticalScenarios != scenarios || len(got.Prefix) != day+1 {
			t.Fatalf("day%d cutoff%v: %+v %v", day, time.UnixMilli(cutoff), got, err)
		}
		if day < 4 {
			for _, scenario := range got.Scenarios {
				if len(scenario.Points) != 4-day || scenario.Points[len(scenario.Points)-1].Time != anchor+4*statisticsDay {
					t.Fatalf("Friday missing or scenario continued past Friday: %+v", scenario)
				}
			}
		} else if len(got.Scenarios) != 0 || len(got.Warnings) == 0 {
			t.Fatal("Friday completion invented a next-week forecast", got)
		}
		if day > 0 {
			before, err := CalculateStatistics(context.Background(), dataset, request, cutoff-1)
			if err != nil || before.Anchor.Time != anchor+int64(day-1)*statisticsDay {
				t.Fatal("open daily candle was included one millisecond early", day, before, err)
			}
		}
	}
	for _, weekend := range []int64{6, 7} {
		got, err := CalculateStatistics(context.Background(), dataset, request, anchor+weekend*statisticsDay)
		if err != nil || got.TheoreticalScenarios != 0 || len(got.Scenarios) != 0 || got.WeekStart != anchor {
			t.Fatal("weekend without a new closed weekday rolled forward", got, err)
		}
	}
}

func TestStatisticsExcludesMissingDojiInvalidAndUnmatchedWeeksAndHonorsLookback(t *testing.T) {
	candles, anchor := statisticsTestHistory()
	// First week incomplete; second week contains a doji; third has invalid
	// OHLC; fourth is eligible but its down Monday does not match the prefix.
	candles = append(candles[:2], candles[3:]...)
	candles[5].Open = candles[5].Close
	candles[10].High = candles[10].Low - 1
	dataset, request, _ := statisticsTestDataset(t, candles)
	got, err := CalculateStatistics(context.Background(), dataset, request, anchor+statisticsDay)
	if err != nil || got.ExcludedIncompleteWeeks != 1 || got.ExcludedDojiWeeks != 1 || got.ExcludedInvalidWeeks != 1 || got.EligibleWeeks != 1 || got.MatchedWeeks != 0 || len(got.Scenarios) != 0 || got.TheoreticalScenarios != 16 {
		t.Fatalf("incomplete/doji/invalid/mismatched weeks were reused or probabilities fabricated: %+v %v", got, err)
	}
	for _, candle := range got.History {
		if !validStatisticsCandle(candle) {
			t.Fatal("invalid OHLC escaped into chart history", candle)
		}
	}
	request.LookbackWeeks = 1
	limited, err := CalculateStatistics(context.Background(), dataset, request, anchor+statisticsDay)
	if err != nil || limited.EligibleWeeks != 1 || limited.ExcludedIncompleteWeeks+limited.ExcludedDojiWeeks+limited.ExcludedInvalidWeeks != 0 {
		t.Fatalf("lookback did not stop at the anchor-relative boundary: %+v %v", limited, err)
	}
}

func TestStatisticsRejectsUnusableCurrentPrefixInsteadOfDroppingAnObservedDay(t *testing.T) {
	for _, condition := range []string{"missing-monday", "doji", "invalid"} {
		t.Run(condition, func(t *testing.T) {
			candles, anchor := statisticsTestHistory()
			switch condition {
			case "missing-monday":
				candles = append(candles[:20], candles[21:]...)
			case "doji":
				candles[20].Open = candles[20].Close
			case "invalid":
				candles[20].Close = -1
			}
			dataset, request, _ := statisticsTestDataset(t, candles)
			if _, err := CalculateStatistics(context.Background(), dataset, request, anchor+2*statisticsDay); err == nil || !strings.Contains(err.Error(), "current week") {
				t.Fatal("unusable current prefix was silently truncated", condition, err)
			}
		})
	}
}

func TestStatisticsIgnoresFutureCandleValuesButKeepsSourceIdentityAndValidatesClosedRows(t *testing.T) {
	candles, anchor := statisticsTestHistory()
	dataset, request, original := statisticsTestDataset(t, candles)
	path := filepath.Join(dataset.Directory, dataset.Filename)
	before, err := CalculateStatistics(context.Background(), dataset, request, anchor+statisticsDay)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(original), "\n")
	lines[22] = fmt.Sprintf("%d,not-yet-known,,,", anchor+statisticsDay)
	modified := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := CalculateStatistics(context.Background(), dataset, request, anchor+statisticsDay)
	if err != nil || !reflect.DeepEqual(before.Scenarios, after.Scenarios) || after.SourceHash == before.SourceHash {
		t.Fatal("open candle leaked into conditional probabilities/price paths or source hash ignored bytes", after, err)
	}
	if _, err := CalculateStatistics(context.Background(), dataset, request, anchor+2*statisticsDay); err == nil || !strings.Contains(err.Error(), "finite number") {
		t.Fatal("a malformed fully closed row became valid data", err)
	}
}

func TestStatisticsRejectsUnsafeCSVSourceAndSourceRequestMismatches(t *testing.T) {
	candles, anchor := statisticsTestHistory()
	dataset, request, _ := statisticsTestDataset(t, candles)
	for _, modify := range []func(*Dataset, *StatisticsRequest){
		func(d *Dataset, r *StatisticsRequest) { d.Filename = "../daily.csv" },
		func(d *Dataset, r *StatisticsRequest) { d.Directory = "relative" },
		func(d *Dataset, r *StatisticsRequest) { d.Period = "h1" },
		func(d *Dataset, r *StatisticsRequest) { r.DataSource = "hyperliquid" },
		func(d *Dataset, r *StatisticsRequest) { r.DataSource = "binance" },
		func(d *Dataset, r *StatisticsRequest) { r.Asset = "other" },
		func(d *Dataset, r *StatisticsRequest) { r.LookbackWeeks = -1 },
		func(d *Dataset, r *StatisticsRequest) { r.LookbackWeeks = 2601 },
	} {
		d, r := dataset, request
		modify(&d, &r)
		if _, err := CalculateStatistics(context.Background(), d, r, anchor+statisticsDay); err == nil {
			t.Fatal("unsafe source or invalid request accepted", d, r)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CalculateStatistics(ctx, dataset, request, anchor+statisticsDay); err == nil || err != context.Canceled {
		t.Fatal("cancelled statistics continued", err)
	}
	snapshot, err := readStatisticsSource(context.Background(), dataset, anchor+statisticsDay)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataset.Directory, dataset.Filename), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.snapshot.unchanged(); err == nil {
		t.Fatal("source mutation was not detected")
	}
}

func TestStatisticsRequestCandlePeriodsAndLegacyDailyDefault(t *testing.T) {
	for _, period := range []string{"", "h1", "d1", "w1"} {
		request := StatisticsRequest{DataSource: "dukascopy", Asset: "eurusd", Period: period}
		if err := ValidateStatisticsRequest(request); err != nil {
			t.Fatal("supported candle timeframe rejected", period, err)
		}
		want := period
		if period == "" {
			want = "d1"
		}
		if StatisticsPeriod(period) != want {
			t.Fatal("legacy default or explicit timeframe changed", period, StatisticsPeriod(period))
		}
	}
	for _, period := range []string{"m1", "mn1", "D1", "tick", "all", " d1"} {
		if err := ValidateStatisticsRequest(StatisticsRequest{DataSource: "dukascopy", Asset: "eurusd", Period: period}); err == nil {
			t.Fatal("unsupported candle timeframe accepted", period)
		}
	}
}

func TestStatisticsCSVRejectsMalformedClosedDataWithoutRepairingOrReorderingIt(t *testing.T) {
	candles, anchor := statisticsTestHistory()
	for _, tc := range []struct {
		name, text, message string
	}{
		{"missing-open", "timestamp,high,low,close\n1704067200000,102,99,101\n", "open, high, low"},
		{"duplicate-header", "timestamp,open,high,low,close,Close\n1704067200000,100,102,99,101,101\n", "duplicate field"},
		{"duplicate-time", statisticsCSV([]StatisticsCandle{candles[0], candles[0]}), "strictly increasing"},
		{"reverse-time", statisticsCSV([]StatisticsCandle{candles[1], candles[0]}), "strictly increasing"},
		{"non-midnight", strings.Replace(statisticsCSV(candles), fmt.Sprint(candles[0].Time), fmt.Sprint(candles[0].Time+3_600_000), 1), "UTC-midnight"},
		{"nan", "timestamp,open,high,low,close\n1704067200000,100,102,99,NaN\n", "finite number"},
		{"blank", "timestamp,open,high,low,close\n1704067200000,100,102,99,\n", "finite number"},
		{"malformed-columns", "timestamp,open,high,low,close\n1704067200000,100,102\n", "inconsistent columns"},
		{"oversized-field", "timestamp,open,high,low,close\n1704067200000,100,102,99,\"" + strings.Repeat("x", 1024*1024+1) + "\"\n", "exceeds 1 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataset, request, _ := statisticsTestDataset(t, nil)
			path := filepath.Join(dataset.Directory, dataset.Filename)
			if err := os.WriteFile(path, []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := CalculateStatistics(context.Background(), dataset, request, anchor+statisticsDay); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatal("malformed closed data accepted or error unclear", err)
			}
			if after, _ := os.ReadFile(path); string(after) != tc.text {
				t.Fatal("malformed data was repaired or reordered")
			}
		})
	}
}

func TestStatisticsHistoryBoundAndUTCYearBoundary(t *testing.T) {
	start := time.Date(2024, 3, 25, 0, 0, 0, 0, time.UTC).UnixMilli()
	candles := []StatisticsCandle{}
	for week := range 40 {
		candles = append(candles, statisticsTestWeek(start+int64(week)*7*statisticsDay, "UUUUU", []float64{100, 101, 102, 103, 104})...)
	}
	anchor := start + 40*7*statisticsDay // Monday 30 December 2024.
	candles = append(candles, statisticsTestWeek(anchor, "UUUUU", []float64{200, 201, 202, 203, 204})...)
	dataset, request, _ := statisticsTestDataset(t, candles)
	got, err := CalculateStatistics(context.Background(), dataset, request, anchor+statisticsDay)
	if err != nil || len(got.History) != 160 || got.History[0] != candles[41] || got.History[159] != candles[200] || got.EligibleWeeks != 40 || got.MatchedWeeks != 40 {
		t.Fatalf("bounded history changed ordering, retained synthetic sessions or lost training weeks: %+v %v", got, err)
	}
	if len(got.Scenarios) != 1 || got.Scenarios[0].Points[3].Time != time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatal("Friday projection crossed the UTC year boundary incorrectly", got.Scenarios)
	}
	if len(got.Warnings) != 0 {
		t.Fatal("40 matching weeks incorrectly received the small-sample heuristic", got.Warnings)
	}
	for _, point := range got.Scenarios[0].Points {
		if point.Time <= got.Anchor.Time || point.Time%statisticsDay != 0 {
			t.Fatal("projected time lost its daily UTC opening timestamp", point)
		}
	}
}

type statisticsMutatingContext struct {
	context.Context
	calls    int
	mutateAt int
	mutate   func()
}

func (ctx *statisticsMutatingContext) Err() error {
	ctx.calls++
	mutateAt := ctx.mutateAt
	if mutateAt == 0 {
		mutateAt = 4
	}
	if ctx.calls == mutateAt {
		ctx.mutate()
	}
	return nil
}

func TestStatisticsDetectsSourceMutationAfterReadingBeforeReturningResult(t *testing.T) {
	candles, anchor := statisticsTestHistory()
	dataset, request, original := statisticsTestDataset(t, candles)
	ctx := &statisticsMutatingContext{Context: context.Background(), mutate: func() {
		if err := os.WriteFile(filepath.Join(dataset.Directory, dataset.Filename), []byte(original+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := CalculateStatistics(ctx, dataset, request, anchor+statisticsDay); err == nil || !strings.Contains(err.Error(), "changed during prediction") {
		t.Fatal("a mixed CSV snapshot escaped after the file changed during calculation", err)
	}
}
