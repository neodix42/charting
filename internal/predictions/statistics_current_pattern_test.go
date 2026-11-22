package predictions

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCurrentPatternUsesLatestNativeClosedBodiesAndMatchesEquivalentCustomQuery(t *testing.T) {
	for _, period := range []string{"h1", "d1", "w1"} {
		for _, count := range []int{3, 5, 10} {
			t.Run(period+"-"+strconv.Itoa(count), func(t *testing.T) {
				dataset, request, bars := patternFixture(t, period, strings.Repeat("DDDUUUUDDDDUUUDU", 3))
				request.PatternMode, request.PatternLength = "current", count
				request.Pattern = []string{"ignored custom draft"}
				cutoff := closedAt(bars[len(bars)-1].Time, period)
				got, err := CalculateStatistics(context.Background(), dataset, request, cutoff)
				if err != nil {
					t.Fatal(err)
				}
				want := make([]string, count)
				for index, bar := range bars[len(bars)-count:] {
					want[index] = statisticsDirection(bar)
					if got.Prefix[index].Time != bar.Time || got.Prefix[index].Direction != want[index] {
						t.Fatal("current pattern time/body did not come from its native source", got.Prefix, bar)
					}
				}
				if got.PatternMode != "current" || got.PatternLength != count || !reflect.DeepEqual(got.Pattern, want) || got.Anchor != bars[len(bars)-1] || request.Pattern[0] != "ignored custom draft" {
					t.Fatal("current mode used the client draft or lost resolved identity", got, request)
				}
				request.PatternMode, request.Pattern = "custom", want
				custom, err := CalculatePatternStatistics(context.Background(), dataset, request, cutoff)
				if err != nil || custom.PatternMode != "custom" || custom.PatternLength != count || custom.MatchedPatterns != got.MatchedPatterns || custom.EligiblePatterns != got.EligiblePatterns || custom.ExcludedDojiPatterns != got.ExcludedDojiPatterns || custom.ExcludedInvalidPatterns != got.ExcludedInvalidPatterns || !reflect.DeepEqual(custom.Scenarios, got.Scenarios) || custom.SourceHash != got.SourceHash {
					t.Fatal("resolved current pattern differs from the same custom query", got, custom, err)
				}
			})
		}
	}
}

func TestCurrentPatternSnapshotCannotBeReplacedByCallerEditsOrFileChanges(t *testing.T) {
	dataset, request, bars := patternFixture(t, "h1", strings.Repeat("D", 20))
	request.PatternMode, request.PatternLength, request.Pattern = "current", 3, nil
	ctx := &statisticsMutatingContext{Context: context.Background(), mutateAt: 2, mutate: func() {
		request.PatternMode, request.PatternLength, request.Pattern = "custom", 10, []string{"up", "up", "up"}
	}}
	got, err := CalculatePatternStatistics(ctx, dataset, request, closedAt(bars[19].Time, "h1"))
	if err != nil || got.PatternMode != "current" || got.PatternLength != 3 || got.MatchedPatterns != 14 || !reflect.DeepEqual(got.Pattern, []string{"down", "down", "down"}) {
		t.Fatal("caller edits replaced an in-flight current query", got, err)
	}
	request.PatternMode, request.PatternLength, request.Pattern = "current", 3, nil
	path := filepath.Join(dataset.Directory, dataset.Filename)
	original, _ := os.ReadFile(path)
	ctx = &statisticsMutatingContext{Context: context.Background(), mutateAt: 3, mutate: func() {
		if err := os.WriteFile(path, append(original, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := CalculatePatternStatistics(ctx, dataset, request, closedAt(bars[19].Time, "h1")); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal("source replacement mixed a current pattern and historical sample", err)
	}
}

func TestCurrentPatternNeverUsesAnUnclosedNativeCandle(t *testing.T) {
	for _, period := range []string{"h1", "d1", "w1"} {
		dataset, request, bars := patternFixture(t, period, "DDDUUUDDDUDU")
		request.PatternMode, request.PatternLength, request.Pattern = "current", 3, nil
		cutoff := closedAt(bars[len(bars)-1].Time, period)
		got, err := CalculatePatternStatistics(context.Background(), dataset, request, cutoff-1)
		if err != nil || got.Anchor != bars[len(bars)-2] || !reflect.DeepEqual(got.Pattern, []string{"down", "up", "down"}) {
			t.Fatal("partial latest candle changed current pattern", period, got, err)
		}
		got, err = CalculatePatternStatistics(context.Background(), dataset, request, cutoff)
		if err != nil || got.Anchor != bars[len(bars)-1] || !reflect.DeepEqual(got.Pattern, []string{"up", "down", "up"}) {
			t.Fatal("exact close did not advance current pattern", period, got, err)
		}
		future := StatisticsCandle{Time: cutoff, Open: 100, High: 101, Low: 99, Close: 100}
		if err := os.WriteFile(filepath.Join(dataset.Directory, dataset.Filename), []byte(statisticsCSV(append(bars, future))), 0600); err != nil {
			t.Fatal(err)
		}
		withFuture, err := CalculatePatternStatistics(context.Background(), dataset, request, cutoff)
		if err != nil || !reflect.DeepEqual(withFuture.Pattern, got.Pattern) || !reflect.DeepEqual(withFuture.Scenarios, got.Scenarios) {
			t.Fatal("an unclosed future doji entered the current pattern", period, withFuture, err)
		}
		if _, err := CalculatePatternStatistics(context.Background(), dataset, request, closedAt(future.Time, period)); err == nil || !strings.Contains(err.Error(), "doji") {
			t.Fatal("a selected closed doji was silently skipped", period, err)
		}
	}
}

func TestCurrentPatternRejectsUnusableSelectedBarsWithoutSkippingThem(t *testing.T) {
	for _, kind := range []string{"doji", "invalid"} {
		for _, position := range []int{0, 1, 2} {
			dataset, request, bars := patternFixture(t, "d1", "DDDUUUDDDUDU")
			request.PatternMode, request.PatternLength, request.Pattern = "current", 3, nil
			bar := &bars[len(bars)-3+position]
			if kind == "doji" {
				bar.Close = bar.Open
			} else {
				bar.High = 1
			}
			if err := os.WriteFile(filepath.Join(dataset.Directory, dataset.Filename), []byte(statisticsCSV(bars)), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := CalculatePatternStatistics(context.Background(), dataset, request, closedAt(bars[len(bars)-1].Time, "d1")); err == nil || !strings.Contains(err.Error(), kind) {
				t.Fatal("an unusable selected bar was guessed or skipped", kind, position, err)
			}
		}
	}
	dataset, request, bars := patternFixture(t, "h1", "UDU")
	request.PatternMode, request.PatternLength, request.Pattern = "current", 4, nil
	if _, err := CalculatePatternStatistics(context.Background(), dataset, request, closedAt(bars[2].Time, "h1")); err == nil || !strings.Contains(err.Error(), "at least 4") {
		t.Fatal("insufficient current bars were accepted", err)
	}
}

func TestCurrentPatternModeAndLengthValidationPreservesCustomAndLegacyRequests(t *testing.T) {
	valid := StatisticsRequest{DataSource: "dukascopy", Asset: "eurusd", Period: "h1"}
	for _, request := range []StatisticsRequest{
		valid,
		{DataSource: "dukascopy", Asset: "eurusd", Pattern: []string{"up", "down", "up"}},
		{DataSource: "dukascopy", Asset: "eurusd", PatternMode: "custom", PatternLength: 3, Pattern: []string{"up", "down", "up"}},
		{DataSource: "dukascopy", Asset: "eurusd", PatternMode: "current", PatternLength: 3},
		{DataSource: "dukascopy", Asset: "eurusd", PatternMode: "current", PatternLength: 10, Pattern: []string{"ignored"}},
	} {
		if err := ValidateStatisticsRequest(request); err != nil {
			t.Fatal("valid mode request rejected", request, err)
		}
	}
	for _, request := range []StatisticsRequest{
		{DataSource: "dukascopy", Asset: "eurusd", PatternMode: "CURRENT", PatternLength: 3},
		{DataSource: "dukascopy", Asset: "eurusd", PatternMode: "custom"},
		{DataSource: "dukascopy", Asset: "eurusd", PatternMode: "custom", PatternLength: 4, Pattern: []string{"up", "down", "up"}},
		{DataSource: "dukascopy", Asset: "eurusd", PatternMode: "current"},
		{DataSource: "dukascopy", Asset: "eurusd", PatternMode: "current", PatternLength: 2},
		{DataSource: "dukascopy", Asset: "eurusd", PatternMode: "current", PatternLength: 11},
		{DataSource: "dukascopy", Asset: "eurusd", PatternLength: 3},
	} {
		if err := ValidateStatisticsRequest(request); err == nil {
			t.Fatal("invalid mode request accepted", request)
		}
	}
	if UsesPatternStatistics(valid) || !UsesPatternStatistics(StatisticsRequest{PatternMode: "current", PatternLength: 3}) {
		t.Fatal("legacy/current native routing changed")
	}
}
