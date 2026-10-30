package screener

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (l *LiveAlerts) check(ctx context.Context, config LiveAlertsConfig, asOf int64) error {
	return l.checkCycle(ctx, config, asOf, false)
}

func (l *LiveAlerts) checkCycle(ctx context.Context, config LiveAlertsConfig, asOf int64, scheduled bool) error {
	if err := validateLiveAlertsConfig(config); err != nil {
		return err
	}
	config = canonicalLiveAlertsConfig(config)
	conditions, err := l.selectedConditions(config)
	if err != nil {
		return err
	}
	m := l.manager
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("screener is closed")
	}
	if m.active || m.pendingLocked() || m.enrichmentActive || m.refreshActive || m.analysisActiveLocked() || m.liveActive {
		m.mu.Unlock()
		return errLiveAlertsBusy
	}
	if l.refresh == nil {
		if message := m.refreshRuntimeError(); message != "" {
			m.mu.Unlock()
			return errors.New(message)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.liveActive, m.liveCancel = true, cancel
	m.refreshActive, m.refreshCancel, m.refreshDone, m.refreshAsOf = true, cancel, done, asOf
	directory := m.directory
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		m.liveActive, m.liveCancel = false, nil
		m.refreshActive, m.refreshCancel = false, nil
		close(done)
		m.mu.Unlock()
		m.wg.Done()
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err = recoverHistoryPublications(directory); err != nil {
		return fmt.Errorf("recover saved history: %w", err)
	}
	groups, err := liveAlertGroups(directory, config, conditions)
	if err != nil {
		return err
	}
	refresh := l.refresh
	if refresh == nil {
		refresh = l.refreshSelected
	}
	refreshed, refreshErr := refresh(ctx, directory, groups, asOf)
	if refreshErr != nil && len(refreshed) == 0 {
		return refreshErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	work, err := os.MkdirTemp(m.profile, ".screener-live-analysis-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	a := &analysisState{directory: directory, work: work, series: map[string]*analysisSeries{}, cache: newAnalysisRowCache(), asOf: asOf}
	// Refresh can rename/consolidate an existing history. Re-list its published
	// canonical name rather than retaining now-stale IDs from before the append.
	names, err := csvNames(directory)
	if err != nil {
		return err
	}
	var failures []error
	if refreshErr != nil {
		failures = append(failures, refreshErr)
	}
	targets := []*analysisSeries{}
	for _, name := range names {
		match := downloadFilename.FindStringSubmatch(strings.ToLower(name))
		if match == nil || !refreshed[match[1]+"/"+match[2]] {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		l.mu.Lock()
		l.status, l.currentAsset = "Checking latest closed bars", strings.ToUpper(match[1])+" "+match[2]
		l.mu.Unlock()
		id := dataFileID(directory, name)
		s, e := indexAnalysisCSV(ctx, directory, name, filepath.Join(work, id+".rows"), func(float64) {})
		if e != nil {
			failures = append(failures, fmt.Errorf("index %s: %w", name, e))
			continue
		}
		s.cache = a.cache
		a.series[id] = s
		if containsLiveValue(liveAlertsPeriods(config), s.metadata.Timeframe) {
			targets = append(targets, s)
		}
	}
	for i, s := range targets {
		if err = ctx.Err(); err != nil {
			return err
		}
		l.mu.Lock()
		l.currentAsset, l.progress = strings.ToUpper(s.metadata.Symbol)+" "+s.metadata.Timeframe, 70+30*float64(i)/float64(max(1, len(targets)))
		l.mu.Unlock()
		closed, e := s.closedCount(asOf)
		if e != nil {
			return e
		}
		if closed == 0 {
			if scheduled {
				failures = append(failures, fmt.Errorf("%s %s: %w", strings.ToUpper(s.metadata.Symbol), s.metadata.Timeframe, errLiveAlertsPendingBar))
			}
			continue
		}
		latest, e := s.row(closed - 1)
		if e != nil {
			return e
		}
		if scheduled {
			interval := time.Hour
			if s.metadata.Timeframe == "d1" {
				interval = 24 * time.Hour
			}
			expected := time.UnixMilli(asOf).UTC().Truncate(interval).Add(-interval).UnixMilli()
			if latest.timestamp != expected {
				failures = append(failures, fmt.Errorf("%s %s: %w; waiting for the bar closing at %s", strings.ToUpper(s.metadata.Symbol), s.metadata.Timeframe, errLiveAlertsPendingBar, time.UnixMilli(expected).Add(interval).UTC().Format(time.RFC3339)))
				continue
			}
		}
		for _, saved := range conditions {
			if liveAlertsConditionPeriod(config, saved.ID) != s.metadata.Timeframe {
				continue
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			key := saved.ID + "/" + s.metadata.Symbol + "/" + s.metadata.Timeframe
			l.mu.Lock()
			previous, seen := l.state.Watermarks[key]
			l.mu.Unlock()
			if seen && latest.timestamp <= previous {
				continue
			}
			a.request = copyAnalysisRequest(saved.Request)
			// The explicit Live Alerts choices select base assets and periods;
			// a saved historical screen's filters do not narrow those choices.
			a.request.Period, a.request.Categories = "all", nil
			matched, e := evaluateLiveAlertLatest(ctx, a, s, closed)
			if e != nil {
				failures = append(failures, fmt.Errorf("%s %s, %s: %w", strings.ToUpper(s.metadata.Symbol), s.metadata.Timeframe, saved.Name, e))
				continue
			}
			// A matching claim commits before sending; non-match watermarks are
			// flushed together after this asset's conditions have been checked.
			var alert *LiveAlert
			if matched {
				value, e := liveAlertFromMatch(saved, s, closed-1, latest)
				if e != nil {
					failures = append(failures, e)
					continue
				}
				alert = &value
			}
			if e = l.claim(ctx, key, latest.timestamp, alert); e != nil {
				return e
			}
			if alert != nil {
				if e = l.deliver(ctx, *alert); e != nil {
					// Delivery uses one shared Telegram recipient. Stop after the
					// first failure instead of repeating a refused/rate-limited
					// request and consuming claims for every remaining signal.
					return errors.Join(append(failures, e)...)
				}
			}
		}
		// Non-matching bars cannot send notifications. Batch their durable
		// watermarks per asset instead of rewriting the entire state per rule.
		l.mu.Lock()
		if l.watermarksDirty {
			err = writeState(l.path, l.state)
			if err == nil {
				l.watermarksDirty = false
			}
		}
		l.mu.Unlock()
		if err != nil {
			return fmt.Errorf("save processed Live Alerts bars: %w", err)
		}
	}
	return errors.Join(failures...)
}

func liveAlertGroups(directory string, config LiveAlertsConfig, conditions []SavedAnalysisCondition) ([]historyGroup, error) {
	periods := map[string]bool{}
	for _, period := range liveAlertsPeriods(config) {
		periods[period] = true
	}
	// An explicit companion timeframe belongs to the same signal snapshot. Its
	// latest data and recipes must be refreshed before compiling the condition.
	for _, saved := range conditions {
		for _, condition := range saved.Request.Conditions {
			for _, operand := range conditionOperands(condition) {
				if operand.Kind == "field" && operand.Timeframe != "" && operand.Timeframe != "same" {
					periods[operand.Timeframe] = true
				}
			}
		}
	}
	names, err := csvNames(directory)
	if err != nil {
		return nil, err
	}
	wanted := []string{}
	for _, name := range names {
		match := downloadFilename.FindStringSubmatch(strings.ToLower(name))
		if match != nil && containsLiveValue(config.Symbols, match[1]) && periods[match[2]] {
			wanted = append(wanted, name)
		}
	}
	for _, period := range liveAlertsPeriods(config) {
		found := false
		for _, name := range wanted {
			match := downloadFilename.FindStringSubmatch(strings.ToLower(name))
			if match[2] == period {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("no downloaded %s history remains for its selected saved condition; download and enrich a selected asset", strings.ToUpper(period))
		}
	}
	return groupHistories(directory, wanted), nil
}

func (l *LiveAlerts) refreshSelected(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
	m := l.manager
	jobs := make([]RefreshJob, len(groups))
	for i, group := range groups {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		jobs[i] = RefreshJob{ID: hex.EncodeToString(random[:]), FileID: dataFileID(directory, group.name), Name: group.name, fileIDAliases: group.ids, Status: "queued", Message: "Waiting for Live Alerts refresh", Columns: []string{}}
	}
	m.mu.Lock()
	m.refreshJobs = jobs
	m.mu.Unlock()
	ctx, cleanup, err := withProviderSession(ctx, m.profile)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	legacy := legacyRecipes(ctx, directory)
	refreshed := map[string]bool{}
	var failures []error
	for i, group := range groups {
		if err = ctx.Err(); err != nil {
			return refreshed, err
		}
		err = nil
		l.mu.Lock()
		l.currentAsset, l.progress = group.name, 70*float64(i)/float64(max(1, len(groups)))
		l.mu.Unlock()
		m.mu.Lock()
		m.refreshJobs[i].Status, m.refreshJobs[i].Message = "running", "Appending closed bars and updating applied indicators"
		job := m.refreshJobs[i]
		m.mu.Unlock()
		if len(group.ids) > 1 {
			job.Name, err = m.consolidateHistory(ctx, directory, job.Name, legacy, func(message string) {
				m.mu.Lock()
				m.refreshJobs[i].Message = message
				m.mu.Unlock()
			})
		}
		if err == nil {
			err = m.refreshFile(ctx, i, directory, job, legacy[job.Name], asOf)
		}
		if err != nil {
			m.mu.Lock()
			m.refreshJobs[i].Status, m.refreshJobs[i].Message = "failed", cleanMessage(err.Error())
			m.mu.Unlock()
			failures = append(failures, fmt.Errorf("refresh %s: %w", group.name, err))
			if liveAlertProviderStopped(ctx, err) {
				m.mu.Lock()
				for j := i + 1; j < len(m.refreshJobs); j++ {
					m.refreshJobs[j].Status, m.refreshJobs[j].Message = "cancelled", "Live Alerts refresh stopped after provider refusal or cancellation"
				}
				m.mu.Unlock()
				// Successfully refreshed assets can still be evaluated. No stale
				// counterpart enters the snapshot after a source failure.
				return refreshed, errors.Join(failures...)
			}
			continue
		}
		match := downloadFilename.FindStringSubmatch(strings.ToLower(job.Name))
		if match != nil {
			refreshed[match[1]+"/"+match[2]] = true
		}
	}
	return refreshed, errors.Join(failures...)
}

func liveAlertProviderStopped(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	if session := sessionFromContext(ctx); session != nil && session.limiter != nil {
		session.limiter.mu.Lock()
		stopped := max(session.limiter.state.DeniedTill, session.limiter.state.LimitedTill) > time.Now().UnixMilli()
		session.limiter.mu.Unlock()
		if stopped {
			return true
		}
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"403", "429", "access-denial", "rate-limit", "rate limit", "cooldown"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// The visual evaluator retains previous values and rolling boolean history.
// Warm just the necessary preceding bars, then expose only the newest fully
// closed result. Occurred/remained wrappers over crosses need one extra bar.
func evaluateLiveAlertLatest(ctx context.Context, a *analysisState, s *analysisSeries, closed int64) (bool, error) {
	compiled, err := compileAnalysisConditions(a, s)
	if err != nil {
		return false, err
	}
	warm := int64(1)
	for _, condition := range a.request.Conditions {
		if condition.Operator == "occurredWithin" || condition.Operator == "remainedFor" {
			warm = max(warm, int64(condition.Bars))
		}
	}
	matched := false
	for i := max(int64(0), closed-1-warm); i < closed; i++ {
		if err = ctx.Err(); err != nil {
			return false, err
		}
		row, e := s.row(i)
		if e != nil {
			return false, e
		}
		matched = true
		for _, condition := range compiled {
			ok, e := condition.evaluate(i, analysisClosedAt(row.timestamp, s.metadata.Timeframe))
			if e != nil {
				return false, e
			}
			matched = matched && ok
		}
	}
	for _, series := range a.series {
		if err = analysisUnchanged(series); err != nil {
			return false, err
		}
	}
	return matched, nil
}

func liveAlertFromMatch(saved SavedAnalysisCondition, s *analysisSeries, index int64, row analysisRow) (LiveAlert, error) {
	position, ok := s.fieldIndex("close")
	if !ok || !analysisFinite(row.values[position]) {
		return LiveAlert{}, errors.New("Live Alerts requires a finite closing price")
	}
	alert := LiveAlert{ConditionID: saved.ID, ConditionName: saved.Name, Symbol: s.metadata.Symbol, AssetName: s.metadata.Description, Timeframe: s.metadata.Timeframe, Time: row.timestamp, EntryPrice: row.values[position]}
	config := saved.Request.TradeSimulation
	if config == nil {
		return alert, nil
	}
	stopIndex, hasStop := s.fieldIndex(config.StopLoss)
	stopRow := index - int64(config.StopLossBarsAgo)
	if hasStop && stopRow >= 0 {
		stop, err := s.row(stopRow)
		if err != nil {
			return alert, err
		}
		value := stop.values[stopIndex]
		if config.validStopLoss(alert.EntryPrice, value) {
			alert.StopLoss = &value
		}
	}
	if config.TakeProfitMultiple > 0 {
		if alert.StopLoss != nil {
			value := config.riskMultipleTarget(alert.EntryPrice, *alert.StopLoss)
			if config.validTakeProfit(alert.EntryPrice, value) {
				alert.TakeProfit = &value
			}
		}
	} else if profitIndex, ok := s.fieldIndex(config.TakeProfit); ok {
		value := row.values[profitIndex]
		if config.validTakeProfit(alert.EntryPrice, value) {
			alert.TakeProfit = &value
		}
	}
	return alert, nil
}

func (l *LiveAlerts) claim(ctx context.Context, key string, timestamp int64, alert *LiveAlert) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || !l.state.Config.Enabled {
		return context.Canceled
	}
	previous, existed := l.state.Watermarks[key]
	if l.state.Watermarks == nil {
		l.state.Watermarks = map[string]int64{}
	}
	l.state.Watermarks[key] = timestamp
	if alert == nil {
		l.watermarksDirty = true
		return nil
	}
	priorRecent := append([]LiveAlertOutcome{}, l.state.Recent...)
	l.state.Recent = append([]LiveAlertOutcome{{ConditionName: alert.ConditionName, Symbol: alert.Symbol, AssetName: alert.AssetName, Timeframe: alert.Timeframe, Time: alert.Time, Status: "claimed", Message: "Delivery claimed; interrupted attempts are not sent again automatically."}}, l.state.Recent...)
	if len(l.state.Recent) > 50 {
		l.state.Recent = l.state.Recent[:50]
	}
	if err := writeState(l.path, l.state); err != nil {
		if existed {
			l.state.Watermarks[key] = previous
		} else {
			delete(l.state.Watermarks, key)
		}
		l.state.Recent = priorRecent
		return fmt.Errorf("record processed Live Alerts bar: %w", err)
	}
	l.watermarksDirty = false
	return nil
}

func (l *LiveAlerts) deliver(ctx context.Context, alert LiveAlert) error {
	var err error
	if l.send == nil {
		err = errors.New("Telegram delivery is unavailable; configure the Telegram Bot settings")
	} else if ctx.Err() != nil {
		err = ctx.Err()
	} else {
		err = l.send(ctx, alert)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err == nil {
		l.state.Sent++
	}
	if len(l.state.Recent) > 0 {
		outcome := &l.state.Recent[0]
		outcome.Status, outcome.Message = "sent", "Telegram alert sent"
		if err != nil {
			outcome.Status, outcome.Message = "failed", cleanMessage(err.Error())+". This bar will not be resent automatically; check Telegram before any manual action."
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "uncertain") {
				outcome.Status = "uncertain"
			}
		}
	}
	if saveErr := writeState(l.path, l.state); saveErr != nil {
		return fmt.Errorf("record Live Alerts delivery result (the saved delivery claim prevents a duplicate): %w", saveErr)
	}
	l.watermarksDirty = false
	if err != nil {
		return fmt.Errorf("Telegram alert for %s, %s: %w", strings.ToUpper(alert.Symbol), alert.ConditionName, err)
	}
	return nil
}
