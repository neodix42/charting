package screener

import (
	"errors"
	"fmt"
	"sort"
)

// Stopping must not queue behind catalog/history work on Manager.mu. The
// submitted draft is already validated and canonical; no discovery or saved-rule
// expansion is needed to cancel a check and durably disable its scheduler.
func (l *LiveAlerts) stopAlerts(config LiveAlertsConfig) (LiveAlertsInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.blocked {
		return l.stopInfoLocked(), errors.New("Live Alerts are closed or their saved state is invalid; saved configuration preserved")
	}
	if l.cycleCancel != nil {
		l.cycleCancel()
	}
	previous := l.state.Config
	l.state.Config = copyLiveAlertsConfig(config)
	if err := writeState(l.path, l.state); err != nil {
		l.state.Config = previous
		return l.stopInfoLocked(), fmt.Errorf("save stopped Live Alerts configuration: %w", err)
	}
	l.watermarksDirty = false
	l.configRevision++
	l.configureRevision++
	l.requested = false
	l.nextRun, l.nextBoundary, l.retryAsOf = 0, 0, 0
	l.retryConfig = nil
	l.retryManual = false
	l.scheduleScope = nil
	l.scheduleCalendar = ""
	l.errorText = ""
	l.status = "Disabled"
	if l.running {
		l.status = "Stopping active check"
	}
	l.signal()
	return l.stopInfoLocked(), nil
}

// This immediate stop response uses only owned memory. The ordinary polling
// endpoint supplies fresh rule/catalog metadata after cancellation has returned.
// Counts, delivery history and whether the cancelled worker is still draining
// remain truthful while the full presentation is being refreshed.
func (l *LiveAlerts) stopInfoLocked() LiveAlertsInfo {
	info := LiveAlertsInfo{Config: copyLiveAlertsConfig(l.state.Config), Running: l.running, LastRun: l.lastRun, NextRun: l.nextRun, Sent: l.state.Sent, Error: liveAlertErrorText(l.errorText), Status: l.status, Progress: l.progress, CurrentAsset: l.currentAsset, Recent: append([]LiveAlertOutcome{}, l.state.Recent...), Assets: []LiveAlertAsset{}, ConditionScopes: []LiveAlertConditionScope{}, Checks: []LiveAlertCheck{}}
	for i := range info.Recent {
		info.Recent[i].Message = liveAlertErrorText(info.Recent[i].Message)
		info.Recent[i].Profitability = copyLiveProfitability(info.Recent[i].Profitability)
	}
	ids := make([]string, 0, len(l.state.Checks))
	for id := range l.state.Checks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		state := l.state.Checks[id]
		name := id
		for _, outcome := range l.state.Recent {
			if outcome.ConditionID == id && outcome.ConditionName != "" {
				name = outcome.ConditionName
				break
			}
		}
		row := LiveAlertCheck{ConditionID: id, Name: name, AssetsScanned: state.AssetsScanned, LastChecked: state.LastChecked, SentAlerts: state.SentAlerts, Status: state.Status, Manual: state.Manual, Error: liveAlertErrorText(state.Error)}
		if info.Config.Alerts != nil {
			entry, exists := liveAlertEntry(info.Config, id)
			if !exists {
				continue
			}
			row.Name, row.AlertID, row.SavedConditionID = entry.Name, entry.ID, entry.ConditionID
		} else if !containsLiveValue(info.Config.ConditionIDs, id) {
			continue
		}
		info.Checks = append(info.Checks, row)
	}
	return info
}
