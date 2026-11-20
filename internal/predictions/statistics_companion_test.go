package predictions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func statisticsNativeCandle(timestamp int64, direction string, close float64) StatisticsCandle {
	candle := statisticsTestWeek(timestamp, direction, []float64{close})[0]
	return candle
}

func statisticsCompanionFixture(t *testing.T, period string) (Dataset, Dataset, StatisticsRequest, int64) {
	t.Helper()
	candles, target := statisticsTestHistory()
	daily, request, _ := statisticsTestDataset(t, candles)
	request.Period = period
	native := daily
	native.Period, native.Filename = period, period+".csv"
	checkpointCloses := []float64{50, 100, 25, 100, 150}
	nativeCandles := []StatisticsCandle{}
	for index, direction := range []string{"U", "U", "U", "D", "U"} {
		timestamp := target - int64(4-index)*7*statisticsDay + 10*3_600_000
		if period == "w1" {
			timestamp = target - int64(5-index)*7*statisticsDay
		}
		nativeCandles = append(nativeCandles, statisticsNativeCandle(timestamp, direction, checkpointCloses[index]))
	}
	if period == "w1" {
		// This current weekly candle remains open until next Monday.
		nativeCandles = append(nativeCandles, statisticsNativeCandle(target, "U", 999))
	}
	if err := os.WriteFile(filepath.Join(native.Directory, native.Filename), []byte(statisticsCSV(nativeCandles)), 0600); err != nil {
		t.Fatal(err)
	}
	return native, daily, request, target
}

func TestStatisticsH1UsesNativeAnchorSameHourBodyAndDailyJointSequences(t *testing.T) {
	native, daily, request, target := statisticsCompanionFixture(t, "h1")
	cutoff := target + 11*3_600_000
	got, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, cutoff)
	if err != nil || got.Period != "h1" || got.Anchor.Time != target+10*3_600_000 || got.Anchor.Close != 150 || got.WeekStart != target || len(got.History) != 5 || got.MatchedWeeks != 3 || got.EligibleWeeks != 4 || got.TheoreticalScenarios != 32 || len(got.Scenarios) != 2 {
		t.Fatalf("H1 anchor, same-hour conditioning or daily suffix probabilities changed: %+v %v", got, err)
	}
	if got.FirstClosedBar != target-4*7*statisticsDay+10*3_600_000 {
		t.Fatal("H1 source coverage start came from its D1 companion", got.FirstClosedBar)
	}
	if len(got.Prefix) != 1 || got.Prefix[0].Day != "Mon 10:00 H1" || got.Prefix[0].Direction != "up" || got.Conditioning == "" {
		t.Fatal("H1 body condition was hidden or daily prefix invented", got.Prefix, got.Conditioning)
	}
	first := got.Scenarios[0]
	if first.ID != "UUDDU" || first.Count != 2 || first.Probability != 2.0/3 || len(first.Points) != 5 {
		t.Fatal("H1 sequence distribution lost joint daily outcomes", first)
	}
	for day, price := range []float64{300, 330, 324, 297, 315} {
		point := first.Points[day]
		if point.Time != target+int64(day)*statisticsDay+23*3_600_000 || math.Abs(point.Close-price) > 1e-10 || point.Observed {
			t.Fatalf("H1 path is not normalized by historical same-hour closes: %+v", first.Points)
		}
	}
	if before, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, cutoff-1); err != nil || before.Anchor.Time == got.Anchor.Time || len(before.History) != 4 || before.FirstClosedBar != got.FirstClosedBar {
		t.Fatal("unclosed H1 anchor entered the model before its exact close", before, err)
	}
	for dataset, want := range map[string]string{native.Filename: got.SourceHash, daily.Filename: got.CompanionSourceHash} {
		bytes, err := os.ReadFile(filepath.Join(native.Directory, dataset))
		checksum := sha256.Sum256(bytes)
		if err != nil || want != hex.EncodeToString(checksum[:]) {
			t.Fatal("selected/companion source hash missing or changed", dataset, want, err)
		}
	}
}

func TestStatisticsH1ExcludesMissingInvalidAndDojiNativeCheckpoints(t *testing.T) {
	native, daily, request, target := statisticsCompanionFixture(t, "h1")
	rows := []StatisticsCandle{
		statisticsNativeCandle(target-4*7*statisticsDay+10*3_600_000, "U", 50),
		statisticsNativeCandle(target-3*7*statisticsDay+10*3_600_000, "=", 100),
		// The third historical same-hour checkpoint is absent.
		statisticsNativeCandle(target-7*statisticsDay+10*3_600_000, "D", 100),
		statisticsNativeCandle(target+10*3_600_000, "U", 150),
	}
	rows[2].High = rows[2].Low - 1
	if err := os.WriteFile(filepath.Join(native.Directory, native.Filename), []byte(statisticsCSV(rows)), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+11*3_600_000)
	if err != nil || got.ExcludedCheckpointWeeks != 3 || got.ExcludedInvalidWeeks != 0 || got.ExcludedDojiWeeks != 0 || got.MatchedWeeks != 1 || got.EligibleWeeks != 1 || len(got.Scenarios) != 1 || got.Scenarios[0].Probability != 1 {
		t.Fatal("unavailable H1 checkpoints became valid down bodies or D1 exclusions", got, err)
	}
	for _, candle := range got.History {
		if !validStatisticsCandle(candle) {
			t.Fatal("invalid native checkpoint escaped into chart history", candle)
		}
	}
	rows[3].Open = rows[3].Close
	if err := os.WriteFile(filepath.Join(native.Directory, native.Filename), []byte(statisticsCSV(rows)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+11*3_600_000); err == nil || !strings.Contains(err.Error(), "last closed H1 candle is a doji") {
		t.Fatal("current H1 doji invented a body condition", err)
	}
}

func TestStatisticsH1FridayStillProjectsUntilTheFinalHourCloses(t *testing.T) {
	native, daily, request, target := statisticsCompanionFixture(t, "h1")
	rows := []StatisticsCandle{}
	for week, close := range []float64{50, 100, 25, 100, 150} {
		for _, hour := range []int64{22, 23} {
			rows = append(rows, statisticsNativeCandle(target-int64(4-week)*7*statisticsDay+4*statisticsDay+hour*3_600_000, "U", close))
		}
	}
	if err := os.WriteFile(filepath.Join(native.Directory, native.Filename), []byte(statisticsCSV(rows)), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+4*statisticsDay+23*3_600_000)
	if err != nil || got.Anchor.Time != target+4*statisticsDay+22*3_600_000 || got.TheoreticalScenarios != 2 || got.MatchedWeeks != 2 || len(got.Scenarios) != 1 || len(got.Scenarios[0].Points) != 1 || got.Scenarios[0].Points[0].Time != target+4*statisticsDay+23*3_600_000 {
		t.Fatal("Friday H1 lost its remaining final close checkpoint", got, err)
	}
	completed, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+5*statisticsDay)
	if err != nil || completed.Anchor.Time != target+4*statisticsDay+23*3_600_000 || completed.TheoreticalScenarios != 0 || len(completed.Scenarios) != 0 {
		t.Fatal("completed Friday H1 rolled into another week", completed, err)
	}
}

func TestStatisticsH1RequiresTheClosedDailyPrefixAndIsolatesCompanionSource(t *testing.T) {
	native, daily, request, target := statisticsCompanionFixture(t, "h1")
	if err := os.WriteFile(filepath.Join(native.Directory, native.Filename), []byte(statisticsCSV([]StatisticsCandle{statisticsNativeCandle(target+statisticsDay+3*3_600_000, "U", 150)})), 0600); err != nil {
		t.Fatal(err)
	}
	candles, _ := statisticsTestHistory()
	candles = append(candles[:20], candles[21:]...) // Missing the already-closed target Monday.
	if err := os.WriteFile(filepath.Join(daily.Directory, daily.Filename), []byte(statisticsCSV(candles)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+statisticsDay+4*3_600_000); err == nil || !strings.Contains(err.Error(), "missing its closed Mon") {
		t.Fatal("H1 silently shortened a missing closed D1 prefix", err)
	}
	daily.DataSource = "hyperliquid"
	if _, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+statisticsDay+4*3_600_000); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatal("another provider's daily data was used as a companion", err)
	}
}

func TestStatisticsW1UsesLastClosedWeeklyBodyAndObservedDailyBridge(t *testing.T) {
	native, daily, request, target := statisticsCompanionFixture(t, "w1")
	got, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target)
	if err != nil || got.Period != "w1" || got.Anchor.Time != target-7*statisticsDay || got.Anchor.Close != 150 || got.WeekStart != target || len(got.History) != 5 || got.MatchedWeeks != 3 || got.TheoreticalScenarios != 32 || len(got.Scenarios) != 2 || len(got.Prefix) != 1 || got.Prefix[0].Day != "Previous W1" {
		t.Fatalf("weekly Monday cutoff or previous-body conditioning changed: %+v %v", got, err)
	}
	if got.FirstClosedBar != target-5*7*statisticsDay {
		t.Fatal("W1 source coverage start came from its D1 companion", got.FirstClosedBar)
	}
	if first := got.Scenarios[0]; first.ID != "UUDDU" || first.Probability != 2.0/3 || len(first.Points) != 5 || first.Points[0].Time != target || first.Points[0].Close != 300 || first.Points[0].Observed {
		t.Fatal("W1 empty-prefix paths did not use previous historical weekly closes", first)
	}
	if before, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target-1); err != nil || before.Anchor.Time != target-14*statisticsDay || before.WeekStart != target-7*statisticsDay || before.FirstClosedBar != got.FirstClosedBar {
		t.Fatal("W1 current candle was treated as closed before Monday 00:00 UTC", before, err)
	}
	observed, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+statisticsDay)
	if err != nil || observed.Anchor != got.Anchor || observed.TheoreticalScenarios != 16 || observed.MatchedWeeks != 3 || len(observed.Prefix) != 2 {
		t.Fatal("W1 native anchor was replaced or current Monday prefix lost", observed, err)
	}
	first := observed.Scenarios[0]
	if len(first.Points) != 5 || !first.Points[0].Observed || first.Points[0].Time != target || first.Points[0].Close != 300 || first.Points[1].Observed || math.Abs(first.Points[1].Close-330) > 1e-10 || first.Points[4].Time != target+4*statisticsDay {
		t.Fatal("known D1 close was forecast or normalized to the old W1 price", first.Points)
	}
	completed, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+5*statisticsDay)
	if err != nil || completed.TheoreticalScenarios != 0 || len(completed.Scenarios) != 0 {
		t.Fatal("W1 with a complete Friday prefix rolled forward", completed, err)
	}
}

func TestStatisticsW1AllowsNoAvailableCurrentDailyPrefixAndBoundsStaleHistory(t *testing.T) {
	native, daily, request, target := statisticsCompanionFixture(t, "w1")
	candles, _ := statisticsTestHistory()
	if err := os.WriteFile(filepath.Join(daily.Directory, daily.Filename), []byte(statisticsCSV(candles[:20])), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+statisticsDay)
	if err != nil || len(got.Prefix) != 1 || got.TheoreticalScenarios != 32 || got.MatchedWeeks != 3 || len(got.Scenarios[0].Points) != 5 || got.Scenarios[0].Points[0].Observed {
		t.Fatal("missing current-week D1 data invented an observed prefix", got, err)
	}
	if !strings.Contains(strings.Join(got.Warnings, " "), "contains 0 of 1") {
		t.Fatal("already-closed but unavailable daily prefix was hidden", got.Warnings)
	}
	// Keep a weekly source that ends one week earlier, while daily data already
	// contains later weeks. No later daily week may become the target prefix.
	weekly := []StatisticsCandle{}
	for index, direction := range []string{"U", "U", "U", "D"} {
		weekly = append(weekly, statisticsNativeCandle(target-int64(5-index)*7*statisticsDay, direction, 100))
	}
	if err := os.WriteFile(filepath.Join(native.Directory, native.Filename), []byte(statisticsCSV(weekly)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(daily.Directory, daily.Filename), []byte(statisticsCSV(candles)), 0600); err != nil {
		t.Fatal(err)
	}
	stale, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+10*statisticsDay)
	if err != nil || stale.WeekStart != target-7*statisticsDay || stale.TheoreticalScenarios != 0 || len(stale.Prefix) != 6 || len(stale.Scenarios) != 0 || !strings.Contains(strings.Join(stale.Warnings, " "), "W1 history is stale") {
		t.Fatal("stale weekly data consumed a later daily prefix or rolled forward", stale, err)
	}
}

func TestStatisticsNativeAlignmentAndCompanionSnapshotsAreValidated(t *testing.T) {
	for _, period := range []string{"h1", "w1"} {
		t.Run(period, func(t *testing.T) {
			native, daily, request, target := statisticsCompanionFixture(t, period)
			badTime := target + 1
			if period == "w1" {
				badTime = target - 6*statisticsDay // A Tuesday cannot be W1.
			}
			if err := os.WriteFile(filepath.Join(native.Directory, native.Filename), []byte(statisticsCSV([]StatisticsCandle{statisticsNativeCandle(badTime, "U", 100)})), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target+statisticsDay); err == nil || !strings.Contains(err.Error(), "timestamp") {
				t.Fatal("misaligned native candles were relabeled to the chosen period", err)
			}
		})
	}
}

func TestStatisticsCompanionChecksBothSourcesAfterCalculation(t *testing.T) {
	for _, source := range []string{"selected", "daily"} {
		t.Run(source, func(t *testing.T) {
			native, daily, request, target := statisticsCompanionFixture(t, "h1")
			mutated, other := native, daily
			if source == "daily" {
				mutated, other = daily, native
			}
			path, otherPath := filepath.Join(mutated.Directory, mutated.Filename), filepath.Join(other.Directory, other.Filename)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			otherOriginal, err := os.ReadFile(otherPath)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &statisticsMutatingContext{Context: context.Background(), mutateAt: 6, mutate: func() {
				if err := os.WriteFile(path, append(original, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}}
			if _, err := CalculateStatisticsWithCompanion(ctx, native, daily, request, target+11*3_600_000); err == nil || !strings.Contains(err.Error(), "changed during prediction") {
				t.Fatal("selected/companion mutation escaped the final snapshot check", source, ctx.calls, err)
			}
			if after, err := os.ReadFile(otherPath); err != nil || string(after) != string(otherOriginal) {
				t.Fatal("calculation changed its other source after external mutation", err)
			}
		})
	}
}

func TestStatisticsNativeBodyNoMatchesDoesNotInventEqualProbabilities(t *testing.T) {
	for _, period := range []string{"h1", "w1"} {
		t.Run(period, func(t *testing.T) {
			native, daily, request, target := statisticsCompanionFixture(t, period)
			rows := []StatisticsCandle{}
			for index := range 5 {
				timestamp := target - int64(4-index)*7*statisticsDay + 10*3_600_000
				if period == "w1" {
					timestamp = target - int64(5-index)*7*statisticsDay
				}
				direction := "D"
				if index == 4 {
					direction = "U"
				}
				rows = append(rows, statisticsNativeCandle(timestamp, direction, 100))
			}
			if err := os.WriteFile(filepath.Join(native.Directory, native.Filename), []byte(statisticsCSV(rows)), 0600); err != nil {
				t.Fatal(err)
			}
			cutoff := target
			if period == "h1" {
				cutoff += 11 * 3_600_000
			}
			got, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, cutoff)
			if err != nil || got.EligibleWeeks != 4 || got.MatchedWeeks != 0 || got.TheoreticalScenarios != 32 || len(got.Scenarios) != 0 || !strings.Contains(strings.Join(got.Warnings, " "), "No complete historical weeks match") {
				t.Fatal("native body mismatch was ignored or uniform probabilities fabricated", got, err)
			}
			request.LookbackWeeks = 1
			limited, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, cutoff)
			if err != nil || limited.EligibleWeeks != 1 || limited.MatchedWeeks != 0 {
				t.Fatal("native-period lookback did not use the target-week boundary", limited, err)
			}
		})
	}
}

func TestStatisticsW1NeverUsesTheOpenNativeWeeklyBody(t *testing.T) {
	native, daily, request, target := statisticsCompanionFixture(t, "w1")
	before, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(native.Directory, native.Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lines[len(lines)-1] = strings.Split(lines[len(lines)-1], ",")[0] + ",still-open,,,"
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := CalculateStatisticsWithCompanion(context.Background(), native, daily, request, target)
	if err != nil || after.Anchor != before.Anchor || after.MatchedWeeks != before.MatchedWeeks || len(after.Scenarios) != len(before.Scenarios) || after.SourceHash == before.SourceHash {
		t.Fatal("unfinished W1 data leaked into the closed anchor or source identity", after, err)
	}
}
