package screener

import (
	"context"
	"errors"
	"math"

	"cryptodesk/internal/exchange"
)

// Progress is transient. Queueing a row never invents an actual check attempt,
// changes its last-checked time, or replaces its previous delivery counters.
type liveAlertProgress struct {
	percent                   float64
	phase, asset, state, mode string
	revision                  uint64
	started                   bool
}

func (l *LiveAlerts) queueCheckProgressLocked(ids []string, manual bool) {
	if l.checkProgress == nil {
		l.checkProgress = map[string]liveAlertProgress{}
	}
	known := map[string]bool{}
	if l.state.Config.Alerts != nil {
		for _, entry := range l.state.Config.Alerts {
			known[entry.ID] = true
		}
	} else {
		for _, id := range l.state.Config.ConditionIDs {
			known[id] = true
		}
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	for id, progress := range l.checkProgress {
		if !known[id] {
			delete(l.checkProgress, id)
			continue
		}
		if progress.state == "queued" && !selected[id] {
			progress.state, progress.phase = "skipped", "Not selected for this check"
			l.checkProgress[id] = progress
		}
	}
	mode := "scheduled"
	if manual {
		mode = "manual"
	}
	for _, id := range ids {
		if known[id] {
			l.checkProgress[id] = liveAlertProgress{state: "queued", phase: "Queued", mode: mode, revision: l.configRevision}
		}
	}
}

func (l *LiveAlerts) startCheckProgressLocked(id string) {
	progress, ok := l.checkProgress[id]
	if !ok {
		return
	}
	progress.state, progress.phase = "running", "Preparing alert"
	progress.revision = l.configRevision
	l.checkProgress[id] = progress
	l.activeProgressID = id
	l.progress, l.currentAsset = 0, ""
}

// A negative percent changes only the phase/asset, for work such as a Telegram
// send whose byte or recipient progress cannot be measured by this owner.
func (l *LiveAlerts) updateCheckProgressLocked(phase string, percent float64, asset string) {
	if l.activeProgressID != "" {
		progress, ok := l.checkProgress[l.activeProgressID]
		if !ok || progress.state != "running" || progress.revision != l.configRevision {
			return
		}
		if percent >= 0 {
			progress.percent = math.Max(progress.percent, math.Min(100, percent))
		}
		progress.phase, progress.asset = phase, asset
		l.checkProgress[l.activeProgressID] = progress
		l.progress = progress.percent
	} else if percent >= 0 {
		l.progress = math.Min(100, math.Max(0, percent))
	}
	l.status, l.currentAsset = phase, asset
}

func (l *LiveAlerts) finishCheckProgressLocked(id string, err error, asOf int64) {
	progress, ok := l.checkProgress[id]
	if !ok {
		return
	}
	switch {
	case progress.revision != l.configRevision:
		progress.state, progress.phase = "cancelled", "Cancelled"
	case completedLiveAlertScan(err):
		progress.state, progress.phase, progress.percent = "completed", "Completed with errors", 100
		if check, ok := l.state.Checks[id]; ok && check.Error == "" {
			progress.phase = "Completed"
		}
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		progress.state, progress.phase = "cancelled", "Cancelled"
	case errors.Is(err, errLiveAlertsBusy):
		progress.state, progress.phase = "queued", "Waiting for other Screener work"
	case errors.Is(err, errLiveAlertsPendingBar) && l.now().UnixMilli()-asOf < liveAlertPublicationWindow.Milliseconds():
		progress.state, progress.phase = "queued", "Waiting for the newly closed candle"
	case err != nil:
		progress.state, progress.phase = "failed", "Failed"
	case !progress.started:
		progress.state, progress.phase, progress.percent = "skipped", "No eligible closed bar", 0
	default:
		progress.state, progress.phase, progress.percent = "completed", "Completed", 100
	}
	progress.asset = ""
	l.checkProgress[id] = progress
	if l.activeProgressID == id {
		l.activeProgressID = ""
	}
}

func (l *LiveAlerts) cancelCheckProgressLocked(phase string) {
	for id, progress := range l.checkProgress {
		if progress.state != "queued" && progress.state != "running" {
			continue
		}
		if progress.state == "queued" && !progress.started {
			progress.phase = phase + " before starting"
		} else {
			progress.phase = phase
		}
		progress.state = "cancelled"
		progress.asset = ""
		l.checkProgress[id] = progress
	}
}

func (l *LiveAlerts) finishQueuedProgressLocked(err error) {
	for id, progress := range l.checkProgress {
		if progress.state != "queued" {
			continue
		}
		switch {
		case progress.revision != l.configRevision || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			progress.state, progress.phase = "cancelled", "Cancelled before starting"
		case errors.Is(err, errLiveAlertsBusy):
			progress.phase = "Waiting to retry check"
		case errors.Is(err, errLiveAlertsPendingBar) && progress.started:
			// This row actually ran and is awaiting provider publication. Its
			// retry state must not be mistaken for an unstarted queue entry.
		case err != nil:
			progress.state, progress.phase = "skipped", "Check did not start"
		default:
			progress.state, progress.phase = "skipped", "No eligible closed bar"
		}
		l.checkProgress[id] = progress
	}
}

func copyLiveAlertProgress(values map[string]liveAlertProgress) map[string]liveAlertProgress {
	copy := make(map[string]liveAlertProgress, len(values))
	for id, value := range values {
		copy[id] = value
	}
	return copy
}

func applyLiveAlertProgress(rows []LiveAlertCheck, values map[string]liveAlertProgress) {
	for i := range rows {
		if progress, ok := values[rows[i].ConditionID]; ok {
			rows[i].Progress, rows[i].Phase, rows[i].CurrentAsset = progress.percent, progress.phase, progress.asset
			rows[i].ProgressState, rows[i].ProgressMode = progress.state, progress.mode
		}
	}
}

func publicAlertProviderRefused(err error) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*exchange.Error); ok {
		switch e.Kind {
		case "restricted", "rateLimited", "auth", "unauthorized", "authentication":
			return true
		}
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if publicAlertProviderRefused(child) {
				return true
			}
		}
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return publicAlertProviderRefused(wrapped.Unwrap())
	}
	return false
}
