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
	ID          string `json:"id"`
	Name        string `json:"name"`
	ConditionID string `json:"conditionId"`
	Period      string `json:"period"`
	DataSource  string `json:"dataSource"`
	Enabled     bool   `json:"enabled"`
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
		if entry.DataSource != "dukascopy" && !supportedPublicAlertSource(entry.DataSource) {
			return errors.New("unsupported Live Alert data source")
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
		if entry.DataSource != "dukascopy" {
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
		definition := entry
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
		if !found || entry != *evaluated.liveAlertDefinition || !entry.Enabled {
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
