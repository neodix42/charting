package screener

import (
	"errors"
	"fmt"
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
	l.cancelCheckProgressLocked("Cancelled")
	l.clearRequestedCheckLocked()
	if l.retryTargeted {
		// A failed durable Stop still cancels this explicit one-row operation.
		// The unchanged enabled setting can retain its ordinary calendar, but
		// must not resurrect a cancelled manual retry in the background.
		l.retryConfig, l.retryTargeted, l.retryManual, l.retryAsOf = nil, false, false, 0
		l.nextRun = l.nextBoundary
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
	l.nextRun, l.nextBoundary, l.retryAsOf = 0, 0, 0
	l.retryConfig = nil
	l.retryManual = false
	l.retryTargeted = false
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
	ids := []string{}
	if info.Config.Alerts != nil {
		for _, entry := range info.Config.Alerts {
			ids = append(ids, entry.ID)
		}
	} else {
		ids = append(ids, info.Config.ConditionIDs...)
	}
	for _, id := range ids {
		state := l.state.Checks[id]
		name := id
		for _, outcome := range l.state.Recent {
			if outcome.ConditionID == id && outcome.ConditionName != "" {
				name = outcome.ConditionName
				break
			}
		}
		row := LiveAlertCheck{ConditionID: id, Name: name, AssetsScanned: state.AssetsScanned, AssetsSkipped: state.AssetsSkipped, AssetsWarmingUp: state.AssetsWarmingUp, LastChecked: state.LastChecked, SentAlerts: state.SentAlerts, Status: state.Status, Manual: state.Manual, Error: liveAlertErrorText(state.Error)}
		applyLiveAlertSentCounts(&row, state)
		applyLiveAlertCheckTiming(&row, state, l.now())
		if row.Status == "" {
			row.Status = "never"
			row.SentAlertsKnown = true
		}
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
	applyLiveAlertProgress(info.Checks, l.checkProgress)
	return info
}
