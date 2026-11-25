package screener

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

var ErrSyntheticHistoryUnavailable = errors.New("download this instrument's D1/W1 history in Screener before opening its calculated chart")

// SyntheticHistory reads the published Screener dataset only. Selecting a chart
// never starts an implicit tick download or authenticates a broker account.
func (m *Manager) SyntheticHistory(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	definition, found := SyntheticDefinitionFor(key.Instrument)
	if !found || key.Exchange != "dukascopy" || (key.Timeframe != "1d" && key.Timeframe != "1w") {
		return nil, errors.New("synthetic retained history supports registered Dukascopy D1/W1 instruments only")
	}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	if from < 0 || to != 0 && to <= from || limit < 0 || limit > 100000 {
		return nil, errors.New("invalid synthetic chart history range")
	}
	m.mu.Lock()
	directory, closed := m.directory, m.closed
	m.mu.Unlock()
	if closed {
		return nil, errors.New("Screener is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names, err := csvNames(directory)
	if err != nil {
		return nil, err
	}
	period := "d1"
	if key.Timeframe == "1w" {
		period = "w1"
	}
	byTime := map[int64]domain.Candle{}
	matched := false
	for _, name := range names {
		match := parseDownloadFilename(directory, name)
		if match == nil || match[1] != definition.ID || match[2] != period {
			continue
		}
		matched = true
		bars, err := readSyntheticCSV(ctx, directory, name, definition, key.Timeframe)
		if err != nil {
			return nil, err
		}
		for _, bar := range bars {
			if previous, exists := byTime[bar.Time]; exists && previous != bar {
				return nil, errors.New("overlapping synthetic histories disagree; consolidate or refresh the Screener dataset")
			}
			byTime[bar.Time] = bar
		}
	}
	if !matched {
		return nil, ErrSyntheticHistoryUnavailable
	}
	out := []domain.Candle{}
	for _, bar := range byTime {
		if bar.Time >= from && (to == 0 || bar.Time < to) {
			out = append(out, bar)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func readSyntheticCSV(ctx context.Context, directory, name string, definition SyntheticDefinition, interval string) ([]domain.Candle, error) {
	filename := filepath.Join(directory, name)
	original, err := os.Lstat(filename)
	if err != nil || !original.Mode().IsRegular() || original.Size() > 64<<20 {
		return nil, errors.New("synthetic history must be a regular CSV of at most 64 MiB")
	}
	metadata, err := readFileMetadata(directory, name)
	if err != nil {
		return nil, err
	}
	provenance := metadata.Provenance
	if definition.Calculated && (provenance == nil || provenance.Kind != "synchronized-bid-ratio") {
		return nil, errors.New("calculated history lacks its synchronized BID provenance; download it again through Screener")
	}
	if provenance != nil {
		if !metadataMatches(metadata, original) || metadata.Checksum == "" {
			return nil, errors.New("synthetic history changed outside its verified publication; original preserved")
		}
		if syntheticData(definition.ID, provenance) && (provenance.Numerator != definition.Numerator || provenance.Denominator != definition.Denominator) {
			return nil, errors.New("synthetic dataset provenance belongs to another ratio")
		}
	}
	input, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	if current, err := input.Stat(); err != nil || !os.SameFile(current, original) {
		return nil, errors.New("synthetic history changed before reading")
	}
	digest := sha256.New()
	limited := &analysisRecordReader{input: io.TeeReader(input, digest), remaining: 1024*1024 + 65536}
	reader := csv.NewReader(bufio.NewReaderSize(limited, 64*1024))
	header, err := reader.Read()
	if err != nil || len(header) > 512 {
		return nil, errors.New("synthetic history has an invalid CSV header")
	}
	positions := map[string]int{}
	for index, field := range header {
		field = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(field, "\ufeff")))
		if field == "" {
			return nil, errors.New("synthetic history has an empty CSV field")
		}
		if _, duplicate := positions[field]; duplicate {
			return nil, errors.New("synthetic history has duplicate CSV fields")
		}
		positions[field] = index
	}
	for _, field := range []string{"timestamp", "open", "high", "low", "close"} {
		if _, found := positions[field]; !found {
			return nil, fmt.Errorf("synthetic history is missing %s", field)
		}
	}
	_, hasVolume := positions["volume"]
	if syntheticData(definition.ID, provenance) && hasVolume {
		return nil, errors.New("calculated synthetic history cannot contain an invented volume column")
	}
	step := exchange.TimeframeSeconds(interval)
	rows := []domain.Candle{}
	previous := int64(-1)
	for count := 0; ; count++ {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		limited.remaining = 1024*1024 + 65536
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || count >= 100000 {
			return nil, errors.New("synthetic history contains invalid or excessive CSV rows")
		}
		stamp, err := parseAnalysisTimestamp(record[positions["timestamp"]])
		if err != nil || stamp%1000 != 0 || stamp/1000 <= previous || exchange.BucketStart(stamp/1000, step) != stamp/1000 {
			return nil, errors.New("synthetic history candles must be ordered and aligned to their UTC period")
		}
		previous = stamp / 1000
		values := map[string]float64{}
		for _, field := range []string{"open", "high", "low", "close", "volume"} {
			index, exists := positions[field]
			if !exists {
				continue
			}
			value, err := strconv.ParseFloat(record[index], 64)
			if err != nil {
				return nil, fmt.Errorf("invalid synthetic history %s", field)
			}
			values[field] = value
		}
		calculated := syntheticData(definition.ID, provenance)
		if provenance != nil && provenance.Kind == "mixed-native-ratio" {
			calculated = false
			covered := false
			for _, segment := range provenance.Segments {
				if segment.From < stamp+step*1000 && segment.To > stamp {
					covered = true
					calculated = calculated || segment.Kind == "synchronized-bid-ratio"
				}
			}
			if !covered {
				return nil, errors.New("mixed synthetic candle is outside its recorded provenance segments")
			}
		}
		bar := domain.Candle{Time: stamp / 1000, Open: values["open"], High: values["high"], Low: values["low"], Close: values["close"], Volume: values["volume"], VolumeUnavailable: !hasVolume, Synthetic: calculated, Approximate: calculated && provenance != nil && provenance.Approximate, Quality: "authoritative", Closed: stamp/1000+step <= time.Now().Unix()}
		if err := bar.Validate(); err != nil {
			return nil, fmt.Errorf("invalid synthetic history candle: %w", err)
		}
		if bar.Closed {
			rows = append(rows, bar)
		}
	}
	if provenance != nil && hex.EncodeToString(digest.Sum(nil)) != metadata.Checksum {
		return nil, errors.New("synthetic history checksum does not match its provenance; original preserved")
	}
	if err = unchangedFile(filename, original); err != nil {
		return nil, err
	}
	return rows, nil
}
