package screener

import (
	"bufio"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CSVs and matches are indexed on disk, not retained as unbounded slices. A
// fixed-size row cache supports previous-bar/window queries and chart paging.
type analysisSeries struct {
	metadata                        DataFile
	source                          string
	original                        os.FileInfo
	indexPath                       string
	headers                         []string
	positions                       map[string]int
	count, first, last, coverageEnd int64
	recordSize                      int64
	cache                           *analysisRowCache
	cursor                          int64
	// Identity of all parsed fields and values, independent of CSV formatting,
	// field order and filename request dates. Used only for duplicate results;
	// each source keeps its own verified companion coverage and stale checks.
	contentHash [sha256.Size]byte
}
type analysisRow struct {
	timestamp int64
	values    []float64
}

// encoding/csv supports quoted newlines; limiting the underlying reads as well
// as checking InputOffset prevents a malformed giant record exhausting memory.
type analysisRecordReader struct {
	input     io.Reader
	remaining int
}

func (r *analysisRecordReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errors.New("CSV record exceeds 1 MiB")
	}
	p = p[:min(len(p), r.remaining)]
	n, err := r.input.Read(p)
	r.remaining -= n
	return n, err
}

type analysisCacheKey struct {
	series *analysisSeries
	block  int64
}
type analysisCacheEntry struct {
	key  analysisCacheKey
	data []byte
}
type analysisRowCache struct {
	entries map[analysisCacheKey]*list.Element
	order   *list.List
	bytes   int
}

const analysisCacheBytes = 16 * 1024 * 1024

func newAnalysisRowCache() *analysisRowCache {
	return &analysisRowCache{entries: map[analysisCacheKey]*list.Element{}, order: list.New()}
}
func (c *analysisRowCache) block(s *analysisSeries, block int64) ([]byte, error) {
	key := analysisCacheKey{s, block}
	if e := c.entries[key]; e != nil {
		c.order.MoveToFront(e)
		return e.Value.(analysisCacheEntry).data, nil
	}
	data := make([]byte, min(int64(256), s.count-block)*s.recordSize)
	f, err := os.Open(s.indexPath)
	if err != nil {
		return nil, err
	}
	_, err = f.ReadAt(data, block*s.recordSize)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	for c.bytes+len(data) > analysisCacheBytes && c.order.Len() > 0 {
		e := c.order.Back()
		entry := e.Value.(analysisCacheEntry)
		delete(c.entries, entry.key)
		c.bytes -= len(entry.data)
		c.order.Remove(e)
	}
	c.entries[key] = c.order.PushFront(analysisCacheEntry{key, data})
	c.bytes += len(data)
	return data, nil
}

func analysisClosedAt(timestamp int64, tf string) int64 {
	switch tf {
	case "tick":
		return timestamp
	case "s1":
		return timestamp + 1000
	case "m1":
		return timestamp + 60_000
	case "m5":
		return timestamp + 300_000
	case "m15":
		return timestamp + 900_000
	case "m30":
		return timestamp + 1_800_000
	case "h1":
		return timestamp + 3_600_000
	case "h4":
		return timestamp + 14_400_000
	case "d1":
		return timestamp + 86_400_000
	case "mn1":
		return time.UnixMilli(timestamp).UTC().AddDate(0, 1, 0).UnixMilli()
	}
	return math.MaxInt64
}
func parseAnalysisTimestamp(text string) (int64, error) {
	if v, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
		// Published Dukascopy CSVs always use milliseconds. Magnitude guessing
		// would misread supported early-1973 daily history as Unix seconds.
		if v >= 0 && v <= 253_402_300_799_999 && v == math.Trunc(v) {
			return int64(v), nil
		}
	}
	if value, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text)); err == nil && value.UnixMilli() >= 0 {
		return value.UnixMilli(), nil
	}
	return 0, errors.New("timestamp must be valid Unix milliseconds or RFC3339 UTC time")
}
func indexAnalysisCSV(ctx context.Context, directory, name, output string, progress func(float64)) (*analysisSeries, error) {
	metadata := describeFile(directory, name)
	if metadata.Error != "" {
		return nil, errors.New(metadata.Error)
	}
	if metadata.Symbol == "" || metadata.Timeframe == "" {
		return nil, errors.New("cannot determine asset and timeframe; CSV name must use symbol-timeframe-from-to.csv")
	}
	source := filepath.Join(directory, name)
	original, err := os.Lstat(source)
	if err != nil || !original.Mode().IsRegular() {
		return nil, errors.New("analysis source is no longer a regular CSV")
	}
	input, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	if current, e := input.Stat(); e != nil || !os.SameFile(current, original) {
		return nil, errors.New("analysis source changed before reading")
	}
	checksum := sha256.New()
	limited := &analysisRecordReader{input: io.TeeReader(input, checksum), remaining: 1024*1024 + 65536}
	reader := csv.NewReader(bufio.NewReaderSize(limited, 64*1024))
	reader.ReuseRecord = true
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	if len(header) > 512 {
		return nil, errors.New("analysis supports at most 512 CSV fields")
	}
	headers := append([]string{}, header...)
	positions := map[string]int{}
	for i, field := range headers {
		field = strings.TrimSpace(strings.TrimPrefix(field, "\ufeff"))
		headers[i] = field
		key := strings.ToLower(field)
		if key == "" {
			return nil, errors.New("analysis CSV contains an empty field name")
		}
		if _, exists := positions[key]; exists {
			return nil, errors.New("analysis CSV contains duplicate fields")
		}
		positions[key] = i
	}
	timestampIndex, exists := positions["timestamp"]
	if !exists {
		return nil, errors.New("analysis CSV requires a timestamp field")
	}
	fieldNames := make([]string, 0, len(positions))
	for field := range positions {
		fieldNames = append(fieldNames, field)
	}
	sort.Strings(fieldNames)
	canonicalPositions := make([]int, len(fieldNames))
	content := sha256.New()
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(len(fieldNames)))
	_, _ = content.Write(length[:])
	for i, field := range fieldNames {
		canonicalPositions[i] = positions[field]
		binary.LittleEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = content.Write(length[:])
		_, _ = io.WriteString(content, field)
	}
	canonicalRow := make([]byte, len(fieldNames)*8)
	out, err := os.OpenFile(output, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		_ = out.Close()
		if !complete {
			_ = os.Remove(output)
		}
	}()
	buffered := bufio.NewWriterSize(out, 256*1024)
	s := &analysisSeries{metadata: metadata, source: source, original: original, indexPath: output, headers: headers, positions: positions, recordSize: int64(8 + len(headers)*8), cache: newAnalysisRowCache(), cursor: -1}
	record := make([]byte, s.recordSize)
	for {
		if s.count%512 == 0 {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			progress(math.Min(100, float64(reader.InputOffset())/float64(max(original.Size(), 1))*100))
		}
		limited.remaining = 1024*1024 + 65536
		previousOffset := reader.InputOffset()
		row, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("CSV row %d: %w", s.count+2, e)
		}
		if reader.InputOffset()-previousOffset > 1024*1024 {
			return nil, fmt.Errorf("CSV row %d exceeds 1 MiB", s.count+2)
		}
		if reader.InputOffset() > original.Size()+1024*1024 {
			return nil, errors.New("analysis source grew while reading")
		}
		timestamp, e := parseAnalysisTimestamp(row[timestampIndex])
		if e != nil {
			return nil, fmt.Errorf("CSV row %d: %w", s.count+2, e)
		}
		if s.count > 0 && (timestamp < s.last || (timestamp == s.last && metadata.Timeframe != "tick")) {
			return nil, fmt.Errorf("CSV row %d: candle timestamps must be unique and chronological", s.count+2)
		}
		binary.LittleEndian.PutUint64(record[:8], uint64(timestamp))
		for i, cell := range row {
			value := math.NaN()
			cell = strings.TrimSpace(cell)
			if i == timestampIndex {
				value = float64(timestamp)
			} else if cell != "" {
				value, e = strconv.ParseFloat(cell, 64)
				if e != nil || math.IsNaN(value) || math.IsInf(value, 0) {
					return nil, fmt.Errorf("CSV row %d field %s is not a finite number", s.count+2, headers[i])
				}
			}
			binary.LittleEndian.PutUint64(record[8+i*8:], math.Float64bits(value))
		}
		readValue := func(key string) (float64, bool) {
			i, exists := positions[key]
			if !exists {
				return 0, false
			}
			return math.Float64frombits(binary.LittleEndian.Uint64(record[8+i*8:])), true
		}
		for _, field := range []string{"open", "high", "low", "close", "bidprice", "askprice"} {
			if value, exists := readValue(field); exists && (math.IsNaN(value) || value <= 0) {
				return nil, fmt.Errorf("CSV row %d field %s must be a positive price", s.count+2, field)
			}
		}
		if volume, exists := readValue("volume"); exists && (math.IsNaN(volume) || volume < 0) {
			return nil, fmt.Errorf("CSV row %d volume must be nonnegative", s.count+2)
		}
		open, hasOpen := readValue("open")
		high, hasHigh := readValue("high")
		low, hasLow := readValue("low")
		closeValue, hasClose := readValue("close")
		if hasOpen && hasHigh && hasLow && hasClose && (low > math.Min(open, closeValue) || high < math.Max(open, closeValue) || low > high) {
			return nil, fmt.Errorf("CSV row %d has invalid OHLC bounds", s.count+2)
		}
		if _, e = buffered.Write(record); e != nil {
			return nil, e
		}
		for i, position := range canonicalPositions {
			bits := binary.LittleEndian.Uint64(record[8+position*8:])
			value := math.Float64frombits(bits)
			if value == 0 {
				bits = 0 // Positive and negative zero compare identically.
			} else if math.IsNaN(value) {
				bits = 0x7ff8000000000000 // Every blank has the same identity.
			}
			binary.LittleEndian.PutUint64(canonicalRow[i*8:], bits)
		}
		_, _ = content.Write(canonicalRow)
		if s.count == 0 {
			s.first = timestamp
		}
		s.last = timestamp
		s.count++
	}
	if s.count == 0 {
		return nil, errors.New("CSV contains no data bars")
	}
	if err = buffered.Flush(); err != nil {
		return nil, err
	}
	if err = out.Sync(); err != nil {
		return nil, err
	}
	if err = analysisUnchanged(s); err != nil {
		return nil, err
	}
	copy(s.contentHash[:], content.Sum(nil))
	// The latest closed companion remains the latest until another companion
	// candle could have closed. Refresh deliberately excludes the open candle,
	// so ending availability at the last close would discard today's D1 signal.
	lastClose := analysisClosedAt(s.last, metadata.Timeframe)
	s.coverageEnd = lastClose
	if metadata.Timeframe != "tick" {
		s.coverageEnd = analysisClosedAt(lastClose, metadata.Timeframe) - 1
	}
	if end, e := time.Parse("2006-01-02", metadata.To); e == nil {
		s.coverageEnd = max(s.coverageEnd, end.UnixMilli()-1)
	}
	// A completed refresh can verify a market closure lasting longer than one
	// interval. Only trust that horizon when the current CSV still has its exact
	// verified bytes, even if an external edit preserved its size and mtime.
	if saved, e := readFileMetadata(directory, name); e == nil && saved.Verified && metadataMatches(saved, original) && saved.Checksum == fmt.Sprintf("%x", checksum.Sum(nil)) {
		s.coverageEnd = max(s.coverageEnd, saved.CheckedThrough)
	}
	complete = true
	return s, nil
}
func analysisUnchanged(s *analysisSeries) error {
	current, err := os.Lstat(s.source)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(current, s.original) || current.Size() != s.original.Size() || !current.ModTime().Equal(s.original.ModTime()) {
		return fmt.Errorf("%s changed after analysis started; apply the rules again", s.metadata.Name)
	}
	return nil
}
func (s *analysisSeries) row(index int64) (analysisRow, error) {
	if index < 0 || index >= s.count {
		return analysisRow{}, io.EOF
	}
	block := index / 256 * 256
	data, err := s.cache.block(s, block)
	if err != nil {
		return analysisRow{}, err
	}
	start := (index - block) * s.recordSize
	record := data[start : start+s.recordSize]
	row := analysisRow{timestamp: int64(binary.LittleEndian.Uint64(record[:8])), values: make([]float64, len(s.headers))}
	for i := range row.values {
		row.values[i] = math.Float64frombits(binary.LittleEndian.Uint64(record[8+i*8:]))
	}
	return row, nil
}
func (s *analysisSeries) fieldIndex(field string) (int, bool) {
	key := strings.ToLower(strings.TrimSpace(field))
	if key == "price" || key == "close" {
		for _, alias := range []string{"close", "bidprice", "askprice"} {
			if i, ok := s.positions[alias]; ok {
				return i, true
			}
		}
	}
	i, ok := s.positions[key]
	return i, ok
}

// closedIndex returns the newest bar that was fully closed by target. A cursor
// handles the usual increasing scan; binary search handles earlier-bar queries.
func (s *analysisSeries) closedIndex(target int64) (int64, error) {
	if target < s.first || target > s.coverageEnd {
		return -1, nil
	}
	if s.cursor >= 0 && s.cursor < s.count {
		current, err := s.row(s.cursor)
		if err != nil {
			return -1, err
		}
		if analysisClosedAt(current.timestamp, s.metadata.Timeframe) <= target {
			for s.cursor+1 < s.count {
				next, e := s.row(s.cursor + 1)
				if e != nil {
					return -1, e
				}
				if analysisClosedAt(next.timestamp, s.metadata.Timeframe) > target {
					break
				}
				s.cursor++
			}
			return s.cursor, nil
		}
	}
	low, high := int64(0), s.count
	for low < high {
		mid := (low + high) / 2
		row, e := s.row(mid)
		if e != nil {
			return -1, e
		}
		if analysisClosedAt(row.timestamp, s.metadata.Timeframe) <= target {
			low = mid + 1
		} else {
			high = mid
		}
	}
	s.cursor = low - 1
	return s.cursor, nil
}

func (s *analysisSeries) closedCount(asOf int64) (int64, error) {
	low, high := int64(0), s.count
	for low < high {
		mid := (low + high) / 2
		row, err := s.row(mid)
		if err != nil {
			return 0, err
		}
		if analysisClosedAt(row.timestamp, s.metadata.Timeframe) <= asOf {
			low = mid + 1
		} else {
			high = mid
		}
	}
	return low, nil
}
