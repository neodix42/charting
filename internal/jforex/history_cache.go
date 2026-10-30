package jforex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"cryptodesk/internal/candlecache"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

const chartCoverageVersion = 1
const chartCoverageBytes = 4 * 1024 * 1024
const chartCoverageEntries = 8192

type chartHistorySpan struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}
type chartHistoryCoverage struct {
	Key domain.FeedKey `json:"key"`
	chartHistorySpan
	Rows   int    `json:"rows"`
	Times  string `json:"times"`
	Stream bool   `json:"stream,omitempty"`
}
type chartCoverageState struct {
	Version int                    `json:"version"`
	Entries []chartHistoryCoverage `json:"entries"`
}

// Candle commits precede coverage publication. Interrupted coverage writes can
// cause a repeated provider query, never a falsely complete range. Timestamp
// fingerprints also invalidate proofs if their database/rows disappear.
type chartHistoryCache struct {
	mu         sync.Mutex
	store      *candlecache.Store
	path       string
	coverage   []chartHistoryCoverage
	generation map[domain.FeedKey]uint64
	gate       chan struct{}
	now        func() time.Time
	closed     bool
	writes     uint64
}

func openChartHistoryCache(directory string) (*chartHistoryCache, error) {
	path := filepath.Join(directory, "coverage.json")
	var state chartCoverageState
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() > chartCoverageBytes {
			return nil, errors.New("local forex coverage must be a regular file no larger than 4 MiB; original preserved")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&state); err != nil {
			return nil, fmt.Errorf("invalid local forex coverage (original preserved): %w", err)
		}
		if decoder.Decode(new(any)) != io.EOF || state.Version != chartCoverageVersion || len(state.Entries) > chartCoverageEntries {
			return nil, errors.New("invalid local forex coverage version/count; original preserved")
		}
		for _, entry := range state.Entries {
			if validateKey(entry.Key) != nil || entry.From < 0 || entry.To <= entry.From || entry.To > 32503680000 || entry.Rows < 0 || entry.Rows >= candlecache.MaxRangeLimit || len(entry.Times) != 64 {
				return nil, errors.New("invalid local forex coverage range; original preserved")
			}
			if _, err = hex.DecodeString(entry.Times); err != nil {
				return nil, errors.New("invalid local forex coverage fingerprint; original preserved")
			}
		}
		if st, statErr := os.Lstat(filepath.Join(directory, candlecache.DatabaseFilename)); statErr != nil || !st.Mode().IsRegular() {
			return nil, errors.New("local forex coverage exists without its candle database; original files preserved")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	store, err := candlecache.Open(directory)
	if err != nil {
		return nil, err
	}
	return &chartHistoryCache{store: store, path: path, coverage: state.Entries, generation: map[domain.FeedKey]uint64{}, gate: make(chan struct{}, 1), now: time.Now}, nil
}

func (m *Manager) chartCacheLocked() (*chartHistoryCache, error) {
	if m.closed {
		return nil, errors.New("The Dukascopy service is closed")
	}
	if m.loadError != nil {
		return nil, m.loadError
	}
	if m.config.Username == "" || m.config.Password == "" {
		return nil, nil
	}
	if m.chartCache == nil {
		var err error
		m.chartCache, err = openChartHistoryCache(filepath.Join(m.cachePath(m.config), "chart-candles"))
		if err != nil {
			m.chartCacheError = "Dukascopy local history: " + err.Error()
			return nil, err
		}
		m.chartCacheError = ""
	}
	return m.chartCache, nil
}

// Caller holds Manager.mu and has stopped operations before closing. Ordinary
// local reads/writes are serialized by the cache mutex as well.
func (m *Manager) closeChartCacheLocked() {
	if m.chartCache != nil {
		c := m.chartCache
		c.mu.Lock()
		c.closed = true
		if err := c.store.Close(); err != nil {
			m.chartCacheError = "Dukascopy local history: " + err.Error()
		}
		c.mu.Unlock()
		m.chartCache = nil
	}
}

func (m *Manager) chartCacheFailure(err error) error {
	if err != nil {
		m.mu.Lock()
		m.chartCacheError = "Dukascopy local history: " + err.Error()
		m.mu.Unlock()
	}
	return err
}

func (m *Manager) chartCacheResult(c *chartHistoryCache, err error, successfulWrite bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.chartCache == c {
		if err != nil {
			m.chartCacheError = "Dukascopy local history: " + err.Error()
		} else if successfulWrite {
			m.chartCacheError = ""
		}
	}
	return err
}

// CachedCandles never prepares Java, authenticates or requests provider data.
func (m *Manager) CachedCandles(key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	m.mu.Lock()
	c, err := m.chartCacheLocked()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if c == nil {
		return []domain.Candle{}, nil
	}
	c.mu.Lock()
	bars, err := c.store.Range(key, from, to, limit)
	c.mu.Unlock()
	for i := range bars {
		bars[i].SourceRevision = 0
	}
	return bars, m.chartCacheResult(c, err, false)
}

// StoreCandles receives market-accepted updates synchronously while the stream
// still holds its account operation. No raw/obsolete event is persisted here.
func (m *Manager) StoreCandles(key domain.FeedKey, bars []domain.Candle) (writeErr error) {
	defer func() {
		if writeErr != nil {
			m.mu.Lock()
			m.chartWriteFailures++
			m.mu.Unlock()
		}
	}()
	if err := validateKey(key); err != nil {
		return err
	}
	// Provisional tick updates stay in memory. Native closed-bar commits are
	// infrequent and durable without a synchronous SQLite fsync for every quote.
	closed := make([]domain.Candle, 0, len(bars))
	for _, bar := range bars {
		if bar.Closed {
			closed = append(closed, bar)
		}
	}
	if len(closed) == 0 {
		return nil
	}
	m.mu.Lock()
	c, err := m.chartCacheLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	c.mu.Lock()
	err = c.store.Upsert(key, closed)
	if err == nil {
		c.generation[key]++
		c.writes++
	}
	c.mu.Unlock()
	return m.chartCacheResult(c, err, err == nil)
}

func chartTimesFingerprint(bars []domain.Candle) string {
	hash := sha256.New()
	for _, bar := range bars {
		fmt.Fprintf(hash, "%d\n", bar.Time)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (c *chartHistoryCache) validCoverageLocked(key domain.FeedKey, from, to int64) ([]chartHistorySpan, error) {
	spans := []chartHistorySpan{}
	for _, entry := range c.coverage {
		if entry.Key != key || entry.To <= from || entry.From >= to {
			continue
		}
		bars, err := c.store.Range(key, entry.From, entry.To-1, candlecache.MaxRangeLimit)
		if err != nil {
			return nil, err
		}
		if !validChartProof(entry, bars) {
			continue
		}
		spans = append(spans, entry.chartHistorySpan)
	}
	return spans, nil
}

func validChartProof(entry chartHistoryCoverage, bars []domain.Candle) bool {
	if len(bars) != entry.Rows || chartTimesFingerprint(bars) != entry.Times {
		return false
	}
	for _, bar := range bars {
		if !bar.Closed {
			return false
		}
	}
	return true
}

func (c *chartHistoryCache) streamChainLocked(key domain.FeedKey) (int64, int64, error) {
	spans := []chartHistorySpan{}
	for _, entry := range c.coverage {
		if entry.Key != key || !entry.Stream {
			continue
		}
		bars, err := c.store.Range(key, entry.From, entry.To-1, candlecache.MaxRangeLimit)
		if err != nil {
			return 0, 0, err
		}
		if validChartProof(entry, bars) {
			spans = append(spans, entry.chartHistorySpan)
		}
	}
	if len(spans) == 0 {
		return 0, 0, nil
	}
	// Stream proofs are recorded in publication order. Only a touching/overlapping
	// chain advances the checkpoint; a later native candle creates no proof.
	from, to := spans[0].From, spans[0].To
	for _, span := range spans[1:] {
		if span.From <= to && span.To >= from {
			from = min(from, span.From)
			to = max(to, span.To)
		}
	}
	return from, to, nil
}

// HistoryAnchor is a confirmed provider checkpoint, never a max(candle.time)
// heuristic. It includes one closed interval of overlap for reconciliation.
func (m *Manager) HistoryAnchor(key domain.FeedKey) (int64, error) {
	if err := validateKey(key); err != nil {
		return 0, err
	}
	m.mu.Lock()
	c, err := m.chartCacheLocked()
	m.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if c == nil {
		return 0, nil
	}
	c.mu.Lock()
	from, to, err := c.streamChainLocked(key)
	c.mu.Unlock()
	if err != nil {
		return 0, m.chartCacheResult(c, err, false)
	}
	if to == 0 {
		return 0, nil
	}
	return max(from, to-exchange.TimeframeSeconds(key.Timeframe)), nil
}

// RecordHistoryCoverage follows accepted closed writes from a successful Java
// seed chunk. Empty weekend chunks advance the same verified chronological
// checkpoint without fabricating a candle.
func (m *Manager) RecordHistoryCoverage(key domain.FeedKey, from, to int64, expected ...domain.Candle) error {
	if err := validateKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	c, err := m.chartCacheLocked()
	if err == nil && m.chartCacheError != "" {
		err = errors.New(m.chartCacheError)
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if from < 0 || to <= from || to > exchange.BucketStart(c.now().Unix(), exchange.TimeframeSeconds(key.Timeframe)) {
		err = errors.New("invalid closed forex stream coverage range")
	} else {
		start, end, chainErr := c.streamChainLocked(key)
		if chainErr != nil {
			err = chainErr
		} else if end != 0 && (from > end || to < start) {
			err = errors.New("forex history coverage cannot skip an unchecked range")
		} else {
			err = c.recordProofLocked(key, from, to, true, expected...)
		}
	}
	c.mu.Unlock()
	return m.chartCacheResult(c, err, err == nil)
}

func missingChartSpans(from, to int64, spans []chartHistorySpan) []chartHistorySpan {
	sort.Slice(spans, func(i, j int) bool { return spans[i].From < spans[j].From })
	cursor := from
	missing := []chartHistorySpan{}
	for _, span := range spans {
		if span.To <= cursor || span.From >= to {
			continue
		}
		if span.From > cursor {
			missing = append(missing, chartHistorySpan{cursor, min(span.From, to)})
		}
		cursor = max(cursor, span.To)
		if cursor >= to {
			break
		}
	}
	if cursor < to {
		missing = append(missing, chartHistorySpan{cursor, to})
	}
	return missing
}

func (c *chartHistoryCache) saveCoverageLocked(entries []chartHistoryCoverage) error {
	data, err := json.Marshal(chartCoverageState{Version: chartCoverageVersion, Entries: entries})
	if err != nil {
		return err
	}
	if len(data) > chartCoverageBytes {
		return errors.New("local forex coverage exceeds its supported size")
	}
	temp, err := os.CreateTemp(filepath.Dir(c.path), ".coverage-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err = temp.Chmod(0600); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, c.path); err != nil {
		return err
	}
	// Windows does not support directory Sync. Candle commits are independently
	// durable; losing only a coverage rename after power loss remains conservative.
	if directory, openErr := os.Open(filepath.Dir(c.path)); openErr == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	c.coverage = entries
	c.writes++
	return nil
}

func (c *chartHistoryCache) recordCoverageLocked(key domain.FeedKey, from, to int64) error {
	return c.recordProofLocked(key, from, to, false)
}

func (c *chartHistoryCache) recordProofLocked(key domain.FeedKey, from, to int64, stream bool, expected ...domain.Candle) error {
	if to <= from {
		return nil
	}
	bars, err := c.store.Range(key, from, to-1, candlecache.MaxRangeLimit)
	if err != nil {
		return err
	}
	if len(bars) >= candlecache.MaxRangeLimit {
		return nil
	}
	stored := make(map[int64]bool, len(bars))
	for _, bar := range bars {
		stored[bar.Time] = bar.Closed
	}
	for _, bar := range expected {
		if bar.Closed && bar.Time >= from && bar.Time < to && !stored[bar.Time] {
			return errors.New("closed forex candles were not committed; the catch-up checkpoint was preserved")
		}
	}
	entry := chartHistoryCoverage{Key: key, chartHistorySpan: chartHistorySpan{from, to}, Rows: len(bars), Times: chartTimesFingerprint(bars), Stream: stream}
	entries := make([]chartHistoryCoverage, 0, min(chartCoverageEntries, len(c.coverage)+1))
	for _, previous := range c.coverage {
		if previous.Key == key && previous.From == from && previous.To == to && previous.Stream == stream {
			continue
		}
		entries = append(entries, previous)
	}
	entries = append(entries, entry)
	if len(entries) > chartCoverageEntries {
		entries = entries[len(entries)-chartCoverageEntries:]
	}
	return c.saveCoverageLocked(entries)
}

type chartStorageError struct{ cause error }

func (e *chartStorageError) Error() string { return e.cause.Error() }
func (e *chartStorageError) Unwrap() error { return e.cause }
func localChartError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var local *chartStorageError
	if errors.As(err, &local) {
		return err
	}
	return &chartStorageError{err}
}

func mergeChartSpans(spans []chartHistorySpan) []chartHistorySpan {
	sort.Slice(spans, func(i, j int) bool { return spans[i].From < spans[j].From })
	result := []chartHistorySpan{}
	for _, span := range spans {
		if len(result) > 0 && span.From <= result[len(result)-1].To {
			result[len(result)-1].To = max(result[len(result)-1].To, span.To)
		} else {
			result = append(result, span)
		}
	}
	return result
}

func (c *chartHistoryCache) history(ctx context.Context, key domain.FeedKey, from, to int64, limit int, fetch func(context.Context, int64, int64, int) ([]domain.Candle, error)) ([]domain.Candle, error) {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.gate }()
	c.mu.Lock()
	spans, err := c.validCoverageLocked(key, from, to)
	if err != nil {
		c.mu.Unlock()
		return nil, localChartError(err)
	}
	closed := exchange.BucketStart(c.now().Unix(), exchange.TimeframeSeconds(key.Timeframe))
	missing := missingChartSpans(from, to, spans)
	// A recent query also reconciles one closed overlapping bar, while immutable
	// older pages (including sparse forex weekends) are wholly reusable.
	if to > closed {
		overlap := max(from, closed-exchange.TimeframeSeconds(key.Timeframe))
		missing = append(missing, chartHistorySpan{overlap, to})
	}
	missing = mergeChartSpans(missing)
	c.mu.Unlock()
	// Newest gaps first: once a full latest page is known, unqueried earlier
	// intervals cannot affect this bounded result and remain unclaimed coverage.
	sort.Slice(missing, func(i, j int) bool { return missing[i].From > missing[j].From })
	transient := map[int64]domain.Candle{}
	for _, gap := range missing {
		if err := ctx.Err(); err != nil {
			return nil, localChartError(err)
		}
		c.mu.Lock()
		current, readErr := c.store.Range(key, from, to-1, limit)
		generation := c.generation[key]
		c.mu.Unlock()
		if readErr != nil {
			return nil, localChartError(readErr)
		}
		if len(current) == limit && gap.To <= current[0].Time {
			continue
		}
		bars, fetchErr := fetch(ctx, gap.From, gap.To, limit)
		if fetchErr != nil {
			return nil, fetchErr
		}
		if err := ctx.Err(); err != nil {
			return nil, localChartError(err)
		}
		coverageFrom := gap.From
		if len(bars) >= limit && len(bars) > 0 {
			coverageFrom = max(coverageFrom, bars[0].Time)
		}
		c.mu.Lock()
		existing, readErr := c.store.Range(key, gap.From, gap.To-1, candlecache.MaxRangeLimit)
		if readErr == nil {
			byTime := make(map[int64]domain.Candle, len(existing))
			for _, bar := range existing {
				byTime[bar.Time] = bar
			}
			updates := make([]domain.Candle, 0, len(bars))
			for _, bar := range bars {
				if !bar.Closed {
					transient[bar.Time] = bar
					continue
				}
				previous, found := byTime[bar.Time]
				if found && (c.generation[key] != generation || (previous.Closed && !bar.Closed)) {
					continue
				}
				updates = append(updates, bar)
			}
			readErr = c.store.Upsert(key, updates)
		}
		if readErr == nil {
			readErr = c.recordCoverageLocked(key, coverageFrom, min(gap.To, closed))
		}
		c.mu.Unlock()
		if readErr != nil {
			return nil, localChartError(readErr)
		}
	}
	c.mu.Lock()
	result, err := c.store.Range(key, from, to-1, limit)
	c.mu.Unlock()
	if err != nil {
		return nil, localChartError(err)
	}
	merged := make(map[int64]domain.Candle, len(result)+len(transient))
	for _, bar := range result {
		merged[bar.Time] = bar
	}
	for timestamp, bar := range transient {
		if existing, found := merged[timestamp]; !found || !existing.Closed {
			merged[timestamp] = bar
		}
	}
	result = result[:0]
	for _, bar := range merged {
		result = append(result, bar)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Time < result[j].Time })
	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	for i := range result {
		result[i].SourceRevision = 0
	}
	return result, nil
}
