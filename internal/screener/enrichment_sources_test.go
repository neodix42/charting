package screener

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var enrichmentSourceFiles = []string{
	"usdcad-h1-2024-01-01-2024-01-02.csv",
	"usdchf-h1-2024-01-01-2024-01-02.csv",
	"usdjpy-h1-2024-01-01-2024-01-02.csv",
	"hyperliquid-btc-h1-2024-01-01-2024-01-02.csv",
	"hyperliquid-eth-h1-2024-01-01-2024-01-02.csv",
}

func sourceGatedEnrichment(t *testing.T) (*Manager, string, []string) {
	t.Helper()
	m := sourceEnrichmentManager(t, true)
	configureTestConcurrency(t, m, 2)
	gates := t.TempDir()
	t.Setenv("CRYPTODESK_SCREENER_TEST_MODE", "enrich-source-gated")
	t.Setenv("CRYPTODESK_SCREENER_TEST_GATES", gates)
	ids := []string{}
	for _, name := range enrichmentSourceFiles {
		writeDataFile(t, m, name, candleCSV)
		ids = append(ids, dataFileID(m.directory, name))
	}
	started, err := m.StartEnrichment(EnrichmentRequest{DataSource: "all", Period: "h1", Indicator: "sma", Parameters: map[string]float64{"period": 3}})
	if err != nil || started.Total != len(ids) || !reflect.DeepEqual(started.SourceOrder, []string{"dukascopy", "hyperliquid"}) || started.ActiveDataSource != "dukascopy" {
		t.Fatalf("all-source start: %+v %v", started, err)
	}
	return m, gates, ids
}

func awaitEnrichmentState(t *testing.T, m *Manager, ids []string, ready func(EnrichmentInfo) bool) EnrichmentInfo {
	t.Helper()
	var info EnrichmentInfo
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		info, err = m.Enrichment(ids)
		if err != nil {
			t.Fatal(err)
		}
		if ready(info) {
			return info
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("enrichment did not reach expected state: %+v", info)
	return info
}

func TestAllEnrichmentSourcesDrainEveryEarlierWorkerBeforeAdvancing(t *testing.T) {
	m, gates, ids := sourceGatedEnrichment(t)
	for _, name := range enrichmentSourceFiles[:2] {
		waitGate(t, gates, name)
	}
	for _, name := range enrichmentSourceFiles[2:] {
		assertNotStarted(t, gates, name)
	}
	info, _ := m.Enrichment(ids)
	for index, job := range info.Jobs {
		if job.Name != enrichmentSourceFiles[index] || job.DataSource != fileDataSource(job.Name) {
			t.Fatalf("planned order or concrete source changed: %+v", info)
		}
	}
	// Mutating returned progress must not change the next poll's source order.
	info.SourceOrder[0] = "changed"
	info, _ = m.Enrichment(ids)
	if !reflect.DeepEqual(info.SourceOrder, []string{"dukascopy", "hyperliquid"}) {
		t.Fatal("source order aliases internal state", info)
	}
	releaseGate(t, gates, enrichmentSourceFiles[0])
	waitGate(t, gates, enrichmentSourceFiles[2])
	for _, name := range enrichmentSourceFiles[3:] {
		assertNotStarted(t, gates, name)
	}
	releaseGate(t, gates, enrichmentSourceFiles[2])
	info = awaitEnrichmentState(t, m, ids, func(info EnrichmentInfo) bool { return info.Completed == 2 })
	if info.ActiveDataSource != "dukascopy" || info.Jobs[1].Status != "running" {
		t.Fatalf("source advanced before every worker drained: %+v", info)
	}
	for _, name := range enrichmentSourceFiles[3:] {
		assertNotStarted(t, gates, name)
	}
	releaseGate(t, gates, enrichmentSourceFiles[1])
	for _, name := range enrichmentSourceFiles[3:] {
		waitGate(t, gates, name)
	}
	info, _ = m.Enrichment(ids)
	if !info.Active || info.ActiveDataSource != "hyperliquid" || info.Completed != 3 {
		t.Fatalf("second source started before first publication: %+v", info)
	}
	for _, name := range enrichmentSourceFiles[3:] {
		releaseGate(t, gates, name)
	}
	info = waitEnrichment(t, m, ids)
	if info.Completed != 5 || info.Failed != 0 || info.Cancelled != 0 || info.Progress != 100 || info.ActiveDataSource != "" || !reflect.DeepEqual(info.SourceOrder, []string{"dukascopy", "hyperliquid"}) {
		t.Fatalf("combined completion: %+v", info)
	}
	for _, job := range info.Jobs {
		backup, err := os.ReadFile(job.BackupPath)
		if err != nil || string(backup) != candleCSV {
			t.Fatalf("missing original backup for %s: %v", job.Name, err)
		}
	}
	assertNoEnrichmentScratch(t, m.directory)
}

func TestAllEnrichmentCancelAndCloseDrainFirstSourceWithoutStartingLaterSource(t *testing.T) {
	for _, closeApp := range []bool{false, true} {
		name := "cancel"
		if closeApp {
			name = "close"
		}
		t.Run(name, func(t *testing.T) {
			m, gates, ids := sourceGatedEnrichment(t)
			for _, name := range enrichmentSourceFiles[:2] {
				waitGate(t, gates, name)
			}
			if closeApp {
				m.Close()
			} else {
				m.CancelEnrichment()
			}
			info, _ := m.Enrichment(ids)
			if info.Active || info.Cancelled != len(ids) || info.Completed != 0 || info.Failed != 0 || info.Progress != 100 || info.ActiveDataSource != "" {
				t.Fatalf("cancel did not finish complete batch: %+v", info)
			}
			for index, name := range enrichmentSourceFiles {
				if index >= 2 {
					assertNotStarted(t, gates, name)
				}
				data, err := os.ReadFile(filepath.Join(m.directory, name))
				if err != nil || string(data) != candleCSV {
					t.Fatalf("cancelled file changed: %s %v", name, err)
				}
			}
			assertNoEnrichmentScratch(t, m.directory)
		})
	}
}

func TestAllEnrichmentFailedFileDoesNotPreventLaterSource(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	bad := "usdcad-h1-2024-01-01-2024-01-02.csv"
	good := "hyperliquid-btc-h1-2024-01-01-2024-01-02.csv"
	broken := "timestamp,close\n1704067200000,1\n"
	writeDataFile(t, m, bad, broken)
	writeDataFile(t, m, good, candleCSV)
	ids := []string{dataFileID(m.directory, bad), dataFileID(m.directory, good)}
	if _, err := m.StartEnrichment(EnrichmentRequest{DataSource: "all", Indicator: "tenkan", Parameters: map[string]float64{"period": 2}}); err != nil {
		t.Fatal(err)
	}
	info := waitEnrichment(t, m, ids)
	if info.Failed != 1 || info.Completed != 1 || info.Jobs[0].DataSource != "dukascopy" || info.Jobs[0].Status != "failed" || info.Jobs[1].DataSource != "hyperliquid" || info.Jobs[1].Status != "completed" {
		t.Fatalf("failed first source prevented continuation: %+v", info)
	}
	data, _ := os.ReadFile(filepath.Join(m.directory, bad))
	if string(data) != broken {
		t.Fatal("failed file changed")
	}
	data, _ = os.ReadFile(filepath.Join(m.directory, good))
	if !strings.Contains(string(data), "tenkan_2") {
		t.Fatal("later source was not enriched", string(data))
	}
}

func TestAllEnrichmentRangeGroupUsesLocalPeriodUnionAndFiltersBeforeConsolidation(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	selected := []string{
		"eurusd-m1-2024-01-01-2024-01-02.csv",
		"usdcad-h4-2024-01-01-2024-01-02.csv",
		"hyperliquid-btc-h1-2024-01-01-2024-01-02.csv",
	}
	excluded := []string{
		"eurusd-d1-2024-01-01-2024-01-03.csv",
		"eurusd-d1-2024-01-02-2024-01-04.csv",
		"eurusd-tick-2024-01-01-2024-01-02.csv",
		"hyperliquid-btc-d1-2024-01-01-2024-01-02.csv",
		"hyperliquid-btc-w1-2024-01-01-2024-01-02.csv",
		"custom.csv",
	}
	ids := []string{}
	for _, name := range append(append([]string{}, selected...), excluded...) {
		writeDataFile(t, m, name, candleCSV)
		ids = append(ids, dataFileID(m.directory, name))
	}
	before := enrichmentTreeSnapshot(t, m.directory)
	started, err := m.StartEnrichment(EnrichmentRequest{DataSource: "all", Period: "all", Group: "Day / Week ranges"})
	if err != nil || started.Total != len(selected) {
		t.Fatalf("intraday plan included unsupported periods or lost local periods: %+v %v", started, err)
	}
	info := waitEnrichment(t, m, ids)
	if info.Completed != len(selected) || info.Failed != 0 || info.Indicator != "Day / Week ranges" || len(info.Jobs) != len(selected) {
		t.Fatalf("combined range group: %+v", info)
	}
	for _, job := range info.Jobs {
		if !reflect.DeepEqual(job.Columns, calendarRangeIDs) || job.DataSource != fileDataSource(job.Name) {
			t.Fatalf("group members or concrete source lost: %+v", job)
		}
		metadata, err := readFileMetadata(m.directory, job.Name)
		if err != nil || len(metadata.Recipes) != len(calendarRangeIDs) {
			t.Fatalf("group recipes lost: %+v %v", metadata, err)
		}
		for _, recipe := range metadata.Recipes {
			if recipe.Group != "" || recipe.Period != "" || recipe.DataSource != "" {
				t.Fatalf("batch scope leaked into replay recipe: %+v", recipe)
			}
		}
	}
	after := enrichmentTreeSnapshot(t, m.directory)
	for _, name := range excluded {
		path := filepath.Join(m.directory, name)
		if !reflect.DeepEqual(before[path], after[path]) {
			t.Fatalf("excluded file was consolidated or enriched: %s", name)
		}
		if _, err := os.Stat(historyMetadataPath(m.directory, name)); !os.IsNotExist(err) {
			t.Fatalf("excluded file received metadata: %s %v", name, err)
		}
	}
	for _, period := range []string{"m1", "h4"} {
		started, err = m.StartEnrichment(EnrichmentRequest{DataSource: "all", Period: period, Indicator: "hour"})
		if err != nil || started.Total != 1 || !reflect.DeepEqual(started.SourceOrder, []string{"dukascopy"}) {
			t.Fatalf("local %s period or empty-source skip failed: %+v %v", period, started, err)
		}
		if done := waitEnrichment(t, m, nil); done.Completed != 1 || done.Failed != 0 {
			t.Fatalf("selected local %s period: %+v", period, done)
		}
	}
}

func TestEnrichmentSingleSourceAndAllWithOnlyHyperliquidKeepConcreteScope(t *testing.T) {
	for _, source := range []string{"dukascopy", "hyperliquid", "all"} {
		t.Run(source, func(t *testing.T) {
			m := sourceEnrichmentManager(t, false)
			duka := "eurusd-h1-2024-01-01-2024-01-02.csv"
			hyper := "hyperliquid-btc-h1-2024-01-01-2024-01-02.csv"
			if source != "all" {
				writeDataFile(t, m, duka, candleCSV)
			}
			writeDataFile(t, m, hyper, candleCSV)
			concrete := source
			if source == "all" {
				concrete = "hyperliquid"
			}
			started, err := m.StartEnrichment(EnrichmentRequest{DataSource: source, Indicator: "hour"})
			if err != nil || started.Total != 1 || !reflect.DeepEqual(started.SourceOrder, []string{concrete}) || started.ActiveDataSource != concrete {
				t.Fatalf("single-source start: %+v %v", started, err)
			}
			ids := []string{dataFileID(m.directory, duka), dataFileID(m.directory, hyper)}
			info := waitEnrichment(t, m, ids)
			if info.Completed != 1 || info.Failed != 0 || info.Jobs[0].DataSource != concrete || info.ActiveDataSource != "" {
				t.Fatalf("single-source result: %+v", info)
			}
			if source != "all" {
				excluded := hyper
				if source == "hyperliquid" {
					excluded = duka
				}
				data, _ := os.ReadFile(filepath.Join(m.directory, excluded))
				if string(data) != candleCSV {
					t.Fatal("other source changed")
				}
			}
		})
	}
}
