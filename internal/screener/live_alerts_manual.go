package screener

import "time"

// Keep the execution subset coherent through canonicalization: named Alerts,
// projected IDs, source choices and runtime calendars all refer to the same rows.
// The stored configuration and the scheduler's full calendar are never mutated.
func subsetLiveAlertsConfig(config LiveAlertsConfig, ids []string) LiveAlertsConfig {
	config = canonicalLiveAlertsConfig(config)
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	config.ConditionIDs = nil
	if config.Alerts != nil {
		entries := []LiveAlertDefinition{}
		for _, entry := range config.Alerts {
			if selected[entry.ID] {
				entries = append(entries, entry)
			}
		}
		config.Alerts, config.derived = entries, false
		config = canonicalLiveAlertsConfig(config)
	} else {
		for _, id := range ids {
			config.ConditionIDs = append(config.ConditionIDs, id)
		}
	}
	sources := []LiveAlertConditionSource{}
	for _, source := range config.ConditionSources {
		if selected[source.ConditionID] {
			sources = append(sources, source)
		}
	}
	config.ConditionSources = sources
	if config.scope != nil {
		scope := []LiveAlertConditionPeriod{}
		for _, period := range config.scope {
			if selected[period.ConditionID] {
				scope = append(scope, period)
			}
		}
		config.scope = scope
	}
	if config.calendar != nil {
		calendar := []SavedAnalysisCondition{}
		for _, condition := range config.calendar {
			if selected[condition.ID] {
				calendar = append(calendar, condition)
			}
		}
		config.calendar = calendar
	}
	return config
}

// A source/row edit or Stop owns the next request; a formerly queued row cannot
// survive that transition and run against a later configuration.
func (l *LiveAlerts) clearRequestedCheckLocked() {
	l.requested = false
	l.requestedIDs = nil
	l.requestedAt = time.Time{}
	l.requestRevision++
}
