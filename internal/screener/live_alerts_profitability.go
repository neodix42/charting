package screener

import (
	"fmt"
	"math"
	"math/big"
)

// SavedConditionProfitabilityFor uses the historical simulation recorded with
// this exact saved rule. It never estimates a live candle's future outcome.
// If multiple downloaded files covered a symbol/period, retain the highest
// qualifying file ratio, with a stable filename tie break.
func SavedConditionProfitabilityFor(saved SavedAnalysisCondition, symbol, period string) (*SavedConditionProfitability, bool) {
	var selected *SavedConditionProfitability
	if saved.Analysis != nil {
		for _, row := range saved.Analysis.Assets {
			if row.Symbol != symbol || row.Timeframe != period || !validLiveProfitability(row) {
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
			copy := row
			copy.ProfitablePercent = 100 * float64(row.TakeProfitHits) / float64(row.Matches)
			selected = &copy
		}
	}
	return selected, saved.Request.ProfitabilityFilter == 0 || selected != nil
}

func validLiveProfitability(row SavedConditionProfitability) bool {
	return row.Matches > 0 && row.TakeProfitHits >= 0 && row.TakeProfitHits <= row.Matches && !math.IsNaN(row.ProfitablePercent) && !math.IsInf(row.ProfitablePercent, 0) && row.ProfitablePercent >= 0 && row.ProfitablePercent <= 100 && math.Abs(row.ProfitablePercent-100*float64(row.TakeProfitHits)/float64(row.Matches)) <= 1e-9
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

func liveAlertBaseAllowed(config LiveAlertsConfig, conditions []SavedAnalysisCondition, symbol, period string) bool {
	if !containsLiveValue(config.Symbols, symbol) {
		return false
	}
	for _, saved := range conditions {
		if liveAlertsConditionPeriod(config, saved.ID) != period {
			continue
		}
		if _, allowed := SavedConditionProfitabilityFor(saved, symbol, period); allowed {
			return true
		}
	}
	return false
}

func missingLiveConditionTargets(saved SavedAnalysisCondition, period string) error {
	if saved.Request.ProfitabilityFilter > 0 {
		if saved.Analysis == nil {
			return fmt.Errorf("%s has no recorded Analysis profitability; apply this saved condition before enabling its %s alerts", saved.Name, period)
		}
		return fmt.Errorf("%s has no selected downloaded %s assets meeting its %d%% profitability filter; apply Analysis or update the alert selection", saved.Name, period, saved.Request.ProfitabilityFilter)
	}
	return fmt.Errorf("download and enrich at least one selected asset in %s for saved condition %s", period, saved.Name)
}
