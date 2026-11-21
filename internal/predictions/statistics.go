package predictions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

const statisticsDay = int64(86_400_000)

var statisticsDays = [...]string{"Mon", "Tue", "Wed", "Thu", "Fri"}

type StatisticsRequest struct {
	DataSource    string `json:"dataSource"`
	Asset         string `json:"asset"`
	Period        string `json:"period,omitempty"`
	LookbackWeeks int    `json:"lookbackWeeks"`
}

type StatisticsCandle struct {
	Time  int64   `json:"time"`
	Open  float64 `json:"open"`
	High  float64 `json:"high"`
	Low   float64 `json:"low"`
	Close float64 `json:"close"`
}

type StatisticsPrefix struct {
	Time      int64  `json:"time"`
	Day       string `json:"day"`
	Direction string `json:"direction"`
}

type StatisticsPoint struct {
	Time      int64   `json:"time"`
	Close     float64 `json:"close"`
	Direction string  `json:"direction"`
	Observed  bool    `json:"observed,omitempty"`
}

type StatisticsScenario struct {
	ID          string            `json:"id"`
	Label       string            `json:"label"`
	Probability float64           `json:"probability"`
	Count       int               `json:"count"`
	Points      []StatisticsPoint `json:"points"`
}

type StatisticsForecast struct {
	DataSource              string               `json:"dataSource"`
	Symbol                  string               `json:"symbol"`
	AssetName               string               `json:"assetName"`
	Period                  string               `json:"period"`
	LookbackWeeks           int                  `json:"lookbackWeeks"`
	AsOf                    int64                `json:"asOf"`
	FirstClosedBar          int64                `json:"firstClosedBar"`
	WeekStart               int64                `json:"weekStart"`
	SourceFile              string               `json:"sourceFile"`
	SourceHash              string               `json:"sourceHash"`
	History                 []StatisticsCandle   `json:"history"`
	Anchor                  StatisticsCandle     `json:"anchor"`
	Prefix                  []StatisticsPrefix   `json:"prefix"`
	Scenarios               []StatisticsScenario `json:"scenarios"`
	MatchedWeeks            int                  `json:"matchedWeeks"`
	EligibleWeeks           int                  `json:"eligibleWeeks"`
	ExcludedIncompleteWeeks int                  `json:"excludedIncompleteWeeks"`
	ExcludedDojiWeeks       int                  `json:"excludedDojiWeeks"`
	ExcludedInvalidWeeks    int                  `json:"excludedInvalidWeeks"`
	ExcludedCheckpointWeeks int                  `json:"excludedCheckpointWeeks,omitempty"`
	TheoreticalScenarios    int                  `json:"theoreticalScenarios"`
	Warnings                []string             `json:"warnings"`
	Conditioning            string               `json:"conditioning,omitempty"`
	CompanionSourceFile     string               `json:"companionSourceFile,omitempty"`
	CompanionSourceHash     string               `json:"companionSourceHash,omitempty"`
}

func ValidateStatisticsRequest(request StatisticsRequest) error {
	if request.DataSource != "dukascopy" && request.DataSource != "hyperliquid" {
		return errors.New("choose Dukascopy or Hyperliquid statistics data")
	}
	if request.Asset == "" || len(request.Asset) > 128 || strings.TrimSpace(request.Asset) != request.Asset || strings.ContainsFunc(request.Asset, unicode.IsControl) {
		return errors.New("choose a downloaded statistics asset")
	}
	switch StatisticsPeriod(request.Period) {
	case "h1", "d1", "w1":
	default:
		return errors.New("statistics candle timeframe must be H1, D1 or W1")
	}
	if request.LookbackWeeks < 0 || request.LookbackWeeks > 2600 {
		return errors.New("statistics history must be all available weeks (0), or between 1 and 2600 weeks")
	}
	return nil
}

// StatisticsPeriod preserves daily requests saved before the timeframe selector
// was introduced; new callers explicitly choose H1, D1 or W1.
func StatisticsPeriod(period string) string {
	if period == "" {
		return "d1"
	}
	return period
}

func statisticsDataSource(dataset Dataset) string {
	if dataset.DataSource == "" {
		return "dukascopy"
	}
	return dataset.DataSource
}

func statisticsWeekStart(timestamp int64) int64 {
	date := time.UnixMilli(timestamp).UTC()
	weekday := (int(date.Weekday()) + 6) % 7
	return time.Date(date.Year(), date.Month(), date.Day()-weekday, 0, 0, 0, 0, time.UTC).UnixMilli()
}

func validStatisticsCandle(candle StatisticsCandle) bool {
	return finite(candle.Open) && finite(candle.High) && finite(candle.Low) && finite(candle.Close) &&
		candle.Open > 0 && candle.High > 0 && candle.Low > 0 && candle.Close > 0 &&
		candle.High >= max(candle.Open, candle.Close) && candle.Low <= min(candle.Open, candle.Close) && candle.Low <= candle.High
}

func statisticsDirection(candle StatisticsCandle) string {
	if candle.Close > candle.Open {
		return "up"
	}
	if candle.Close < candle.Open {
		return "down"
	}
	return ""
}

// CalculateStatistics counts complete historical weekday sequences conditional
// on the already closed daily bodies in the anchor week. It uses no model or
// provider calls, and never writes to the source data or forecast storage.
func CalculateStatistics(ctx context.Context, dataset Dataset, request StatisticsRequest, asOf int64) (StatisticsForecast, error) {
	result := StatisticsForecast{}
	if err := ValidateStatisticsRequest(request); err != nil {
		return result, err
	}
	if dataset.Period != "d1" || StatisticsPeriod(request.Period) != "d1" || dataset.Symbol != request.Asset || statisticsDataSource(dataset) != request.DataSource {
		return result, errors.New("statistics source does not match the selected data source, asset and daily period")
	}
	if asOf < 0 || asOf > 253_402_300_799_999 {
		return result, errors.New("statistics cutoff is outside the supported timestamp range")
	}
	source, err := readStatisticsSource(ctx, dataset, asOf)
	if err != nil {
		return result, err
	}
	weekStart := statisticsWeekStart(source.anchor.Time)
	anchorDay := int((source.anchor.Time - weekStart) / statisticsDay)
	result = StatisticsForecast{DataSource: request.DataSource, Symbol: dataset.Symbol, AssetName: dataset.Name,
		Period: "d1", LookbackWeeks: request.LookbackWeeks, AsOf: asOf, WeekStart: weekStart,
		SourceFile: dataset.Filename, SourceHash: source.snapshot.checksum, FirstClosedBar: source.firstClosedBar, History: source.history, Anchor: source.anchor,
		Prefix: []StatisticsPrefix{}, Scenarios: []StatisticsScenario{}, Warnings: []string{}}
	if result.AssetName == "" {
		result.AssetName = strings.ToUpper(dataset.Symbol)
	}
	if source.invalidHistory > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d candles with invalid OHLC prices were omitted from chart history.", source.invalidHistory))
	}
	if !validStatisticsCandle(source.anchor) {
		return StatisticsForecast{}, errors.New("the last closed daily candle has invalid OHLC prices")
	}
	current := source.weeks[weekStart]
	for day := 0; day <= min(anchorDay, 4); day++ {
		if current == nil || current.days[day] == nil {
			return StatisticsForecast{}, fmt.Errorf("the current week is missing its closed %s daily candle; refresh the downloaded data before calculating statistics", statisticsDays[day])
		}
		candle := *current.days[day]
		if !validStatisticsCandle(candle) {
			return StatisticsForecast{}, fmt.Errorf("the current week's %s daily candle has invalid OHLC prices", statisticsDays[day])
		}
		direction := statisticsDirection(candle)
		if direction == "" {
			return StatisticsForecast{}, fmt.Errorf("the current week's %s daily candle is a doji (open equals close); an up/down prefix cannot be estimated", statisticsDays[day])
		}
		result.Prefix = append(result.Prefix, StatisticsPrefix{Time: candle.Time, Day: statisticsDays[day], Direction: direction})
	}
	if asOf-closedAt(source.anchor.Time, "d1") >= statisticsDay {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Latest downloaded closed candle: %s UTC. These scenarios use that candle's week; refresh Screener data for a newer cutoff.", time.UnixMilli(source.anchor.Time).UTC().Format("2006-01-02 15:04:05")))
	}
	completedWeek := anchorDay >= 4
	if completedWeek {
		result.Warnings = append(result.Warnings, "Friday is already closed in the anchor week. There are no remaining Monday–Friday scenarios for that week.")
	} else {
		result.TheoreticalScenarios = 1 << (4 - anchorDay)
	}
	firstWeek := source.firstWeek
	if request.LookbackWeeks > 0 {
		firstWeek = max(firstWeek, weekStart-int64(request.LookbackWeeks)*7*statisticsDay)
	}
	type group struct {
		count int
		means []float64
	}
	groups := map[string]*group{}
	for start, index := firstWeek, 0; start < weekStart; start, index = start+7*statisticsDay, index+1 {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return StatisticsForecast{}, err
			}
		}
		week := source.weeks[start]
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
		result.EligibleWeeks++
		matches := true
		for day, prefix := range result.Prefix {
			matches = matches && statisticsDirection(*week.days[day]) == prefix.Direction
		}
		if !matches {
			continue
		}
		result.MatchedWeeks++
		// Historical matches remain meaningful after Friday, but there is no
		// unobserved suffix to group, price or present as a future scenario.
		if completedWeek {
			continue
		}
		var suffix strings.Builder
		for day := anchorDay + 1; day < 5; day++ {
			if statisticsDirection(*week.days[day]) == "up" {
				suffix.WriteByte('U')
			} else {
				suffix.WriteByte('D')
			}
		}
		key := suffix.String()
		item := groups[key]
		if item == nil {
			item = &group{means: make([]float64, 4-anchorDay)}
			groups[key] = item
		}
		item.count++
		for day := anchorDay + 1; day < 5; day++ {
			ratio := week.days[day].Close / week.days[anchorDay].Close
			if !finite(ratio) || ratio <= 0 {
				return StatisticsForecast{}, errors.New("historical price ratio exceeds the supported numerical range")
			}
			index := day - anchorDay - 1
			item.means[index] += (ratio - item.means[index]) / float64(item.count)
		}
	}
	for id, group := range groups {
		item := StatisticsScenario{ID: id, Count: group.count, Probability: float64(group.count) / float64(result.MatchedWeeks), Points: []StatisticsPoint{}}
		labels := []string{}
		for index, mean := range group.means {
			day := anchorDay + index + 1
			direction, arrow := "down", "↓"
			if id[index] == 'U' {
				direction, arrow = "up", "↑"
			}
			close := source.anchor.Close * mean
			if !finite(close) || close <= 0 {
				return StatisticsForecast{}, errors.New("scenario close exceeds the supported numerical range")
			}
			item.Points = append(item.Points, StatisticsPoint{Time: weekStart + int64(day)*statisticsDay, Close: close, Direction: direction})
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
		if completedWeek {
			result.Warnings = append(result.Warnings, "No complete historical weeks match the completed observed up/down sequence in the selected lookback.")
		} else {
			result.Warnings = append(result.Warnings, "No complete historical weeks match the observed up/down prefix. No scenario probabilities or prices can be estimated.")
		}
	} else if result.MatchedWeeks < 30 {
		// Thirty is a presentation heuristic, not a calibration or confidence
		// guarantee. The exact matching count remains visible to the user.
		result.Warnings = append(result.Warnings, fmt.Sprintf("Only %d matching historical weeks; these frequencies have a small sample size and may be unstable.", result.MatchedWeeks))
	}
	if result.ExcludedIncompleteWeeks+result.ExcludedDojiWeeks+result.ExcludedInvalidWeeks > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Historical weeks excluded: %d incomplete, %d with a doji, %d with invalid OHLC prices.", result.ExcludedIncompleteWeeks, result.ExcludedDojiWeeks, result.ExcludedInvalidWeeks))
	}
	if err := ctx.Err(); err != nil {
		return StatisticsForecast{}, err
	}
	return result, source.snapshot.unchanged()
}
