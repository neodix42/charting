package screener

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

func (l *LiveAlerts) check(ctx context.Context, config LiveAlertsConfig, asOf int64) error {
	return l.checkCycle(ctx, config, asOf, false)
}

func (l *LiveAlerts) checkCycle(ctx context.Context, config LiveAlertsConfig, asOf int64, scheduled bool) (cycleErr error) {
	if err := validateLiveAlertsConfig(config); err != nil {
		return err
	}
	config = canonicalLiveAlertsConfig(config)
	l.mu.Lock()
	if l.checkExecuting {
		l.mu.Unlock()
		return errLiveAlertsBusy
	}
	l.checkExecuting = true
	l.queueCheckProgressLocked(config.ConditionIDs, !scheduled)
	l.prepareCheckProgressLocked(config.ConditionIDs, "Preparing saved conditions")
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		finishErr := cycleErr
		if ctx.Err() != nil {
			finishErr = ctx.Err()
		} else if errors.Is(cycleErr, errLiveAlertsBusy) {
			finishErr = errLiveAlertsBusy
		}
		l.finishQueuedProgressLocked(finishErr)
		l.checkExecuting = false
		l.mu.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	conditions, err := l.selectedConditionsForCheck(ctx, config)
	if err != nil {
		return err
	}
	selected := []SavedAnalysisCondition{}
	ids := []string{}
	for _, saved := range conditions {
		due := !scheduled
		if scheduled {
			periods := savedLiveAlertPeriods(saved)
			if liveAlertSource(config, saved.ID) == "dukascopy" {
				periods = nil
				for pair := range liveConditionEligiblePairs(saved) {
					_, period, _ := strings.Cut(pair, "/")
					periods = append(periods, period)
				}
			}
			for _, period := range periods {
				if liveAlertsScopeAllows(config, saved.ID, period) && liveAlertConditionDueAt(saved, period, time.UnixMilli(asOf)) {
					due = true
					break
				}
			}
		}
		if due {
			selected = append(selected, saved)
			ids = append(ids, saved.ID)
		}
	}
	l.mu.Lock()
	l.queueCheckProgressLocked(ids, !scheduled)
	l.mu.Unlock()
	if len(selected) == 0 {
		return nil
	}
	l.reportCheckPreparation(ctx, ids, "Pausing downloads and reserving alert workers")
	// Reserve Manager once for the complete batch. Public checks own private
	// snapshots; Dukascopy still has one owner of its shared refresh job list.
	checkContext, release, err := l.reserveCheckManager(ctx, asOf)
	if err != nil {
		return err
	}
	defer release()
	l.reportCheckPreparation(ctx, ids, "Waiting for an alert worker")
	cycleCtx, cancel := context.WithCancel(checkContext)
	defer cancel()
	work := liveAlertPriorityWork(selected, config, asOf, scheduled)
	groups := make([]liveAlertHistoryGroup, len(selected))
	remaining := make([]int, len(selected))
	started := make([]bool, len(selected))
	conditionErrors := make([]error, len(selected))
	for _, task := range work {
		remaining[task.condition]++
	}
	failures := runPriorityAlertWork(cycleCtx, work, func(taskCtx context.Context, task liveAlertWork) error {
		saved := selected[task.condition]
		subset := copyLiveAlertsConfig(config)
		subset.ConditionIDs = []string{saved.ID}
		if task.period != "" {
			subset.scope = []LiveAlertConditionPeriod{{ConditionID: saved.ID, Period: task.period}}
		} else if config.scope != nil {
			subset.scope = []LiveAlertConditionPeriod{}
			for _, scope := range config.scope {
				if scope.ConditionID == saved.ID {
					subset.scope = append(subset.scope, scope)
				}
			}
		}
		taskCtx = context.WithValue(taskCtx, liveAlertProgressKey{}, saved.ID)
		taskCtx = context.WithValue(taskCtx, liveAlertHistoryGroupKey{}, &groups[task.condition])
		var err error
		if task.source == "dukascopy" {
			err = l.checkDukascopyCycle(taskCtx, subset, asOf, scheduled)
		} else {
			err = l.checkPublicCycle(taskCtx, subset, task.source, asOf, scheduled)
		}
		if err != nil {
			return &liveAlertWorkIssue{condition: saved.ID, period: task.period, error: err}
		}
		return nil
	}, func(task liveAlertWork) {
		if !started[task.condition] {
			started[task.condition] = true
			l.mu.Lock()
			l.startCheckProgressLocked(selected[task.condition].ID)
			l.mu.Unlock()
		}
	}, func(task liveAlertWork, err error) {
		index := task.condition
		conditionErrors[index] = joinLiveAlertErrors(conditionErrors[index], err)
		remaining[index]--
		if remaining[index] == 0 {
			if groups[index].run != nil {
				if problem := groups[index].run.finish(conditionErrors[index]); problem != nil {
					conditionErrors[index] = joinLiveAlertErrors(conditionErrors[index], problem)
					cancel()
				}
			}
			l.mu.Lock()
			l.finishCheckProgressLocked(selected[index].ID, conditionErrors[index], asOf)
			l.mu.Unlock()
		}
	}, func(task liveAlertWork, phase string) {
		l.mu.Lock()
		l.prepareCheckProgressLocked([]string{selected[task.condition].ID}, phase)
		l.mu.Unlock()
	})
	for index := range groups {
		if groups[index].run != nil && remaining[index] > 0 {
			failures = append(failures, groups[index].run.finish(joinLiveAlertErrors(conditionErrors[index], context.Canceled)))
		}
		failures = append(failures, conditionErrors[index])
	}

	return errors.Join(failures...)
}

const liveAlertCheckConcurrency = 4

func (l *LiveAlerts) reserveCheckManager(ctx context.Context, asOf int64) (context.Context, func(), error) {
	return l.manager.reserveAlertPriority(ctx, asOf)
}

func (l *LiveAlerts) checkDukascopyCycle(ctx context.Context, config LiveAlertsConfig, asOf int64, scheduled bool) (cycleErr error) {
	if err := validateLiveAlertsConfig(config); err != nil {
		return err
	}
	scope := config.scope
	config = canonicalLiveAlertsConfig(config)
	conditions, err := l.selectedConditionsForCheck(ctx, config)
	if err != nil {
		return err
	}
	if scope == nil {
		config = resolvedLiveAlertsConfig(config, conditions)
	} else {
		config.scope = scope
	}
	if scheduled {
		// Re-check current saved timing before any provider work. A queued
		// scheduled scope can outlive a calendar-rule edit; manual checks still
		// refresh and evaluate the latest closed candle immediately.
		due := []SavedAnalysisCondition{}
		dueIDs := []string{}
		dueScope := []LiveAlertConditionPeriod{}
		seen := map[string]bool{}
		for _, saved := range conditions {
			found := false
			for pair := range liveConditionEligiblePairs(saved) {
				_, period, _ := strings.Cut(pair, "/")
				if !liveAlertsScopeAllows(config, saved.ID, period) || !liveAlertConditionDueAt(saved, period, time.UnixMilli(asOf)) {
					continue
				}
				found = true
				key := saved.ID + "/" + period
				if !seen[key] {
					seen[key] = true
					dueScope = append(dueScope, LiveAlertConditionPeriod{ConditionID: saved.ID, Period: period})
				}
			}
			if found {
				due, dueIDs = append(due, saved), append(dueIDs, saved.ID)
			}
		}
		conditions, config.ConditionIDs, config.scope = due, dueIDs, dueScope
		if len(conditions) == 0 {
			return nil
		}
	}
	qualified := make(map[string]map[string]*SavedConditionProfitability, len(conditions))
	basePairs := map[string]bool{}
	for _, saved := range conditions {
		qualified[saved.ID] = liveConditionEligiblePairs(saved)
		for pair := range qualified[saved.ID] {
			_, period, _ := strings.Cut(pair, "/")
			if liveAlertsScopeAllows(config, saved.ID, period) {
				basePairs[pair] = true
			}
		}
	}
	l.reportCheckPreparation(ctx, config.ConditionIDs, "Preparing Dukascopy history refresh")
	m := l.manager
	m.mu.Lock()
	runtimeMessage := ""
	if l.refresh == nil {
		runtimeMessage = m.refreshRuntimeError()
	}
	directory := m.directory
	m.mu.Unlock()

	run, err := l.beginScopedCheckHistory(ctx, config, !scheduled)
	if err != nil {
		return err
	}
	defer func() { cycleErr = errors.Join(cycleErr, run.finish(cycleErr)) }()
	if runtimeMessage != "" {
		return errors.New(runtimeMessage)
	}
	failPair := func(symbol, period string, failure error) {
		for _, saved := range conditions {
			if _, allowed := qualified[saved.ID][symbol+"/"+period]; allowed && liveAlertsScopeAllows(config, saved.ID, period) {
				run.skipped(saved.ID, symbol, failure)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	l.reportCheckPreparation(ctx, config.ConditionIDs, "Recovering saved history publications")
	if err = recoverHistoryPublications(directory); err != nil {
		return fmt.Errorf("recover saved history: %w", err)
	}
	l.reportCheckPreparation(ctx, config.ConditionIDs, "Selecting assets and required companion history")
	groups, err := liveAlertGroups(directory, config, conditions)
	if err != nil {
		return err
	}
	plannedBase := map[string]bool{}
	for _, group := range groups {
		match := downloadFilename.FindStringSubmatch(strings.ToLower(group.name))
		if match == nil || !basePairs[match[1]+"/"+match[2]] {
			continue
		}
		pair := match[1] + "/" + match[2]
		plannedBase[pair] = true
		for _, saved := range conditions {
			if _, allowed := qualified[saved.ID][pair]; allowed && liveAlertsScopeAllows(config, saved.ID, match[2]) {
				run.expectPair(saved.ID, match[1], match[2])
			}
		}
	}
	refresh := l.refresh
	if refresh == nil {
		required := liveAlertRequiredFields(config, conditions)
		refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
			return l.refreshSelectedWithFields(ctx, directory, groups, asOf, required)
		}
	}
	refreshAsOf := asOf
	if scheduled {
		// A publication/busy retry retains its original signal cutoff, but
		// refresh still catches data up through this attempt's execution time.
		refreshAsOf = max(asOf, l.now().UTC().UnixMilli())
	}
	m.mu.Lock()
	m.refreshAsOf = refreshAsOf
	m.mu.Unlock()
	refreshed, refreshErr := refresh(ctx, directory, groups, refreshAsOf)
	if refreshErr != nil {
		for pair := range plannedBase {
			if !refreshed[pair] {
				symbol, period, _ := strings.Cut(pair, "/")
				failPair(symbol, period, refreshErr)
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if refreshErr != nil && len(refreshed) == 0 {
		return finishedLiveAlertScan(refreshErr)
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
		l.updateCheckProgressContextLocked(ctx, "Indexing closed history", 70, strings.ToUpper(match[1])+" "+match[2])
		l.mu.Unlock()
		id := dataFileID(directory, name)
		s, e := indexAnalysisCSV(ctx, directory, name, filepath.Join(work, id+".rows"), func(float64) {})
		if e != nil {
			failures = append(failures, fmt.Errorf("index %s: %w", name, e))
			failPair(match[1], match[2], e)
			continue
		}
		s.cache = a.cache
		a.series[id] = s
		if basePairs[s.metadata.Symbol+"/"+s.metadata.Timeframe] {
			targets = append(targets, s)
		}
	}
	for i, s := range targets {
		if err = ctx.Err(); err != nil {
			return err
		}
		l.mu.Lock()
		l.updateCheckProgressContextLocked(ctx, "Checking latest closed bars", 70+30*float64(i)/float64(max(1, len(targets))), strings.ToUpper(s.metadata.Symbol)+" "+s.metadata.Timeframe)
		l.mu.Unlock()
		closed, e := s.closedCount(asOf)
		if e != nil {
			return e
		}
		if closed == 0 {
			if scheduled {
				issue := fmt.Errorf("%s %s: %w; no closed candle before %s UTC", strings.ToUpper(s.metadata.Symbol), s.metadata.Timeframe, errLiveAlertsPendingBar, time.UnixMilli(asOf).UTC().Format("2006-01-02 15:04"))
				failures = append(failures, issue)
				failPair(s.metadata.Symbol, s.metadata.Timeframe, issue)
			}
			continue
		}
		latest, e := s.row(closed - 1)
		if e != nil {
			return e
		}
		if scheduled {
			interval, _, _ := liveAlertCalendarPeriod(SavedAnalysisCondition{}, s.metadata.Timeframe)
			expected := liveAlertPeriodStart(time.UnixMilli(asOf), s.metadata.Timeframe).Add(-interval).UnixMilli()
			if latest.timestamp != expected {
				issue := fmt.Errorf("%s %s: %w; expected candle %s UTC, latest available %s UTC", strings.ToUpper(s.metadata.Symbol), s.metadata.Timeframe, errLiveAlertsPendingBar, time.UnixMilli(expected).UTC().Format("2006-01-02 15:04"), time.UnixMilli(latest.timestamp).UTC().Format("2006-01-02 15:04"))
				failures = append(failures, issue)
				failPair(s.metadata.Symbol, s.metadata.Timeframe, issue)
				continue
			}
		}
		for _, saved := range conditions {
			if !liveAlertsScopeAllows(config, saved.ID, s.metadata.Timeframe) {
				continue
			}
			if _, allowed := qualified[saved.ID][s.metadata.Symbol+"/"+s.metadata.Timeframe]; !allowed {
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
				run.processedPair(saved.ID, s.metadata.Symbol, s.metadata.Timeframe)
				continue
			}
			a.request = copyAnalysisRequest(saved.Request)
			// Base scope inherits the saved period and recorded matching assets;
			// historical categories do not change an already-qualified live pair.
			a.request.Period, a.request.Categories = "all", nil
			run.scanned(saved.ID, s.metadata.Symbol)
			matched, e := evaluateLiveAlertLatest(ctx, a, s, closed)
			if e != nil {
				failures = append(failures, fmt.Errorf("%s %s, %s: %w", strings.ToUpper(s.metadata.Symbol), s.metadata.Timeframe, saved.Name, e))
				run.skipped(saved.ID, s.metadata.Symbol, e)
				run.processedPair(saved.ID, s.metadata.Symbol, s.metadata.Timeframe)
				continue
			}
			// A matching claim commits before sending; non-match watermarks are
			// flushed together after this asset's conditions have been checked.
			var alert *LiveAlert
			if matched {
				value, e := liveAlertFromMatch(saved, s, closed-1, latest)
				if e != nil {
					failures = append(failures, e)
					run.failed(saved.ID, e)
					continue
				}
				if e = attachLiveAlertChart(ctx, a, s, closed-1, &value); e != nil {
					return e
				}
				alert = &value
			}
			claimed, claimErr := l.claimCurrentCondition(ctx, saved, s.metadata.Symbol, s.metadata.Timeframe, key, latest.timestamp, alert)
			if claimErr != nil {
				run.failed(saved.ID, claimErr)
				return claimErr
			}
			if !claimed {
				failure := fmt.Errorf("%s changed while its latest bar was being checked; the obsolete result was not claimed", saved.Name)
				failures = append(failures, failure)
				run.failed(saved.ID, failure)
				continue
			}
			if alert != nil {
				if e = l.deliver(ctx, *alert); e != nil {
					run.failed(saved.ID, e)
					if !recoverableLiveAlertDelivery(ctx, e) {
						return errors.Join(append(failures, e)...)
					}
					failures = append(failures, e)
				}
			}
			run.processedPair(saved.ID, s.metadata.Symbol, s.metadata.Timeframe)
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
			return &liveAlertPersistenceError{fmt.Errorf("save processed Live Alerts bars: %w", err)}
		}
	}
	return finishedLiveAlertScan(failures...)
}

// A saved rule can be updated while a slow refresh is in flight. Serialize its
// final scope/rule check with the durable claim, so an update before this point
// cannot publish a stale evaluation. Delivery follows the committed claim.
func (l *LiveAlerts) claimCurrentCondition(ctx context.Context, evaluated SavedAnalysisCondition, symbol, period, key string, timestamp int64, alert *LiveAlert) (bool, error) {
	l.manager.mu.Lock()
	defer l.manager.mu.Unlock()
	saved, err := l.manager.readSavedConditions()
	if err != nil {
		return false, err
	}
	currentConditions, revision, valid := l.currentLiveAlertConditions(saved, evaluated)
	if !valid {
		return false, nil
	}
	for _, current := range currentConditions {
		if current.ID != evaluated.ID {
			continue
		}
		if !sameAnalysisProfitabilityRequest(current.Request, evaluated.Request) {
			return false, nil
		}
		stats, allowed := SavedConditionProfitabilityFor(current, symbol, period)
		previousStats, previouslyAllowed := SavedConditionProfitabilityFor(evaluated, symbol, period)
		if !allowed || !previouslyAllowed || !reflect.DeepEqual(stats, previousStats) {
			return false, nil
		}
		return true, l.claimSourceRevision(ctx, "dukascopy", evaluated.ID, key, timestamp, alert, &revision)
	}
	return false, nil
}

func liveAlertGroups(directory string, config LiveAlertsConfig, conditions []SavedAnalysisCondition) ([]historyGroup, error) {
	names, err := csvNames(directory)
	if err != nil {
		return nil, err
	}
	// Build base and dependency pairs per condition. A qualifying symbol for one
	// condition cannot widen another condition's profitability or period scope.
	pairs := map[string]bool{}
	for _, saved := range conditions {
		eligible := liveConditionEligiblePairs(saved)
		found := false
		for _, name := range names {
			match := downloadFilename.FindStringSubmatch(strings.ToLower(name))
			if match == nil || !liveAlertsScopeAllows(config, saved.ID, match[2]) {
				continue
			}
			if _, allowed := eligible[match[1]+"/"+match[2]]; !allowed {
				continue
			}
			found = true
			pairs[match[1]+"/"+match[2]] = true
			for _, condition := range saved.Request.Conditions {
				for _, operand := range conditionOperands(condition) {
					if operand.Kind == "field" && operand.Timeframe != "" && operand.Timeframe != "same" {
						pairs[match[1]+"/"+operand.Timeframe] = true
					}
				}
			}
		}
		if !found {
			return nil, missingLiveConditionTargets(saved, strings.ToUpper(saved.Request.Period))
		}
	}
	// Every derived weekly history needs its retained daily source refreshed
	// first, whether w1 is a base target or an explicit operand dependency.
	for pair := range pairs {
		symbol, period, _ := strings.Cut(pair, "/")
		if period == "w1" {
			pairs[symbol+"/d1"] = true
		}
	}
	wanted := []string{}
	for _, name := range names {
		match := downloadFilename.FindStringSubmatch(strings.ToLower(name))
		if match != nil && pairs[match[1]+"/"+match[2]] {
			wanted = append(wanted, name)
		}
	}
	groups := groupHistories(directory, wanted)
	// Preserve normal ordering within each phase while ensuring that even a
	// differently sorted symbol's d1 prerequisite precedes all w1 rebuilds.
	sort.SliceStable(groups, func(i, j int) bool {
		left := downloadFilename.FindStringSubmatch(strings.ToLower(groups[i].name))
		right := downloadFilename.FindStringSubmatch(strings.ToLower(groups[j].name))
		return left != nil && right != nil && left[2] != "w1" && right[2] == "w1"
	})
	return groups, nil
}

func (l *LiveAlerts) refreshSelected(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
	return l.refreshSelectedWithFields(ctx, directory, groups, asOf, nil)
}

func (l *LiveAlerts) refreshSelectedWithFields(ctx context.Context, directory string, groups []historyGroup, asOf int64, required map[string][]string) (map[string]bool, error) {
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
	l.reportCheckPreparation(ctx, nil, "Preparing provider session")
	ctx, cleanup, err := withProviderSession(ctx, m.profile)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	l.reportCheckPreparation(ctx, nil, "Loading saved indicator recipes")
	legacy := legacyRecipes(ctx, directory)
	refreshed := map[string]bool{}
	var failures []error
	for i, group := range groups {
		if err = ctx.Err(); err != nil {
			return refreshed, err
		}
		err = nil
		l.mu.Lock()
		l.updateCheckProgressContextLocked(ctx, "Refreshing closed history and indicators", 70*float64(i)/float64(max(1, len(groups))), group.name)
		l.mu.Unlock()
		m.mu.Lock()
		m.refreshJobs[i].Status, m.refreshJobs[i].Message = "running", "Appending closed bars and updating applied indicators"
		job := m.refreshJobs[i]
		m.mu.Unlock()
		if match := downloadFilename.FindStringSubmatch(strings.ToLower(job.Name)); match != nil && match[2] == "w1" && !refreshed[match[1]+"/d1"] {
			err = errors.New("daily history did not refresh; original weekly history kept; download or refresh retained d1 data first")
		}
		if err == nil && len(group.ids) > 1 {
			job.Name, err = m.consolidateHistory(ctx, directory, job.Name, legacy, func(message string) {
				m.mu.Lock()
				m.refreshJobs[i].Message = message
				m.mu.Unlock()
				l.mu.Lock()
				l.updateCheckProgressContextLocked(ctx, message, 70*float64(i)/float64(max(1, len(groups))), job.Name)
				l.mu.Unlock()
			})
		}
		if err == nil {
			pair := ""
			if match := downloadFilename.FindStringSubmatch(strings.ToLower(job.Name)); match != nil {
				pair = match[1] + "/" + match[2]
			}
			err = m.refreshFileRequiredProgress(ctx, i, directory, job, legacy[job.Name], asOf, required[pair], func(percent float64, message string) {
				l.mu.Lock()
				defer l.mu.Unlock()
				phase := message
				if strings.Contains(strings.ToLower(phase), "recalculating") {
					phase = "Enriching indicators · " + phase
				}
				if phase == "" {
					phase = "Refreshing closed history and indicators"
				}
				l.updateCheckProgressContextLocked(ctx, fmt.Sprintf("%s · asset %d of %d", phase, i+1, len(groups)), 70*(float64(i)+math.Max(0, math.Min(100, percent))/100)/float64(max(1, len(groups))), job.Name)
			})
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
	matched, _, err := evaluateLiveAlertWindow(ctx, a, s, closed)
	if err != nil {
		return false, err
	}
	if matched && a.request.OnlyOneSignalPerDay && intradayEnrichmentPeriod(s.metadata.Timeframe) {
		latest, err := s.row(closed - 1)
		if err != nil {
			return false, err
		}
		day := analysisSignalDay(latest.timestamp)
		for _, other := range a.series {
			if other == s || other.metadata.Symbol != s.metadata.Symbol || other.metadata.Timeframe != s.metadata.Timeframe || other.last < day {
				continue
			}
			// A separately downloaded file may contain an earlier signal today.
			// Inspect only bars closed before this signal's open, with the same
			// temporal warmup; never rescan another history's entire lifetime.
			if _, err := compileAnalysisConditions(a, other); err != nil {
				continue
			}
			count, err := other.closedCount(latest.timestamp)
			if err != nil {
				return false, err
			}
			_, first, err := evaluateLiveAlertWindow(ctx, a, other, count)
			if err != nil {
				return false, err
			}
			if first >= 0 {
				row, err := other.row(first)
				if err != nil {
					return false, err
				}
				if row.timestamp >= day {
					matched = false
					break
				}
			}
		}
	}
	for _, series := range a.series {
		if err = analysisUnchanged(series); err != nil {
			return false, err
		}
	}
	return matched, nil
}

func evaluateLiveAlertWindow(ctx context.Context, a *analysisState, s *analysisSeries, closed int64) (bool, int64, error) {
	if closed <= 0 {
		return false, -1, nil
	}
	compiled, err := compileAnalysisConditions(a, s)
	if err != nil {
		return false, -1, err
	}
	warm := int64(1)
	for _, condition := range a.request.Conditions {
		if condition.Operator == "occurredWithin" || condition.Operator == "remainedFor" {
			warm = max(warm, int64(condition.Bars))
		}
	}
	start := max(int64(0), closed-1-warm)
	limitDaily := a.request.OnlyOneSignalPerDay && intradayEnrichmentPeriod(s.metadata.Timeframe)
	var day int64
	if limitDaily {
		latest, err := s.row(closed - 1)
		if err != nil {
			return false, -1, err
		}
		day = analysisSignalDay(latest.timestamp)
		// Re-evaluate today's matches rather than storing a second durable claim
		// key. This also preserves the rule across restarts and manual checks.
		lo, hi := int64(0), closed
		for lo < hi {
			mid := lo + (hi-lo)/2
			row, err := s.row(mid)
			if err != nil {
				return false, -1, err
			}
			if row.timestamp < day {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		start = max(int64(0), lo-warm)
	}
	matched, firstToday := false, int64(-1)
	for i := start; i < closed; i++ {
		if err = ctx.Err(); err != nil {
			return false, -1, err
		}
		row, e := s.row(i)
		if e != nil {
			return false, -1, e
		}
		matched = true
		for _, condition := range compiled {
			ok, e := condition.evaluate(i, analysisClosedAt(row.timestamp, s.metadata.Timeframe))
			if e != nil {
				return false, -1, e
			}
			matched = matched && ok
		}
		if limitDaily && matched && firstToday < 0 && row.timestamp >= day {
			firstToday = i
		}
	}
	return matched && (!limitDaily || firstToday == closed-1), firstToday, nil
}

func liveAlertFromMatch(saved SavedAnalysisCondition, s *analysisSeries, index int64, row analysisRow) (LiveAlert, error) {
	position, ok := s.fieldIndex("close")
	if !ok || !analysisFinite(row.values[position]) {
		return LiveAlert{}, errors.New("Live Alerts requires a finite closing price")
	}
	alert := LiveAlert{ConditionID: saved.ID, ConditionName: saved.Name, Symbol: s.metadata.Symbol, AssetName: s.metadata.Description, Timeframe: s.metadata.Timeframe, Time: row.timestamp, EntryPrice: row.values[position]}
	liveAlertAttribution(&alert, saved)
	alert.Profitability, _ = SavedConditionProfitabilityFor(saved, s.metadata.Symbol, s.metadata.Timeframe)
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
	return l.claimSource(ctx, "", "", key, timestamp, alert)
}

func (l *LiveAlerts) claimSource(ctx context.Context, source, conditionID, key string, timestamp int64, alert *LiveAlert) error {
	return l.claimSourceRevision(ctx, source, conditionID, key, timestamp, alert, nil)
}

func (l *LiveAlerts) claimSourceRevision(ctx context.Context, source, conditionID, key string, timestamp int64, alert *LiveAlert, expectedRevision *uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || !l.state.Config.Enabled || expectedRevision != nil && *expectedRevision != l.configRevision {
		return context.Canceled
	}
	if source != "" && (liveAlertSource(l.state.Config, conditionID) != source || !containsLiveValue(l.state.Config.ConditionIDs, conditionID)) {
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
	l.state.Recent = append([]LiveAlertOutcome{{AlertID: alert.AlertID, AlertName: alert.AlertName, SavedConditionID: alert.SavedConditionID, SavedConditionName: alert.SavedConditionName, ConditionID: alert.ConditionID, DataSource: alert.DataSource, Market: alert.Market, ConditionName: alert.ConditionName, Symbol: alert.Symbol, AssetName: alert.AssetName, Timeframe: alert.Timeframe, Time: alert.Time, Status: "claimed", Message: "Delivery claimed; interrupted attempts are not sent again automatically.", Profitability: copyLiveProfitability(alert.Profitability)}}, l.state.Recent...)
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
		return &liveAlertPersistenceError{fmt.Errorf("record processed Live Alerts bar: %w", err)}
	}
	l.watermarksDirty = false
	return nil
}

func (l *LiveAlerts) deliver(ctx context.Context, alert LiveAlert) error {
	l.mu.Lock()
	l.updateCheckProgressContextLocked(ctx, "Sending alert", -1, alert.Symbol+" "+alert.Timeframe)
	l.mu.Unlock()
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
	if err == nil || confirmedAlertDelivery(err) {
		l.state.Sent++
		l.recordConfirmedAlertLocked(alert.ConditionID)
	}
	for i := range l.state.Recent {
		outcome := &l.state.Recent[i]
		if outcome.ConditionID != alert.ConditionID || outcome.DataSource != alert.DataSource || outcome.Market != alert.Market || outcome.Symbol != alert.Symbol || outcome.Timeframe != alert.Timeframe || outcome.Time != alert.Time {
			continue
		}
		outcome.ConfirmedRecipients = confirmedAlertRecipientCount(err)
		outcome.Status, outcome.Message = "sent", "Telegram alert sent"
		if err != nil {
			outcome.Status, outcome.Message = "failed", liveAlertErrorText(err.Error()+". This bar will not be resent automatically; check Telegram before any manual action.")
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "uncertain") {
				outcome.Status = "uncertain"
			}
		}
		break
	}
	if saveErr := writeState(l.path, l.state); saveErr != nil {
		return &liveAlertPersistenceError{fmt.Errorf("record Live Alerts delivery result (the saved delivery claim prevents a duplicate): %w", saveErr)}
	}
	l.watermarksDirty = false
	if err != nil {
		return &liveAlertDeliveryIssue{fmt.Errorf("Telegram alert for %s, %s: %w", strings.ToUpper(alert.Symbol), alert.ConditionName, err)}
	}
	return nil
}
