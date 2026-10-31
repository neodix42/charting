package screener

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LiveAlertCheck summarizes the latest real attempt for one selected condition.
// AssetsScanned counts distinct symbols, even when both saved periods are used.
// SentAlerts counts confirmed deliveries since condition-level tracking began.
type LiveAlertCheck struct {
	ConditionID   string `json:"conditionId"`
	Name          string `json:"name"`
	AssetsScanned int    `json:"assetsScanned"`
	LastChecked   int64  `json:"lastChecked"`
	NextCheck     int64  `json:"nextCheck"`
	SentAlerts    int64  `json:"sentAlerts"`
	Status        string `json:"status"`
	Manual        bool   `json:"manual"`
	Error         string `json:"error"`
	TimingHint    string `json:"timingHint,omitempty"`
}

type liveAlertCheckState struct {
	AssetsScanned int    `json:"assetsScanned"`
	LastChecked   int64  `json:"lastChecked"`
	SentAlerts    int64  `json:"sentAlerts"`
	Status        string `json:"status"`
	Manual        bool   `json:"manual"`
	Error         string `json:"error,omitempty"`
}

type liveAlertCheckRun struct {
	live           *LiveAlerts
	scannedSymbols map[string]map[string]bool
	expectedPairs  map[string]map[string]bool
	processedPairs map[string]map[string]bool
	failures       map[string]error
	finished       bool
}

func copyLiveCheckStates(values map[string]liveAlertCheckState) map[string]liveAlertCheckState {
	copies := make(map[string]liveAlertCheckState, len(values))
	for id, value := range values {
		copies[id] = value
	}
	return copies
}

func validateLiveCheckStates(values map[string]liveAlertCheckState, sent int64) error {
	if len(values) > savedConditionsLimit {
		return errors.New("too many Live Alerts condition check records")
	}
	var recordedSent int64
	for id, record := range values {
		if err := validateLiveAlertsConfig(LiveAlertsConfig{ConditionIDs: []string{id}}); err != nil {
			return err
		}
		if record.AssetsScanned < 0 || record.AssetsScanned > len(catalogAssets) || record.LastChecked <= 0 || record.LastChecked > 253402300799999 || record.SentAlerts < 0 || record.SentAlerts > sent || len(record.Error) > 5000 {
			return errors.New("invalid Live Alerts condition check counts or timestamp")
		}
		switch record.Status {
		case "running", "completed", "failed", "cancelled":
		default:
			return errors.New("invalid Live Alerts condition check status")
		}
		if record.SentAlerts > sent-recordedSent {
			return errors.New("Live Alerts condition sent counts exceed confirmed total")
		}
		recordedSent += record.SentAlerts
	}
	return nil
}

func (l *LiveAlerts) beginCheckHistory(config LiveAlertsConfig, manual bool) (*liveAlertCheckRun, error) {
	run := &liveAlertCheckRun{live: l, scannedSymbols: map[string]map[string]bool{}, expectedPairs: map[string]map[string]bool{}, processedPairs: map[string]map[string]bool{}, failures: map[string]error{}}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.blocked || l.closed {
		return nil, errors.New("Live Alerts check history is closed or invalid")
	}
	previous := copyLiveCheckStates(l.state.Checks)
	if l.state.Checks == nil {
		l.state.Checks = map[string]liveAlertCheckState{}
	}
	for _, id := range canonicalLiveAlertsConfig(config).ConditionIDs {
		if !containsLiveValue(l.state.Config.ConditionIDs, id) {
			continue
		}
		run.scannedSymbols[id] = map[string]bool{}
		run.expectedPairs[id] = map[string]bool{}
		run.processedPairs[id] = map[string]bool{}
		prior := l.state.Checks[id]
		l.state.Checks[id] = liveAlertCheckState{LastChecked: l.now().UTC().UnixMilli(), SentAlerts: prior.SentAlerts, Status: "running", Manual: manual}
	}
	if len(run.scannedSymbols) == 0 {
		return run, nil
	}
	if err := writeState(l.path, l.state); err != nil {
		l.state.Checks = previous
		return nil, fmt.Errorf("record Live Alerts check start: %w", err)
	}
	return run, nil
}

func (r *liveAlertCheckRun) expectPair(conditionID, symbol, period string) {
	if r == nil {
		return
	}
	r.live.mu.Lock()
	defer r.live.mu.Unlock()
	if pairs, known := r.expectedPairs[conditionID]; known && !r.finished {
		pairs[symbol+"/"+period] = true
	}
}

func (r *liveAlertCheckRun) processedPair(conditionID, symbol, period string) {
	if r == nil {
		return
	}
	r.live.mu.Lock()
	defer r.live.mu.Unlock()
	if pairs, known := r.processedPairs[conditionID]; known && !r.finished {
		pairs[symbol+"/"+period] = true
	}
}

func (r *liveAlertCheckRun) scanned(conditionID, symbol string) {
	if r == nil {
		return
	}
	r.live.mu.Lock()
	defer r.live.mu.Unlock()
	if r.finished {
		return
	}
	symbols, known := r.scannedSymbols[conditionID]
	if !known {
		return
	}
	symbols[symbol] = true
	if state, exists := r.live.state.Checks[conditionID]; exists {
		state.AssetsScanned = len(symbols)
		r.live.state.Checks[conditionID] = state
	}
}

func (r *liveAlertCheckRun) failed(conditionID string, err error) {
	if r == nil || err == nil {
		return
	}
	r.live.mu.Lock()
	defer r.live.mu.Unlock()
	if !r.finished {
		r.failures[conditionID] = errors.Join(r.failures[conditionID], err)
	}
}

func checkHistoryMessage(err error) string {
	if err == nil {
		return ""
	}
	message := cleanMessage(err.Error())
	if len(message) > 5000 {
		message = message[:5000]
	}
	return message
}

func (r *liveAlertCheckRun) finish(cycleErr error) error {
	if r == nil {
		return nil
	}
	l := r.live
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.finished {
		return nil
	}
	r.finished = true
	changed := false
	for id, symbols := range r.scannedSymbols {
		state, exists := l.state.Checks[id]
		if !exists {
			continue
		} // A deleted condition cannot be restored by a late cycle.
		state.AssetsScanned = len(symbols)
		state.LastChecked = l.now().UTC().UnixMilli()
		state.Status, state.Error = "completed", ""
		if errors.Is(cycleErr, context.Canceled) || errors.Is(cycleErr, context.DeadlineExceeded) {
			state.Status, state.Error = "cancelled", checkHistoryMessage(cycleErr)
		} else if failure := r.failures[id]; failure != nil {
			state.Status, state.Error = "failed", checkHistoryMessage(failure)
		} else if cycleErr != nil {
			complete := len(r.expectedPairs[id]) > 0
			for pair := range r.expectedPairs[id] {
				if !r.processedPairs[id][pair] {
					complete = false
					break
				}
			}
			if !complete {
				state.Status, state.Error = "failed", checkHistoryMessage(cycleErr)
			}
		}
		l.state.Checks[id] = state
		changed = true
	}
	if !changed {
		return nil
	}
	if err := writeState(l.path, l.state); err != nil {
		return fmt.Errorf("save Live Alerts check history: %w", err)
	}
	return nil
}

// Called inside deliver's confirmed-success branch, with l.mu held, before its
// existing atomic state write. Failed/uncertain sends never increment this.
func (l *LiveAlerts) recordConfirmedAlertLocked(conditionID string) {
	state, exists := l.state.Checks[conditionID]
	if !exists {
		return
	}
	state.SentAlerts++
	l.state.Checks[conditionID] = state
}

func liveAlertCheckRows(saved []SavedAnalysisCondition, scopes []LiveAlertConditionScope, config LiveAlertsConfig, states map[string]liveAlertCheckState, now time.Time, retry *LiveAlertsConfig, retryAt, retryAsOf int64, retryManual bool) []LiveAlertCheck {
	rows := []LiveAlertCheck{}
	for _, condition := range saved {
		if !containsLiveValue(config.ConditionIDs, condition.ID) {
			continue
		}
		state, checked := states[condition.ID]
		row := LiveAlertCheck{ConditionID: condition.ID, Name: condition.Name, AssetsScanned: state.AssetsScanned, LastChecked: state.LastChecked, SentAlerts: state.SentAlerts, Status: state.Status, Manual: state.Manual, Error: state.Error}
		if !checked {
			row.Status = "never"
		}
		if config.Enabled {
			periods := []string{}
			for _, scope := range scopes {
				if scope.ConditionID == condition.ID {
					for _, target := range scope.Targets {
						if !containsLiveValue(periods, target.Period) {
							periods = append(periods, target.Period)
						}
					}
				}
			}
			var next time.Time
			var hints []string
			for _, period := range periods {
				candidate, ok := nextLiveAlertConditionClose(now, condition, period)
				if ok && (next.IsZero() || candidate.Before(next)) {
					next = candidate
				}
				if hint := liveAlertConditionTimingHint(condition, period, candidate); hint != "" {
					hints = append(hints, hint)
				}
			}
			if !next.IsZero() {
				row.NextCheck = next.UnixMilli()
			}
			retrying := retry != nil && containsLiveValue(retry.ConditionIDs, condition.ID) && retryAt > 0 && (row.NextCheck == 0 || retryAt < row.NextCheck)
			if retrying {
				row.NextCheck = retryAt
			}
			row.TimingHint = strings.Join(hints, " ")
			if retrying {
				kind := "bar-close check"
				if retryManual {
					kind = "manual check"
				}
				row.TimingHint = fmt.Sprintf("Retrying the %s from %s at %s.", kind, time.UnixMilli(retryAsOf).UTC().Format("2006-01-02 15:04:05 UTC"), time.UnixMilli(retryAt).UTC().Format("2006-01-02 15:04:05 UTC"))
			}
		}
		rows = append(rows, row)
	}
	return rows
}
