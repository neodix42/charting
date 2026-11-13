package screener

import (
	"context"
	"cryptodesk/internal/exchange"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

type liveAlertWork struct {
	condition int
	source    string
	period    string
	duration  int64
}

func liveAlertPriorityWork(selected []SavedAnalysisCondition, config LiveAlertsConfig, asOf int64, scheduled bool) []liveAlertWork {
	var work []liveAlertWork
	for i, saved := range selected {
		source := liveAlertSource(config, saved.ID)
		periods := savedLiveAlertPeriods(saved)
		if source == "dukascopy" {
			seen := map[string]bool{}
			periods = nil
			for pair := range liveConditionEligiblePairs(saved) {
				_, period, _ := strings.Cut(pair, "/")
				if !seen[period] {
					seen[period] = true
					periods = append(periods, period)
				}
			}
			sort.Strings(periods)
		}
		before := len(work)
		for _, period := range periods {
			if liveAlertsScopeAllows(config, saved.ID, period) && (!scheduled || liveAlertConditionDueAt(saved, period, time.UnixMilli(asOf))) {
				work = append(work, liveAlertWork{condition: i, source: source, period: period, duration: exchange.TimeframeSeconds(publicAlertIntervals[period])})
			}
		}
		if len(work) == before {
			// Retain the existing no-eligible-assets diagnostic for manual checks.
			work = append(work, liveAlertWork{condition: i, source: source})
		}
	}
	return work
}

// Each source advances to its next timeframe only after every current-tier
// task finishes. Other sources can keep using the global bounded worker pool.
func runPriorityAlertWork(parent context.Context, work []liveAlertWork, run func(context.Context, liveAlertWork) error, start func(liveAlertWork), finish func(liveAlertWork, error), wait func(liveAlertWork, string)) []error {
	work = append([]liveAlertWork(nil), work...)
	sort.SliceStable(work, func(i, j int) bool { return work[i].duration > work[j].duration })
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	type result struct {
		index int
		err   error
	}
	completed := make(chan result, len(work))
	pending := make([]int, len(work))
	for i := range pending {
		pending[i] = i
	}
	active, counts := 0, map[string]int{}
	tiers := map[string]int64{}
	failures := make([]error, len(work))
	var workers sync.WaitGroup
	for len(pending) > 0 || active > 0 {
		launched := false
		if ctx.Err() == nil && active < liveAlertCheckConcurrency {
			for at, index := range pending {
				task := work[index]
				if counts[task.source] > 0 && tiers[task.source] != task.duration {
					if wait != nil {
						wait(task, "Waiting for higher-timeframe "+task.source+" alerts")
					}
					continue
				}
				if task.source == "dukascopy" && counts[task.source] > 0 {
					if wait != nil {
						wait(task, "Waiting for the active Dukascopy refresh")
					}
					continue
				}
				pending = append(pending[:at], pending[at+1:]...)
				active++
				counts[task.source]++
				tiers[task.source] = task.duration
				if start != nil {
					start(task)
				}
				workers.Add(1)
				go func() {
					defer workers.Done()
					err := run(ctx, task)
					if fatalLiveAlertPersistence(err) || errors.Is(err, errLiveAlertsBusy) {
						cancel()
					}
					completed <- result{index, err}
				}()
				launched = true
				break
			}
		}
		if launched {
			continue
		}
		if active == 0 {
			break
		}
		result := <-completed
		task := work[result.index]
		failures[result.index] = result.err
		counts[task.source]--
		active--
		if finish != nil {
			finish(task, result.err)
		}
	}
	workers.Wait()
	if ctx.Err() != nil {
		failures = append(failures, ctx.Err())
	}
	return failures
}

type liveAlertHistoryGroupKey struct{}
type liveAlertTriggerKey struct{}

func (l *LiveAlerts) checkTriggerTime(ctx context.Context) time.Time {
	if trigger, ok := ctx.Value(liveAlertTriggerKey{}).(time.Time); ok && !trigger.IsZero() {
		return trigger
	}
	return l.now()
}

type liveAlertHistoryGroup struct {
	once sync.Once
	run  *liveAlertCheckRun
	err  error
}

func (l *LiveAlerts) beginScopedCheckHistory(ctx context.Context, config LiveAlertsConfig, manual bool) (*liveAlertCheckRun, error) {
	group, _ := ctx.Value(liveAlertHistoryGroupKey{}).(*liveAlertHistoryGroup)
	if group == nil {
		return l.beginCheckHistory(config, manual, l.checkTriggerTime(ctx))
	}
	group.once.Do(func() { group.run, group.err = l.beginCheckHistory(config, manual, l.checkTriggerTime(ctx)) })
	if group.err != nil {
		return nil, group.err
	}
	child := *group.run
	child.borrowed = true
	return &child, nil
}
