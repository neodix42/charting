package screener

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SavedConditionAnalysis records historical simulation evidence, not a forecast
// or an estimate recalculated from the latest signal candle.
type SavedConditionAnalysis struct {
	AnalyzedAt int64                         `json:"analyzedAt"`
	Assets     []SavedConditionProfitability `json:"assets"`
}

type SavedConditionProfitability struct {
	Symbol            string  `json:"symbol"`
	Timeframe         string  `json:"timeframe"`
	FileName          string  `json:"fileName"`
	Matches           int64   `json:"matches"`
	TakeProfitHits    int64   `json:"takeProfitHits"`
	ProfitablePercent float64 `json:"profitablePercent"`
}

const savedProfitabilityAssetsLimit = 32000

func validateProfitabilityFilter(value int) error {
	if value < 0 || value > 100 || value%5 != 0 {
		return errors.New("profitability filter must be 0–100 in steps of 5")
	}
	return nil
}

func analysisResultMeetsProfitability(result AnalysisResult, threshold int) bool {
	if threshold == 0 || result.Status == "failed" || result.Status == "cancelled" {
		return true
	}
	if result.Status != "completed" || result.Matches <= 0 || result.Simulation == nil || result.Simulation.Unavailable == result.Matches {
		return false
	}
	value := result.Simulation.ProfitablePercent
	if !analysisFinite(value) || result.Simulation.TakeProfitHits < 0 || result.Simulation.TakeProfitHits > result.Matches {
		return false
	}
	// Compare integer counts without multiplying a potentially large match
	// count by 100 or rounding its displayed percentage.
	minimumHits := result.Matches/100*int64(threshold) + (result.Matches%100*int64(threshold)+99)/100
	return result.Simulation.TakeProfitHits >= minimumHits
}

func copySavedConditionAnalysis(value *SavedConditionAnalysis) *SavedConditionAnalysis {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Assets = append([]SavedConditionProfitability{}, value.Assets...)
	return &copy
}

func copySavedAnalysisConditions(values []SavedAnalysisCondition) []SavedAnalysisCondition {
	copies := append([]SavedAnalysisCondition{}, values...)
	for i := range copies {
		copies[i].Request = copyAnalysisRequest(copies[i].Request)
		copies[i].Analysis = copySavedConditionAnalysis(copies[i].Analysis)
	}
	return copies
}

func validateSavedConditionAnalysis(value *SavedConditionAnalysis) error {
	if value == nil {
		return nil
	}
	if value.AnalyzedAt <= 0 || value.AnalyzedAt > 253402300799999 || len(value.Assets) > savedProfitabilityAssetsLimit {
		return errors.New("invalid recorded analysis timestamp or asset count")
	}
	seen := map[string]bool{}
	for _, row := range value.Assets {
		if row.Symbol == "" || len(row.Symbol) > 1024 || !utf8.ValidString(row.Symbol) || strings.ContainsFunc(row.Symbol, unicode.IsControl) || row.Timeframe == "" || row.Timeframe == "same" || !validAnalysisTimeframe(row.Timeframe) {
			return errors.New("invalid recorded profitability asset or timeframe")
		}
		if row.FileName == "" || len(row.FileName) > 4096 || filepath.Base(row.FileName) != row.FileName || strings.ContainsAny(row.FileName, "\\\x00") || !utf8.ValidString(row.FileName) || strings.ContainsFunc(row.FileName, unicode.IsControl) {
			return errors.New("invalid recorded profitability filename")
		}
		if row.Matches <= 0 || row.TakeProfitHits < 0 || row.TakeProfitHits > row.Matches || !analysisFinite(row.ProfitablePercent) || row.ProfitablePercent < 0 || row.ProfitablePercent > 100 || math.Abs(row.ProfitablePercent-100*float64(row.TakeProfitHits)/float64(row.Matches)) > 1e-9 {
			return errors.New("invalid recorded profitability counts or percentage")
		}
		key := row.Symbol + "\x00" + row.Timeframe + "\x00" + row.FileName
		if seen[key] {
			return errors.New("duplicate recorded profitability file")
		}
		seen[key] = true
	}
	return nil
}

// Threshold changes only alter the result view. They do not change which bars
// matched or the outcomes of a trade simulation, so its evidence is reusable.
func sameAnalysisProfitabilityRequest(left, right AnalysisRequest) bool {
	normalize := func(value AnalysisRequest) AnalysisRequest {
		value = copyAnalysisRequest(value)
		value.ProfitabilityFilter = 0
		if value.Period == "" {
			value.Period = "all"
		}
		if value.Categories != nil {
			sort.Strings(value.Categories)
		}
		if value.TradeSimulation != nil && value.TradeSimulation.Direction == "" {
			value.TradeSimulation.Direction = "long"
		}
		for i := range value.Conditions {
			value.Conditions[i].ID = ""
		}
		return value
	}
	return reflect.DeepEqual(normalize(left), normalize(right))
}

func completedAnalysisProfitability(a *analysisState) (*SavedConditionAnalysis, error) {
	if a == nil || a.active || a.phase != "completed" || a.errorText != "" || a.asOf <= 0 {
		return nil, nil
	}
	snapshot := &SavedConditionAnalysis{AnalyzedAt: a.asOf, Assets: []SavedConditionProfitability{}}
	for _, result := range a.results {
		if a.duplicateOf[result.ID] != "" {
			continue
		}
		if result.Status != "completed" || result.Error != "" {
			return nil, nil
		}
		if result.Matches <= 0 || result.Simulation == nil || result.Simulation.Unavailable == result.Matches {
			continue
		}
		row := SavedConditionProfitability{Symbol: result.Symbol, Timeframe: result.Timeframe, FileName: result.Name, Matches: result.Matches, TakeProfitHits: result.Simulation.TakeProfitHits, ProfitablePercent: result.Simulation.ProfitablePercent}
		// Files with no meaningful base period can still be inspected in Analysis,
		// but cannot become period-specific alert evidence.
		if row.Timeframe == "" || row.Timeframe == "same" || !validAnalysisTimeframe(row.Timeframe) {
			continue
		}
		snapshot.Assets = append(snapshot.Assets, row)
	}
	// Multiple legacy files can describe one asset/period. Retaining the best
	// full-precision ratio preserves every eligible pair without repeating the
	// same asset across many overlapping histories in every saved condition.
	best := make(map[string]SavedConditionProfitability)
	for _, row := range snapshot.Assets {
		key := row.Symbol + "\x00" + row.Timeframe
		previous, found := best[key]
		if !found || row.ProfitablePercent > previous.ProfitablePercent || row.ProfitablePercent == previous.ProfitablePercent && row.FileName < previous.FileName {
			best[key] = row
		}
	}
	snapshot.Assets = snapshot.Assets[:0]
	for _, row := range best {
		snapshot.Assets = append(snapshot.Assets, row)
	}
	sort.Slice(snapshot.Assets, func(i, j int) bool {
		left, right := snapshot.Assets[i], snapshot.Assets[j]
		if left.Symbol != right.Symbol {
			return left.Symbol < right.Symbol
		}
		return left.Timeframe < right.Timeframe
	})
	if err := validateSavedConditionAnalysis(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (m *Manager) persistCompletedAnalysisProfitabilityLocked(a *analysisState) error {
	snapshot, err := completedAnalysisProfitability(a)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	saved, err := m.readSavedConditions()
	if err != nil {
		return err
	}
	changed := false
	for i := range saved {
		if sameAnalysisProfitabilityRequest(saved[i].Request, a.request) {
			saved[i].Analysis = copySavedConditionAnalysis(snapshot)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	state := savedAnalysisConditions{Version: 1, Conditions: saved}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(encoded)+1 > savedConditionsBytes {
		return errors.New("recorded profitability exceeds saved conditions size limit; previous snapshots preserved")
	}
	if err := writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), state); err != nil {
		return fmt.Errorf("persist recorded analysis: %w", err)
	}
	return nil
}
