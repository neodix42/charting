package predictions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// CalculatePatternStatistics searches native consecutive candle bodies without
// weekday matching. Only closed outcomes strictly before the latest N-bar
// anchor window contribute; the selected pattern itself is a user hypothesis.
// Two immutable streaming passes keep memory independent of source length.
func CalculatePatternStatistics(ctx context.Context, dataset Dataset, request StatisticsRequest, asOf int64) (StatisticsForecast, error) {
	if request.Pattern == nil {
		return StatisticsForecast{}, errors.New("choose a statistics pattern of 3 to 10 bars")
	}
	request.Pattern = append([]string{}, request.Pattern...)
	if err := ValidateStatisticsRequest(request); err != nil {
		return StatisticsForecast{}, err
	}
	period := StatisticsPeriod(request.Period)
	if dataset.Period != period || dataset.Symbol != request.Asset || statisticsDataSource(dataset) != request.DataSource {
		return StatisticsForecast{}, errors.New("statistics source does not match the selected data source, asset and candle timeframe")
	}
	if asOf < 0 || asOf > 253_402_300_799_999 {
		return StatisticsForecast{}, errors.New("statistics cutoff is outside the supported timestamp range")
	}
	length := len(request.Pattern)
	recent := make([]StatisticsCandle, 0, length)
	retain := func(candle StatisticsCandle) error {
		if len(recent) == length {
			copy(recent, recent[1:])
			recent = recent[:length-1]
		}
		recent = append(recent, candle)
		return nil
	}
	source, err := readStatisticsCandleStream(ctx, dataset, asOf, period, false, retain)
	if err != nil {
		return StatisticsForecast{}, err
	}
	if len(recent) < length {
		return StatisticsForecast{}, fmt.Errorf("the downloaded %s data needs at least %d fully closed candles for this pattern", strings.ToUpper(period), length)
	}
	if !validStatisticsCandle(source.anchor) {
		return StatisticsForecast{}, fmt.Errorf("the last closed %s candle has invalid OHLC prices", strings.ToUpper(period))
	}
	result := StatisticsForecast{
		DataSource: request.DataSource, Symbol: dataset.Symbol, AssetName: dataset.Name, Period: period,
		LookbackWeeks: request.LookbackWeeks, AsOf: asOf, FirstClosedBar: source.firstClosedBar,
		WeekStart: statisticsWeekStart(source.anchor.Time), SourceFile: dataset.Filename, SourceHash: source.snapshot.checksum,
		History: source.history, Anchor: source.anchor, Pattern: append([]string{}, request.Pattern...),
		Prefix: []StatisticsPrefix{}, Scenarios: []StatisticsScenario{}, Warnings: []string{},
		NextBarTime: closedAt(source.anchor.Time, period), TheoreticalScenarios: 3,
		Conditioning: fmt.Sprintf("The specified %d-bar pattern is matched against consecutive closed %s candles, independently of weekdays or hours. The next available native candle is the outcome.", length, strings.ToUpper(period)),
	}
	if result.AssetName == "" {
		result.AssetName = strings.ToUpper(dataset.Symbol)
	}
	for index, candle := range recent {
		result.Prefix = append(result.Prefix, StatisticsPrefix{Time: candle.Time, Direction: request.Pattern[index]})
	}
	if source.invalidHistory > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d candles with invalid OHLC prices were omitted from chart history.", source.invalidHistory))
	}
	step := result.NextBarTime - source.anchor.Time
	if asOf-result.NextBarTime >= step {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Latest downloaded closed %s candle: %s UTC. Refresh downloaded data for a newer anchor; the result predicts the next available candle after this anchor.", strings.ToUpper(period), time.UnixMilli(source.anchor.Time).UTC().Format("2006-01-02 15:04:05")))
	}
	firstTime := int64(0)
	if request.LookbackWeeks > 0 {
		firstTime = max(firstTime, source.anchor.Time-int64(request.LookbackWeeks)*7*statisticsDay)
	}
	type group struct {
		count int
		mean  float64
	}
	groups := map[string]*group{}
	window := make([]StatisticsCandle, 0, length+1)
	visit := func(candle StatisticsCandle) error {
		if len(window) == length+1 {
			copy(window, window[1:])
			window = window[:length]
		}
		window = append(window, candle)
		if len(window) != length+1 || window[0].Time < firstTime || candle.Time >= recent[0].Time {
			return nil
		}
		invalid, doji := false, false
		for index, bar := range window {
			invalid = invalid || !validStatisticsCandle(bar)
			if index < length {
				doji = doji || statisticsDirection(bar) == ""
			}
		}
		if invalid {
			result.ExcludedInvalidPatterns++
			return nil
		}
		if doji {
			result.ExcludedDojiPatterns++
			return nil
		}
		result.EligiblePatterns++
		for index, direction := range request.Pattern {
			if statisticsDirection(window[index]) != direction {
				return nil
			}
		}
		result.MatchedPatterns++
		direction := statisticsDirection(candle)
		if direction == "" {
			direction = "doji"
			result.NextBarDojiCount++
		}
		item := groups[direction]
		if item == nil {
			item = &group{}
			groups[direction] = item
		}
		ratio := candle.Close / window[length-1].Close
		if !finite(ratio) || ratio <= 0 {
			return errors.New("historical next-bar price ratio exceeds the supported numerical range")
		}
		item.count++
		item.mean += (ratio - item.mean) / float64(item.count)
		return nil
	}
	checked, err := readStatisticsCandleStream(ctx, dataset, asOf, period, false, visit)
	if err != nil {
		return StatisticsForecast{}, err
	}
	if checked.snapshot.checksum != source.snapshot.checksum {
		return StatisticsForecast{}, errors.New("source CSV changed between statistics scans; calculate again")
	}
	for direction, item := range groups {
		id, label := "D", "Next bar bearish"
		if direction == "up" {
			id, label = "U", "Next bar bullish"
		} else if direction == "doji" {
			id, label = "0", "Next bar doji"
		}
		close := source.anchor.Close * item.mean
		if !finite(close) || close <= 0 {
			return StatisticsForecast{}, errors.New("next-bar scenario close exceeds the supported numerical range")
		}
		result.Scenarios = append(result.Scenarios, StatisticsScenario{ID: id, Label: label, Count: item.count,
			Probability: float64(item.count) / float64(result.MatchedPatterns),
			Points:      []StatisticsPoint{{Time: result.NextBarTime, Close: close, Direction: direction}}})
	}
	sort.Slice(result.Scenarios, func(i, j int) bool {
		if result.Scenarios[i].Count != result.Scenarios[j].Count {
			return result.Scenarios[i].Count > result.Scenarios[j].Count
		}
		return result.Scenarios[i].ID < result.Scenarios[j].ID
	})
	if result.MatchedPatterns == 0 {
		result.Warnings = append(result.Warnings, "No historical windows match the specified pattern in this lookback. No next-bar probability or price can be estimated.")
	} else if result.MatchedPatterns < 30 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Only %d matching historical patterns; these frequencies have a small sample size and may be unstable.", result.MatchedPatterns))
	}
	if result.ExcludedDojiPatterns+result.ExcludedInvalidPatterns > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Historical windows excluded: %d with doji input bars and %d with invalid OHLC input/outcome prices.", result.ExcludedDojiPatterns, result.ExcludedInvalidPatterns))
	}
	if err := ctx.Err(); err != nil {
		return StatisticsForecast{}, err
	}
	return result, source.snapshot.unchanged()
}
