package screener

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sourceEnrichmentManager(t *testing.T, helper bool) *Manager {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("enrichment integration requires the project-local Node runtime")
	}
	worker, err := filepath.Abs("../../screener-runtime/worker.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(worker), "node_modules/trading-signals/package.json")); err != nil {
		t.Skip("run npm ci --prefix screener-runtime for enrichment integration")
	}
	if helper {
		node = os.Args[0]
		t.Setenv("CRYPTODESK_SCREENER_TEST_HELPER", "1")
		t.Setenv("GORACE", "atexit_sleep_ms=0")
		t.Setenv("GOMAXPROCS", "2")
	}
	m := newManager(t.TempDir(), func() (workerRuntime, error) { return workerRuntime{node: node, worker: worker}, nil })
	t.Cleanup(m.Close)
	if err := os.MkdirAll(m.Info().Directory, 0700); err != nil {
		t.Fatal(err)
	}
	return m
}

func writeDataFile(t *testing.T, m *Manager, name, text string) string {
	t.Helper()
	path := filepath.Join(m.Info().Directory, name)
	if err := os.WriteFile(path, []byte(text), 0640); err != nil {
		t.Fatal(err)
	}
	return path
}

const candleCSV = "timestamp,open,high,low,close,volume\n1704067200000,1,2,0,1,3\n1704067260000,2,3,1,2,4\n1704067320000,3,4,2,3,5\n1704067380000,4,5,3,4,6\n"

func waitEnrichment(t *testing.T, m *Manager, ids []string) EnrichmentInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		info, err := m.Enrichment(ids)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Active {
			return info
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("enrichment did not finish")
	return EnrichmentInfo{}
}

func assertNoEnrichmentScratch(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".cryptodesk-enrichment-") && entry.Name() != ".cryptodesk-enrichment-backups" {
			t.Fatalf("enrichment scratch remains: %s", entry.Name())
		}
	}
}

func TestEnrichmentPublishesRealLibraryValuesWithExactOriginalBackupAndRestoresHeaders(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	name := "btcusd-m1-2024-01-01-2024-01-02.csv"
	path := writeDataFile(t, m, name, candleCSV)
	page, err := m.Files(0, 50)
	if err != nil || len(page.Files) != 1 {
		t.Fatalf("files: %+v %v", page, err)
	}
	if page.Files[0].From != "2024-01-01" || page.Files[0].To != "2024-01-02" || page.Files[0].Timeframe != "m1" || strings.Join(page.Files[0].Fields, ",") != "timestamp,open,high,low,close,volume" {
		t.Fatalf("metadata: %+v", page.Files[0])
	}
	if _, err = m.StartEnrichment(EnrichmentRequest{Indicator: "sma", Parameters: map[string]float64{"period": 3}}); err != nil {
		t.Fatal(err)
	}
	info := waitEnrichment(t, m, []string{page.Files[0].ID})
	if info.Completed != 1 || len(info.Jobs) != 1 || info.Jobs[0].Rows != 4 || info.Jobs[0].Progress != 100 || !reflect.DeepEqual(info.Jobs[0].Columns, []string{"sma_3"}) {
		t.Fatalf("enrichment: %+v", info)
	}
	backup, err := os.ReadFile(info.Jobs[0].BackupPath)
	if err != nil || string(backup) != candleCSV {
		t.Fatalf("original backup: %q %v", backup, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(rows) != 5 || rows[1][6] != "" || rows[2][6] != "" || rows[3][6] != "2" || rows[4][6] != "3" {
		t.Fatalf("calculated CSV: %q %v", data, err)
	}
	metadata, err := os.ReadFile(strings.TrimSuffix(info.Jobs[0].BackupPath, ".csv") + ".json")
	if err != nil || !strings.Contains(string(metadata), name) {
		t.Fatalf("backup metadata: %s %v", metadata, err)
	}
	m.Close()
	restarted := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
	defer restarted.Close()
	restored, err := restarted.Files(0, 50)
	if err != nil || len(restored.Files) != 1 || !reflect.DeepEqual(restored.Files[0].Fields, rows[0]) {
		t.Fatalf("restart file discovery: %+v %v", restored, err)
	}
	assertNoEnrichmentScratch(t, m.Info().Directory)
}

func TestEnrichmentFailureIsPerFileAndSuccessfulRerunDoesNotDuplicateColumns(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	bad := writeDataFile(t, m, "a-ticks.csv", "timestamp,bidPrice,askPrice\n1,2,3\n")
	good := writeDataFile(t, m, "b-candles.csv", candleCSV)
	page, _ := m.Files(0, 50)
	ids := []string{page.Files[0].ID, page.Files[1].ID}
	for range 2 {
		if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "tenkan", Parameters: map[string]float64{"period": 2}}); err != nil {
			t.Fatal(err)
		}
		info := waitEnrichment(t, m, ids)
		if info.Failed != 1 || info.Completed != 1 || !strings.Contains(info.Jobs[0].Message, "Requires high, low") || info.Jobs[1].Status != "completed" {
			t.Fatalf("independent results: %+v", info)
		}
	}
	data, _ := os.ReadFile(good)
	if strings.Count(string(data), "tenkan_2") != 1 {
		t.Fatalf("duplicate output column: %s", data)
	}
	data, _ = os.ReadFile(bad)
	if string(data) != "timestamp,bidPrice,askPrice\n1,2,3\n" {
		t.Fatal("incompatible input changed")
	}
	assertNoEnrichmentScratch(t, m.Info().Directory)
}

func gatedEnrichment(t *testing.T) (*Manager, string, string) {
	t.Helper()
	m := sourceEnrichmentManager(t, true)
	path := writeDataFile(t, m, "a.csv", candleCSV)
	writeDataFile(t, m, "b.csv", candleCSV)
	gates := t.TempDir()
	t.Setenv("CRYPTODESK_SCREENER_TEST_MODE", "enrich-gated")
	t.Setenv("CRYPTODESK_SCREENER_TEST_GATES", gates)
	if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "sma", Parameters: map[string]float64{"period": 3}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(gates, "started")); err == nil {
			return m, path, gates
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("enrichment helper did not start")
	return nil, "", ""
}

func TestEnrichmentCancelAndCloseDrainWorkersAndKeepAllOriginals(t *testing.T) {
	for _, closeApp := range []bool{false, true} {
		t.Run(fmt.Sprint(closeApp), func(t *testing.T) {
			m, path, _ := gatedEnrichment(t)
			if _, err := m.ConfigureDirectory(t.TempDir()); err == nil {
				t.Fatal("folder changed during enrichment")
			}
			if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "sma"}); err == nil {
				t.Fatal("overlapping batch started")
			}
			if closeApp {
				m.Close()
			} else {
				m.CancelEnrichment()
			}
			info := waitEnrichment(t, m, nil)
			if info.Cancelled != 2 || info.Completed != 0 || info.Failed != 0 {
				t.Fatalf("cancelled batch: %+v", info)
			}
			data, _ := os.ReadFile(path)
			if string(data) != candleCSV {
				t.Fatal("cancelled input changed")
			}
			assertNoEnrichmentScratch(t, m.Info().Directory)
		})
	}
}

func TestEnrichmentDetectsConcurrentSourceChangesAndKeepsTheChangedFile(t *testing.T) {
	m, path, gates := gatedEnrichment(t)
	changed := candleCSV + "1704067440000,5,6,4,5,7\n"
	if err := os.WriteFile(path, []byte(changed), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gates, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	page, _ := m.Files(0, 50)
	info := waitEnrichment(t, m, []string{page.Files[0].ID, page.Files[1].ID})
	if info.Failed != 1 || info.Completed != 1 || !strings.Contains(info.Jobs[0].Message, "source CSV changed") {
		t.Fatalf("changed source not detected: %+v", info)
	}
	data, _ := os.ReadFile(path)
	if string(data) != changed {
		t.Fatal("concurrently modified data was overwritten")
	}
	assertNoEnrichmentScratch(t, m.Info().Directory)
}

func TestEnrichmentCannotPublishWithoutAnOriginalBackup(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	path := writeDataFile(t, m, "data.csv", candleCSV)
	if err := os.WriteFile(filepath.Join(m.Info().Directory, ".cryptodesk-enrichment-backups"), []byte("user file"), 0600); err != nil {
		t.Fatal(err)
	}
	page, _ := m.Files(0, 50)
	if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "sma", Parameters: map[string]float64{"period": 3}}); err != nil {
		t.Fatal(err)
	}
	info := waitEnrichment(t, m, []string{page.Files[0].ID})
	if info.Failed != 1 || info.Completed != 0 || !strings.Contains(info.Jobs[0].Message, "backup folder") {
		t.Fatalf("backup failure: %+v", info)
	}
	data, _ := os.ReadFile(path)
	if string(data) != candleCSV {
		t.Fatal("input replaced despite backup failure")
	}
	assertNoEnrichmentScratch(t, m.Info().Directory)
}

func TestCSVInventoryBoundsPagesAndExcludesNonPublishedData(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	for i := range 55 {
		writeDataFile(t, m, fmt.Sprintf("custom-%02d.csv", i), candleCSV)
	}
	writeDataFile(t, m, ".hidden.csv", candleCSV)
	writeDataFile(t, m, "data.part", candleCSV)
	if err := os.Mkdir(filepath.Join(m.Info().Directory, "folder.csv"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(m.Info().Directory, "custom-00.csv"), filepath.Join(m.Info().Directory, "linked.csv")); err != nil {
		t.Fatal(err)
	}
	page, err := m.Files(50, 50)
	if err != nil || len(page.Files) != 5 || page.Total != 55 || page.Files[0].Name != "custom-50.csv" {
		t.Fatalf("page: %+v %v", page, err)
	}
	page, err = m.Files(math.MaxInt, 50)
	if err != nil || len(page.Files) != 0 || page.Total != 55 {
		t.Fatalf("beyond page: %+v %v", page, err)
	}
	for _, args := range [][2]int{{-1, 50}, {0, 0}, {0, 201}} {
		if _, err := m.Files(args[0], args[1]); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
	if _, err := m.Enrichment(make([]string, 201)); err == nil {
		t.Fatal("unbounded progress request accepted")
	}
}

func TestIndicatorValidationUsesReviewedBoundsAndCopiesCallerParameters(t *testing.T) {
	m := &Manager{}
	catalog := m.Indicators()
	if catalog.Version != "8.3.0" || len(catalog.Indicators) != 42 {
		t.Fatal("incorrect catalog")
	}
	for _, indicator := range catalog.Indicators {
		request, err := m.validateEnrichment(EnrichmentRequest{Indicator: indicator.ID})
		if err != nil || len(request.Parameters) != len(indicator.Parameters) {
			t.Fatalf("defaults for %s: %+v %v", indicator.ID, request, err)
		}
	}
	for _, indicator := range []string{"hma", "rsi", "cci", "stochrsi", "obv"} {
		if _, err := m.validateEnrichment(EnrichmentRequest{Indicator: indicator, Parameters: map[string]float64{"period": 1}}); err == nil {
			t.Fatalf("%s accepts an unusable library period", indicator)
		}
	}
	parameters := map[string]float64{"period": 3}
	request, err := m.validateEnrichment(EnrichmentRequest{Indicator: "sma", Parameters: parameters})
	parameters["period"] = 5
	if err != nil || request.Parameters["period"] != 3 {
		t.Fatal("request aliases caller parameters")
	}
	for _, invalid := range []EnrichmentRequest{{Indicator: "unknown"}, {Indicator: "sma", Source: "script"}, {Indicator: "sma", Parameters: map[string]float64{"period": -1}}, {Indicator: "sma", Parameters: map[string]float64{"period": math.NaN()}}, {Indicator: "sma", Parameters: map[string]float64{"period": 1.5}}, {Indicator: "sma", Parameters: map[string]float64{"file": 1}}, {Indicator: "macd", Parameters: map[string]float64{"fast": 26, "slow": 12}}} {
		if _, err := m.validateEnrichment(invalid); err == nil {
			t.Fatalf("invalid enrichment accepted: %+v", invalid)
		}
	}
	catalog.Indicators[0].Name = "changed"
	if m.Indicators().Indicators[0].Name == "changed" {
		t.Fatal("catalog aliases callers")
	}
	data, _ := json.Marshal(request)
	if !strings.Contains(string(data), `"source":"auto"`) {
		t.Fatal("default source missing")
	}
}

func TestCalendarFieldCatalogValidationUsesTimestampOnlyAndCanonicalSource(t *testing.T) {
	m := &Manager{}
	catalog := m.Indicators()
	technical, calendar := 0, 0
	for _, indicator := range catalog.Indicators {
		if indicator.Input != "timestamp" {
			technical++
			continue
		}
		calendar++
		if (indicator.ID != "weekday" && indicator.ID != "hour") || indicator.Category != "Time" || len(indicator.Parameters) != 0 || !strings.Contains(indicator.Note, "UTC") {
			t.Fatalf("incorrect time field catalog: %+v", indicator)
		}
		for _, source := range []string{"", "auto", "close", "open", "bidprice", "askprice"} {
			request, err := m.validateEnrichment(EnrichmentRequest{Indicator: indicator.ID, Source: source})
			if err != nil || request.Source != "auto" || len(request.Parameters) != 0 {
				t.Fatalf("calendar source is not canonical: %+v %v", request, err)
			}
		}
		if _, err := m.validateEnrichment(EnrichmentRequest{Indicator: indicator.ID, Parameters: map[string]float64{"period": 1}}); err == nil {
			t.Fatal("parameterless calendar field accepted period")
		}
	}
	if technical != 40 || calendar != 2 {
		t.Fatalf("catalog must retain 40 technical indicators plus 2 time fields: %d/%d", technical, calendar)
	}
}

func TestCalendarEnrichmentUsesRealUTCValuesWithoutPricesAndPersistsRecipes(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	const original = "timestamp,notes\n-1,early\n0,epoch\n1704067140000,sunday\n1704067200000,monday\n"
	name := "calendar.csv"
	path := writeDataFile(t, m, name, original)
	id := dataFileID(m.directory, name)
	for _, indicator := range []string{"weekday", "hour", "weekday"} {
		if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: indicator, Source: "close"}); err != nil {
			t.Fatal(err)
		}
		info := waitEnrichment(t, m, []string{id})
		if info.Completed != 1 || info.Failed != 0 || info.Jobs[0].Rows != 4 || !reflect.DeepEqual(info.Jobs[0].Columns, []string{indicator}) {
			t.Fatalf("calendar field failed without price columns: %+v", info)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	want := [][]string{{"timestamp", "notes", "weekday", "hour"}, {"-1", "early", "3", "23"}, {"0", "epoch", "4", "0"}, {"1704067140000", "sunday", "7", "23"}, {"1704067200000", "monday", "1", "0"}}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("UTC values: %+v %v", rows, err)
	}
	metadata, err := readFileMetadata(m.directory, name)
	if err != nil || len(metadata.Recipes) != 2 || metadata.Recipes[0].Indicator != "weekday" || metadata.Recipes[1].Indicator != "hour" || metadata.Recipes[0].Source != "auto" || metadata.Recipes[1].Source != "auto" {
		t.Fatalf("calendar recipes were lost or duplicated: %+v %v", metadata, err)
	}
}
