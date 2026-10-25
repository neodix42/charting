package screener

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func analysisTestManager(t *testing.T) *Manager {
	t.Helper()
	m := newManager(t.TempDir(), func() (workerRuntime, error) { return workerRuntime{}, errors.New("analysis does not require Node") })
	t.Cleanup(m.Close)
	if err := os.MkdirAll(m.Info().Directory, 0700); err != nil {
		t.Fatal(err)
	}
	return m
}
func analysisNumber(value float64) AnalysisOperand {
	return AnalysisOperand{Kind: "number", Value: value}
}
func analysisField(field string) AnalysisOperand {
	return AnalysisOperand{Kind: "field", Field: field, Timeframe: "same", Aggregate: "value", Window: 1}
}
func analysisCondition(operator string, left, right AnalysisOperand) AnalysisCondition {
	return AnalysisCondition{ID: "first", Operator: operator, Left: left, Right: right, Multiplier: 1, Bars: 3}
}
func analysisCSV(values []float64) string {
	var b strings.Builder
	b.WriteString("timestamp,open,high,low,close,volume,sma_3,atr_3\n")
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for i, value := range values {
		fmt.Fprintf(&b, "%d,%g,%g,%g,%g,10,3,2\n", start+int64(i)*60000, value, value+1, value-0.5, value)
	}
	return b.String()
}
func waitAnalysis(t *testing.T, m *Manager) AnalysisInfo {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		info, err := m.Analysis(0, 200)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Active {
			return info
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("analysis did not finish")
	return AnalysisInfo{}
}
func startTestAnalysis(t *testing.T, m *Manager, conditions ...AnalysisCondition) AnalysisInfo {
	t.Helper()
	if _, err := m.StartAnalysis(AnalysisRequest{Conditions: conditions}); err != nil {
		t.Fatal(err)
	}
	return waitAnalysis(t, m)
}
func resultNamed(t *testing.T, info AnalysisInfo, name string) AnalysisResult {
	t.Helper()
	for _, r := range info.Results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("missing %s in %+v", name, info)
	return AnalysisResult{}
}
func TestAnalysisEveryOperatorAndPreviousBarsUseProductionCSV(t *testing.T) {
	m := analysisTestManager(t)
	name := "btcusd-m1-2024-01-01-2024-01-02.csv"
	writeDataFile(t, m, name, analysisCSV([]float64{1, 2, 4, 5, 3, 6, 2}))
	windowField := analysisField("close")
	windowField.Offset = 1
	windowField.Aggregate = "highest"
	windowField.Window = 2
	previous := analysisField("close")
	previous.Offset = 1
	tests := []struct {
		name      string
		condition AnalysisCondition
		indices   []int64
	}{
		{"above", analysisCondition("above", analysisField("close"), analysisField("sma_3")), []int64{2, 3, 5}},
		{"below", analysisCondition("below", analysisField("close"), analysisNumber(3)), []int64{0, 1, 6}},
		{"cross above", analysisCondition("crossesAbove", analysisField("close"), analysisNumber(3)), []int64{2, 5}},
		{"cross below equality", analysisCondition("crossesBelow", analysisField("close"), analysisNumber(3)), []int64{6}},
		{"earlier bar", analysisCondition("above", analysisField("close"), previous), []int64{1, 2, 3, 5}},
		{"previous window operand", analysisCondition("above", analysisField("close"), windowField), []int64{2, 3, 5}},
		{"between inclusive", func() AnalysisCondition {
			c := analysisCondition("between", analysisField("close"), analysisNumber(2))
			c.Upper = analysisNumber(4)
			return c
		}(), []int64{1, 2, 4, 6}},
		{"distance ATR", func() AnalysisCondition {
			c := analysisCondition("withinDistance", analysisField("close"), analysisField("sma_3"))
			c.Distance = analysisField("atr_3")
			c.Multiplier = 0.5
			return c
		}(), []int64{1, 2, 4, 6}},
		{"occurred 3 including now", func() AnalysisCondition {
			c := analysisCondition("occurredWithin", analysisField("close"), analysisNumber(3))
			c.Comparison = "crossesAbove"
			return c
		}(), []int64{2, 3, 4, 5, 6}},
		{"remained 2", func() AnalysisCondition {
			c := analysisCondition("remainedFor", analysisField("close"), analysisNumber(3))
			c.Comparison = "above"
			c.Bars = 2
			return c
		}(), []int64{3}},
		{"highest previous2", func() AnalysisCondition {
			c := analysisCondition("highest", analysisField("close"), analysisField("close"))
			c.Bars = 2
			return c
		}(), []int64{2, 3, 5}},
		{"lowest previous2", func() AnalysisCondition {
			c := analysisCondition("lowest", analysisField("close"), analysisField("close"))
			c.Bars = 2
			return c
		}(), []int64{4, 6}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := startTestAnalysis(t, m, test.condition)
			if info.Failed != 0 || len(info.Results) != 1 {
				t.Fatalf("scan: %+v", info)
			}
			r := info.Results[0]
			if r.Matches != int64(len(test.indices)) {
				t.Fatalf("matches %+v want%v", r, test.indices)
			}
			var got []int64
			for i := range test.indices {
				chart, err := m.AnalysisChart(r.ID, i, 1, 1)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, chart.MatchIndex)
				if chart.Bars[chart.MatchIndex-chart.StartIndex].Values["close"] == 0 {
					t.Fatal("missing candle")
				}
			}
			if !reflect.DeepEqual(got, test.indices) {
				t.Fatalf("matches %v want%v", got, test.indices)
			}
		})
	}
}
func TestAnalysisRowsAreANDAndWarmupBlanksNeverBecomeZero(t *testing.T) {
	m := analysisTestManager(t)
	text := strings.Replace(analysisCSV([]float64{1, 2, 4, 5, 3, 6, 2}), ",10,3,2", ",10,,2", 1)
	writeDataFile(t, m, "btcusd-m1-2024-01-01-2024-01-02.csv", text)
	a := analysisCondition("above", analysisField("close"), analysisNumber(3))
	b := analysisCondition("below", analysisField("close"), analysisNumber(6))
	b.ID = "second"
	info := startTestAnalysis(t, m, a, b)
	if info.TotalMatches != 2 {
		t.Fatalf("AND %+v", info)
	}
	c := analysisCondition("above", analysisField("sma_3"), analysisNumber(-1))
	info = startTestAnalysis(t, m, c)
	if info.TotalMatches != 6 {
		t.Fatalf("blank warmup counted %+v", info)
	}
}
func TestAnalysisHigherTimeframeUsesOnlyClosedSameAssetBarsAndDeterministicOverlap(t *testing.T) {
	m := analysisTestManager(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-04.csv", fmt.Sprintf("timestamp,close,macd_histogram_12_26_9\n%d,100,10\n%d,200,20\n%d,300,30\n", base, base+86400000, base+2*86400000))
	// Same timestamp conflict: lexical earliest name consistently wins.
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-04-2.csv", fmt.Sprintf("timestamp,close,macd_histogram_12_26_9\n%d,900,-1\n%d,900,-1\n%d,900,-1\n", base, base+86400000, base+2*86400000))
	// A different asset must never satisfy BTC operands.
	writeDataFile(t, m, "ethusd-d1-2024-01-01-2024-01-04.csv", fmt.Sprintf("timestamp,close,macd_histogram_12_26_9\n%d,900,1000\n%d,900,1000\n", base, base+86400000))
	name := "btcusd-m1-2024-01-01-2024-01-04.csv"
	writeDataFile(t, m, name, fmt.Sprintf("timestamp,close\n%d,101\n%d,101\n%d,101\n%d,201\n", base+60_000, base+86400000-60000, base+86400000+60000, base+2*86400000-60000))
	daily := analysisField("close")
	daily.Timeframe = "d1"
	info := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), daily))
	r := resultNamed(t, info, name)
	// The -2 name precedes .csv lexically and wins the intentional overlap.
	if r.Matches != 0 || r.Status != "completed" {
		t.Fatalf("deterministic overlap %+v", r)
	}
	if err := os.Remove(filepath.Join(m.Info().Directory, "btcusd-d1-2024-01-01-2024-01-04-2.csv")); err != nil {
		t.Fatal(err)
	}
	info = startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), daily))
	r = resultNamed(t, info, name)
	if r.Matches != 3 {
		t.Fatalf("closed daily alignment %+v", r)
	}
	first, err := m.AnalysisChart(r.ID, 0, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.MatchIndex != 1 {
		t.Fatalf("future daily candle leaked into first minute: %+v", first)
	}
	if _, exists := first.Bars[0].Values["d1:close"]; exists {
		t.Fatal("popup exposed not-yet-closed daily value")
	}
	if first.Bars[1].Values["d1:close"] != 100 {
		t.Fatalf("popup lost aligned daily value %+v", first.Bars)
	}
	// Last match uses Jan2 D1 only once its close is reached, at Jan3 00:00.
	last, err := m.AnalysisChart(r.ID, 2, 0, 0)
	if err != nil || last.MatchIndex != 3 {
		t.Fatalf("final close: %+v %v", last, err)
	}
}
func TestAnalysisMonthlyBoundariesAndOpenCandlesExcluded(t *testing.T) {
	jan := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	feb := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	march := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	if analysisClosedAt(jan, "mn1") != feb || analysisClosedAt(feb, "mn1") != march {
		t.Fatal("calendar month close broken")
	}
	m := analysisTestManager(t)
	future := time.Now().UTC().Add(time.Hour).UnixMilli()
	writeDataFile(t, m, "btcusd-m1-2024-01-01-2099-01-01.csv", fmt.Sprintf("timestamp,close\n%d,2\n%d,3\n", jan, future))
	info := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), analysisNumber(0)))
	if info.TotalMatches != 1 {
		t.Fatalf("open/future candle matched %+v", info)
	}
	if info.Rows != 1 || info.Results[0].Rows != 1 {
		t.Fatalf("nonclosed rows included in scan counts %+v", info)
	}
	chart, err := m.AnalysisChart(info.Results[0].ID, 0, 0, 500)
	if err != nil || len(chart.Bars) != 1 {
		t.Fatalf("popup exposed future candles %+v %v", chart, err)
	}
}
func TestAnalysisPaginationAllMatchesAndChangedCSVRejectsChart(t *testing.T) {
	m := analysisTestManager(t)
	values := make([]float64, 1305)
	for i := range values {
		values[i] = float64(i + 1)
	}
	name := "btcusd-m1-2024-01-01-2024-02-01.csv"
	path := writeDataFile(t, m, name, analysisCSV(values))
	writeDataFile(t, m, "ethusd-m1-2024-01-01-2024-02-01.csv", analysisCSV([]float64{1}))
	info := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), analysisNumber(0)))
	if info.TotalMatches != 1306 {
		t.Fatalf("matches capped %+v", info)
	}
	page, err := m.Analysis(1, 1)
	if err != nil || len(page.Results) != 1 || page.TotalResults != 2 || page.Results[0].Symbol != "ethusd" {
		t.Fatalf("page %+v %v", page, err)
	}
	r := resultNamed(t, info, name)
	chart, err := m.AnalysisChart(r.ID, 1304, 500, 500)
	if err != nil || chart.MatchIndex != 1304 || len(chart.Bars) != 501 {
		t.Fatalf("uncapped occurrence %+v %v", chart, err)
	}
	if err = os.WriteFile(path, []byte(analysisCSV([]float64{1, 2})), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.AnalysisChart(r.ID, 0, 1, 1); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale index accepted %v", err)
	}
	if _, err = m.Analysis(-1, 10); err == nil {
		t.Fatal("negative page accepted")
	}
	if _, err = m.AnalysisChart(r.ID, 0, 501, 0); err == nil {
		t.Fatal("oversize chart accepted")
	}
}
func TestAnalysisMissingFieldsMalformedFilesAndTickPriceAlias(t *testing.T) {
	m := analysisTestManager(t)
	writeDataFile(t, m, "btcusd-tick-2024-01-01-2024-01-02.csv", "timestamp,askPrice,bidPrice,askVolume,bidVolume\n1704067200000,101,100,1,2\n1704067200000,102,101,2,3\n")
	writeDataFile(t, m, "ethusd-m1-2024-01-01-2024-01-02.csv", "timestamp,close\n1704067200000,2\n1704067200000,3\n")
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-02.csv", "timestamp,open,high,low,close\n1704067200000,2,1,3,2\n")
	writeDataFile(t, m, "custom.csv", "timestamp,close\n1704067200000,1\n")
	info := startTestAnalysis(t, m, analysisCondition("above", analysisField("price"), analysisNumber(0)))
	if info.Failed != 3 || info.TotalMatches != 2 {
		t.Fatalf("per-file errors %+v", info)
	}
	r := resultNamed(t, info, "btcusd-tick-2024-01-01-2024-01-02.csv")
	chart, err := m.AnalysisChart(r.ID, 1, 1, 0)
	if err != nil || len(chart.Bars) != 2 || chart.Bars[1].Values["bidPrice"] != 101 {
		t.Fatalf("tick chart %+v %v", chart, err)
	}
	info = startTestAnalysis(t, m, analysisCondition("above", analysisField("sma_200"), analysisNumber(0)))
	if info.Failed != 4 {
		t.Fatalf("missing field not visible %+v", info)
	}
	catalog, err := m.AnalysisCatalog()
	if err != nil || len(catalog.Files) != 4 {
		t.Fatalf("catalog %+v %v", catalog, err)
	}
	if !strings.Contains(strings.Join(catalog.Fields, ","), "price") {
		t.Fatal("price alias absent")
	}
}
func TestAnalysisDraftPersistenceProtectsInvalidStateAndValidatesBounds(t *testing.T) {
	m := analysisTestManager(t)
	request := defaultAnalysisRequest()
	request.Conditions[0].Left.Offset = 3
	request.Conditions[0].Right = analysisNumber(-1.2)
	if _, err := m.SaveAnalysisView(request); err != nil {
		t.Fatal(err)
	}
	restarted := newManager(m.profile, func() (workerRuntime, error) { return workerRuntime{}, errors.New("unused") })
	t.Cleanup(restarted.Close)
	got, err := restarted.AnalysisView()
	if err != nil || !reflect.DeepEqual(got, request) {
		t.Fatalf("draft lost %+v %v", got, err)
	}
	request.Conditions[0].Left.Offset = 10001
	if _, err = m.SaveAnalysisView(request); err == nil {
		t.Fatal("oversize offset accepted")
	}
	bad := []byte(`{"version":99,"request":{"conditions":[]}}`)
	path := filepath.Join(m.profile, "screener-analysis.json")
	if err = os.WriteFile(path, bad, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.SaveAnalysisView(defaultAnalysisRequest()); err == nil {
		t.Fatal("future state overwritten")
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(bad) {
		t.Fatal("future state changed")
	}
}
func TestAnalysisCancellationAndCloseCleanDiskIndexes(t *testing.T) {
	m := analysisTestManager(t)
	values := make([]float64, 100000)
	for i := range values {
		values[i] = float64(i + 1)
	}
	writeDataFile(t, m, "btcusd-m1-2024-01-01-2025-01-01.csv", analysisCSV(values))
	if _, err := m.StartAnalysis(defaultAnalysisRequest()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		info, err := m.Analysis(0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if info.Progress > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := m.ConfigureDirectory(t.TempDir()); err == nil {
		t.Fatal("directory changed while scanning")
	}
	info := m.CancelAnalysis()
	if info.Active || info.Phase != "cancelled" {
		t.Fatalf("cancel %+v", info)
	}
	if info.TotalMatches != 0 {
		t.Fatalf("partial matches exposed %+v", info)
	}
	if info.Failed != 0 || len(info.Results) != 1 || info.Results[0].Status != "cancelled" {
		t.Fatalf("cancellation reported as file failure %+v", info)
	}
	work := m.analysis.work
	m.Close()
	if _, err := os.Stat(work); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch remains %v", err)
	}
}
func TestAnalysisCSVRecordMemoryBoundAndCrossTimeframeSnapshot(t *testing.T) {
	m := analysisTestManager(t)
	base := int64(1704067200000)
	name := "btcusd-m1-2024-01-01-2024-01-03.csv"
	writeDataFile(t, m, name, fmt.Sprintf("timestamp,close\n%d,101\n", base+86400000))
	dailyPath := writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-03.csv", fmt.Sprintf("timestamp,close\n%d,100\n", base))
	op := analysisField("close")
	op.Timeframe = "d1"
	info := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), op))
	r := resultNamed(t, info, name)
	if r.Matches != 1 {
		t.Fatalf("crossTF %+v", r)
	}
	if err := os.WriteFile(dailyPath, []byte(fmt.Sprintf("timestamp,close\n%d,99\n", base)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AnalysisChart(r.ID, 0, 0, 0); err == nil {
		t.Fatal("changed cross-timeframe dependency accepted")
	}
	name = "ethusd-m1-2024-01-01-2024-01-02.csv"
	writeDataFile(t, m, name, "timestamp,close\n1704067200000,"+strings.Repeat("1", 2*1024*1024)+"\n")
	_, err := indexAnalysisCSV(context.Background(), m.directory, name, filepath.Join(t.TempDir(), "index"), func(float64) {})
	if err == nil || !strings.Contains(err.Error(), "MiB") {
		t.Fatalf("unbounded record accepted %v", err)
	}
}

func TestAnalysisChartAndSummaryAreJSONFinite(t *testing.T) {
	m := analysisTestManager(t)
	text := strings.Replace(analysisCSV([]float64{1, 2}), ",10,3,2", ",10,,2", 1)
	writeDataFile(t, m, "btcusd-m1-2024-01-01-2024-01-02.csv", text)
	info := startTestAnalysis(t, m, defaultAnalysisRequest().Conditions...)
	chart, err := m.AnalysisChart(info.Results[0].ID, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = json.Marshal(chart); err != nil {
		t.Fatalf("non-finite chart JSON %v", err)
	}
	if _, exists := chart.Bars[0].Values["sma_3"]; exists {
		t.Fatal("warmup blank serialized as number")
	}
}

func TestAnalysisLargePreviousWindowRemainsBoundedAndRetainsEveryMatch(t *testing.T) {
	m := analysisTestManager(t)
	values := make([]float64, 12050)
	for i := range values {
		values[i] = float64(i + 1)
	}
	writeDataFile(t, m, "btcusd-m1-2024-01-01-2024-02-01.csv", analysisCSV(values))
	c := analysisCondition("highest", analysisField("close"), analysisField("close"))
	c.Bars = 10000
	info := startTestAnalysis(t, m, c)
	if info.Failed != 0 || info.TotalMatches != 2050 {
		t.Fatalf("large window %+v", info)
	}
	if m.analysis.cache.bytes > analysisCacheBytes {
		t.Fatalf("cache exceeded bound %d", m.analysis.cache.bytes)
	}
	chart, err := m.AnalysisChart(info.Results[0].ID, 2049, 0, 0)
	if err != nil || chart.MatchIndex != 12049 {
		t.Fatalf("last window match %+v %v", chart, err)
	}
}

func TestAnalysisAllAssetMatchCountsSurviveFilePagination(t *testing.T) {
	m := analysisTestManager(t)
	writeDataFile(t, m, "btcusd-m1-2024-01-01-2024-01-02.csv", analysisCSV([]float64{1, 2, 3}))
	writeDataFile(t, m, "btcusd-m5-2024-01-01-2024-01-02.csv", analysisCSV([]float64{1, 2}))
	writeDataFile(t, m, "ethusd-m1-2024-01-01-2024-01-02.csv", analysisCSV([]float64{1}))
	startTestAnalysis(t, m, defaultAnalysisRequest().Conditions...)
	page, err := m.Analysis(1, 1)
	if err != nil || len(page.Results) != 1 {
		t.Fatalf("page %+v %v", page, err)
	}
	if page.Results[0].AssetMatches != 5 || page.Results[0].AssetFiles != 2 {
		t.Fatalf("asset totals became page local %+v", page.Results)
	}
}

func TestAnalysisSupportedEarlyDailyHistoryKeepsMillisecondTimestampUnits(t *testing.T) {
	m := analysisTestManager(t)
	// Dukascopy's EURUSD daily history includes March 1973, spanning 1e11 ms.
	start := time.Date(1973, 3, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	writeDataFile(t, m, "eurusd-d1-1973-03-01-1973-03-10.csv", fmt.Sprintf("timestamp,close\n%d,1\n%d,2\n%d,3\n%d,4\n", start, start+86400000, start+2*86400000, start+3*86400000))
	info := startTestAnalysis(t, m, defaultAnalysisRequest().Conditions...)
	if info.Failed != 0 || info.TotalMatches != 4 {
		t.Fatalf("early history units %+v", info)
	}
	chart, err := m.AnalysisChart(info.Results[0].ID, 0, 0, 3)
	if err != nil || chart.Bars[0].Time != start || chart.Bars[3].Time != start+3*86400000 {
		t.Fatalf("early timestamp chart %+v %v", chart, err)
	}
}

func TestAnalysisHigherTimeframePreviousWindowDoesNotRequireBaseFileWarmup(t *testing.T) {
	m := analysisTestManager(t)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	var daily strings.Builder
	daily.WriteString("timestamp,close\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&daily, "%d,%d\n", start+int64(i)*86400000, i+1)
	}
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-27.csv", daily.String())
	name := "btcusd-m1-2024-01-25-2024-01-26.csv"
	writeDataFile(t, m, name, fmt.Sprintf("timestamp,close\n%d,100\n", start+24*86400000))
	right := analysisField("close")
	right.Timeframe = "d1"
	c := analysisCondition("highest", analysisField("close"), right)
	c.Bars = 20
	info := startTestAnalysis(t, m, c)
	r := resultNamed(t, info, name)
	if r.Matches != 1 {
		t.Fatalf("base index wrongly limited higher-TF window %+v", r)
	}
}

func TestAnalysisOverlappingCompanionsPreferDatasetsContainingTheRequestedIndicator(t *testing.T) {
	m := analysisTestManager(t)
	base := int64(1704067200000)
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-03.csv", fmt.Sprintf("timestamp,close,sma_200\n%d,100,99\n", base))
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-03-2.csv", fmt.Sprintf("timestamp,close\n%d,100\n", base))
	name := "btcusd-m1-2024-01-02-2024-01-03.csv"
	writeDataFile(t, m, name, fmt.Sprintf("timestamp,close\n%d,101\n", base+86400000))
	right := analysisField("sma_200")
	right.Timeframe = "d1"
	info := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), right))
	r := resultNamed(t, info, name)
	if r.Status != "completed" || r.Matches != 1 {
		t.Fatalf("unenriched overlap hid usable indicator %+v", r)
	}
}

func TestAnalysisFreshlyExtendedClosedDailyDataRemainsAvailableUntilNextDailyClose(t *testing.T) {
	m := analysisTestManager(t)
	day := int64(86400000)
	start := time.Date(2024, 1, 24, 0, 0, 0, 0, time.UTC).UnixMilli()
	// Refresh retains the original filename even as its actual history grows.
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-02.csv", fmt.Sprintf("timestamp,close,macd_histogram_12_26_9\n%d,100,2\n", start))
	name := "btcusd-m1-2024-01-25-2024-01-27.csv"
	writeDataFile(t, m, name, fmt.Sprintf("timestamp,close\n%d,101\n%d,101\n", start+day+12*3600000, start+2*day))
	right := analysisField("macd_histogram_12_26_9")
	right.Timeframe = "d1"
	info := startTestAnalysis(t, m, analysisCondition("above", right, analysisNumber(0)))
	r := resultNamed(t, info, name)
	if r.Status != "completed" || r.Matches != 1 {
		t.Fatalf("latest closed daily unavailable or carried indefinitely %+v", r)
	}
	chart, err := m.AnalysisChart(r.ID, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if chart.Bars[0].Values["d1:macd_histogram_12_26_9"] != 2 {
		t.Fatalf("latest closed D1 missing from popup %+v", chart.Bars)
	}
	if _, exists := chart.Bars[1].Values["d1:macd_histogram_12_26_9"]; exists {
		t.Fatal("stale D1 carried beyond next expected close")
	}
}

func TestAnalysisVerifiedRefreshHorizonCoversMarketClosuresOnlyWhileChecksumMatches(t *testing.T) {
	m := analysisTestManager(t)
	day := int64(86400000)
	start := time.Date(2024, 1, 24, 0, 0, 0, 0, time.UTC).UnixMilli()
	dailyName := "btcusd-d1-2024-01-01-2024-01-02.csv"
	daily := fmt.Sprintf("timestamp,close,macd_histogram_12_26_9\n%d,100,2\n", start)
	path := writeDataFile(t, m, dailyName, daily)
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(daily))
	metadata := fileMetadata{Verified: true, Bytes: stat.Size(), Modified: stat.ModTime().UnixNano(), Checksum: fmt.Sprintf("%x", digest), CheckedThrough: start + 5*day}
	if err = writeFileMetadata(m.directory, dailyName, metadata); err != nil {
		t.Fatal(err)
	}
	name := "btcusd-m1-2024-01-28-2024-01-29.csv"
	writeDataFile(t, m, name, fmt.Sprintf("timestamp,close\n%d,101\n", start+4*day+12*3600000))
	right := analysisField("macd_histogram_12_26_9")
	right.Timeframe = "d1"
	request := analysisCondition("above", right, analysisNumber(0))
	info := startTestAnalysis(t, m, request)
	r := resultNamed(t, info, name)
	if r.Matches != 1 {
		t.Fatalf("verified market closure lost %+v", r)
	}
	// Reusing a size and mtime must not launder a stale coverage horizon.
	if err = os.WriteFile(path, []byte(strings.Replace(daily, ",100,2", ",100,3", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	info = startTestAnalysis(t, m, request)
	r = resultNamed(t, info, name)
	if r.Matches != 0 || r.Status != "completed" {
		t.Fatalf("changed bytes inherited verified horizon %+v", r)
	}
}
