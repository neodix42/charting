package screener

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFilePeriodCountsCoverEveryPageAndOnlyPublishedRecognizedHistories(t *testing.T) {
	m := New(t.TempDir())
	defer m.Close()
	if err := os.MkdirAll(m.directory, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		writeDataFile(t, m, fmt.Sprintf("btcusd-m1-2024-01-01-2024-02-01-%d.csv", i), candleCSV)
	}
	for _, name := range []string{"ETHUSD-H1-2024-01-01-2024-02-01.CSV", "ethusd-d1-2024-01-01-2024-02-01.csv", "custom.csv", ".eurusd-d1-2024-01-01-2024-02-01.csv"} {
		writeDataFile(t, m, name, candleCSV)
	}
	if err := os.Mkdir(filepath.Join(m.directory, "eurusd-d1-2024-01-01-2024-02-01.csv"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(m.directory, "custom.csv"), filepath.Join(m.directory, "audusd-d1-2024-01-01-2024-02-01.csv")); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"m1": 25, "h1": 1, "d1": 1}
	for _, offset := range []int{0, 20, 100} {
		page, err := m.Files(offset, 20)
		if err != nil || page.Total != 28 || !reflect.DeepEqual(page.PeriodCounts, want) {
			t.Fatalf("page %d lost off-page period counts: %+v %v", offset, page, err)
		}
		page.PeriodCounts["m1"] = 0
	}
}

func TestFilePeriodsReadActualCSVEndpointsWithoutRefreshMetadata(t *testing.T) {
	for _, test := range []struct {
		name, text  string
		first, last int64
	}{
		{"daily", "timestamp,close\n1704067200000,1\n1704240000000,2\n", 1704067200000, 1704240000000},
		{"bom-crlf", "\ufefftimestamp,close\r\n\"1704067200000\",1\r\n1704240000000,2\r\n\r\n", 1704067200000, 1704240000000},
		{"reordered-iso-no-newline", "close,Timestamp\n1,2024-01-01T02:00:00+02:00\n2,2024-01-03T00:00:00Z", 1704067200000, 1704240000000},
		{"epoch", "timestamp,close\n0,1\n0,2\n", 0, 0},
		{"negative", "timestamp,close\n-1,1\n0,2\n", -1, 0},
		{"early-milliseconds", "timestamp,close\n94694400000,1\n94780800000,2", 94694400000, 94780800000},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			name := "eurusd-d1-2010-01-01-2026-10-27.csv"
			if err := os.WriteFile(filepath.Join(directory, name), []byte(test.text), 0600); err != nil {
				t.Fatal(err)
			}
			file := describeFile(directory, name)
			if file.Error != "" || file.FirstTimestamp == nil || file.LastTimestamp == nil || *file.FirstTimestamp != test.first || *file.LastTimestamp != test.last {
				t.Fatalf("actual CSV endpoints: %+v", file)
			}
			encoded, _ := json.Marshal(file)
			if !strings.Contains(string(encoded), `"firstTimestamp":`) || !strings.Contains(string(encoded), `"lastTimestamp":`) {
				t.Fatalf("epoch omitted: %s", encoded)
			}
		})
	}
}

func TestFilePeriodFollowsAppendedCSVInsteadOfFilenameOrSavedMetadata(t *testing.T) {
	directory, name := t.TempDir(), "eurusd-d1-2024-01-01-2024-01-02.csv"
	full := filepath.Join(directory, name)
	text := "timestamp,close,sma_2\n1704067200000,1,\n1704153600000,2,1.5\n"
	if err := os.WriteFile(full, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(full)
	// Even matching metadata is not the authority for the period in the table.
	if err := writeFileMetadata(directory, name, fileMetadata{Bytes: st.Size(), Modified: st.ModTime().UnixNano(), FirstTimestamp: 123, LastTimestamp: 456}); err != nil {
		t.Fatal(err)
	}
	file := describeFile(directory, name)
	if file.Error != "" || file.LastTimestamp == nil || *file.LastTimestamp != 1704153600000 {
		t.Fatalf("stale metadata period: %+v", file)
	}
	if err := os.WriteFile(full, []byte(text+"1704240000000,3,2.5\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file = describeFile(directory, name)
	if file.Error != "" || file.LastTimestamp == nil || *file.LastTimestamp != 1704240000000 || file.To != "2024-01-02" {
		t.Fatalf("appended CSV period: %+v", file)
	}
}

func TestFilePeriodUsesBoundedReadsForLargeHistoriesAndWideRecords(t *testing.T) {
	directory := t.TempDir()
	full := filepath.Join(directory, "large.csv")
	input, err := os.Create(full)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err := input.WriteString("timestamp,close\n1704067200000,1\n"); err != nil {
		t.Fatal(err)
	}
	// A sparse interior cannot be parsed as CSV. Listing must read the endpoints,
	// and must not scan or allocate this 256 MiB history to find the final bar.
	if _, err := input.WriteAt([]byte("\n1704240000000,2\n"), 256*1024*1024); err != nil {
		t.Fatal(err)
	}
	file := describeFile(directory, "large.csv")
	if file.Error != "" || file.LastTimestamp == nil || *file.LastTimestamp != 1704240000000 {
		t.Fatalf("large period: %+v", file)
	}
	wide := "timestamp,note\n1704067200000,first\n1704240000000," + strings.Repeat("x", 100000)
	if err := os.WriteFile(filepath.Join(directory, "wide.csv"), []byte(wide), 0600); err != nil {
		t.Fatal(err)
	}
	file = describeFile(directory, "wide.csv")
	if file.Error != "" || file.LastTimestamp == nil || *file.LastTimestamp != 1704240000000 {
		t.Fatalf("wide period: %+v", file)
	}
}

func TestUnknownOrInvalidCSVPeriodDoesNotInventFilenameDates(t *testing.T) {
	for _, text := range []string{"timestamp,close\n", "close\n1\n", "timestamp,close\ninvalid,1\n", "timestamp,close\n1704067200000,1\nNaN,2\n", "timestamp,close\n1704067200000,1\n1704240000000,2,3\n"} {
		directory, name := t.TempDir(), "eurusd-d1-2024-01-01-2024-02-01.csv"
		if err := os.WriteFile(filepath.Join(directory, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		file := describeFile(directory, name)
		if file.FirstTimestamp != nil || file.LastTimestamp != nil {
			t.Fatalf("invented period: %+v", file)
		}
	}
}

func TestCSVPeriodRejectsTruncationDuringListingWithoutPanicking(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "changed.csv")
	if err := os.WriteFile(filename, []byte("timestamp,close\n1704067200000,1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input, err := os.OpenFile(filename, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	reader := csv.NewReader(input)
	header, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := input.Truncate(1); err != nil {
		t.Fatal(err)
	}
	info, err := input.Stat()
	if err != nil {
		t.Fatal(err)
	}
	first, last, err := csvPeriod(input, reader, header, info.Size(), reader.InputOffset())
	if err == nil || first != nil || last != nil {
		t.Fatalf("truncated CSV produced a period: %v %v %v", first, last, err)
	}
}

func TestDownloadedSymbolsUsePublishedFilesAcrossRestartAndFolderChanges(t *testing.T) {
	m := New(t.TempDir())
	defer m.Close()
	if err := os.MkdirAll(m.directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"btcusd-d1-2024-01-01-2024-02-01.csv", "btcusd-m1-2024-01-01-2024-02-01-2.csv", "ETHUSD-D1-2024-01-01-2024-02-01.CSV", "custom.csv", ".eurusd-d1-2024-01-01-2024-02-01.csv"} {
		writeDataFile(t, m, name, candleCSV)
	}
	if err := os.Mkdir(filepath.Join(m.directory, "eurusd-d1-2024-01-01-2024-02-01.csv"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(m.directory, "custom.csv"), filepath.Join(m.directory, "xauusd-d1-2024-01-01-2024-02-01.csv")); err != nil {
		t.Fatal(err)
	}
	wanted := []string{"btcusd", "ethusd"}
	if got := m.Info().DownloadedSymbols; !reflect.DeepEqual(got, wanted) {
		t.Fatalf("downloaded symbols: %v", got)
	}
	copy := m.Info()
	copy.DownloadedSymbols[0] = "mutated"
	restarted := New(m.profile)
	defer restarted.Close()
	if got := restarted.Info().DownloadedSymbols; !reflect.DeepEqual(got, wanted) {
		t.Fatalf("restart lost existing assets: %v", got)
	}
	if err := os.Remove(filepath.Join(m.directory, "ETHUSD-D1-2024-01-01-2024-02-01.CSV")); err != nil {
		t.Fatal(err)
	}
	if got := m.Info().DownloadedSymbols; !reflect.DeepEqual(got, []string{"btcusd"}) {
		t.Fatalf("deleted asset remained: %v", got)
	}
	info, err := m.ConfigureDirectory(t.TempDir())
	if err != nil || info.DownloadedSymbols == nil || len(info.DownloadedSymbols) != 0 {
		t.Fatalf("old folder assets retained: %+v %v", info, err)
	}
}

func TestFilesForPeriodFiltersMixedInventoryBeforePaginationAndKeepsGlobalCounts(t *testing.T) {
	m := New(t.TempDir())
	defer m.Close()
	if err := os.MkdirAll(m.directory, 0700); err != nil {
		t.Fatal(err)
	}
	for i := range 27 {
		for _, period := range []string{"d1", "w1"} {
			writeDataFile(t, m, fmt.Sprintf("asset%03d-%s-2024-01-01-2024-02-01.csv", i, period), candleCSV)
		}
	}
	writeDataFile(t, m, "EURUSD-W1-2024-01-01-2024-02-01.CSV", candleCSV)
	writeDataFile(t, m, "custom.csv", candleCSV)
	wantCounts := map[string]int{"d1": 27, "w1": 28}
	seen := map[string]bool{}
	for _, offset := range []int{0, 20, 40} {
		page, err := m.FilesForPeriod(offset, 20, "w1")
		wantRows := 20
		if offset == 20 {
			wantRows = 8
		} else if offset == 40 {
			wantRows = 0
		}
		if err != nil || page.Total != 28 || page.AllTotal != 56 || page.Offset != offset || page.Limit != 20 || len(page.Files) != wantRows || !reflect.DeepEqual(page.PeriodCounts, wantCounts) {
			t.Fatalf("weekly page %d has wrong filtered/global counts: %+v %v", offset, page, err)
		}
		for _, file := range page.Files {
			if file.Timeframe != "w1" || seen[file.ID] || file.Error != "" || len(file.Fields) == 0 {
				t.Fatalf("weekly page included a duplicate/nonweekly/unreadable history: %+v", file)
			}
			seen[file.ID] = true
		}
	}
	if len(seen) != 28 {
		t.Fatalf("weekly pagination lost files: %d", len(seen))
	}
	for _, period := range []string{"", "all"} {
		page, err := m.FilesForPeriod(40, 20, period)
		if err != nil || page.Total != 56 || page.AllTotal != 56 || len(page.Files) != 16 || !reflect.DeepEqual(page.PeriodCounts, wantCounts) {
			t.Fatalf("All periods page lost global inventory: %+v %v", page, err)
		}
		if page.Files[len(page.Files)-1].Name != "custom.csv" {
			t.Fatalf("All periods omitted custom CSV: %+v", page.Files)
		}
	}
	legacy, err := m.Files(40, 20)
	all, allErr := m.FilesForPeriod(40, 20, "all")
	if err != nil || allErr != nil || !reflect.DeepEqual(legacy, all) {
		t.Fatalf("legacy Files call changed its inventory semantics: %+v %+v %v %v", legacy, all, err, allErr)
	}
	missing, err := m.FilesForPeriod(0, 20, "h1")
	if err != nil || missing.Total != 0 || missing.AllTotal != 56 || len(missing.Files) != 0 || !reflect.DeepEqual(missing.PeriodCounts, wantCounts) {
		t.Fatalf("empty period lost total inventory counts: %+v %v", missing, err)
	}
}

func TestFilesForPeriodValidatesPeriodAndPageWithoutReadingUnrelatedFiles(t *testing.T) {
	m := New(t.TempDir())
	defer m.Close()
	if err := os.MkdirAll(m.directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, period := range []string{"1w", "W1", " d1", "unknown"} {
		if _, err := m.FilesForPeriod(0, 20, period); err == nil {
			t.Fatalf("accepted unsupported file period %q", period)
		}
	}
	for _, bounds := range [][2]int{{-1, 20}, {0, 0}, {0, 201}} {
		if _, err := m.FilesForPeriod(bounds[0], bounds[1], "w1"); err == nil {
			t.Fatalf("accepted invalid file page %v", bounds)
		}
	}
	// Bad data from another period or a custom file must not become a visible
	// weekly error, including when the matching period has no files.
	writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-02-01.csv", "\"unterminated")
	writeDataFile(t, m, "custom.csv", "\"unterminated")
	page, err := m.FilesForPeriod(0, 20, "w1")
	if err != nil || page.Total != 0 || page.AllTotal != 2 || len(page.Files) != 0 || page.PeriodCounts["d1"] != 1 {
		t.Fatalf("unrelated malformed files leaked into weekly page: %+v %v", page, err)
	}
}
