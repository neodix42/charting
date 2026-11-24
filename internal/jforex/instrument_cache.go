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
	"strconv"
	"strings"
	"time"
)

const instrumentCatalogMaxBytes = 16 * 1024 * 1024
const instrumentCatalogFreshFor = time.Hour
const instrumentMarketHoursVersion = 2

func instrumentCatalogFresh(snapshot *savedInstrumentCatalog, now time.Time) bool {
	if snapshot == nil || snapshot.MarketHoursVersion != instrumentMarketHoursVersion {
		return false
	}
	freshFor := instrumentCatalogFreshFor
	available, unavailable := false, false
	needsCalendar, retained := false, false
	for _, item := range snapshot.Items {
		metadata := item.ProviderMetadata
		available = available || metadata["marketHoursStatus"] == "available"
		unavailable = unavailable || metadata["marketHoursStatus"] == "unavailable"
		retained = retained || metadata["marketHoursRetained"] == "true"
		if metadata["marketHoursSource"] == "JForex regular market session" {
			open, close := metadata["marketHoursOpen"], metadata["marketHoursClose"]
			first, last := metadata["marketHoursFirstDay"], metadata["marketHoursLastDay"]
			needsCalendar = needsCalendar || open != "" && close != "" && open >= close || first != "" && last != "" && first > last
		}
		if metadata["marketHoursSource"] == "JForex instrument calendar" {
			until, err := strconv.ParseInt(metadata["marketHoursTo"], 10, 64)
			if err != nil || until <= now.UnixMilli() {
				return false
			}
		}
	}
	// A cold SDK may still be filling some market records/calendars after the
	// bounded readiness wait. Keep the saved list usable, but retry partial
	// schedules sooner; a fully cold catalog gets an even shorter grace period.
	if unavailable || needsCalendar || retained {
		freshFor = 5 * time.Minute
	}
	if unavailable && !available {
		freshFor = time.Minute
	}
	return now.Sub(time.UnixMilli(snapshot.FetchedAt)) < freshFor
}

type savedInstrumentCatalog struct {
	Version            int                 `json:"version"`
	Account            string              `json:"account"`
	Environment        string              `json:"environment"`
	Capability         string              `json:"capability"`
	FetchedAt          int64               `json:"fetchedAt"`
	MarketHoursVersion int                 `json:"marketHoursVersion,omitempty"`
	Items              []domain.Instrument `json:"items"`
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

// A cold SDK refresh can return a valid catalog before its session metadata is
// ready. Keep complete last-known hours for matching instruments in this account,
// while all identities and non-hours fields still come from the fresh catalog.
func mergeInstrumentCatalogHours(previous *savedInstrumentCatalog, fresh []domain.Instrument, account, environment string, now time.Time) []domain.Instrument {
	result := cloneInstruments(fresh)
	older := map[string]domain.Instrument{}
	if previous != nil && previous.Version == 1 && previous.Account == account && previous.Environment == environment && previous.Capability == bridgeInstrumentCatalog {
		for _, item := range previous.Items {
			older[item.NativeID+"\x00"+item.Market] = item
		}
	}
	for index := range result {
		item := &result[index]
		metadata := item.ProviderMetadata
		if metadata != nil {
			delete(metadata, "marketHoursRetained")
			delete(metadata, "marketHoursNotice")
			delete(metadata, "marketHoursFetchedAt")
		}
		if usableInstrumentMarketHours(metadata, now) {
			metadata["marketHoursFetchedAt"] = strconv.FormatInt(now.UnixMilli(), 10)
			continue
		}
		old, found := older[item.NativeID+"\x00"+item.Market]
		if !found || !usableInstrumentMarketHours(old.ProviderMetadata, now) {
			continue
		}
		fetchedAt := previous.FetchedAt
		if text := old.ProviderMetadata["marketHoursFetchedAt"]; text != "" {
			value, err := strconv.ParseInt(text, 10, 64)
			if err != nil {
				continue
			}
			fetchedAt = value
		} else if old.ProviderMetadata["marketHoursRetained"] == "true" {
			// Never replace a lost original timestamp with a later retry time.
			continue
		}
		if fetchedAt <= 0 || fetchedAt > now.UnixMilli() {
			continue
		}
		if metadata == nil {
			metadata = map[string]string{}
			item.ProviderMetadata = metadata
		}
		for key := range metadata {
			if strings.HasPrefix(key, "marketHours") {
				delete(metadata, key)
			}
		}
		for key, value := range old.ProviderMetadata {
			if strings.HasPrefix(key, "marketHours") {
				metadata[key] = value
			}
		}
		metadata["marketHoursRetained"] = "true"
		metadata["marketHoursFetchedAt"] = strconv.FormatInt(fetchedAt, 10)
		metadata["marketHoursNotice"] = "Last-known provider schedule retained; the latest Dukascopy refresh did not supply usable hours."
	}
	return result
}

func usableInstrumentMarketHours(metadata map[string]string, now time.Time) bool {
	if metadata["marketHoursStatus"] != "available" {
		return false
	}
	switch metadata["marketHoursSource"] {
	case "JForex regular market session":
		first, last := metadata["marketHoursFirstDay"], metadata["marketHoursLastDay"]
		if len(first) != 1 || len(last) != 1 || first < "1" || last > "7" || first > last {
			return false
		}
		open, close := metadata["marketHoursOpen"], metadata["marketHoursClose"]
		for _, clock := range []string{open, close} {
			value, err := time.Parse("15:04:05", clock)
			if err != nil || value.Format("15:04:05") != clock {
				return false
			}
		}
		zone := metadata["marketHoursTimeZone"]
		if zone == "" || zone == "Local" || open >= close {
			return false
		}
		_, err := time.LoadLocation(zone)
		return err == nil
	case "JForex instrument calendar":
		from, fromErr := strconv.ParseInt(metadata["marketHoursFrom"], 10, 64)
		to, toErr := strconv.ParseInt(metadata["marketHoursTo"], 10, 64)
		validTime := func(value int64) bool { return value > 0 && value <= 8_640_000_000_000_000 }
		if fromErr != nil || toErr != nil || !validTime(from) || !validTime(to) || from >= to || now.UnixMilli() < from || now.UnixMilli() >= to || metadata["marketHoursTimeZone"] != "UTC" {
			return false
		}
		encoded := metadata["marketHoursClosedIntervals"]
		var intervals [][]int64
		if len(encoded) > 32768 || json.Unmarshal([]byte(encoded), &intervals) != nil || len(intervals) == 0 || len(intervals) > 512 {
			return false
		}
		overlaps := false
		for _, interval := range intervals {
			if len(interval) != 2 || !validTime(interval[0]) || !validTime(interval[1]) || interval[0] >= interval[1] {
				return false
			}
			overlaps = overlaps || interval[1] > from && interval[0] < to
		}
		return overlaps
	}
	return false
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
	// Older snapshots stay available to Instruments while normal selection
	// upgrades the metadata. Incomplete/expired hours retry sooner.
	if !force && instrumentCatalogFresh(m.catalog, time.Now()) {
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
		now := time.Now()
		account := filepath.Base(m.cachePath(flight.config))
		items = mergeInstrumentCatalogHours(m.catalog, items, account, flight.config.Environment, now)
		snapshot := savedInstrumentCatalog{Version: 1, Account: account, Environment: flight.config.Environment, Capability: bridgeInstrumentCatalog, FetchedAt: now.UnixMilli(), MarketHoursVersion: instrumentMarketHoursVersion, Items: cloneInstruments(items)}
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
