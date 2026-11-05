package screener

import (
	"errors"
	"regexp"
	"sort"
	"strings"
)

func liveAlertSource(config LiveAlertsConfig, id string) string {
	if entry, found := liveAlertEntry(config, id); found {
		return entry.DataSource
	}
	for _, choice := range config.ConditionSources {
		if choice.ConditionID == id {
			return choice.Source
		}
	}
	return "dukascopy"
}
func supportedPublicAlertSource(source string) bool {
	return source == "hyperliquid" || source == "binance" || source == "bybit"
}
func validateLiveAlertSources(choices []LiveAlertConditionSource) error {
	if len(choices) > savedConditionsLimit {
		return errors.New("too many Live Alerts data sources")
	}
	seen := map[string]bool{}
	for _, choice := range choices {
		if len(choice.ConditionID) != 32 || strings.Trim(choice.ConditionID, "0123456789abcdef") != "" || seen[choice.ConditionID] || (choice.Source != "dukascopy" && !supportedPublicAlertSource(choice.Source)) {
			return errors.New("invalid or duplicate Live Alerts data source")
		}
		seen[choice.ConditionID] = true
	}
	return nil
}
func savedLiveAlertPeriods(saved SavedAnalysisCondition) []string {
	if supportedLiveAlertPeriod(saved.Request.Period) {
		return []string{saved.Request.Period}
	}
	seen := map[string]bool{}
	if saved.Analysis != nil {
		for _, asset := range saved.Analysis.Assets {
			if supportedLiveAlertPeriod(asset.Timeframe) {
				seen[asset.Timeframe] = true
			}
		}
	}
	if len(seen) == 0 {
		return []string{"h1", "d1", "w1"}
	}
	periods := []string{}
	for period := range seen {
		periods = append(periods, period)
	}
	sort.Strings(periods)
	return periods
}
func liveConditionScopeForConfig(saved SavedAnalysisCondition, assets []LiveAlertAsset, config LiveAlertsConfig) LiveAlertConditionScope {
	source := liveAlertSource(config, saved.ID)
	if source == "dukascopy" {
		scope := liveConditionScope(saved, assets)
		scope.DataSource = source
		return scope
	}
	period := saved.Request.Period
	if period == "" {
		period = "all"
	}
	return LiveAlertConditionScope{ConditionID: saved.ID, Period: period, DataSource: source, AllAssets: true, Targets: []LiveAlertTarget{}}
}

var publicLiveAlias = regexp.MustCompile(`^(spot|perpetual)[a-f0-9]{64}$`)

func validPublicLiveAlertClaim(value string) bool {
	parts := strings.Split(value, ":")
	return len(parts) == 3 && parts[0] == "public" && supportedPublicAlertSource(parts[1]) && publicLiveAlias.MatchString(parts[2])
}
func livePublicClaimKey(id, source, symbol, period string) string {
	return id + "/public:" + source + ":" + symbol + "/" + period
}
