package screener

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type jobOwner struct{ ID, Symbol, Timeframe, From, To string }

func ownerOf(job Job) jobOwner { return jobOwner{job.ID, job.Symbol, job.Timeframe, job.From, job.To} }

func ownedJob(directory string, job Job) bool {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	var owner jobOwner
	return readState(filepath.Join(directory, "owner.json"), 4096, &owner) == nil && owner == ownerOf(job)
}

func (m *Manager) prepareJob(job Job) (string, error) {
	directory := m.jobDirectory(job)
	if err := os.Mkdir(directory, 0700); err != nil {
		if !errors.Is(err, os.ErrExist) || !ownedJob(directory, job) {
			return "", fmt.Errorf("cannot restore owned download checkpoint: %w", err)
		}
		return directory, nil
	}
	if err := writeState(filepath.Join(directory, "owner.json"), ownerOf(job)); err != nil {
		_ = os.Remove(directory)
		return "", err
	}
	return directory, nil
}

func (m *Manager) cleanupJobAt(base string, job Job) {
	directory := filepath.Join(base, ".cryptodesk-screener-job-"+job.ID)
	if ownedJob(directory, job) {
		_ = os.RemoveAll(directory)
	}
}
