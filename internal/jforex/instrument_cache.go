package jforex

import (
	"bytes"
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const instrumentCatalogMaxBytes = 16 * 1024 * 1024
const instrumentCatalogFreshFor = time.Hour

type savedInstrumentCatalog struct {
	Version     int                 `json:"version"`
	Account     string              `json:"account"`
	Environment string              `json:"environment"`
	Capability  string              `json:"capability"`
	FetchedAt   int64               `json:"fetchedAt"`
	Items       []domain.Instrument `json:"items"`
}

type instrumentCatalogFlight struct {
	config configuration
	done   chan struct{}
	items  []domain.Instrument
	err    error
}

func cloneInstruments(items []domain.Instrument) []domain.Instrument {
	result := append([]domain.Instrument(nil), items...)
	for i := range result {
		result[i].SupportedIntervals = append([]string(nil), items[i].SupportedIntervals...)
		if items[i].ProviderMetadata != nil {
			result[i].ProviderMetadata = map[string]string{}
			for key, value := range items[i].ProviderMetadata {
				result[i].ProviderMetadata[key] = value
			}
		}
	}
	return result
}

func (m *Manager) loadInstrumentCatalogLocked() error {
	if m.closed {
		return errors.New("The Dukascopy service is closed")
	}
	if m.loadError != nil {
		return m.loadError
	}
	if m.config.Username == "" || m.config.Password == "" {
		return errors.New("Save JForex credentials in Settings → Dukascopy")
	}
	if m.catalogLoaded {
		return m.catalogError
	}
	m.catalogLoaded = true
	path := filepath.Join(m.cachePath(m.config), "instruments.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	invalid := errors.New("Could not read the saved JForex instrument list; original file preserved. Use Retry discovery to download it again")
	if err != nil || !info.Mode().IsRegular() || info.Size() > instrumentCatalogMaxBytes {
		m.catalogError = invalid
		return invalid
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > instrumentCatalogMaxBytes {
		m.catalogError = invalid
		return invalid
	}
	var snapshot savedInstrumentCatalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil || decoder.Decode(new(any)) != io.EOF || snapshot.Version != 1 || snapshot.Account != filepath.Base(m.cachePath(m.config)) || snapshot.Environment != m.config.Environment || snapshot.Capability != bridgeInstrumentCatalog || snapshot.FetchedAt <= 0 || snapshot.FetchedAt > time.Now().Add(5*time.Minute).UnixMilli() || validateInstrumentCatalog(snapshot.Items) != nil {
		m.catalogError = invalid
		return invalid
	}
	m.catalog = &snapshot
	m.instrumentCount = len(snapshot.Items)
	return nil
}

// Selection checks freshness without holding up the picker or chart attachment.
// A failed refresh leaves the previous snapshot intact. Concurrent selections
// share one operation owned by the manager, so one caller cannot cancel peers.
func (m *Manager) RefreshInstruments(ctx context.Context, force bool) ([]domain.Instrument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	loadErr := m.loadInstrumentCatalogLocked()
	if m.closed || m.loadError != nil || m.config.Username == "" || m.config.Password == "" || loadErr != nil && !force {
		m.mu.Unlock()
		return nil, loadErr
	}
	if !force && m.catalog != nil && time.Since(time.UnixMilli(m.catalog.FetchedAt)) < instrumentCatalogFreshFor {
		items := cloneInstruments(m.catalog.Items)
		m.mu.Unlock()
		return items, nil
	}
	if !force && m.catalogCheckError != nil && time.Since(m.catalogCheckedAt) < 5*time.Minute {
		err := m.catalogCheckError
		m.mu.Unlock()
		return nil, err
	}
	flight := m.catalogFlight
	if flight == nil || flight.config != m.config {
		flight = &instrumentCatalogFlight{config: m.config, done: make(chan struct{})}
		m.catalogFlight = flight
		m.wg.Add(1)
		go m.refreshInstrumentCatalog(flight)
	}
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-flight.done:
		return cloneInstruments(flight.items), flight.err
	}
}

func (m *Manager) refreshInstrumentCatalog(flight *instrumentCatalogFlight) {
	defer m.wg.Done()
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Minute)
	defer cancel()
	items, err := m.fetchInstruments(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.config != flight.config {
		err = errors.New("JForex account changed; retry instrument discovery")
	}
	if err == nil {
		snapshot := savedInstrumentCatalog{Version: 1, Account: filepath.Base(m.cachePath(flight.config)), Environment: flight.config.Environment, Capability: bridgeInstrumentCatalog, FetchedAt: time.Now().UnixMilli(), Items: cloneInstruments(items)}
		err = m.writeInstrumentCatalogLocked(snapshot)
		if err == nil {
			m.catalog, m.catalogError, m.catalogLoaded = &snapshot, nil, true
		}
	}
	if err == nil {
		flight.items = items
	} else if m.config == flight.config && !m.closed {
		m.lastError = err.Error()
	}
	flight.err = err
	if m.config == flight.config && !m.closed {
		m.catalogCheckedAt, m.catalogCheckError = time.Now(), err
	}
	if m.catalogFlight == flight {
		m.catalogFlight = nil
	}
	close(flight.done)
}

func (m *Manager) writeInstrumentCatalogLocked(snapshot savedInstrumentCatalog) error {
	path := filepath.Join(m.cachePath(m.config), "instruments.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("Saved JForex instrument list must be a regular file; original preserved")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(snapshot)
	if err != nil || len(data) > instrumentCatalogMaxBytes {
		return errors.New("JForex instrument list exceeds the saved catalog limit")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".instruments-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = file.Chmod(0600); err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if m.catalogError != nil {
		// An explicit retry can repair a damaged derived cache while retaining
		// the original bytes for inspection/recovery.
		if _, err := os.Lstat(path); err == nil {
			backup := fmt.Sprintf("%s.invalid-%d", path, time.Now().UnixNano())
			if err := os.Rename(path, backup); err != nil {
				return err
			}
		}
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	if directory, err := os.Open(filepath.Dir(path)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
