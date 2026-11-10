package screener

import (
	"context"
	"time"
)

// Signals belong to the UTC day of the bar's open, including the 23:00 H1 bar
// that closes at midnight. This is also the day used by the range indicators.
func analysisSignalDay(timestamp int64) int64 {
	return time.UnixMilli(timestamp).UTC().Truncate(24 * time.Hour).UnixMilli()
}

type analysisDailyWinner struct {
	ResultID string
	Name     string
	Index    int64
	Time     int64
}

// A single history is filtered in its ordinary scan. With multiple histories
// for an asset/period, choose the earliest match across files before publishing
// any occurrences. Keep only one asset/period's daily winners in memory.
func firstAnalysisSignals(ctx context.Context, a *analysisState, symbol, period string) (map[int64]analysisDailyWinner, error) {
	var results []AnalysisResult
	for _, result := range a.results {
		if result.Symbol == symbol && result.Timeframe == period && result.Status != "failed" && a.duplicateOf[result.ID] == "" && a.series[result.ID] != nil {
			results = append(results, result)
		}
	}
	if len(results) <= 1 {
		return nil, nil
	}
	winners := map[int64]analysisDailyWinner{}
	for _, result := range results {
		s := a.series[result.ID]
		compiled, err := compileAnalysisConditions(a, s)
		if err != nil {
			// Let the normal scan report this file's missing enrichment without
			// suppressing signals from usable histories for the same asset.
			continue
		}
		closed, err := s.closedCount(a.asOf)
		if err != nil {
			return nil, err
		}
		local := map[int64]analysisDailyWinner{}
		for i := int64(0); i < closed; i++ {
			if i%256 == 0 {
				if err = ctx.Err(); err != nil {
					return nil, err
				}
			}
			row, err := s.row(i)
			if err != nil {
				return nil, err
			}
			matched := true
			for _, condition := range compiled {
				ok, err := condition.evaluate(i, analysisClosedAt(row.timestamp, period))
				if err != nil {
					return nil, err
				}
				matched = matched && ok
			}
			day := analysisSignalDay(row.timestamp)
			if _, exists := local[day]; matched && !exists {
				local[day] = analysisDailyWinner{ResultID: result.ID, Name: result.Name, Index: i, Time: row.timestamp}
			}
		}
		for day, candidate := range local {
			previous, exists := winners[day]
			if !exists || candidate.Time < previous.Time || candidate.Time == previous.Time && candidate.Name < previous.Name {
				winners[day] = candidate
			}
		}
	}
	return winners, nil
}
