package predictions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCSVSnapshotStreamsLatestContextAndHashesAllOriginalBytes(t *testing.T) {
	start := time.Date(1973, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	directory := t.TempDir()
	dataset := Dataset{Symbol: "eurusd", Period: "h1", Filename: "history.csv", Directory: directory}
	data := "\ufeff" + candleCSV(start, 1026)
	path := filepath.Join(directory, dataset.Filename)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	selection := Selection{Asset: "eurusd", Period: "h1", ContextLength: 1024, Horizon: 20}
	asOf := start + 1024*3_600_000
	snapshot, err := readSource(context.Background(), dataset, selection, asOf)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256([]byte(data))
	if len(snapshot.history) != 1024 || snapshot.history[0].Time != start || snapshot.history[1023].Time != start+1023*3_600_000 || snapshot.history[1023].Close != 1033 || snapshot.checksum != hex.EncodeToString(checksum[:]) {
		t.Fatal("early Unix milliseconds, closed-bar cutoff or full source checksum changed", snapshot)
	}
	if _, err = readSource(context.Background(), dataset, selection, asOf-1); err == nil || !strings.Contains(err.Error(), "1023 fully closed") {
		t.Fatal("open candle entered an exact-boundary snapshot", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = readSource(ctx, dataset, selection, asOf); err == nil {
		t.Fatal("cancelled source scan continued")
	}
	if err = os.WriteFile(path, []byte(data+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = snapshot.unchanged(); err == nil {
		t.Fatal("source modification was not detected")
	}
}

func TestCSVRejectsMalformedOrderedAndFiniteCloseInputs(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	good := candleCSV(start, 513)
	lines := strings.Split(strings.TrimSpace(good), "\n")
	for _, tc := range []struct{ name, csv, message string }{
		{"insufficient", candleCSV(start, 511), "511 fully closed"},
		{"missing-close", strings.Replace(good, ",close,", ",other,", 1), "timestamp and close"},
		{"duplicate-header", strings.Replace(good, ",sma_200", ",close", 1), "duplicate field"},
		{"duplicate-time", strings.Replace(good, fmt.Sprint(start+3_600_000), fmt.Sprint(start), 1), "strictly increasing"},
		{"invalid-time", strings.Replace(good, fmt.Sprint(start), "unknown-time", 1), "strictly increasing"},
		{"blank-close", lines[0] + "\n" + fmt.Sprintf("%d,10,11,9,,1,\n", start) + strings.Join(lines[2:], "\n") + "\n", "finite number"},
		{"nan-close", lines[0] + "\n" + fmt.Sprintf("%d,10,11,9,NaN,1,\n", start) + strings.Join(lines[2:], "\n") + "\n", "finite number"},
		{"oversize-record", lines[0] + "\n" + fmt.Sprintf("%d,10,11,9,10,1,\"%s\"\n", start, strings.Repeat("x", 1024*1024+1)), "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			dataset := Dataset{Symbol: "eurusd", Period: "h1", Filename: "history.csv", Directory: directory}
			path := filepath.Join(directory, dataset.Filename)
			if err := os.WriteFile(path, []byte(tc.csv), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := readSource(context.Background(), dataset, Selection{Asset: "eurusd", Period: "h1", ContextLength: 512, Horizon: 1}, start+600*3_600_000)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("invalid source accepted or error unclear: %v", err)
			}
			if data, _ := os.ReadFile(path); string(data) != tc.csv {
				t.Fatal("invalid source was modified")
			}
		})
	}
}

func TestCSVTimestampAndMonthlyBoundaryAreUTC(t *testing.T) {
	for _, text := range []string{"94694400000", "1973-01-01T00:00:00Z", "1973-01-01T04:00:00+04:00"} {
		value, err := parseTimestamp(text)
		if err != nil || value != time.Date(1973, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() {
			t.Fatalf("timestamp was relabeled or shifted: %q => %d %v", text, value, err)
		}
	}
	for _, month := range []time.Month{time.January, time.February, time.December} {
		start := time.Date(2024, month, 1, 0, 0, 0, 0, time.UTC)
		want := start.AddDate(0, 1, 0).UnixMilli()
		if got := closedAt(start.UnixMilli(), "mn1"); got != want {
			t.Fatalf("monthly close did not follow the calendar: %s => %d", start, got)
		}
	}
}
