package screener

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
)

// SavedConditionProfitabilityFor uses the historical simulation recorded with
// this exact saved rule. It never estimates a live candle's future outcome.
// If multiple downloaded files covered a symbol/period, retain the highest
// qualifying file ratio, with a stable filename tie break.
func SavedConditionProfitabilityFor(saved SavedAnalysisCondition, symbol, period string) (*SavedConditionProfitability, bool) {
	var selected *SavedConditionProfitability
	matched := false
	if !savedConditionPeriodAllowed(saved, period) {
		return nil, false
	}
	if saved.Analysis != nil {
		for _, row := range saved.Analysis.Assets {
			if row.Symbol != symbol || row.Timeframe != period || row.Matches <= 0 {
				continue
			}
			if row.ProfitabilityUnavailable {
				if saved.Request.ProfitabilityFilter == 0 {
					matched = true
				}
				continue
			}
			if !validLiveProfitability(row) {
				continue
			}
			threshold := saved.Request.ProfitabilityFilter
			if threshold > 0 && !liveProfitabilityAtLeast(row, threshold) {
				continue
			}
			if selected != nil {
				comparison := compareLiveProfitability(row, *selected)
				if comparison < 0 || comparison == 0 && row.FileName >= selected.FileName {
					continue
				}
			}
			matched = true
			copy := row
			copy.ProfitablePercent = 100 * float64(row.TakeProfitHits) / float64(row.Matches)
			selected = &copy
		}
	}
	return selected, matched
}

func validLiveProfitability(row SavedConditionProfitability) bool {
	return !row.ProfitabilityUnavailable && row.Matches > 0 && row.TakeProfitHits >= 0 && row.TakeProfitHits <= row.Matches && !math.IsNaN(row.ProfitablePercent) && !math.IsInf(row.ProfitablePercent, 0) && row.ProfitablePercent >= 0 && row.ProfitablePercent <= 100 && math.Abs(row.ProfitablePercent-100*float64(row.TakeProfitHits)/float64(row.Matches)) <= 1e-9
}

func liveProfitabilityAtLeast(row SavedConditionProfitability, threshold int) bool {
	// hits * 100 >= matches * threshold, without integer overflow or a rounded
	// display percentage admitting a value below the selected minimum.
	minimum := row.Matches/100*int64(threshold) + (row.Matches%100*int64(threshold)+99)/100
	return row.TakeProfitHits >= minimum
}

func compareLiveProfitability(left, right SavedConditionProfitability) int {
	var a, b big.Int
	a.Mul(big.NewInt(left.TakeProfitHits), big.NewInt(right.Matches))
	b.Mul(big.NewInt(right.TakeProfitHits), big.NewInt(left.Matches))
	return a.Cmp(&b)
}

func copyLiveProfitability(value *SavedConditionProfitability) *SavedConditionProfitability {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func savedConditionPeriodAllowed(saved SavedAnalysisCondition, period string) bool {
	return (period == "h1" || period == "d1") && includesAnalysisPeriod(saved.Request.Period, period)
}

// Build one bounded lookup per condition when previewing or checking a batch.
// A nil value denotes recorded matches whose profitability was unavailable.
func savedConditionEligiblePairs(saved SavedAnalysisCondition) map[string]*SavedConditionProfitability {
	pairs := map[string]*SavedConditionProfitability{}
	if saved.Analysis == nil {
		return pairs
	}
	for _, row := range saved.Analysis.Assets {
		if row.Matches <= 0 || !savedConditionPeriodAllowed(saved, row.Timeframe) {
			continue
		}
		if _, known := catalogAssets[row.Symbol]; !known {
			continue
		}
		key := row.Symbol + "/" + row.Timeframe
		if row.ProfitabilityUnavailable {
			if saved.Request.ProfitabilityFilter == 0 {
				if _, exists := pairs[key]; !exists {
					pairs[key] = nil
				}
			}
			continue
		}
		if !validLiveProfitability(row) || !liveProfitabilityAtLeast(row, saved.Request.ProfitabilityFilter) {
			continue
		}
		if previous := pairs[key]; previous != nil {
			comparison := compareLiveProfitability(row, *previous)
			if comparison < 0 || comparison == 0 && row.FileName >= previous.FileName {
				continue
			}
		}
		copy := row
		copy.ProfitablePercent = 100 * float64(row.TakeProfitHits) / float64(row.Matches)
		pairs[key] = &copy
	}
	return pairs
}

func resolvedLiveAlertsConfig(config LiveAlertsConfig, conditions []SavedAnalysisCondition) LiveAlertsConfig {
	config = canonicalLiveAlertsConfig(config)
	config.scope = []LiveAlertConditionPeriod{}
	config.calendar = append([]SavedAnalysisCondition(nil), conditions...)
	config.ConditionIDs = []string{}
	for _, saved := range conditions {
		config.ConditionIDs = append(config.ConditionIDs, saved.ID)
		for pair := range savedConditionEligiblePairs(saved) {
			_, period, _ := strings.Cut(pair, "/")
			if !liveAlertsScopeAllows(config, saved.ID, period) {
				config.scope = append(config.scope, LiveAlertConditionPeriod{ConditionID: saved.ID, Period: period})
			}
		}
	}
	sort.Slice(config.scope, func(i, j int) bool {
		left, right := config.scope[i], config.scope[j]
		if left.ConditionID != right.ConditionID {
			return left.ConditionID < right.ConditionID
		}
		return left.Period < right.Period
	})
	return config
}

func liveConditionScope(saved SavedAnalysisCondition, assets []LiveAlertAsset) LiveAlertConditionScope {
	period := saved.Request.Period
	if period == "" {
		period = "all"
	}
	scope := LiveAlertConditionScope{ConditionID: saved.ID, Period: period, Targets: []LiveAlertTarget{}}
	eligible := savedConditionEligiblePairs(saved)
	for _, asset := range assets {
		for _, period := range asset.Periods {
			stats, allowed := eligible[asset.Symbol+"/"+period]
			if allowed {
				scope.Targets = append(scope.Targets, LiveAlertTarget{Symbol: asset.Symbol, Name: asset.Name, Category: asset.Category, Period: period, Profitability: stats})
			}
		}
	}
	if len(scope.Targets) == 0 {
		scope.Error = missingLiveConditionTargets(saved, strings.ToUpper(period)).Error()
	}
	return scope
}

func missingLiveConditionTargets(saved SavedAnalysisCondition, period string) error {
	if saved.Analysis == nil {
		return fmt.Errorf("%s has no recorded Analysis matching assets; apply this saved condition in Analysis before enabling its %s alerts", saved.Name, period)
	}
	if saved.Request.ProfitabilityFilter > 0 {
		return fmt.Errorf("%s has no downloaded %s assets meeting its recorded %d%% profitability filter; reapply its saved rules in Analysis", saved.Name, period, saved.Request.ProfitabilityFilter)
	}
	return fmt.Errorf("%s has no downloaded %s assets in its recorded Analysis matches; reapply its saved rules in Analysis", saved.Name, period)
}
