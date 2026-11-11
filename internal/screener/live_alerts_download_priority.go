package screener

import (
	"context"
	"errors"
	"sync"
)

// Reserve before cancelling download workers so a new batch cannot take the
// gap. Their normal cancellation path retains queued jobs and checkpoints.
func (m *Manager) reserveAlertPriority(parent context.Context, asOf int64) (context.Context, func(), error) {
	if err := parent.Err(); err != nil {
		return parent, nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return parent, nil, errors.New("screener is closed")
	}
	if m.enrichmentActive || m.refreshActive || m.analysisActiveLocked() || m.liveActive || m.active && (m.cancel == nil || m.batchDone == nil) {
		m.mu.Unlock()
		return parent, nil, errLiveAlertsBusy
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	resume := m.active
	var downloadDone chan struct{}
	m.liveActive, m.liveCancel = true, cancel
	m.refreshActive, m.refreshCancel, m.refreshDone, m.refreshAsOf = true, cancel, done, asOf
	m.downloadsPaused = resume
	m.wg.Add(1)
	if resume {
		downloadDone = m.batchDone
		m.cancel()
	}
	m.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			if downloadDone != nil {
				<-downloadDone
			}
			m.mu.Lock()
			m.liveActive, m.liveCancel = false, nil
			m.refreshActive, m.refreshCancel, m.downloadsPaused = false, nil, false
			close(done)
			if resume && !m.closed && !m.active && m.pendingLocked() {
				if err := m.resumeAfterAlertsLocked(); err != nil {
					m.lastError = "Resume downloads after Live Alerts: " + err.Error()
				}
			}
			m.mu.Unlock()
			m.wg.Done()
		})
	}
	if downloadDone != nil {
		select {
		case <-downloadDone:
		case <-ctx.Done():
			release()
			return ctx, nil, ctx.Err()
		}
		m.mu.Lock()
		for i := range m.jobs {
			if m.jobs[i].Status == "queued" {
				m.jobs[i].Message = "Paused for Live Alerts; resumes after checks finish"
			}
		}
		saved := m.saveDownloadsLocked()
		problem := m.stateError
		m.mu.Unlock()
		if !saved {
			release()
			return ctx, nil, errors.New("Pause downloads for Live Alerts: " + problem)
		}
	}
	if err := ctx.Err(); err != nil {
		release()
		return ctx, nil, err
	}
	return ctx, release, nil
}

func (m *Manager) resumeAfterAlertsLocked() error {
	if m.batchDirectory != "" {
		if err := recoverHistoryPublications(m.batchDirectory); err != nil {
			return err
		}
		m.restorePublishedDownloads()
		if err := m.reconcileCompletedHistoryPaths(m.batchDirectory); err != nil {
			return err
		}
	}
	if !m.saveDownloadsLocked() {
		return errors.New(m.stateError)
	}
	if m.pendingLocked() {
		// Deliberately do not retry failed jobs or re-create cancelled jobs.
		m.beginLocked()
	}
	return nil
}
