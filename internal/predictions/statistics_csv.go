package predictions

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type statisticsWeek struct {
	days [5]*StatisticsCandle
}

type statisticsSource struct {
	snapshot       sourceSnapshot
	weeks          map[int64]*statisticsWeek
	history        []StatisticsCandle
	anchor         StatisticsCandle
	firstWeek      int64
	invalidHistory int
	checkpoints    map[int64]StatisticsCandle
}

// Read all rows to validate ordering and hash the exact immutable source bytes,
// but use only candles already closed at the requested cutoff. Retain compact
// weekday groups and the last 160 actual candles, never synthetic gap fills.
func readStatisticsSource(ctx context.Context, dataset Dataset, asOf int64) (statisticsSource, error) {
	return readStatisticsCandleSource(ctx, dataset, asOf, "d1")
}

func readStatisticsCandleSource(ctx context.Context, dataset Dataset, asOf int64, period string) (statisticsSource, error) {
	result := statisticsSource{weeks: map[int64]*statisticsWeek{}, history: []StatisticsCandle{}, checkpoints: map[int64]StatisticsCandle{}}
	if !filepath.IsAbs(dataset.Directory) || filepath.Base(dataset.Filename) != dataset.Filename || dataset.Filename == "." || dataset.Filename == "" || strings.ContainsRune(dataset.Filename, 0) {
		return result, errors.New("statistics source must identify an existing downloaded CSV")
	}
	path := filepath.Join(dataset.Directory, dataset.Filename)
	original, err := os.Lstat(path)
	if err != nil || !original.Mode().IsRegular() {
		return result, errors.New("statistics source is no longer a regular CSV")
	}
	input, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(opened, original) {
		return result, errors.New("statistics source changed before it could be opened")
	}
	hash := sha256.New()
	limited := &recordReader{input: io.TeeReader(input, hash), remaining: 1024*1024 + 65536}
	reader := csv.NewReader(limited)
	header, err := reader.Read()
	if err != nil || reader.InputOffset() > 1024*1024 {
		return result, errors.New("CSV header is missing, malformed or larger than 1 MiB")
	}
	fields := map[string]int{}
	for index, name := range header {
		name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))
		if _, exists := fields[name]; exists || name == "" {
			return result, errors.New("CSV contains empty or duplicate field names")
		}
		fields[name] = index
	}
	for _, field := range []string{"timestamp", "open", "high", "low", "close"} {
		if _, exists := fields[field]; !exists {
			return result, errors.New("statistics requires timestamp, open, high, low and close columns in daily candle data")
		}
	}
	previous := int64(-1)
	var closedCount int64
	ring := make([]StatisticsCandle, 160)
	for rowIndex := 0; ; rowIndex++ {
		if rowIndex%256 == 0 {
			if err := ctx.Err(); err != nil {
				return result, err
			}
		}
		limited.remaining = 1024*1024 + 65536
		before := reader.InputOffset()
		row, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || reader.InputOffset()-before > 1024*1024 {
			return result, fmt.Errorf("CSV row %d is malformed, has inconsistent columns or exceeds 1 MiB", rowIndex+2)
		}
		timestamp, err := parseTimestamp(row[fields["timestamp"]])
		aligned, alignment := timestamp%statisticsDay == 0, "UTC-midnight daily"
		switch period {
		case "h1":
			aligned, alignment = timestamp%3_600_000 == 0, "whole-hour UTC H1"
		case "w1":
			aligned, alignment = timestamp%statisticsDay == 0 && statisticsWeekStart(timestamp) == timestamp, "Monday 00:00 UTC W1"
		}
		if err != nil || timestamp <= previous || !aligned {
			return result, fmt.Errorf("CSV row %d requires a %s timestamp in strictly increasing order", rowIndex+2, alignment)
		}
		previous = timestamp
		if closedAt(timestamp, period) > asOf {
			continue
		}
		var values [4]float64
		for index, field := range []string{"open", "high", "low", "close"} {
			values[index], err = strconv.ParseFloat(strings.TrimSpace(row[fields[field]]), 64)
			if err != nil || !finite(values[index]) {
				return result, fmt.Errorf("CSV row %d %s must be a finite number", rowIndex+2, field)
			}
		}
		candle := StatisticsCandle{Time: timestamp, Open: values[0], High: values[1], Low: values[2], Close: values[3]}
		weekStart := statisticsWeekStart(timestamp)
		if closedCount == 0 {
			result.firstWeek = weekStart
		}
		ring[closedCount%160] = candle
		closedCount++
		result.anchor = candle
		if period != "d1" {
			result.checkpoints[timestamp] = candle
		}
		day := int((timestamp - weekStart) / statisticsDay)
		if period == "d1" && day < 5 {
			week := result.weeks[weekStart]
			if week == nil {
				week = &statisticsWeek{}
				result.weeks[weekStart] = week
			}
			week.days[day] = &candle
		}
	}
	if closedCount == 0 {
		if period == "d1" {
			return result, errors.New("the downloaded data has no fully closed daily candles")
		}
		return result, fmt.Errorf("the downloaded data has no fully closed %s candles", strings.ToUpper(period))
	}
	for index := max(int64(0), closedCount-160); index < closedCount; index++ {
		if candle := ring[index%160]; validStatisticsCandle(candle) {
			result.history = append(result.history, candle)
		} else {
			result.invalidHistory++
		}
	}
	result.snapshot = sourceSnapshot{checksum: hex.EncodeToString(hash.Sum(nil)), path: path, original: original}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := result.snapshot.unchanged(); err != nil {
		return result, err
	}
	return result, nil
}
