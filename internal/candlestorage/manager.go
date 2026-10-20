// Package candlestorage owns machine-local cache preferences and serializes disk
// work. It is independent of workspace import/export, Wails and market adapters.
package candlestorage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"cryptodesk/internal/candlecache"
	"cryptodesk/internal/domain"
)

type Info struct {
	Enabled   bool   `json:"enabled"`
	Directory string `json:"directory"`
	SizeBytes int64  `json:"sizeBytes"`
	Error     string `json:"error,omitempty"`
}
type preferences struct {
	Enabled   bool   `json:"enabled"`
	Directory string `json:"directory"`
}
type operation func()

// Manager's bounded queue applies backpressure rather than losing accepted bars.
// Its worker alone owns the database and drains all pending writes before Close.
type Manager struct {
	mu        sync.Mutex
	closed    bool
	active    atomic.Bool
	jobs      chan operation
	done      chan struct{}
	store     *candlecache.Store
	prefs     preferences
	path      string
	lastError string
	onError   func(string)
}

func New(profile string, onError func(string)) *Manager {
	m := &Manager{jobs: make(chan operation, 128), done: make(chan struct{}), path: filepath.Join(profile, "candle-storage.json"), prefs: preferences{Directory: filepath.Join(profile, "candles")}, onError: onError}
	data, err := os.ReadFile(m.path)
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var p preferences
		err = decoder.Decode(&p)
		if err == nil && decoder.Decode(new(any)) != io.EOF {
			err = errors.New("unexpected trailing preferences data")
		}
		if err == nil {
			p.Directory, err = candlecache.NormalizeDirectory(p.Directory)
		}
		if err == nil {
			m.prefs = p
		}
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err == nil && m.prefs.Enabled {
		m.store, err = candlecache.Open(m.prefs.Directory)
	}
	if err != nil {
		m.lastError = "Candle storage: " + err.Error()
	}
	m.active.Store(m.store != nil)
	go func() {
		defer close(m.done)
		for job := range m.jobs {
			job()
		}
	}()
	return m
}

func (m *Manager) submit(job operation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return candlecache.ErrClosed
	}
	m.jobs <- job
	return nil
}
func (m *Manager) failure(err error) {
	if err == nil {
		return
	}
	message := "Candle storage: " + err.Error()
	changed := m.lastError != message
	m.lastError = message
	if changed && m.onError != nil {
		m.onError(message)
	}
}
func (m *Manager) info() Info {
	info := Info{Enabled: m.prefs.Enabled, Directory: m.prefs.Directory, Error: m.lastError}
	var err error
	info.SizeBytes, err = candlecache.SizeBytes(info.Directory)
	if err != nil {
		info.Error = "Measure candle folder: " + err.Error()
	}
	return info
}
func (m *Manager) Info() (Info, error) {
	result := make(chan Info, 1)
	if err := m.submit(func() { result <- m.info() }); err != nil {
		return Info{}, err
	}
	return <-result, nil
}

// Configure opens the destination and durably commits preferences before closing
// the previous cache. Existing data is never migrated or deleted implicitly.
func (m *Manager) Configure(enabled bool, directory string) (Info, error) {
	directory, err := candlecache.NormalizeDirectory(directory)
	if err != nil {
		return Info{}, err
	}
	type response struct {
		info Info
		err  error
	}
	result := make(chan response, 1)
	err = m.submit(func() {
		p := preferences{Enabled: enabled, Directory: directory}
		current := m.store
		next := current
		var configErr error
		if !enabled {
			next = nil
		} else if current == nil || directory != m.prefs.Directory {
			next, configErr = candlecache.Open(directory)
		}
		if configErr == nil {
			configErr = savePreferences(m.path, p)
		}
		if configErr != nil {
			if next != nil && next != current {
				_ = next.Close()
			}
			result <- response{err: configErr}
			return
		}
		m.prefs, m.store, m.lastError = p, next, ""
		m.active.Store(next != nil)
		if current != nil && current != next {
			m.failure(current.Close())
		}
		result <- response{info: m.info()}
	})
	if err != nil {
		return Info{}, err
	}
	r := <-result
	return r.info, r.err
}

func savePreferences(path string, p preferences) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".candle-storage-*")
	if err != nil {
		return fmt.Errorf("save candle preferences: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	return err
}

func (m *Manager) Put(key domain.FeedKey, bars []domain.Candle) {
	if len(bars) == 0 || !m.active.Load() {
		return
	}
	copyBars := append([]domain.Candle(nil), bars...)
	_ = m.submit(func() {
		if m.store != nil {
			m.failure(m.store.Upsert(key, copyBars))
		}
	})
}
func (m *Manager) Range(key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	type response struct {
		bars []domain.Candle
		err  error
	}
	result := make(chan response, 1)
	if err := m.submit(func() {
		if m.store == nil {
			result <- response{}
			return
		}
		bars, err := m.store.Range(key, from, to, limit)
		m.failure(err)
		result <- response{bars, err}
	}); err != nil {
		return nil, err
	}
	r := <-result
	return r.bars, r.err
}
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		<-m.done
		return nil
	}
	m.closed = true
	m.active.Store(false)
	result := make(chan error, 1)
	m.jobs <- func() {
		if m.store != nil {
			result <- m.store.Close()
		} else {
			result <- nil
		}
	}
	close(m.jobs)
	m.mu.Unlock()
	err := <-result
	<-m.done
	return err
}
