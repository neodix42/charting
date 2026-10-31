package screener

import (
	"sort"
	"strings"
)

// Required fields follow each eligible base pair to its actual operand
// timeframe. Companion-only histories never become alert targets.
func liveAlertRequiredFields(config LiveAlertsConfig, conditions []SavedAnalysisCondition) map[string][]string {
	sets := map[string]map[string]bool{}
	add := func(symbol, period, field string) {
		field = strings.ToLower(strings.TrimSpace(field))
		if field == "price" || field == "close" {
			field = "close"
			if period == "tick" {
				field = "bidprice"
			}
		}
		if field == "" {
			return
		}
		pair := symbol + "/" + period
		if sets[pair] == nil {
			sets[pair] = map[string]bool{}
		}
		sets[pair][field] = true
	}
	for _, saved := range conditions {
		for pair := range savedConditionEligiblePairs(saved) {
			symbol, period, _ := strings.Cut(pair, "/")
			if !liveAlertsScopeAllows(config, saved.ID, period) {
				continue
			}
			add(symbol, period, "close")
			for _, condition := range saved.Request.Conditions {
				for _, operand := range conditionOperands(condition) {
					if operand.Kind != "field" {
						continue
					}
					timeframe := operand.Timeframe
					if timeframe == "" || timeframe == "same" {
						timeframe = period
					}
					add(symbol, timeframe, operand.Field)
				}
			}
			if trade := saved.Request.TradeSimulation; trade != nil {
				add(symbol, period, trade.StopLoss)
				if trade.TakeProfitMultiple == 0 {
					add(symbol, period, trade.TakeProfit)
				}
			}
		}
	}
	result := make(map[string][]string, len(sets))
	for pair, fields := range sets {
		for field := range fields {
			result[pair] = append(result[pair], field)
		}
		sort.Strings(result[pair])
	}
	return result
}
