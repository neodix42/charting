package screener

import "errors"

// A Statistics view retains the complete accepted batch identity. It must not
// mistake a replacement queue for successful completion or cancel its work.
func (m *Manager) checkDownloadBatchLocked(ids []string) error {
	if len(ids) == 0 || len(ids) != len(m.jobs) {
		return errors.New("the Statistics download batch is no longer available; no other download was changed")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			return errors.New("invalid Statistics download batch identity")
		}
		seen[id] = true
	}
	for _, job := range m.jobs {
		if !seen[job.ID] {
			return errors.New("the Statistics download batch was replaced; no other download was changed")
		}
	}
	return nil
}

func (m *Manager) DownloadBatch(ids []string) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkDownloadBatchLocked(ids); err != nil {
		return Info{}, err
	}
	return m.infoLocked(), nil
}

func (m *Manager) CancelDownloadBatch(ids []string) (Info, error) {
	m.mu.Lock()
	if err := m.checkDownloadBatchLocked(ids); err != nil {
		m.mu.Unlock()
		return Info{}, err
	}
	m.cancelLocked()
	m.saveDownloadsLocked()
	done := m.batchDone
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkDownloadBatchLocked(ids); err != nil {
		return Info{}, err
	}
	if m.saveDownloadsLocked() {
		for _, job := range m.jobs {
			if job.Status == "cancelled" {
				m.cleanupJobAt(m.batchDirectory, job)
			}
		}
	}
	return m.infoLocked(), nil
}
