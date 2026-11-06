package screener

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Missing source selections inherit the complete eligible universe. A custom
// source selection is an explicit list, so newly listed markets are not added
// behind the user's selection. Selecting all removes that source override.
type LiveAlertAssetRef struct {
	Market string `json:"market,omitempty"`
	Symbol string `json:"symbol"`
}
type LiveAlertAssetSelection struct {
	DataSource string              `json:"dataSource"`
	Assets     []LiveAlertAssetRef `json:"assets"`
}

func validateLiveAlertAssetSelections(values []LiveAlertAssetSelection) (int, error) {
	if len(values) > 4 {
		return 0, errors.New("too many Live Alerts asset source selections")
	}
	sources := map[string]bool{}
	total := 0
	for _, selection := range values {
		source := selection.DataSource
		if (source != "dukascopy" && !supportedPublicAlertSource(source)) || sources[source] {
			return 0, errors.New("invalid or duplicate asset selection source")
		}
		sources[source] = true
		if len(selection.Assets) == 0 || len(selection.Assets) > 20000 {
			return 0, errors.New("select between 1 and 20,000 assets, or disable the alert")
		}
		seen := map[LiveAlertAssetRef]bool{}
		for _, asset := range selection.Assets {
			if seen[asset] {
				return 0, errors.New("duplicate selected asset")
			}
			seen[asset] = true
			if source == "dukascopy" {
				if _, known := catalogAssets[asset.Symbol]; !known || asset.Market != "" {
					return 0, errors.New("invalid selected Dukascopy asset")
				}
			} else if (asset.Market != "spot" && asset.Market != "perpetual") || asset.Symbol == "" || len(asset.Symbol) > 512 || !utf8.ValidString(asset.Symbol) || strings.TrimSpace(asset.Symbol) != asset.Symbol || strings.IndexFunc(asset.Symbol, unicode.IsControl) >= 0 {
				return 0, errors.New("invalid selected public market identity")
			}
		}
		total += len(selection.Assets)
	}
	return total, nil
}

func copyLiveAlertDefinition(entry LiveAlertDefinition) LiveAlertDefinition {
	if len(entry.AssetSelections) == 0 {
		entry.AssetSelections = nil
		return entry
	}
	entry.AssetSelections = append([]LiveAlertAssetSelection{}, entry.AssetSelections...)
	for i := range entry.AssetSelections {
		entry.AssetSelections[i].Assets = append([]LiveAlertAssetRef{}, entry.AssetSelections[i].Assets...)
		sort.Slice(entry.AssetSelections[i].Assets, func(a, b int) bool {
			left, right := entry.AssetSelections[i].Assets[a], entry.AssetSelections[i].Assets[b]
			if left.Market != right.Market {
				return left.Market < right.Market
			}
			return left.Symbol < right.Symbol
		})
	}
	sort.Slice(entry.AssetSelections, func(i, j int) bool { return entry.AssetSelections[i].DataSource < entry.AssetSelections[j].DataSource })
	return entry
}

func liveAlertSelectedAssets(saved SavedAnalysisCondition, source string) map[LiveAlertAssetRef]bool {
	if saved.liveAlertDefinition == nil {
		return nil
	}
	for _, selection := range saved.liveAlertDefinition.AssetSelections {
		if selection.DataSource != source {
			continue
		}
		selected := make(map[LiveAlertAssetRef]bool, len(selection.Assets))
		for _, asset := range selection.Assets {
			selected[asset] = true
		}
		return selected
	}
	return nil
}

func liveConditionEligiblePairs(saved SavedAnalysisCondition) map[string]*SavedConditionProfitability {
	eligible := savedConditionEligiblePairs(saved)
	selected := liveAlertSelectedAssets(saved, "dukascopy")
	if selected == nil {
		return eligible
	}
	for pair := range eligible {
		symbol, _, _ := strings.Cut(pair, "/")
		if !selected[LiveAlertAssetRef{Symbol: symbol}] {
			delete(eligible, pair)
		}
	}
	return eligible
}

func publicAlertSelectedAssets(conditions []SavedAnalysisCondition, source string) map[LiveAlertAssetRef]bool {
	selected := map[LiveAlertAssetRef]bool{}
	for _, saved := range conditions {
		assets := liveAlertSelectedAssets(saved, source)
		if assets == nil {
			return nil
		}
		for asset := range assets {
			selected[asset] = true
		}
	}
	return selected
}
