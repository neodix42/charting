package screener

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

func periodTimestamp(text string) (int64, error) {
	text = strings.TrimSpace(text)
	if value, err := strconv.ParseFloat(text, 64); err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value && math.Abs(value) <= 8640000000000000 {
		// Dukascopy timestamps are milliseconds, including early/epoch dates.
		return int64(value), nil
	}
	if value, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return value.UnixMilli(), nil
	}
	return 0, errors.New("CSV period requires valid Unix millisecond or RFC3339 timestamps")
}

func csvPeriod(input *os.File, reader *csv.Reader, header []string, size, dataStart int64) (*int64, *int64, error) {
	index := -1
	for i, field := range header {
		if strings.EqualFold(strings.TrimSpace(field), "timestamp") {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, nil, nil
	}
	if dataStart > size {
		return nil, nil, errors.New("CSV changed while reading its period; reload the file list")
	}
	firstRow, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read first CSV timestamp: %w", err)
	}
	first, err := periodTimestamp(firstRow[index])
	if err != nil {
		return nil, nil, err
	}
	// Standard downloaded/enriched rows fit easily in 64 KiB. Expand for wide
	// CSVs, with a hard 1 MiB bound instead of scanning gigabytes on each poll.
	for limit := int64(64 * 1024); limit <= 1024*1024; limit *= 4 {
		start := max(dataStart, size-limit)
		data := make([]byte, size-start)
		if _, err := input.ReadAt(data, start); err != nil {
			return nil, nil, fmt.Errorf("read last CSV timestamp: %w", err)
		}
		if start > dataStart {
			boundary := bytes.IndexByte(data, '\n')
			if boundary < 0 {
				continue
			}
			data = data[boundary+1:]
		}
		tail := csv.NewReader(bytes.NewReader(data))
		tail.FieldsPerRecord = len(header)
		var lastRow []string
		for {
			row, readErr := tail.Read()
			if errors.Is(readErr, io.EOF) {
				if lastRow != nil {
					last, err := periodTimestamp(lastRow[index])
					if err != nil {
						return nil, nil, err
					}
					if last < first {
						return nil, nil, errors.New("CSV end timestamp precedes its first row")
					}
					return &first, &last, nil
				}
				break
			}
			if readErr != nil {
				if start == dataStart {
					return nil, nil, fmt.Errorf("read last CSV timestamp: %w", readErr)
				}
				break
			}
			lastRow = row
		}
	}
	return nil, nil, errors.New("CSV final record is invalid or larger than 1 MiB")
}
