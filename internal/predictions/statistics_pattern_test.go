package predictions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func patternFixture(t *testing.T, period, directions string) (Dataset, StatisticsRequest, []StatisticsCandle) {
	t.Helper()
	step := map[string]int64{"h1": 3_600_000, "d1": statisticsDay, "w1": 7 * statisticsDay}[period]
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	bars := make([]StatisticsCandle, 0, len(directions))
	for index, direction := range directions {
		open, close := float64(100+index), float64(101+index)
		if direction == 'D' {
			close = open - 1
		} else if direction == '=' {
			close = open
		}
		bars = append(bars, StatisticsCandle{Time: start + int64(index)*step, Open: open, High: max(open, close) + 1, Low: min(open, close) - 1, Close: close})
	}
	dataset, request, _ := statisticsTestDataset(t, bars)
	dataset.Period, request.Period = period, period
	request.Pattern = []string{"down", "down", "down"}
	return dataset, request, bars
}

func TestPatternStatisticsNativePeriodsCountSlidingOutcomesIncludingDoji(t *testing.T) {
	for _, period := range []string{"h1", "d1", "w1"} {
		t.Run(period, func(t *testing.T) {
			dataset, request, bars := patternFixture(t, period, "DDDUUUU"+"DDDDUUUU"+"DDD=UUUU"+"DDD")
			cutoff := closedAt(bars[len(bars)-1].Time, period)
			got, err := CalculateStatistics(context.Background(), dataset, request, cutoff)
			if err != nil {
				t.Fatal(err)
			}
			if got.MatchedPatterns != 4 || got.NextBarDojiCount != 1 || got.Period != period || len(got.Scenarios) != 3 || got.CompanionSourceFile != "" || !reflect.DeepEqual(got.Pattern, request.Pattern) {
				t.Fatalf("wrong native outcomes or unexpected companion: %+v", got)
			}
			counts, probability := map[string]int{}, float64(0)
			for _, scenario := range got.Scenarios {
				counts[scenario.ID] = scenario.Count
				probability += scenario.Probability
				if len(scenario.Points) != 1 || scenario.Points[0].Time != cutoff || scenario.Probability != float64(scenario.Count)/4 {
					t.Fatal("outcome is not a single native next bar", scenario)
				}
			}
			if !reflect.DeepEqual(counts, map[string]int{"U": 2, "D": 1, "0": 1}) || probability != 1 || got.ExcludedDojiPatterns != 3 {
				t.Fatal("doji changed denominator or input windows", counts, probability, got.ExcludedDojiPatterns)
			}
			// Projection uses next close / the preceding native close, not
			// next candle body return and not a daily companion checkpoint.
			for _, scenario := range got.Scenarios {
				if scenario.ID == "D" {
					want := got.Anchor.Close * bars[10].Close / bars[9].Close
					if math.Abs(scenario.Points[0].Close-want) > 1e-12 {
						t.Fatal("wrong close-to-close normalization", scenario.Points[0].Close, want)
					}
				}
			}
			request.Pattern[0] = "up"
			if got.Pattern[0] != "down" || got.Prefix[0].Direction != "down" {
				t.Fatal("returned result aliases a mutable request")
			}
		})
	}
}

func TestPatternStatisticsExcludesTheEntireAnchorWindowAndDoesNotUseFutureValues(t *testing.T) {
	dataset, request, bars := patternFixture(t, "h1", "DDDDDDDDDDDDUUU")
	cutoff := closedAt(bars[len(bars)-1].Time, "h1")
	got, err := CalculatePatternStatistics(context.Background(), dataset, request, cutoff)
	if err != nil || got.MatchedPatterns != 9 || got.EligiblePatterns != 9 || len(got.Scenarios) != 1 || got.Scenarios[0].ID != "D" {
		t.Fatal("anchor-overlapping or unmatched next candles entered the sample", got, err)
	}
	future := append(append([]StatisticsCandle{}, bars...), StatisticsCandle{Time: cutoff, Open: 10, High: 11, Low: 9, Close: 10})
	path := filepath.Join(dataset.Directory, dataset.Filename)
	if err := os.WriteFile(path, []byte(statisticsCSV(future)), 0600); err != nil {
		t.Fatal(err)
	}
	withFuture, err := CalculatePatternStatistics(context.Background(), dataset, request, cutoff)
	if err != nil || withFuture.MatchedPatterns != got.MatchedPatterns || !reflect.DeepEqual(withFuture.Scenarios, got.Scenarios) || withFuture.Anchor != got.Anchor {
		t.Fatal("partial next bar changed the result", withFuture, err)
	}
	// Exact cutoff makes the final row eligible, while one millisecond before
	// retains the preceding native bar as the anchor.
	before, err := CalculatePatternStatistics(context.Background(), dataset, request, cutoff-1)
	if err != nil || before.Anchor.Time != bars[len(bars)-2].Time {
		t.Fatal("last closed native candle cutoff changed", before.Anchor, err)
	}
}

func TestPatternStatisticsIgnoresWeekdaysAndAllowsActualSessionGaps(t *testing.T) {
	dataset, request, bars := patternFixture(t, "d1", "DDDUUUU"+"DDDDUUUU"+"DDD")
	// A Friday-to-Monday session gap remains adjacent downloaded candles.
	for index := 5; index < len(bars); index++ {
		bars[index].Time += 2 * statisticsDay
	}
	path := filepath.Join(dataset.Directory, dataset.Filename)
	if err := os.WriteFile(path, []byte(statisticsCSV(bars)), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := CalculatePatternStatistics(context.Background(), dataset, request, closedAt(bars[len(bars)-1].Time, "d1"))
	if err != nil || got.MatchedPatterns != 3 || len(got.Scenarios) != 2 {
		t.Fatal("session gaps or weekdays prevented native matching", got, err)
	}
	for _, warning := range got.Warnings {
		if strings.Contains(warning, "remaining") || strings.Contains(warning, "Monday") {
			t.Fatal("legacy weekday horizon escaped into native pattern mode", warning)
		}
	}
}

func TestPatternStatisticsLookbackUsesAnchorTimeAndIncludesBoundaryWindows(t *testing.T) {
	dataset, request, bars := patternFixture(t, "d1", strings.Repeat("D", 25))
	request.LookbackWeeks = 1
	got, err := CalculatePatternStatistics(context.Background(), dataset, request, closedAt(bars[24].Time, "d1")+3*statisticsDay)
	// Starts17..18 have outcomes20..21, strictly before anchor window22..24.
	if err != nil || got.EligiblePatterns != 2 || got.MatchedPatterns != 2 {
		t.Fatal("lookback was rounded to weekdays, cutoff or excluded its boundary", got, err)
	}
}

func TestPatternStatisticsPreservesSourceIdentityAndExcludesInvalidOHLCWindows(t *testing.T) {
	dataset, request, bars := patternFixture(t, "d1", strings.Repeat("D", 15))
	bars[4].High = 1 // Finite but structurally invalid; never drop it to join neighbors.
	text := statisticsCSV(bars)
	if err := os.WriteFile(filepath.Join(dataset.Directory, dataset.Filename), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := CalculatePatternStatistics(context.Background(), dataset, request, closedAt(bars[14].Time, "d1"))
	hash := sha256.Sum256([]byte(text))
	if err != nil || got.ExcludedInvalidPatterns != 4 || got.MatchedPatterns != 5 || got.SourceHash != hex.EncodeToString(hash[:]) || got.FirstClosedBar != bars[0].Time {
		t.Fatal("invalid bars were repaired, dropped from windows or hash/coverage changed", got, err)
	}
	after, _ := os.ReadFile(filepath.Join(dataset.Directory, dataset.Filename))
	if string(after) != text {
		t.Fatal("statistics changed downloaded data")
	}
}

func TestPatternStatisticsValidatesBoundsDirectionsSourceAndCancellation(t *testing.T) {
	dataset, request, bars := patternFixture(t, "d1", strings.Repeat("D", 25))
	for _, pattern := range [][]string{{}, {"up", "down"}, strings.Fields(strings.Repeat("up ", 11)), {"up", "down", "doji"}, {"up", "DOWN", "up"}} {
		invalid := request
		invalid.Pattern = pattern
		if _, err := CalculatePatternStatistics(context.Background(), dataset, invalid, closedAt(bars[24].Time, "d1")); err == nil {
			t.Fatal("invalid pattern accepted", pattern)
		}
	}
	request.Pattern = strings.Fields(strings.Repeat("down ", 10))
	if _, err := CalculatePatternStatistics(context.Background(), dataset, request, closedAt(bars[24].Time, "d1")); err != nil {
		t.Fatal("maximum supported pattern rejected", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CalculatePatternStatistics(ctx, dataset, request, closedAt(bars[24].Time, "d1")); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled pattern scan continued", err)
	}
	wrong := dataset
	wrong.Period = "h1"
	if _, err := CalculatePatternStatistics(context.Background(), wrong, request, closedAt(bars[24].Time, "d1")); err == nil {
		t.Fatal("another timeframe accepted")
	}
}

func TestPatternStatisticsDetectsChangesDuringEitherScanAndBeforeReturn(t *testing.T) {
	for _, check := range []int{2, 3, 4, 5} {
		dataset, request, bars := patternFixture(t, "h1", strings.Repeat("D", 20))
		path := filepath.Join(dataset.Directory, dataset.Filename)
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		ctx := &statisticsMutatingContext{Context: context.Background(), mutateAt: check, mutate: func() {
			if err := os.WriteFile(path, append(original, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
		}}
		if _, err := CalculatePatternStatistics(ctx, dataset, request, closedAt(bars[19].Time, "h1")); err == nil || !strings.Contains(err.Error(), "changed") {
			t.Fatal("a mixed source snapshot escaped", check, ctx.calls, err)
		}
	}
}

func TestPatternStatisticsComparesExactHashesBetweenPassesEvenIfMetadataIsRestored(t *testing.T) {
	dataset, request, bars := patternFixture(t, "h1", strings.Repeat("D", 20))
	path := filepath.Join(dataset.Directory, dataset.Filename)
	original, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	ctx := &statisticsMutatingContext{Context: context.Background(), mutateAt: 2, mutate: func() {
		changed := strings.Replace(string(original), ",99\n", ",98\n", 1)
		if changed == string(original) {
			t.Fatal("fixture did not replace a candle")
		}
		if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := CalculatePatternStatistics(ctx, dataset, request, closedAt(bars[19].Time, "h1")); err == nil || !strings.Contains(err.Error(), "between statistics scans") {
		t.Fatal("same-size/modtime source replacement bypassed hash comparison", err)
	}
}

func TestPatternStatisticsCapturesTheRequestBeforeReadingData(t *testing.T) {
	dataset, request, bars := patternFixture(t, "h1", strings.Repeat("D", 20))
	ctx := &statisticsMutatingContext{Context: context.Background(), mutateAt: 2, mutate: func() {
		request.Pattern[0] = "up"
	}}
	got, err := CalculatePatternStatistics(ctx, dataset, request, closedAt(bars[19].Time, "h1"))
	if err != nil || got.MatchedPatterns != 14 || got.Pattern[0] != "down" || got.Prefix[0].Direction != "down" {
		t.Fatal("request mutation changed an in-flight calculation", got, err)
	}
}
