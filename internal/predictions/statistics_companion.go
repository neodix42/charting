package predictions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// CalculateStatisticsWithCompanion keeps H1/W1 chart anchors native while
// estimating joint Monday–Friday daily-body sequences from genuine D1 data.
// H1 adds a same-hour body condition; W1 adds the preceding weekly body.
func CalculateStatisticsWithCompanion(ctx context.Context, selectedDataset, dailyDataset Dataset, request StatisticsRequest, asOf int64) (StatisticsForecast, error) {
	if err := ValidateStatisticsRequest(request); err != nil {
		return StatisticsForecast{}, err
	}
	period := StatisticsPeriod(request.Period)
	if period == "d1" {
		return CalculateStatistics(ctx, selectedDataset, request, asOf)
	}
	if selectedDataset.Period != period || selectedDataset.Symbol != request.Asset || statisticsDataSource(selectedDataset) != request.DataSource ||
		dailyDataset.Period != "d1" || dailyDataset.Symbol != request.Asset || statisticsDataSource(dailyDataset) != request.DataSource {
		return StatisticsForecast{}, errors.New("statistics source and companion must match the selected data source, asset and candle timeframe")
	}
	if asOf < 0 || asOf > 253_402_300_799_999 {
		return StatisticsForecast{}, errors.New("statistics cutoff is outside the supported timestamp range")
	}
	native, err := readStatisticsCandleSource(ctx, selectedDataset, asOf, period)
	if err != nil {
		return StatisticsForecast{}, err
	}
	if !validStatisticsCandle(native.anchor) {
		return StatisticsForecast{}, fmt.Errorf("the last closed %s candle has invalid OHLC prices", strings.ToUpper(period))
	}
	anchorDirection := statisticsDirection(native.anchor)
	if anchorDirection == "" {
		return StatisticsForecast{}, fmt.Errorf("the last closed %s candle is a doji (open equals close); its up/down body cannot condition historical weeks", strings.ToUpper(period))
	}
	targetWeek, dailyCutoff := statisticsWeekStart(native.anchor.Time), asOf
	if period == "h1" {
		dailyCutoff = min(asOf, closedAt(native.anchor.Time, "h1"))
	} else {
		targetWeek = closedAt(native.anchor.Time, "w1")
	}
	daily, err := readStatisticsSource(ctx, dailyDataset, dailyCutoff)
	if err != nil {
		return StatisticsForecast{}, fmt.Errorf("daily companion: %w", err)
	}
	result := StatisticsForecast{DataSource: request.DataSource, Symbol: selectedDataset.Symbol, AssetName: selectedDataset.Name,
		Period: period, LookbackWeeks: request.LookbackWeeks, AsOf: asOf, WeekStart: targetWeek,
		SourceFile: selectedDataset.Filename, SourceHash: native.snapshot.checksum,
		CompanionSourceFile: dailyDataset.Filename, CompanionSourceHash: daily.snapshot.checksum,
		FirstClosedBar: native.firstClosedBar, History: native.history, Anchor: native.anchor, Prefix: []StatisticsPrefix{}, Scenarios: []StatisticsScenario{}, Warnings: []string{}}
	if result.AssetName == "" {
		result.AssetName = strings.ToUpper(selectedDataset.Symbol)
	}
	if native.invalidHistory > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d candles with invalid OHLC prices were omitted from chart history.", native.invalidHistory))
	}
	current := daily.weeks[targetWeek]
	prefixLength := 0
	if period == "h1" {
		prefixLength = max(0, min(5, int((dailyCutoff-targetWeek)/statisticsDay)))
		result.Conditioning = "Daily sequences match the completed weekday prefix and the same weekday/hour H1 candle body. Price paths use historical same-hour close ratios."
	} else {
		if current != nil {
			for day, candle := range current.days {
				if candle != nil {
					prefixLength = day + 1
				}
			}
		}
		result.Conditioning = "Daily sequences match the previous W1 candle body and the observed weekday prefix. Known current-week D1 closes remain observed bridge points."
		result.Prefix = append(result.Prefix, StatisticsPrefix{Time: native.anchor.Time, Day: "Previous W1", Direction: anchorDirection})
	}
	dailyPrefix := []StatisticsPrefix{}
	var observed *StatisticsCandle
	for day := 0; day < prefixLength; day++ {
		if current == nil || current.days[day] == nil {
			return StatisticsForecast{}, fmt.Errorf("the target week is missing its closed %s daily companion candle; refresh downloaded D1 data before calculating statistics", statisticsDays[day])
		}
		candle := *current.days[day]
		if !validStatisticsCandle(candle) {
			return StatisticsForecast{}, fmt.Errorf("the target week's %s daily companion candle has invalid OHLC prices", statisticsDays[day])
		}
		direction := statisticsDirection(candle)
		if direction == "" {
			return StatisticsForecast{}, fmt.Errorf("the target week's %s daily companion candle is a doji (open equals close); an up/down prefix cannot be estimated", statisticsDays[day])
		}
		dailyPrefix = append(dailyPrefix, StatisticsPrefix{Time: candle.Time, Day: statisticsDays[day], Direction: direction})
		observed = &candle
	}
	result.Prefix = append(result.Prefix, dailyPrefix...)
	if period == "h1" {
		result.Prefix = append(result.Prefix, StatisticsPrefix{Time: native.anchor.Time, Day: time.UnixMilli(native.anchor.Time).UTC().Format("Mon 15:04") + " H1", Direction: anchorDirection})
	}
	if period == "w1" {
		expected := max(0, min(5, int((asOf-targetWeek)/statisticsDay)))
		if prefixLength < expected {
			result.Warnings = append(result.Warnings, fmt.Sprintf("The D1 companion contains %d of %d already-closed target-week weekdays. Projections use only the latest available contiguous prefix.", prefixLength, expected))
		}
		if statisticsWeekStart(asOf) > targetWeek {
			result.Warnings = append(result.Warnings, "The selected W1 history is stale. These scenarios remain bounded to the week following its last closed weekly candle; refresh Screener data for the current week.")
		}
	} else if asOf-closedAt(native.anchor.Time, period) >= 3_600_000 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Latest downloaded closed H1 candle: %s UTC. Scenarios use its historical cutoff; refresh Screener data for a newer anchor.", time.UnixMilli(native.anchor.Time).UTC().Format("2006-01-02 15:04:05")))
	}
	if prefixLength >= 5 || (period == "h1" && (native.anchor.Time-targetWeek)/statisticsDay >= 5) {
		result.Warnings = append(result.Warnings, "Friday is already closed in the target week. There are no remaining Monday–Friday scenarios for that week.")
		return result, unchangedStatisticsSources(ctx, native, daily)
	}
	result.TheoreticalScenarios = 1 << (5 - prefixLength)
	priceAnchor := native.anchor.Close
	if period == "w1" && observed != nil {
		priceAnchor = observed.Close
	}
	type group struct {
		count int
		means []float64
	}
	groups := map[string]*group{}
	firstWeek := daily.firstWeek
	if request.LookbackWeeks > 0 {
		firstWeek = max(firstWeek, targetWeek-int64(request.LookbackWeeks)*7*statisticsDay)
	}
	for start, index := firstWeek, 0; start < targetWeek; start, index = start+7*statisticsDay, index+1 {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return StatisticsForecast{}, err
			}
		}
		week := daily.weeks[start]
		complete, invalid, doji := week != nil, false, false
		if week != nil {
			for _, candle := range week.days {
				if candle == nil {
					complete = false
					continue
				}
				invalid = invalid || !validStatisticsCandle(*candle)
				doji = doji || statisticsDirection(*candle) == ""
			}
		}
		if !complete {
			result.ExcludedIncompleteWeeks++
			continue
		}
		if invalid {
			result.ExcludedInvalidWeeks++
			continue
		}
		if doji {
			result.ExcludedDojiWeeks++
			continue
		}
		checkpointTime := start + native.anchor.Time - targetWeek
		if period == "w1" {
			checkpointTime = start - 7*statisticsDay
		}
		checkpoint, found := native.checkpoints[checkpointTime]
		if !found || !validStatisticsCandle(checkpoint) || statisticsDirection(checkpoint) == "" {
			result.ExcludedCheckpointWeeks++
			continue
		}
		result.EligibleWeeks++
		matches := statisticsDirection(checkpoint) == anchorDirection
		for day, prefix := range dailyPrefix {
			matches = matches && statisticsDirection(*week.days[day]) == prefix.Direction
		}
		if !matches {
			continue
		}
		result.MatchedWeeks++
		var suffix strings.Builder
		for day := prefixLength; day < 5; day++ {
			if statisticsDirection(*week.days[day]) == "up" {
				suffix.WriteByte('U')
			} else {
				suffix.WriteByte('D')
			}
		}
		key := suffix.String()
		item := groups[key]
		if item == nil {
			item = &group{means: make([]float64, 5-prefixLength)}
			groups[key] = item
		}
		item.count++
		historicalAnchor := checkpoint.Close
		if period == "w1" && prefixLength > 0 {
			historicalAnchor = week.days[prefixLength-1].Close
		}
		for day := prefixLength; day < 5; day++ {
			ratio := week.days[day].Close / historicalAnchor
			if !finite(ratio) || ratio <= 0 {
				return StatisticsForecast{}, errors.New("historical price ratio exceeds the supported numerical range")
			}
			position := day - prefixLength
			item.means[position] += (ratio - item.means[position]) / float64(item.count)
		}
	}
	for id, group := range groups {
		item := StatisticsScenario{ID: id, Count: group.count, Probability: float64(group.count) / float64(result.MatchedWeeks), Points: []StatisticsPoint{}}
		if period == "w1" && observed != nil {
			item.Points = append(item.Points, StatisticsPoint{Time: observed.Time, Close: observed.Close, Direction: statisticsDirection(*observed), Observed: true})
		}
		labels := []string{}
		for position, mean := range group.means {
			day := prefixLength + position
			direction, arrow := "down", "↓"
			if id[position] == 'U' {
				direction, arrow = "up", "↑"
			}
			close := priceAnchor * mean
			if !finite(close) || close <= 0 {
				return StatisticsForecast{}, errors.New("scenario close exceeds the supported numerical range")
			}
			timestamp := targetWeek + int64(day)*statisticsDay
			if period == "h1" {
				timestamp += 23 * 3_600_000
			}
			item.Points = append(item.Points, StatisticsPoint{Time: timestamp, Close: close, Direction: direction})
			labels = append(labels, statisticsDays[day]+" "+arrow)
		}
		item.Label = strings.Join(labels, " · ")
		result.Scenarios = append(result.Scenarios, item)
	}
	sort.Slice(result.Scenarios, func(i, j int) bool {
		if result.Scenarios[i].Count != result.Scenarios[j].Count {
			return result.Scenarios[i].Count > result.Scenarios[j].Count
		}
		return result.Scenarios[i].ID < result.Scenarios[j].ID
	})
	if result.MatchedWeeks == 0 {
		result.Warnings = append(result.Warnings, "No complete historical weeks match the daily prefix and selected-timeframe candle body. No scenario probabilities or prices can be estimated.")
	} else if result.MatchedWeeks < 30 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Only %d matching historical weeks; these frequencies have a small sample size and may be unstable.", result.MatchedWeeks))
	}
	if result.ExcludedIncompleteWeeks+result.ExcludedDojiWeeks+result.ExcludedInvalidWeeks > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Historical weeks excluded: %d incomplete, %d with a doji, %d with invalid OHLC prices.", result.ExcludedIncompleteWeeks, result.ExcludedDojiWeeks, result.ExcludedInvalidWeeks))
	}
	if result.ExcludedCheckpointWeeks > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d complete historical weeks were excluded because the required %s checkpoint was missing, invalid or a doji.", result.ExcludedCheckpointWeeks, strings.ToUpper(period)))
	}
	return result, unchangedStatisticsSources(ctx, native, daily)
}

func unchangedStatisticsSources(ctx context.Context, sources ...statisticsSource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, source := range sources {
		if err := source.snapshot.unchanged(); err != nil {
			return err
		}
	}
	return nil
}
