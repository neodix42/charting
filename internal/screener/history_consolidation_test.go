package screener

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Both ranges contribute unique rows and their own indicator. The overlapping
// January 3 bar deliberately disagrees: the broader original has priority.
const duplicateHistoryCSV = "timestamp,open,high,low,close,volume,ema_2\n1704153600000,20,21,19,20,10,\n1704240000000,30,31,29,30,10,25\n1704326400000,40,41,39,40,10,35\n"
const duplicateHistoryName = "eurusd-d1-2024-01-02-2024-01-06.csv"

func seedDuplicateHistories(t *testing.T, m *Manager) []string {
	t.Helper()
	writeDataFile(t, m, refreshName, refreshCSV)
	writeDataFile(t, m, duplicateHistoryName, duplicateHistoryCSV)
	return []string{dataFileID(m.directory, refreshName), dataFileID(m.directory, duplicateHistoryName)}
}

func assertMergedHistory(t *testing.T, m *Manager, name string, hour bool) {
	t.Helper()
	names, err := csvNames(m.directory)
	if err != nil || len(names) != 1 || names[0] != name {
		t.Fatalf("overlapping histories were not replaced by one CSV: %v %v", names, err)
	}
	data, err := os.ReadFile(filepath.Join(m.directory, name))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(rows) != 5 {
		t.Fatalf("merged history lost rows or repeated overlapping bars: %q %v", data, err)
	}
	columns := map[string]int{}
	for index, name := range rows[0] {
		columns[name] = index
	}
	for _, field := range []string{"sma_2", "ema_2"} {
		if _, exists := columns[field]; !exists {
			t.Fatalf("lost applied indicator %s: %q", field, data)
		}
	}
	for index, expected := range []string{"10", "20", "50", "40"} {
		if rows[index+1][columns["close"]] != expected {
			t.Fatalf("history row precedence changed: %q", data)
		}
	}
	if rows[2][columns["sma_2"]] != "15" || rows[3][columns["sma_2"]] != "35" || rows[4][columns["sma_2"]] != "45" {
		t.Fatalf("indicators were not replayed across the merged history: %q", data)
	}
	if hour {
		position, exists := columns["hour"]
		if !exists || rows[1][position] != "0" || rows[4][position] != "0" {
			t.Fatalf("new enrichment did not cover the complete merged history: %q", data)
		}
	}
	metadata, err := readFileMetadata(m.directory, name)
	expected := 2
	if hour {
		expected++
	}
	if err != nil || len(metadata.Recipes) != expected {
		t.Fatalf("merged indicator recipes were not retained: %+v %v", metadata, err)
	}
	for _, previous := range []string{refreshName, duplicateHistoryName} {
		if previous == name {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.directory, previous)); !os.IsNotExist(err) {
			t.Fatalf("old range remains visible: %s %v", previous, err)
		}
	}
}

func TestEnrichmentConsolidatesLegacyRangesOfflineBeforeApplyingIndicator(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	ids := seedDuplicateHistories(t, m)
	// Any accidental provider request is a test failure, including a successful
	// request made during a supposedly local merge.
	folder := t.TempDir()
	trace, preload := filepath.Join(folder, "trace"), filepath.Join(folder, "offline.cjs")
	module, _ := json.Marshal(filepath.Join(filepath.Dir(m.runtime.worker), "node_modules", "dukascopy-node"))
	traceJS, _ := json.Marshal(trace)
	code := fmt.Sprintf(`const fs=require('node:fs'),api=require(%s);api.BufferFetcher.prototype.fetchBuffer=async function(url){fs.appendFileSync(%s,url+'\n');throw Error('Offline consolidation must not download');};`, module, traceJS)
	if err := os.WriteFile(preload, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	started, err := m.StartEnrichment(EnrichmentRequest{Indicator: "hour"})
	if err != nil || started.Total != 1 {
		t.Fatalf("duplicate ranges were queued independently: %+v %v", started, err)
	}
	info := waitEnrichment(t, m, ids)
	if info.Completed != 1 || info.Failed != 0 || len(info.Jobs) != 1 || info.Jobs[0].Rows != 4 {
		t.Fatalf("merged enrichment: %+v", info)
	}
	name := "eurusd-d1-2024-01-01-2024-01-06.csv"
	if info.Jobs[0].Name != name || info.Jobs[0].FileID != dataFileID(m.directory, name) {
		t.Fatalf("polling did not follow renamed history: %+v", info.Jobs)
	}
	assertMergedHistory(t, m, name, true)
	metadata, err := readFileMetadata(m.directory, name)
	if err != nil || metadata.Verified || metadata.CheckedThrough != 0 {
		t.Fatalf("offline merge incorrectly verified legacy provider coverage: %+v %v", metadata, err)
	}
	if data, err := os.ReadFile(trace); !os.IsNotExist(err) {
		t.Fatalf("local enrichment contacted its data provider: %s %v", data, err)
	}
	m.Close()
	restarted := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
	defer restarted.Close()
	assertMergedHistory(t, restarted, name, true)
	page, err := restarted.Files(0, 20)
	if err != nil || page.Total != 1 || page.Files[0].ID != info.Jobs[0].FileID {
		t.Fatalf("restart restored duplicate histories: %+v %v", page, err)
	}
}

func TestRefreshConsolidatesLegacyRangesBeforeProviderDenial(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	ids := seedDuplicateHistories(t, m)
	folder := t.TempDir()
	trace, preload := filepath.Join(folder, "trace"), filepath.Join(folder, "denied.cjs")
	module, _ := json.Marshal(filepath.Join(filepath.Dir(m.runtime.worker), "node_modules", "dukascopy-node"))
	traceJS, _ := json.Marshal(trace)
	code := fmt.Sprintf(`const fs=require('node:fs'),api=require(%s);api.BufferFetcher.prototype.fetchBuffer=async function(url){fs.appendFileSync(%s,url+'\n');throw new api.WafTokenError('Headless browser could not acquire a WAF token for '+url+' (last HTTP status: 403, WAF action: none)');};`, module, traceJS)
	if err := os.WriteFile(preload, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	started, err := m.StartRefresh()
	if err != nil || started.Total != 1 {
		t.Fatalf("duplicate ranges were refreshed independently: %+v %v", started, err)
	}
	info := waitRefresh(t, m, ids)
	if info.Failed != 1 || info.Completed != 0 || len(info.Jobs) != 1 || !strings.Contains(info.Jobs[0].Message, "HTTP 403") {
		t.Fatalf("expected provider denial after local merge: %+v", info)
	}
	name := "eurusd-d1-2024-01-01-2024-01-06.csv"
	if info.Jobs[0].Name != name || info.Jobs[0].FileID != dataFileID(m.directory, name) {
		t.Fatalf("failed refresh did not retain renamed merged history: %+v", info.Jobs)
	}
	assertMergedHistory(t, m, name, false)
	data, err := os.ReadFile(trace)
	if err != nil || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("provider denial retried per legacy range: %s %v", data, err)
	}
}

func TestCancelledConsolidationKeepsEveryOriginalAndDrainsScratch(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	ids := seedDuplicateHistories(t, m)
	folder := t.TempDir()
	started, preload := filepath.Join(folder, "started"), filepath.Join(folder, "gate.cjs")
	startedJS, _ := json.Marshal(started)
	code := fmt.Sprintf(`const fs=require('node:fs/promises'),path=require('node:path'),open=fs.open;fs.open=async function(filename,...args){if(path.basename(filename)==='consolidated.csv'){await fs.writeFile(%s,process.cwd());await new Promise(()=>{});}return open.call(this,filename,...args);};`, startedJS)
	if err := os.WriteFile(preload, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "hour"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var work []byte
	for time.Now().Before(deadline) {
		work, _ = os.ReadFile(started)
		if len(work) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(work) == 0 {
		t.Fatal("consolidation did not reach its staged output")
	}
	m.CancelEnrichment()
	info := waitEnrichment(t, m, ids)
	if info.Cancelled != 1 || info.Completed != 0 || info.Failed != 0 {
		t.Fatalf("consolidation was not cancelled: %+v", info)
	}
	for name, expected := range map[string]string{refreshName: refreshCSV, duplicateHistoryName: duplicateHistoryCSV} {
		actual, err := os.ReadFile(filepath.Join(m.directory, name))
		if err != nil || string(actual) != expected {
			t.Fatalf("cancellation changed original %s: %q %v", name, actual, err)
		}
	}
	if _, err := os.Stat(string(work)); !os.IsNotExist(err) {
		t.Fatalf("cancelled consolidation left scratch or a live writer: %v", err)
	}
}

func TestRefreshAfterConsolidationRepairsUnverifiedLegacyInteriorGap(t *testing.T) {
	m, _, _ := refreshManager(t, false)
	writeDataFile(t, m, refreshName, refreshCSV)
	// January 2 is absent and lies outside the recent overlap around January
	// 10. Consolidating these files must not skip first-time full verification.
	otherName := "eurusd-d1-2024-01-02-2024-01-11.csv"
	writeDataFile(t, m, otherName, "timestamp,open,high,low,close,volume,ema_2\n1704844800000,100,101,99,100,10,\n")
	started, err := m.StartRefresh()
	if err != nil || started.Total != 1 {
		t.Fatalf("refresh start: %+v %v", started, err)
	}
	info := waitRefresh(t, m, []string{dataFileID(m.directory, refreshName)})
	if info.Completed != 1 || info.Failed != 0 || len(info.Jobs) != 1 {
		t.Fatalf("consolidated refresh failed: %+v", info)
	}
	data, err := os.ReadFile(filepath.Join(m.directory, info.Jobs[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows[1:] {
		if row[0] == "1704153600000" {
			found = true
			if row[4] != "20" || row[6] != "15" {
				t.Fatalf("repaired gap did not replay its indicators: %v", row)
			}
		}
	}
	if !found {
		t.Fatal("offline consolidation caused refresh to skip the legacy interior gap")
	}
}
