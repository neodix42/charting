package predictions

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func validPeriod(period string) bool {
	switch period {
	case "s1", "m1", "m5", "m15", "m30", "h1", "h4", "d1", "mn1":
		return true
	}
	return false
}

func closedAt(timestamp int64, period string) int64 {
	switch period {
	case "mn1":
		date := time.UnixMilli(timestamp).UTC()
		return time.Date(date.Year(), date.Month()+1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
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
	}
	return math.MaxInt64
}

func parseTimestamp(text string) (int64, error) {
	if value, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil && finite(value) && value >= 0 && value <= 253_402_300_799_999 && value == math.Trunc(value) {
		return int64(value), nil
	}
	if value, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text)); err == nil && value.UnixMilli() >= 0 && value.UnixMilli() <= 253_402_300_799_999 {
		return value.UnixMilli(), nil
	}
	return 0, errors.New("timestamp must be Unix milliseconds or an RFC3339 timestamp")
}

type sourceSnapshot struct {
	history  []Observation
	checksum string
	path     string
	original os.FileInfo
}

func (snapshot sourceSnapshot) unchanged() error {
	current, err := os.Lstat(snapshot.path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(current, snapshot.original) || current.Size() != snapshot.original.Size() || !current.ModTime().Equal(snapshot.original.ModTime()) {
		return errors.New("source CSV changed during prediction; run the forecast again")
	}
	return nil
}

// A bounded reader keeps a malformed quoted CSV field from allocating memory
// proportional to the source size. The CSV reader can read ahead one buffer;
// InputOffset enforces the exact 1 MiB record bound after each successful read.
type recordReader struct {
	input     io.Reader
	remaining int
}

func (reader *recordReader) Read(data []byte) (int, error) {
	if reader.remaining <= 0 {
		return 0, errors.New("CSV record exceeds 1 MiB")
	}
	data = data[:min(len(data), reader.remaining)]
	n, err := reader.input.Read(data)
	reader.remaining -= n
	return n, err
}

func readSource(ctx context.Context, dataset Dataset, selection Selection, asOf int64) (sourceSnapshot, error) {
	if !filepath.IsAbs(dataset.Directory) || filepath.Base(dataset.Filename) != dataset.Filename || dataset.Filename == "." || dataset.Filename == "" || strings.ContainsRune(dataset.Filename, 0) {
		return sourceSnapshot{}, errors.New("prediction source must identify an existing downloaded CSV")
	}
	path := filepath.Join(dataset.Directory, dataset.Filename)
	original, err := os.Lstat(path)
	if err != nil || !original.Mode().IsRegular() {
		return sourceSnapshot{}, errors.New("prediction source is no longer a regular CSV")
	}
	input, err := os.Open(path)
	if err != nil {
		return sourceSnapshot{}, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !os.SameFile(opened, original) {
		return sourceSnapshot{}, errors.New("prediction source changed before it could be opened")
	}
	hash := sha256.New()
	limited := &recordReader{input: io.TeeReader(input, hash), remaining: 1024*1024 + 65536}
	reader := csv.NewReader(limited)
	header, err := reader.Read()
	if err != nil || reader.InputOffset() > 1024*1024 {
		return sourceSnapshot{}, errors.New("CSV header is missing, malformed or larger than 1 MiB")
	}
	fields := map[string]int{}
	for i, field := range header {
		field = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(field, "\ufeff")))
		if _, exists := fields[field]; field == "" || exists {
			return sourceSnapshot{}, errors.New("CSV contains empty or duplicate field names")
		}
		fields[field] = i
	}
	timestampIndex, hasTime := fields["timestamp"]
	closeIndex, hasClose := fields["close"]
	if !hasTime || !hasClose {
		return sourceSnapshot{}, errors.New("prediction requires timestamp and close columns in candle data")
	}
	observations := make([]Observation, selection.ContextLength)
	var count int64
	previous := int64(-1)
	for rowIndex := 0; ; rowIndex++ {
		if rowIndex%256 == 0 {
			if err = ctx.Err(); err != nil {
				return sourceSnapshot{}, err
			}
		}
		limited.remaining = 1024*1024 + 65536
		before := reader.InputOffset()
		row, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || reader.InputOffset()-before > 1024*1024 {
			return sourceSnapshot{}, fmt.Errorf("CSV row %d is malformed, has inconsistent columns or exceeds 1 MiB", rowIndex+2)
		}
		timestamp, err := parseTimestamp(row[timestampIndex])
		if err != nil || timestamp <= previous {
			return sourceSnapshot{}, fmt.Errorf("CSV row %d requires a valid, strictly increasing timestamp", rowIndex+2)
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(row[closeIndex]), 64)
		if err != nil || !finite(value) {
			return sourceSnapshot{}, fmt.Errorf("CSV row %d close must be a finite number", rowIndex+2)
		}
		previous = timestamp
		if closedAt(timestamp, selection.Period) <= asOf {
			observations[count%int64(selection.ContextLength)] = Observation{Time: timestamp, Close: value}
			count++
		}
	}
	if count < int64(selection.ContextLength) {
		return sourceSnapshot{}, fmt.Errorf("%s %s has %d fully closed bars; download at least %d before running this forecast", strings.ToUpper(dataset.Symbol), dataset.Period, count, selection.ContextLength)
	}
	history := make([]Observation, selection.ContextLength)
	for i := range history {
		history[i] = observations[(count-int64(selection.ContextLength)+int64(i))%int64(selection.ContextLength)]
	}
	snapshot := sourceSnapshot{history: history, checksum: hex.EncodeToString(hash.Sum(nil)), path: path, original: original}
	if err = ctx.Err(); err != nil {
		return sourceSnapshot{}, err
	}
	if err = snapshot.unchanged(); err != nil {
		return sourceSnapshot{}, err
	}
	return snapshot, nil
}
