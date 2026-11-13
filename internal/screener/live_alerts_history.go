package screener

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LiveAlertCheck summarizes the latest real attempt for one selected condition.
// AssetsScanned counts distinct symbols, even when both saved periods are used.
// SentAlerts counts signals confirmed for at least one recipient during the
// latest check, including partial recipient batches. Legacy attempt counts are
// explicitly unknown; LifetimeSentAlerts preserves their historical total.
type LiveAlertCheck struct {
	Progress           float64 `json:"progress,omitempty"`
	Phase              string  `json:"phase,omitempty"`
	CurrentAsset       string  `json:"currentAsset,omitempty"`
	ProgressState      string  `json:"progressState,omitempty"`
	ProgressMode       string  `json:"progressMode,omitempty"`
	AlertID            string  `json:"alertId,omitempty"`
	SavedConditionID   string  `json:"savedConditionId,omitempty"`
	ConditionID        string  `json:"conditionId"`
	Name               string  `json:"name"`
	AssetsScanned      int     `json:"assetsScanned"`
	AssetsSkipped      int     `json:"assetsSkipped,omitempty"`
	AssetsWarmingUp    int     `json:"assetsWarmingUp,omitempty"`
	LastChecked        int64   `json:"lastChecked"`
	TriggeredAt        int64   `json:"triggeredAt"`
	DurationMs         int64   `json:"durationMs"`
	DurationKnown      bool    `json:"durationKnown"`
	NextCheck          int64   `json:"nextCheck"`
	SentAlerts         int64   `json:"sentAlerts"`
	SentAlertsKnown    bool    `json:"sentAlertsKnown"`
	LifetimeSentAlerts int64   `json:"lifetimeSentAlerts"`
	Status             string  `json:"status"`
	Manual             bool    `json:"manual"`
	Error              string  `json:"error"`
	TimingHint         string  `json:"timingHint,omitempty"`
}

type liveAlertCheckState struct {
	AssetsScanned       int    `json:"assetsScanned"`
	AssetsSkipped       int    `json:"assetsSkipped,omitempty"`
	AssetsWarmingUp     int    `json:"assetsWarmingUp,omitempty"`
	LastChecked         int64  `json:"lastChecked"`
	TriggeredAt         int64  `json:"triggeredAt,omitempty"`
	DurationMs          *int64 `json:"durationMs,omitempty"`
	SentAlerts          int64  `json:"sentAlerts"`
	LastCheckSentAlerts *int64 `json:"lastCheckSentAlerts,omitempty"`
	CheckID             string `json:"checkId,omitempty"`
	Status              string `json:"status"`
	Manual              bool   `json:"manual"`
	Error               string `json:"error,omitempty"`
	// The original time retains Go's monotonic clock while this process owns
	// the attempt. It is deliberately absent from the saved file: an interrupted
	// check has no trustworthy finish instant after restarting the application.
	triggerTime time.Time
}

type liveAlertCheckRun struct {
	live           *LiveAlerts
	scannedSymbols map[string]map[string]bool
	skippedSymbols map[string]map[string]bool
	warmingSymbols map[string]map[string]bool
	expectedPairs  map[string]map[string]bool
	processedPairs map[string]map[string]bool
	failures       map[string]error
	checkIDs       map[string]string
	finished       bool
	borrowed       bool
}

func copyLiveCheckStates(values map[string]liveAlertCheckState) map[string]liveAlertCheckState {
	copies := make(map[string]liveAlertCheckState, len(values))
	for id, value := range values {
		if value.LastCheckSentAlerts != nil {
			count := *value.LastCheckSentAlerts
			value.LastCheckSentAlerts = &count
		}
		if value.DurationMs != nil {
			duration := *value.DurationMs
			value.DurationMs = &duration
		}
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
		if record.AssetsWarmingUp < 0 || record.AssetsWarmingUp > 20000 || record.AssetsSkipped < 0 || record.AssetsSkipped > 20000 || record.AssetsScanned < 0 || record.AssetsScanned > 20000 || record.LastChecked <= 0 || record.LastChecked > 253402300799999 || record.SentAlerts < 0 || record.SentAlerts > sent || len(record.Error) > 5000 {
			return errors.New("invalid Live Alerts condition check counts or timestamp")
		}
		if record.LastCheckSentAlerts != nil {
			if !validLiveAlertID(record.CheckID) || *record.LastCheckSentAlerts < 0 || *record.LastCheckSentAlerts > record.SentAlerts {
				return errors.New("invalid Live Alerts latest-check delivery count")
			}
		} else if record.CheckID != "" {
			return errors.New("Live Alerts check identity has no delivery counter")
		}
		if record.TriggeredAt < 0 || record.TriggeredAt > 253402300799999 || record.TriggeredAt > 0 && !validLiveAlertID(record.CheckID) {
			return errors.New("invalid Live Alerts check trigger timestamp")
		}
		if record.DurationMs != nil && (record.TriggeredAt == 0 || *record.DurationMs < 0 || *record.DurationMs > 253402300799999 || record.Status == "running") {
			return errors.New("invalid Live Alerts check duration")
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

func (l *LiveAlerts) beginCheckHistory(config LiveAlertsConfig, manual bool, triggers ...time.Time) (*liveAlertCheckRun, error) {
	run := &liveAlertCheckRun{live: l, scannedSymbols: map[string]map[string]bool{}, skippedSymbols: map[string]map[string]bool{}, warmingSymbols: map[string]map[string]bool{}, expectedPairs: map[string]map[string]bool{}, processedPairs: map[string]map[string]bool{}, failures: map[string]error{}, checkIDs: map[string]string{}}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.blocked || l.closed {
		return nil, errors.New("Live Alerts check history is closed or invalid")
	}
	trigger := l.now()
	if len(triggers) > 0 && !triggers[0].IsZero() {
		trigger = triggers[0]
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
		run.skippedSymbols[id] = map[string]bool{}
		run.warmingSymbols[id] = map[string]bool{}
		run.expectedPairs[id] = map[string]bool{}
		run.processedPairs[id] = map[string]bool{}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			l.state.Checks = previous
			return nil, &liveAlertPersistenceError{fmt.Errorf("create Live Alerts check identity: %w", err)}
		}
		checkID := hex.EncodeToString(nonce[:])
		zero := int64(0)
		run.checkIDs[id] = checkID
		prior := l.state.Checks[id]
		l.state.Checks[id] = liveAlertCheckState{LastChecked: l.now().UTC().UnixMilli(), TriggeredAt: trigger.UTC().UnixMilli(), triggerTime: trigger, SentAlerts: prior.SentAlerts, LastCheckSentAlerts: &zero, CheckID: checkID, Status: "running", Manual: manual}
	}
	if len(run.scannedSymbols) == 0 {
		return run, nil
	}
	if err := writeState(l.path, l.state); err != nil {
		l.state.Checks = previous
		return nil, &liveAlertPersistenceError{fmt.Errorf("record Live Alerts check start: %w", err)}
	}
	for id := range run.scannedSymbols {
		if progress, ok := l.checkProgress[id]; ok {
			progress.started = true
			l.checkProgress[id] = progress
		}
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
	if state, exists := r.live.state.Checks[conditionID]; exists && state.CheckID == r.checkIDs[conditionID] {
		state.AssetsScanned = len(symbols)
		r.live.state.Checks[conditionID] = state
	}
}

// Waiting for genuine history is a completed inspection, not an invalid rule
// or provider outage. Account for this pair only in the current run; do not
// claim its candle, so the next eligible close can become ready normally.
func (r *liveAlertCheckRun) warmingUp(id, symbol, period string) {
	if r == nil {
		return
	}
	r.live.mu.Lock()
	defer r.live.mu.Unlock()
	if r.finished {
		return
	}
	if symbols, known := r.warmingSymbols[id]; known {
		symbols[symbol] = true
		if state, exists := r.live.state.Checks[id]; exists && state.CheckID == r.checkIDs[id] {
			state.AssetsWarmingUp = len(symbols)
			r.live.state.Checks[id] = state
		}
		r.processedPairs[id][symbol+"/"+period] = true
	}
}

func (r *liveAlertCheckRun) skipped(id, symbol string, err error) {
	if r == nil {
		return
	}
	r.failed(id, err)
	r.live.mu.Lock()
	defer r.live.mu.Unlock()
	if r.finished {
		return
	}
	if symbols, known := r.skippedSymbols[id]; known {
		symbols[symbol] = true
		if state, exists := r.live.state.Checks[id]; exists && state.CheckID == r.checkIDs[id] {
			state.AssetsSkipped = len(symbols)
			r.live.state.Checks[id] = state
		}
	}
}

func (r *liveAlertCheckRun) failed(conditionID string, err error) {
	if r == nil || err == nil {
		return
	}
	r.live.mu.Lock()
	defer r.live.mu.Unlock()
	if !r.finished {
		r.failures[conditionID] = joinLiveAlertErrors(r.failures[conditionID], err)
	}
}

// Keep distinct asset/context errors and their original types for errors.Is;
// repeated shared failures must not crowd out a later delivery error.
func joinLiveAlertErrors(values ...error) error {
	seen := map[string]bool{}
	var unique []error
	var visit func(error)
	visit = func(err error) {
		if err == nil {
			return
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				visit(child)
			}
			return
		}
		if text := err.Error(); !seen[text] {
			seen[text] = true
			unique = append(unique, err)
		}
	}
	for _, err := range values {
		visit(err)
	}
	return errors.Join(unique...)
}

func liveAlertErrorText(message string) string {
	seen := map[string]bool{}
	var lines []string
	for _, line := range strings.Split(message, "\n") {
		text := strings.Join(strings.Fields(line), " ")
		// Earlier versions flattened errors.Join into adjacent identical sentences.
		pending := errLiveAlertsPendingBar.Error()
		for strings.Contains(text, pending+" "+pending) {
			text = strings.ReplaceAll(text, pending+" "+pending, pending)
		}
		if text != "" && !seen[text] {
			seen[text] = true
			lines = append(lines, text)
		}
	}
	message = strings.Join(lines, "\n")
	// Saved check/outcome records have an existing 5,000-byte resource limit.
	if len(message) > 5000 {
		message = strings.ToValidUTF8(message[:5000], "")
	}
	return message
}

func checkHistoryMessage(err error) string {
	if err == nil {
		return ""
	}
	return liveAlertErrorText(err.Error())
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
	if r.borrowed {
		for id := range r.scannedSymbols {
			complete := len(r.expectedPairs[id]) > 0
			for pair := range r.expectedPairs[id] {
				complete = complete && r.processedPairs[id][pair]
			}
			if !complete {
				r.failures[id] = joinLiveAlertErrors(r.failures[id], cycleErr)
			}
		}
		return nil
	}
	changed := false
	for id, symbols := range r.scannedSymbols {
		state, exists := l.state.Checks[id]
		if !exists || state.CheckID != r.checkIDs[id] {
			continue
		} // A deleted/newer attempt cannot be replaced by an obsolete cycle.
		state.AssetsScanned = len(symbols)
		state.AssetsSkipped = len(r.skippedSymbols[id])
		state.AssetsWarmingUp = len(r.warmingSymbols[id])
		finishedAt := l.now()
		state.LastChecked = finishedAt.UTC().UnixMilli()
		if !state.triggerTime.IsZero() {
			duration := liveAlertElapsedMs(state.triggerTime, finishedAt)
			state.DurationMs = &duration
		}
		state.Status, state.Error = "completed", ""
		if completedLiveAlertScan(cycleErr) {
			state.Error = checkHistoryMessage(r.failures[id])
			if state.Error == "" {
				complete := len(r.expectedPairs[id]) > 0
				for pair := range r.expectedPairs[id] {
					complete = complete && r.processedPairs[id][pair]
				}
				if !complete {
					state.Error = checkHistoryMessage(cycleErr)
				}
			}
		} else if errors.Is(cycleErr, context.Canceled) || errors.Is(cycleErr, context.DeadlineExceeded) {
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
		return &liveAlertPersistenceError{fmt.Errorf("save Live Alerts check history: %w", err)}
	}
	return nil
}

// Called when at least one recipient is confirmed, with l.mu held, before the
// existing atomic state write. A partially successful batch counts one alert.
func (l *LiveAlerts) recordConfirmedAlertLocked(conditionID string, checkIDs ...string) {
	state, exists := l.state.Checks[conditionID]
	if !exists {
		return
	}
	state.SentAlerts++ // Historical/global accounting is independent of a run.
	if len(checkIDs) > 0 && checkIDs[0] != "" && checkIDs[0] == state.CheckID && state.LastCheckSentAlerts != nil {
		next := *state.LastCheckSentAlerts + 1
		state.LastCheckSentAlerts = &next
	}
	l.state.Checks[conditionID] = state
}

// Capture the originating attempt before awaiting a notification response. A
// late acknowledgement can update its own latest/cancelled check, but must never
// increment a newer check which happens to use the same alert identifier.
func (l *LiveAlerts) deliveryCheckIDLocked(ctx context.Context, id string) string {
	if group, _ := ctx.Value(liveAlertHistoryGroupKey{}).(*liveAlertHistoryGroup); group != nil {
		if group.run != nil {
			return group.run.checkIDs[id]
		}
		return ""
	}
	// Direct/internal callers have no group; bind only to the currently running
	// attempt at invocation, never to a completed historical row.
	if state, ok := l.state.Checks[id]; ok && state.Status == "running" {
		return state.CheckID
	}
	return ""
}

func applyLiveAlertSentCounts(row *LiveAlertCheck, state liveAlertCheckState) {
	row.LifetimeSentAlerts = state.SentAlerts
	row.SentAlerts = 0
	row.SentAlertsKnown = state.LastCheckSentAlerts != nil
	if state.LastCheckSentAlerts != nil {
		row.SentAlerts = *state.LastCheckSentAlerts
	}
}

// A running snapshot uses the same backend clock as the check. Finished values
// are frozen and persisted; legacy/interrupted checks remain explicitly unknown.
func applyLiveAlertCheckTiming(row *LiveAlertCheck, state liveAlertCheckState, now time.Time) {
	row.TriggeredAt, row.DurationMs, row.DurationKnown = state.TriggeredAt, 0, false
	if state.DurationMs != nil {
		row.DurationMs, row.DurationKnown = *state.DurationMs, true
	} else if state.Status == "running" && !state.triggerTime.IsZero() {
		row.DurationMs, row.DurationKnown = liveAlertElapsedMs(state.triggerTime, now), true
	}
}

func liveAlertElapsedMs(trigger, now time.Time) int64 {
	return max(int64(0), now.Sub(trigger).Milliseconds())
}

func liveAlertCheckRows(saved []SavedAnalysisCondition, scopes []LiveAlertConditionScope, config LiveAlertsConfig, states map[string]liveAlertCheckState, now time.Time, retry *LiveAlertsConfig, retryAt, retryAsOf int64, retryManual bool) []LiveAlertCheck {
	rows := []LiveAlertCheck{}
	for _, condition := range saved {
		entry, named := liveAlertEntry(config, condition.ID)
		if !containsLiveValue(config.ConditionIDs, condition.ID) && !named {
			continue
		}
		state, checked := states[condition.ID]
		row := LiveAlertCheck{ConditionID: condition.ID, Name: condition.Name, AssetsScanned: state.AssetsScanned, AssetsSkipped: state.AssetsSkipped, AssetsWarmingUp: state.AssetsWarmingUp, LastChecked: state.LastChecked, SentAlerts: state.SentAlerts, Status: state.Status, Manual: state.Manual, Error: liveAlertErrorText(state.Error)}
		applyLiveAlertSentCounts(&row, state)
		applyLiveAlertCheckTiming(&row, state, now)
		if named {
			row.AlertID, row.SavedConditionID = entry.ID, entry.ConditionID
		}
		if !checked {
			row.Status = "never"
			row.SentAlertsKnown = true
		}
		if config.Enabled && (!named || entry.Enabled) {
			periods := []string{}
			for _, scope := range scopes {
				if scope.ConditionID == condition.ID {
					if scope.AllAssets {
						periods = append(periods, savedLiveAlertPeriods(condition)...)
						continue
					}
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
