package screener

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEnrichmentPeriodValidatesAgainstDownloadCatalog(t *testing.T) {
	m := &Manager{}
	for _, period := range append([]string{"", "all"}, m.Catalog().Timeframes...) {
		request, err := m.validateEnrichment(EnrichmentRequest{Indicator: "sma", Period: period})
		if err != nil || request.Period != period {
			t.Fatalf("supported period %q changed or rejected: %+v %v", period, request, err)
		}
	}
	for _, period := range []string{"m2", "1m", "D1", "same", " all "} {
		if _, err := m.validateEnrichment(EnrichmentRequest{Indicator: "sma", Period: period}); err == nil || !strings.Contains(err.Error(), "enrichment period") {
			t.Fatalf("invalid period %q accepted: %v", period, err)
		}
	}
}

// Include metadata and private backups in snapshots: an excluded period must
// not be consolidated, rewritten, or given a replacement recipe or backup.
type enrichmentFileSnapshot struct {
	Contents string
	Mode     fs.FileMode
	Modified time.Time
}

func enrichmentTreeSnapshot(t *testing.T, directory string) map[string]enrichmentFileSnapshot {
	t.Helper()
	result := map[string]enrichmentFileSnapshot{}
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result[path] = enrichmentFileSnapshot{string(data), info.Mode(), info.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestEnrichmentPeriodFiltersBeforeConsolidationAndKeepsExcludedDataAndBackups(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	selectedIDs := seedDuplicateHistories(t, m)
	unselected := []string{
		"eurusd-h1-2024-01-01-2024-01-04.csv",
		"eurusd-h1-2024-01-02-2024-01-06.csv",
		"btcusd-m1-2024-01-01-2024-01-02.csv",
		"btcusd-m5-2024-01-01-2024-01-02.csv",
		"custom.csv",
	}
	for _, name := range unselected {
		writeDataFile(t, m, name, "timestamp,close\ninvalid,2\n")
		if err := writeFileMetadata(m.directory, name, fileMetadata{}); err != nil {
			t.Fatal(err)
		}
	}
	backupDirectory := filepath.Join(m.directory, ".cryptodesk-enrichment-backups")
	if err := os.Mkdir(backupDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	backupCSV := filepath.Join(backupDirectory, "previous.csv")
	backupJSON := filepath.Join(backupDirectory, "previous.json")
	if err := os.WriteFile(backupCSV, []byte("previous exact original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupJSON, []byte(`{"filename":"`+unselected[0]+`","request":{"indicator":"sma","parameters":{"period":2},"source":"close"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	before := enrichmentTreeSnapshot(t, m.directory)
	ids := append([]string{}, selectedIDs...)
	for _, name := range unselected {
		ids = append(ids, dataFileID(m.directory, name))
	}
	started, err := m.StartEnrichment(EnrichmentRequest{Indicator: "hour", Period: "d1"})
	if err != nil || started.Total != 1 {
		t.Fatalf("selected ranges did not become one job: %+v %v", started, err)
	}
	info := waitEnrichment(t, m, ids)
	if info.Total != 1 || info.Completed != 1 || info.Failed != 0 || info.Progress != 100 || len(info.Jobs) != 1 || info.Jobs[0].Rows != 4 {
		t.Fatalf("progress included unselected files: %+v", info)
	}
	wantName := "eurusd-d1-2024-01-01-2024-01-06.csv"
	if info.Jobs[0].Name != wantName {
		t.Fatalf("selected duplicate ranges were not consolidated: %+v", info.Jobs[0])
	}
	merged, err := os.ReadFile(filepath.Join(m.directory, wantName))
	if err != nil || !strings.Contains(string(merged), "hour") || !strings.Contains(string(merged), "sma_2") || !strings.Contains(string(merged), "ema_2") {
		t.Fatalf("selected history lost indicator values: %q %v", merged, err)
	}
	after := enrichmentTreeSnapshot(t, m.directory)
	for _, name := range unselected {
		for _, path := range []string{filepath.Join(m.directory, name), historyMetadataPath(m.directory, name)} {
			if !reflect.DeepEqual(before[path], after[path]) {
				t.Fatalf("unselected data or metadata changed: %s", path)
			}
		}
	}
	for _, path := range []string{backupCSV, backupJSON} {
		if !reflect.DeepEqual(before[path], after[path]) {
			t.Fatalf("existing backup changed: %s", path)
		}
	}
	backups, err := os.ReadDir(backupDirectory)
	if err != nil || len(backups) != 4 {
		t.Fatalf("batch backed up more than its one selected file: %+v %v", backups, err)
	}
	assertNoEnrichmentScratch(t, m.directory)
}

func TestEnrichmentPeriodAllRetainsLegacyCustomFiles(t *testing.T) {
	for _, period := range []string{"", "all"} {
		t.Run("period-"+period, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			for _, name := range []string{"btcusd-m1-2024-01-01-2024-01-02.csv", "btcusd-d1-2024-01-01-2024-01-02.csv", "custom.csv"} {
				writeDataFile(t, m, name, candleCSV)
			}
			if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "hour", Period: period}); err != nil {
				t.Fatal(err)
			}
			info := waitEnrichment(t, m, nil)
			if info.Total != 3 || info.Completed != 3 || info.Failed != 0 {
				t.Fatalf("all-period compatibility changed: %+v", info)
			}
		})
	}
}

func TestEnrichmentPeriodRejectsUnavailableOrInvalidPeriodWithoutChangingDataOrPreviousResults(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	name := "btcusd-d1-2024-01-01-2024-01-02.csv"
	writeDataFile(t, m, name, candleCSV)
	ids := []string{dataFileID(m.directory, name)}
	if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "hour", Period: "d1"}); err != nil {
		t.Fatal(err)
	}
	previous := waitEnrichment(t, m, ids)
	before := enrichmentTreeSnapshot(t, m.directory)
	for _, period := range []string{"h1", "m2", "D1"} {
		_, err := m.StartEnrichment(EnrichmentRequest{Indicator: "hour", Period: period})
		if err == nil || !strings.Contains(err.Error(), period) {
			t.Fatalf("unavailable/invalid period %q accepted: %v", period, err)
		}
		if got, _ := m.Enrichment(ids); !reflect.DeepEqual(got, previous) {
			t.Fatalf("rejected period %q replaced previous results: %+v", period, got)
		}
		if after := enrichmentTreeSnapshot(t, m.directory); !reflect.DeepEqual(before, after) {
			t.Fatalf("rejected period %q changed data, metadata, or backups", period)
		}
	}
}

func TestEnrichmentPeriodKeepsRecipeIdentityAndRefreshReplayIndependentOfScope(t *testing.T) {
	m, _, _ := refreshManager(t, false)
	writeDataFile(t, m, refreshName, refreshCSV)
	id := dataFileID(m.directory, refreshName)
	for _, period := range []string{"d1", "all", ""} {
		if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "sma", Parameters: map[string]float64{"period": 2}, Period: period}); err != nil {
			t.Fatal(err)
		}
		info := waitEnrichment(t, m, []string{id})
		if info.Completed != 1 || info.Failed != 0 || len(info.Jobs) != 1 {
			t.Fatalf("enrichment period %q failed: %+v", period, info)
		}
		metadata, err := readFileMetadata(m.directory, refreshName)
		if err != nil || len(metadata.Recipes) != 1 || metadata.Recipes[0].Period != "" {
			t.Fatalf("batch scope changed recipe identity: %+v %v", metadata, err)
		}
		backupData, err := os.ReadFile(strings.TrimSuffix(info.Jobs[0].BackupPath, ".csv") + ".json")
		var backup struct {
			Request EnrichmentRequest `json:"request"`
		}
		if err != nil || json.Unmarshal(backupData, &backup) != nil || backup.Request.Period != "" {
			t.Fatalf("batch scope persisted in legacy recipe: %s %v", backupData, err)
		}
	}
	legacy := legacyRecipes(context.Background(), m.directory)
	if len(legacy[refreshName]) != 1 || legacy[refreshName][0].Period != "" {
		t.Fatalf("batch scope duplicated legacy recipes: %+v", legacy)
	}
	if _, err := m.StartRefresh(); err != nil {
		t.Fatal(err)
	}
	info := waitRefresh(t, m, []string{id})
	if info.Completed != 1 || info.Failed != 0 || len(info.Jobs) != 1 {
		t.Fatalf("period-selected indicator prevented refresh: %+v", info)
	}
	data, err := os.ReadFile(filepath.Join(m.directory, info.Jobs[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(rows) < 4 || rows[2][6] != "15" || rows[3][6] != "25" {
		t.Fatalf("refresh did not replay the period-selected recipe: %q %v", data, err)
	}
	metadata, err := readFileMetadata(m.directory, info.Jobs[0].Name)
	if err != nil || len(metadata.Recipes) != 1 || metadata.Recipes[0].Period != "" {
		t.Fatalf("refresh changed recipe identity: %+v %v", metadata, err)
	}
}
