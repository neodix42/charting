package screener

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// A definition owns its identity independently of its reusable saved condition.
// Keeping the ID stable across pause/resume and edits preserves delivery claims.
type LiveAlertDefinition struct {
	ID              string                    `json:"id"`
	Name            string                    `json:"name"`
	ConditionID     string                    `json:"conditionId"`
	Period          string                    `json:"period"`
	DataSource      string                    `json:"dataSource"`
	Enabled         bool                      `json:"enabled"`
	AssetSelections []LiveAlertAssetSelection `json:"assetSelections,omitempty"`
}

func liveAlertEntry(config LiveAlertsConfig, id string) (LiveAlertDefinition, bool) {
	for _, entry := range config.Alerts {
		if entry.ID == id {
			return entry, true
		}
	}
	return LiveAlertDefinition{}, false
}

func validLiveAlertID(id string) bool {
	return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == ""
}

func validateLiveAlertEntries(entries []LiveAlertDefinition) error {
	if len(entries) > savedConditionsLimit {
		return errors.New("at most 100 named Live Alerts may be saved")
	}
	seen := map[string]bool{}
	selectedAssets := 0
	for _, entry := range entries {
		if !validLiveAlertID(entry.ID) || !validLiveAlertID(entry.ConditionID) || seen[entry.ID] {
			return errors.New("invalid or duplicate Live Alert identity")
		}
		if name, err := conditionName(entry.Name); err != nil || name != entry.Name {
			return errors.New("Live Alert name must contain 1–100 characters without control characters")
		}
		if !supportedLiveAlertPeriod(entry.Period) && entry.Period != "all" {
			return errors.New("Live Alert period must be H1, D1 or W1")
		}
		if entry.DataSource != "" && entry.DataSource != "dukascopy" && !supportedPublicAlertSource(entry.DataSource) {
			return errors.New("unsupported Live Alert data source")
		}
		count, err := validateLiveAlertAssetSelections(entry.AssetSelections)
		if err != nil {
			return err
		}
		selectedAssets += count
		if selectedAssets > 100000 {
			return errors.New("too many custom selected Live Alerts assets")
		}
		seen[entry.ID] = true
	}
	return nil
}

func projectLiveAlertEntries(config LiveAlertsConfig) LiveAlertsConfig {
	config.ConditionIDs = []string{}
	config.ConditionSources = nil
	for _, entry := range config.Alerts {
		if entry.Enabled {
			config.ConditionIDs = append(config.ConditionIDs, entry.ID)
		}
		if entry.DataSource != "" && entry.DataSource != "dukascopy" {
			config.ConditionSources = append(config.ConditionSources, LiveAlertConditionSource{ConditionID: entry.ID, Source: entry.DataSource})
		}
	}
	config.derived = true
	return config
}

// Legacy selections become one row per known condition. Selected rows keep
// their old IDs, source and period; inactive conditions with claims/check counts
// become paused rows so selecting them again cannot resend their previous bar.
func migrateLiveAlertEntries(config LiveAlertsConfig, saved []SavedAnalysisCondition, watermarks map[string]int64, checks map[string]liveAlertCheckState) LiveAlertsConfig {
	config = inheritLiveAlertSources(config, saved, LiveAlertsConfig{})
	if config.Alerts != nil {
		return canonicalLiveAlertsConfig(config)
	}
	config = canonicalLiveAlertsConfig(config)
	known := map[string]bool{}
	for _, id := range config.ConditionIDs {
		known[id] = true
	}
	for _, choice := range config.ConditionSources {
		known[choice.ConditionID] = true
	}
	for key := range watermarks {
		id, _, _ := strings.Cut(key, "/")
		known[id] = true
	}
	for id := range checks {
		known[id] = true
	}
	config.Alerts = []LiveAlertDefinition{}
	for _, condition := range saved {
		if !known[condition.ID] {
			continue
		}
		period := condition.Request.Period
		if !supportedLiveAlertPeriod(period) {
			period = "all"
		}
		config.Alerts = append(config.Alerts, LiveAlertDefinition{ID: condition.ID, Name: condition.Name, ConditionID: condition.ID, Period: period, DataSource: liveAlertSource(config, condition.ID), Enabled: containsLiveValue(config.ConditionIDs, condition.ID)})
	}
	return projectLiveAlertEntries(config)
}

// Resolve omitted sources once, against the saved rule used to create the
// alert. Persisted explicit overrides then survive later edits to that rule.
func inheritLiveAlertSources(config LiveAlertsConfig, saved []SavedAnalysisCondition, previous LiveAlertsConfig) LiveAlertsConfig {
	config = copyLiveAlertsConfig(config)
	defaults := map[string]string{}
	for _, condition := range saved {
		defaults[condition.ID] = normalizeDataSource(condition.Request.DataSource)
	}
	if config.Alerts != nil {
		for i, entry := range config.Alerts {
			if entry.DataSource != "" {
				continue
			}
			source := defaults[entry.ConditionID]
			if old, exists := liveAlertEntry(previous, entry.ID); exists && old.DataSource != "" {
				source = old.DataSource
			}
			config.Alerts[i].DataSource = normalizeDataSource(source)
		}
		config.derived = false
		return canonicalLiveAlertsConfig(config)
	}
	for _, id := range config.ConditionIDs {
		explicit := false
		for _, choice := range config.ConditionSources {
			explicit = explicit || choice.ConditionID == id
		}
		if !explicit && defaults[id] == "hyperliquid" {
			config.ConditionSources = append(config.ConditionSources, LiveAlertConditionSource{ConditionID: id, Source: "hyperliquid"})
		}
	}
	return config
}

func needsLiveAlertSourceDefaults(config LiveAlertsConfig) bool {
	if config.Alerts == nil {
		if config.Enabled {
			for _, id := range config.ConditionIDs {
				explicit := false
				for _, choice := range config.ConditionSources {
					explicit = explicit || choice.ConditionID == id
				}
				if !explicit {
					return true
				}
			}
		}
		return false
	}
	for _, entry := range config.Alerts {
		if entry.DataSource == "" {
			return true
		}
	}
	return false
}

// Existing sources are owned by the alert, so even an incomplete Stop draft can
// retain them from memory without queueing behind manager/history work.
func preserveLiveAlertSourceOverrides(config, previous LiveAlertsConfig) LiveAlertsConfig {
	if config.Alerts == nil {
		return config
	}
	config = copyLiveAlertsConfig(config)
	for i, entry := range config.Alerts {
		if entry.DataSource != "" {
			continue
		}
		if old, exists := liveAlertEntry(previous, entry.ID); exists && old.DataSource != "" {
			config.Alerts[i].DataSource = old.DataSource
		}
	}
	config.derived = false
	return canonicalLiveAlertsConfig(config)
}

func (l *LiveAlerts) resolveSourceDefaults(config, previous LiveAlertsConfig) (LiveAlertsConfig, error) {
	l.manager.mu.Lock()
	saved, err := l.manager.readSavedConditions()
	l.manager.mu.Unlock()
	if err != nil {
		return config, err
	}
	return inheritLiveAlertSources(config, saved, previous), nil
}

func materializeLiveAlertConditions(saved []SavedAnalysisCondition, config LiveAlertsConfig, includeDisabled bool) []SavedAnalysisCondition {
	if config.Alerts == nil {
		out := []SavedAnalysisCondition{}
		for _, condition := range saved {
			if includeDisabled || containsLiveValue(config.ConditionIDs, condition.ID) {
				out = append(out, condition)
			}
		}
		return out
	}
	byID := map[string]SavedAnalysisCondition{}
	for _, condition := range saved {
		byID[condition.ID] = condition
	}
	out := []SavedAnalysisCondition{}
	for _, entry := range config.Alerts {
		if !includeDisabled && (!entry.Enabled || !containsLiveValue(config.ConditionIDs, entry.ID)) {
			continue
		}
		condition, found := byID[entry.ConditionID]
		if !found {
			continue
		}
		originalName := condition.Name
		condition.ID, condition.Name = entry.ID, entry.Name
		condition.Request = copyAnalysisRequest(condition.Request)
		condition.Request.Period = entry.Period
		definition := copyLiveAlertDefinition(entry)
		condition.liveAlertDefinition, condition.liveSavedConditionName = &definition, originalName
		out = append(out, condition)
	}
	return out
}

func liveAlertAttribution(alert *LiveAlert, saved SavedAnalysisCondition) {
	if alert == nil || saved.liveAlertDefinition == nil {
		return
	}
	entry := saved.liveAlertDefinition
	alert.AlertID, alert.AlertName = entry.ID, entry.Name
	alert.SavedConditionID, alert.SavedConditionName = entry.ConditionID, saved.liveSavedConditionName
}

// Called with Manager.mu held. The returned copy uses the current row and the
// original saved rule, so an in-flight result cannot claim after either changes.
func (l *LiveAlerts) currentLiveAlertConditions(saved []SavedAnalysisCondition, evaluated SavedAnalysisCondition) ([]SavedAnalysisCondition, uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	config := canonicalLiveAlertsConfig(l.state.Config)
	if evaluated.liveAlertDefinition != nil {
		entry, found := liveAlertEntry(config, evaluated.ID)
		if !found || !reflect.DeepEqual(entry, *evaluated.liveAlertDefinition) || !entry.Enabled {
			return nil, 0, false
		}
	}
	return materializeLiveAlertConditions(saved, config, false), l.configRevision, true
}

func namedLiveAlertScope(scope LiveAlertConditionScope, saved SavedAnalysisCondition) LiveAlertConditionScope {
	if saved.liveAlertDefinition != nil {
		scope.AlertID = saved.ID
		scope.SavedConditionID = saved.liveAlertDefinition.ConditionID
	}
	return scope
}

func (l *LiveAlerts) pruneNamedEntriesLocked(saved []SavedAnalysisCondition) (bool, error) {
	if l.state.Config.Alerts == nil {
		return false, nil
	}
	known := map[string]bool{}
	for _, condition := range saved {
		known[condition.ID] = true
	}
	entries := []LiveAlertDefinition{}
	valid := map[string]bool{}
	for _, entry := range l.state.Config.Alerts {
		if known[entry.ConditionID] {
			entries = append(entries, entry)
			valid[entry.ID] = true
		}
	}
	changed := len(entries) != len(l.state.Config.Alerts)
	for id := range l.state.Checks {
		changed = changed || !valid[id]
	}
	for key := range l.state.Watermarks {
		id, _, _ := strings.Cut(key, "/")
		changed = changed || !valid[id]
	}
	if !changed {
		return true, nil
	}
	if l.closed || l.blocked {
		return true, errors.New("Live Alerts are closed or their saved state is invalid; original history preserved")
	}
	previous := l.state
	l.state.Config = copyLiveAlertsConfig(l.state.Config)
	l.state.Config.Alerts = entries
	l.state.Config = projectLiveAlertEntries(l.state.Config)
	if len(entries) == 0 {
		l.state.Config.Enabled = false
	}
	l.state.Checks = map[string]liveAlertCheckState{}
	for id, check := range previous.Checks {
		if valid[id] {
			l.state.Checks[id] = check
		}
	}
	l.state.Watermarks = map[string]int64{}
	for key, timestamp := range previous.Watermarks {
		id, _, _ := strings.Cut(key, "/")
		if valid[id] {
			l.state.Watermarks[key] = timestamp
		}
	}
	if err := writeState(l.path, l.state); err != nil {
		l.state = previous
		return true, fmt.Errorf("remove deleted Live Alerts history: %w", err)
	}
	if !sameActiveLiveAlertEntries(previous.Config, l.state.Config) {
		l.configRevision++
		l.clearRequestedCheckLocked()
		l.retryConfig = nil
		l.retryTargeted = false
		if l.cycleCancel != nil {
			l.cycleCancel()
		}
	}
	l.signal()
	return true, nil
}

func sameActiveLiveAlertEntries(left, right LiveAlertsConfig) bool {
	if left.Alerts == nil || right.Alerts == nil || left.Enabled != right.Enabled {
		return false
	}
	active := func(config LiveAlertsConfig) []LiveAlertDefinition {
		entries := []LiveAlertDefinition{}
		for _, entry := range config.Alerts {
			if entry.Enabled {
				entries = append(entries, entry)
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
		return entries
	}
	return reflect.DeepEqual(active(left), active(right))
}
