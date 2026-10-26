package screener

import (
	"crypto/sha256"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAnalysisIdenticalRequestedRangesPublishOneSetOfMatches(t *testing.T) {
	m := analysisTestManager(t)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	var csv strings.Builder
	csv.WriteString("timestamp,close\n")
	for i := 0; i < 655; i++ {
		close := 1
		if i >= 574 {
			close = 2
		}
		fmt.Fprintf(&csv, "%d,%d\n", start+int64(i)*86_400_000, close)
	}
	canonical := "xagaed-d1-2006-01-01-2026-10-25.csv"
	duplicate := "xagaed-d1-2010-01-01-2026-10-25.csv"
	canonicalPath := writeDataFile(t, m, canonical, csv.String())
	duplicatePath := writeDataFile(t, m, duplicate, csv.String())
	info := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), analysisNumber(1)))
	if info.Total != 2 || info.Completed != 2 || info.Failed != 0 || info.TotalResults != 1 || info.MatchedFiles != 1 || info.TotalMatches != 81 || info.Rows != 655 {
		t.Fatalf("duplicate counted as another history: %+v", info)
	}
	r := info.Results[0]
	if r.Name != canonical || r.Rows != 655 || r.Matches != 81 || r.AssetFiles != 1 || r.AssetMatches != 81 {
		t.Fatalf("canonical result: %+v", r)
	}
	for occurrence := 0; occurrence < 81; occurrence++ {
		chart, err := m.AnalysisChart(r.ID, occurrence, 0, 0)
		if err != nil || chart.MatchIndex != int64(574+occurrence) || chart.TotalMatches != 81 {
			t.Fatalf("occurrence %d: %+v %v", occurrence, chart, err)
		}
	}
	if _, err := m.AnalysisChart(dataFileID(m.directory, duplicate), 0, 0, 0); err == nil {
		t.Fatal("hidden duplicate exposed another set of occurrences")
	}
	if len(m.analysis.series) != 2 || len(m.analysis.matchFiles) != 1 {
		t.Fatal("duplicate companion was removed or scanned again")
	}
	for _, path := range []string{canonicalPath, duplicatePath} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != csv.String() {
			t.Fatalf("analysis changed an original download: %s %v", path, err)
		}
	}
	// Reapplying keeps a stable canonical filename and match order.
	again := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), analysisNumber(1)))
	if !reflect.DeepEqual(again.Results, info.Results) {
		t.Fatalf("canonical result changed between scans: %+v", again.Results)
	}
}

func TestAnalysisContentIdentityNormalizesFormattingButPreservesDistinctHistories(t *testing.T) {
	m := analysisTestManager(t)
	text := "timestamp,close,volume,sma_3\n1704067200000,2,0,\n1704067260000,4,10,3\n"
	canonical := "btcusd-m1-2000-01-01-2024-01-02.csv"
	writeDataFile(t, m, canonical, text)
	writeDataFile(t, m, "btcusd-m1-2001-01-01-2024-01-02.csv", "SMA_3, volume , CLOSE , TIMESTAMP\n,-0.0,2e0,2024-01-01T00:00:00Z\n3.000,1e1,4.000,2024-01-01T00:01:00Z\n")
	// Even fields not used in this rule remain part of the identity. Identical
	// match timestamps alone must not hide different enrichments or prices.
	writeDataFile(t, m, "btcusd-m1-2002-01-01-2024-01-02.csv", strings.Replace(text, ",4,10,3", ",4,10,3.5", 1))
	writeDataFile(t, m, "btcusd-m1-2003-01-01-2024-01-02.csv", "timestamp,close,volume,sma_3,extra\n1704067200000,2,0,,1\n1704067260000,4,10,3,1\n")
	writeDataFile(t, m, "btcusd-m1-2004-01-01-2024-01-02.csv", text+"1704067320000,5,10,3\n")
	writeDataFile(t, m, "btcusd-m1-2005-01-01-2024-01-02.csv", strings.Replace(text, "1704067260000", "1704067320000", 1))
	writeDataFile(t, m, "btcusd-m1-2006-01-01-2024-01-02.csv", strings.Replace(text, ",4,10,3", ",5,10,3", 1))
	writeDataFile(t, m, "btcusd-h1-2000-01-01-2024-01-02.csv", text)
	writeDataFile(t, m, "ethusd-m1-2000-01-01-2024-01-02.csv", text)
	info := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), analysisNumber(0)))
	if info.Total != 9 || info.Completed != 9 || info.Failed != 0 || info.TotalResults != 8 || info.TotalMatches != 17 || info.Rows != 17 {
		t.Fatalf("distinct data lost or formatting duplicate retained: %+v", info)
	}
	for _, r := range info.Results {
		if strings.Contains(r.Name, "2001-01-01") {
			t.Fatal("equivalent formatting published twice")
		}
		if _, err := m.AnalysisChart(r.ID, 0, 0, 0); err != nil {
			t.Fatalf("preserved result has no usable chart: %+v %v", r, err)
		}
	}
	if r := resultNamed(t, info, canonical); r.AssetFiles != 7 || r.AssetMatches != 15 {
		t.Fatalf("asset totals counted aliases: %+v", r)
	}
}

func TestAnalysisDuplicateRemovalPrecedesCategoryPagingAndChartNavigation(t *testing.T) {
	m := analysisTestManager(t)
	crypto, forex := categoryForSymbol(t, "btcusd"), categoryForSymbol(t, "eurusd")
	var selected []Instrument
	for _, group := range loadCatalog().Groups {
		if group.Name == crypto {
			selected = append(selected, group.Instruments[:12]...)
		}
		if group.Name == forex {
			selected = append(selected, group.Instruments[:2]...)
		}
	}
	for _, instrument := range selected {
		for _, year := range []int{2000, 2001} {
			writeDataFile(t, m, fmt.Sprintf("%s-m1-%d-01-01-2024-01-02.csv", instrument.ID, year), analysisCSV([]float64{2, 3, 4}))
		}
	}
	info := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), analysisNumber(1)))
	if info.Total != 28 || info.Completed != 28 || info.TotalResults != 14 || info.MatchedFiles != 14 || info.TotalMatches != 42 {
		t.Fatalf("raw input and unique-result totals: %+v", info)
	}
	for _, includeEmpty := range []bool{false, true} {
		filter := AnalysisFilter{Categories: []string{crypto}, IncludeEmpty: includeEmpty}
		first, err := m.AnalysisFiltered(0, 10, filter)
		if err != nil || first.TotalResults != 12 || len(first.Results) != 10 {
			t.Fatalf("first canonical page: %+v %v", first, err)
		}
		second, err := m.AnalysisFiltered(10, 10, filter)
		if err != nil || second.TotalResults != 12 || len(second.Results) != 2 {
			t.Fatalf("second canonical page: %+v %v", second, err)
		}
		seen := map[string]bool{}
		for _, r := range append(first.Results, second.Results...) {
			if seen[r.Symbol] || r.Category != crypto || !strings.Contains(r.Name, "2000-01-01") || r.AssetFiles != 1 || r.AssetMatches != 3 {
				t.Fatalf("duplicate shifted filtered chart navigation: %+v", r)
			}
			seen[r.Symbol] = true
			for _, occurrence := range []int{0, 2} {
				chart, chartErr := m.AnalysisChart(r.ID, occurrence, 0, 0)
				if chartErr != nil || chart.MatchIndex != int64(occurrence) || chart.TotalMatches != 3 {
					t.Fatalf("filtered canonical occurrence: %+v %v", chart, chartErr)
				}
			}
		}
		last, err := m.AnalysisFiltered(12, 10, filter)
		if err != nil || len(last.Results) != 0 || last.TotalResults != 12 {
			t.Fatalf("aliases remained beyond final page: %+v %v", last, err)
		}
	}
}

func TestAnalysisDuplicateCompanionsRetainVerifiedCoverageAndStaleChecks(t *testing.T) {
	m := analysisTestManager(t)
	day := int64(86_400_000)
	start := time.Date(2024, 1, 24, 0, 0, 0, 0, time.UTC).UnixMilli()
	daily := fmt.Sprintf("timestamp,close,macd_histogram_12_26_9\n%d,100,2\n", start)
	writeDataFile(t, m, "btcusd-d1-2000-01-01-2024-01-02.csv", daily)
	aliasName := "btcusd-d1-2001-01-01-2024-01-02.csv"
	aliasPath := writeDataFile(t, m, aliasName, daily)
	stat, err := os.Stat(aliasPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(daily))
	metadata := fileMetadata{Verified: true, Bytes: stat.Size(), Modified: stat.ModTime().UnixNano(), Checksum: fmt.Sprintf("%x", digest), CheckedThrough: start + 5*day}
	if err = writeFileMetadata(m.directory, aliasName, metadata); err != nil {
		t.Fatal(err)
	}
	minuteName := "btcusd-m1-2024-01-28-2024-01-29.csv"
	writeDataFile(t, m, minuteName, fmt.Sprintf("timestamp,close\n%d,101\n", start+4*day+12*3_600_000))
	right := analysisField("macd_histogram_12_26_9")
	right.Timeframe = "d1"
	info := startTestAnalysis(t, m, analysisCondition("above", right, analysisNumber(0)))
	r := resultNamed(t, info, minuteName)
	if info.TotalResults != 2 || r.Matches != 1 || len(m.analysis.series) != 3 {
		t.Fatalf("deduplication lost verified closure companion: %+v", info)
	}
	chart, err := m.AnalysisChart(r.ID, 0, 0, 0)
	if err != nil || chart.Bars[0].Values["d1:macd_histogram_12_26_9"] != 2 {
		t.Fatalf("canonical companion chart: %+v %v", chart, err)
	}
	if err = os.WriteFile(aliasPath, []byte(strings.Replace(daily, ",100,2", ",100,3", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.AnalysisChart(r.ID, 0, 0, 0); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed duplicate companion accepted stale match: %v", err)
	}
}
