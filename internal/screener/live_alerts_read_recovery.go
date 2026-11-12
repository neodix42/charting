package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"errors"
	"fmt"
	"net"
	"time"
)

// Only failed public data reads receive this marker. Delivery errors retain
// their existing durable no-resend handling, even if their cause is a timeout.
type liveAlertReadIssue struct{ error }

func (e *liveAlertReadIssue) Unwrap() error { return e.error }

func liveAlertReadFailure(err error) error {
	var existing *liveAlertReadIssue
	if errors.As(err, &existing) {
		return err
	}
	if exchange.RetryableReadError(err) {
		return &liveAlertReadIssue{err}
	}
	return err
}

type liveAlertWorkIssue struct {
	condition, period string
	error
}

func (e *liveAlertWorkIssue) Unwrap() error { return e.error }

func liveAlertRetryWindow(err error, period string) time.Duration {
	var read *liveAlertReadIssue
	if errors.As(err, &read) {
		if period == "d1" || period == "w1" {
			return 24 * time.Hour
		}
		return time.Hour
	}
	if errors.Is(err, errLiveAlertsPendingBar) {
		if period == "d1" || period == "w1" {
			return 24 * time.Hour
		}
		return liveAlertPublicationWindow
	}
	return 0
}

func visitLiveAlertWorkIssues(err error, visit func(*liveAlertWorkIssue)) {
	if err == nil {
		return
	}
	if work, ok := err.(*liveAlertWorkIssue); ok {
		visit(work)
		return
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			visitLiveAlertWorkIssues(child, visit)
		}
	} else if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		visitLiveAlertWorkIssues(wrapped.Unwrap(), visit)
	}
}

func retryLiveAlertConfig(config LiveAlertsConfig, err error, asOf int64, now time.Time, manual bool) (LiveAlertsConfig, bool) {
	if fatalLiveAlertPersistence(err) {
		return LiveAlertsConfig{}, false
	}
	if errors.Is(err, errLiveAlertsBusy) {
		return copyLiveAlertsConfig(config), true
	}
	ids := []string{}
	scope := []LiveAlertConditionPeriod{}
	seen := map[string]bool{}
	visitLiveAlertWorkIssues(err, func(work *liveAlertWorkIssue) {
		window := liveAlertRetryWindow(work.error, work.period)
		base := time.UnixMilli(asOf)
		if manual {
			// A manual row retry must not hold unrelated automatic schedules
			// for the full daily recovery window.
			window = min(window, liveAlertPublicationWindow)
		} else if work.period == "d1" || work.period == "w1" {
			base = liveAlertPeriodStart(base, work.period)
		}
		key := work.condition + "/" + work.period
		if window == 0 || now.Sub(base) >= window || seen[key] {
			return
		}
		seen[key] = true
		scope = append(scope, LiveAlertConditionPeriod{ConditionID: work.condition, Period: work.period})
		if !containsLiveValue(ids, work.condition) {
			ids = append(ids, work.condition)
		}
	})
	if len(ids) == 0 {
		return LiveAlertsConfig{}, false
	}
	retry := subsetLiveAlertsConfig(config, ids)
	retry.scope = scope
	return retry, true
}

func liveAlertReadRetryAt(err error) int64 {
	next := exchange.RetryAt(err)
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			next = max(next, liveAlertReadRetryAt(child))
		}
	} else if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		next = max(next, liveAlertReadRetryAt(wrapped.Unwrap()))
	}
	return next
}

func (d *publicAlertData) readProblem(source string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if problem := d.readProblems[source]; problem.until.After(d.readTimeLocked()) {
		return problem.err
	}
	return nil
}

type publicAlertReadProblem struct {
	err   error
	until time.Time
}

func (d *publicAlertData) recordReadProblem(source string, err error) error {
	if !exchange.RetryableReadError(err) {
		return err
	}
	message := "Temporary " + source + " network/provider failure; the scheduled candle check will retry automatically"
	var dns *net.DNSError
	if errors.As(err, &dns) {
		message = source + " API hostname could not be resolved by system DNS; waiting to retry the scheduled candle check"
	}
	issue := liveAlertReadFailure(fmt.Errorf("%s: %w", message, err))
	// One illiquid market's request timeout must not suppress healthy peers.
	// DNS/dial failures and the provider-wide rate budget affect the source.
	var dial *net.OpError
	if !errors.As(err, &dns) && !(errors.As(err, &dial) && dial.Op == "dial") && exchange.ErrorKind(err) != "rateLimited" {
		return issue
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	until := d.readTimeLocked().Add(liveAlertRetryDelay)
	if retry := time.Unix(liveAlertReadRetryAt(err), 0); retry.After(until) {
		until = retry
	}
	if d.readProblems == nil {
		d.readProblems = map[string]publicAlertReadProblem{}
	}
	if previous := d.readProblems[source]; previous.until.After(until) {
		return previous.err
	}
	d.readProblems[source] = publicAlertReadProblem{err: issue, until: until}
	return issue
}

func (d *publicAlertData) readTimeLocked() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

// Circuit ownership is per source and shared by all alert workers. A failed
// DNS lookup must not cause hundreds of further lookups for the same host.
type publicAlertReadAdapter struct {
	exchange.Adapter
	data   *publicAlertData
	source string
}

func (a publicAlertReadAdapter) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if err := a.data.readProblem(a.source); err != nil {
		return nil, err
	}
	bars, err := a.Adapter.History(ctx, key, from, to, limit)
	if err != nil && ctx.Err() == nil {
		err = a.data.recordReadProblem(a.source, err)
	}
	return bars, err
}

func (l *LiveAlerts) markReadRetryProgressLocked(config LiveAlertsConfig, err error) {
	if errors.Is(err, errLiveAlertsBusy) {
		return
	}
	phase := "Waiting for the newly closed candle; automatic retry scheduled"
	var read *liveAlertReadIssue
	if errors.As(err, &read) {
		phase = "Waiting for network/provider recovery; automatic retry scheduled"
	}
	for _, id := range config.ConditionIDs {
		progress, ok := l.checkProgress[id]
		if ok && progress.revision == l.configRevision {
			progress.state, progress.phase, progress.asset = "queued", phase, ""
			l.checkProgress[id] = progress
		}
	}
}

func (l *LiveAlerts) completedPublicAlertAssets(assets []publicAlertAsset, conditions []SavedAnalysisCondition, periods map[string][]string, source string, asOf int64, processed map[string]bool) map[string]bool {
	allowed := map[string]map[LiveAlertAssetRef]bool{}
	for _, saved := range conditions {
		allowed[saved.ID] = liveAlertSelectedAssets(saved, source)
	}
	completed := map[string]bool{}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, asset := range assets {
		all, any := true, false
		for _, saved := range conditions {
			if selection := allowed[saved.ID]; selection != nil && !selection[LiveAlertAssetRef{Market: asset.Market, Symbol: asset.NativeID}] {
				continue
			}
			for _, period := range periods[saved.ID] {
				any = true
				interval := exchange.TimeframeSeconds(publicAlertIntervals[period])
				expected := liveAlertPeriodStart(time.UnixMilli(asOf), period).UnixMilli() - interval*1000
				key := livePublicClaimKey(saved.ID, source, asset.Symbol, period)
				watermark, known := l.state.Watermarks[key]
				processed[key] = known && watermark >= expected
				all = all && processed[key]
			}
		}
		completed[asset.Symbol] = any && all
	}
	return completed
}
