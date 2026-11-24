package screener

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Failed jobs retain every worker's durable recovery state. An interrupted
// public-to-SDK transition can leave only its archive or unexplained partial;
// keep those bytes rather than treating absence of a public checkpoint as proof
// that the owned directory is disposable. Completed/cancelled replacement work
// still uses cleanupJobAt directly.
func retainedDownloadWork(directory string) bool {
	for _, name := range []string{"checkpoint.json", "ranges.json", "synthetic-checkpoint.json", "sdk-checkpoint.json", "public-sdk-upgrade.json"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	if info, err := os.Lstat(filepath.Join(directory, "data.part")); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 0 {
			return true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return true
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return !errors.Is(err, os.ErrNotExist)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".public-partial-") {
			return true
		}
	}
	return false
}
