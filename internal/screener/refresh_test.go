package screener

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const refreshCSV = "timestamp,open,high,low,close,volume,sma_2\n1704067200000,10,11,9,10,10,\n1704240000000,50,51,49,50,10,30\n"
const refreshName = "eurusd-d1-2024-01-01-2024-01-04.csv"

// Only the actual pinned downloader transport is replaced. These child Node
// processes run production validation, source planning, wire decoding, merging,
// trading-signals calculations, owner pipe and Go publication without networking.
func refreshManager(t *testing.T, gated bool) (*Manager, string, string) {
	t.Helper()
	m := sourceEnrichmentManager(t, false)
	folder := t.TempDir()
	trace, gates := filepath.Join(folder, "trace"), filepath.Join(folder, "gates")
	if err := os.Mkdir(gates, 0700); err != nil {
		t.Fatal(err)
	}
	module, _ := json.Marshal(filepath.Join(filepath.Dir(m.runtime.worker), "node_modules", "dukascopy-node"))
	traceJS, _ := json.Marshal(trace)
	gatesJS, _ := json.Marshal(gates)
	bootstrap := fmt.Sprintf(`
const fs=require('node:fs/promises'),path=require('node:path');
const api=require(%s),trace=%s,gates=%s,gated=%t;
api.BufferFetcher.prototype.fetchBuffer=async function(url){
  await fs.appendFile(trace,url+'\n');
  if(gated){await fs.writeFile(path.join(gates,'started'),'');while(true){try{await fs.stat(path.join(gates,'release'));break;}catch{await new Promise(resolve=>setTimeout(resolve,5));}}}
  const parsed=new URL(url),last=Number(parsed.pathname.split('/').at(-1));
  const year=Number.isFinite(last)?last:new Date(Number(parsed.searchParams.get('from'))).getUTCFullYear();
  return Buffer.from(JSON.stringify({timestamp:Date.UTC(year,0,1),shift:86400000,multiplier:1,open:10,high:11,low:9,close:10,
    times:[0,1,1],opens:[0,10,10],highs:[0,10,10],lows:[0,10,10],closes:[0,10,10],volumes:[10,10,10]}));
};
`, module, traceJS, gatesJS, gated)
	preload := filepath.Join(folder, "transport.cjs")
	if err := os.WriteFile(preload, []byte(bootstrap), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	return m, trace, gates
}

func waitRefresh(t *testing.T, m *Manager, ids []string) RefreshInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		info, err := m.Refresh(ids)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Active {
			return info
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("refresh did not finish")
	return RefreshInfo{}
}

func assertNoRefreshScratch(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".cryptodesk-refresh-") && entry.Name() != ".cryptodesk-refresh-backups" {
			t.Fatalf("refresh scratch remains: %s", entry.Name())
		}
	}
}

func TestRefreshRepairsHistoryReplaysLegacyIndicatorsBacksUpAndPersistsVerifiedCoverage(t *testing.T) {
	m, trace, _ := refreshManager(t, false)
	path := writeDataFile(t, m, refreshName, refreshCSV)
	id := dataFileID(m.directory, refreshName)
	if _, err := m.StartRefresh(); err != nil {
		t.Fatal(err)
	}
	info := waitRefresh(t, m, []string{id})
	if info.Completed != 1 || info.Failed != 0 || len(info.Jobs) != 1 || info.Jobs[0].AddedRows < 1 || info.Jobs[0].UpdatedRows != 1 {
		t.Fatalf("refresh: %+v", info)
	}
	backup, err := os.ReadFile(info.Jobs[0].BackupPath)
	if err != nil || string(backup) != refreshCSV {
		t.Fatalf("backup is not exact original: %q %v", backup, err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(actual))).ReadAll()
	if err != nil || rows[2][6] != "15" || rows[3][6] != "25" || rows[3][4] != "30" {
		t.Fatalf("data gap and indicators: %q %v", actual, err)
	}
	metadata, err := readFileMetadata(m.directory, refreshName)
	st, _ := os.Stat(path)
	if err != nil || !metadata.Verified || !metadataMatches(metadata, st) || len(metadata.Recipes) != 1 || metadata.Recipes[0].Indicator != "sma" {
		t.Fatalf("durable recipe and coverage metadata: %+v %v", metadata, err)
	}
	if metadata.CheckedThrough < time.Now().Add(-time.Minute).UnixMilli() {
		t.Fatalf("successful checked-through watermark was not retained: %+v", metadata)
	}
	page, err := m.Files(0, 50)
	if err != nil || page.Files[0].FirstTimestamp != 1704067200000 || page.Files[0].LastTimestamp <= 1704240000000 {
		t.Fatalf("actual file period: %+v %v", page, err)
	}
	firstRequests, _ := os.ReadFile(trace)
	m.Close()
	restarted := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
	defer restarted.Close()
	if _, err = restarted.StartRefresh(); err != nil {
		t.Fatal(err)
	}
	info = waitRefresh(t, restarted, []string{id})
	if info.Completed != 1 || info.Jobs[0].AddedRows != 0 || info.Jobs[0].UpdatedRows != 0 {
		t.Fatalf("repeat refresh: %+v", info)
	}
	requests, _ := os.ReadFile(trace)
	if strings.Count(string(requests), "\n")-strings.Count(string(firstRequests), "\n") != 1 {
		t.Fatalf("verified repeat did not restrict to recent overlap: %s", requests)
	}
	second, _ := os.ReadFile(path)
	if string(second) != string(actual) {
		t.Fatal("repeat refresh duplicated or changed rows")
	}
	assertNoRefreshScratch(t, m.directory)
}

func TestRefreshCancellationCloseAndConcurrentSourceChangesKeepOriginalData(t *testing.T) {
	for _, mode := range []string{"cancel", "close", "source-change"} {
		t.Run(mode, func(t *testing.T) {
			m, _, gates := refreshManager(t, true)
			path := writeDataFile(t, m, refreshName, refreshCSV)
			if _, err := m.StartRefresh(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(gates, "started")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("refresh transport did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if _, err := m.ConfigureDirectory(t.TempDir()); err == nil {
				t.Fatal("changed folder during refresh")
			}
			if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "sma"}); err == nil {
				t.Fatal("enrichment overlapped mutation")
			}
			if _, err := m.Start(Request{Symbols: []string{"btcusd"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-02"}); err == nil {
				t.Fatal("download exceeded shared refresh request budget")
			}
			if _, err := m.Resume(); err == nil {
				t.Fatal("download resumed during refresh")
			}
			expected := refreshCSV
			if mode == "source-change" {
				expected += "1704326400000,40,41,39,40,10,45\n"
				if err := os.WriteFile(path, []byte(expected), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(gates, "release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else if mode == "close" {
				m.Close()
			} else {
				m.CancelRefresh()
			}
			info := waitRefresh(t, m, []string{dataFileID(m.directory, refreshName)})
			if mode == "source-change" && (info.Failed != 1 || !strings.Contains(info.Jobs[0].Message, "source CSV changed")) {
				t.Fatalf("source change not detected: %+v", info)
			} else if mode != "source-change" && info.Cancelled != 1 {
				t.Fatalf("worker not cancelled: %+v", info)
			}
			actual, _ := os.ReadFile(path)
			if string(actual) != expected {
				t.Fatal("source was replaced after cancel or concurrent edit")
			}
			assertNoRefreshScratch(t, m.directory)
		})
	}
}

func TestRefreshFailuresAreIndependentAndUnknownColumnsArePreserved(t *testing.T) {
	m, _, _ := refreshManager(t, false)
	writeDataFile(t, m, refreshName, refreshCSV)
	unknownName := "gbpusd-d1-2024-01-01-2024-01-04.csv"
	unknown := strings.Replace(refreshCSV, "sma_2", "custom_unknown", 1)
	unknownPath := writeDataFile(t, m, unknownName, unknown)
	customPath := writeDataFile(t, m, "unknown.csv", refreshCSV)
	page, _ := m.Files(0, 50)
	ids := []string{}
	for _, file := range page.Files {
		ids = append(ids, file.ID)
	}
	if _, err := m.StartRefresh(); err != nil {
		t.Fatal(err)
	}
	info := waitRefresh(t, m, ids)
	if info.Completed != 1 || info.Failed != 2 || !strings.Contains(info.Jobs[1].Message, "unrecognized derived") || !strings.Contains(info.Jobs[2].Message, "cannot identify") {
		t.Fatalf("independent results: %+v", info)
	}
	data, _ := os.ReadFile(unknownPath)
	custom, _ := os.ReadFile(customPath)
	if string(data) != unknown || string(custom) != refreshCSV {
		t.Fatal("failed CSV was changed")
	}
	if _, err := m.Refresh(make([]string, 201)); err == nil {
		t.Fatal("unbounded progress polling accepted")
	}
}

func TestAppliedRecipeMetadataSurvivesEnrichmentAndRejectsFutureState(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	name := "btcusd-m1-2024-01-01-2024-01-02.csv"
	path := writeDataFile(t, m, name, candleCSV)
	for _, request := range []EnrichmentRequest{{Indicator: "sma", Parameters: map[string]float64{"period": 3}}, {Indicator: "tenkan", Parameters: map[string]float64{"period": 2}}} {
		if _, err := m.StartEnrichment(request); err != nil {
			t.Fatal(err)
		}
		info := waitEnrichment(t, m, nil)
		if info.Completed != 1 {
			t.Fatalf("enrichment: %+v", info)
		}
	}
	metadata, err := readFileMetadata(m.directory, name)
	if err != nil || len(metadata.Recipes) != 2 || !reflect.DeepEqual(metadata.Recipes[0].Parameters, map[string]float64{"period": 3}) {
		t.Fatalf("applied recipes lost: %+v %v", metadata, err)
	}
	before, _ := os.ReadFile(path)
	metadataPath := filepath.Join(metadataDirectory(m.directory), dataFileID(m.directory, name)+".json")
	future := []byte(`{"version":999,"filename":"` + name + `"}`)
	if err := os.WriteFile(metadataPath, future, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.StartEnrichment(EnrichmentRequest{Indicator: "ema"}); err != nil {
		t.Fatal(err)
	}
	info := waitEnrichment(t, m, []string{dataFileID(m.directory, name)})
	if info.Failed != 1 || !strings.Contains(info.Jobs[0].Message, "unsupported version") {
		t.Fatalf("future metadata accepted: %+v", info)
	}
	after, _ := os.ReadFile(path)
	current, _ := os.ReadFile(metadataPath)
	if string(after) != string(before) || string(current) != string(future) {
		t.Fatal("future metadata or original CSV overwritten")
	}
}

func TestEnrichmentCannotReverifyChangedHistoryWithPreservedSizeAndTimestamp(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	name := "btcusd-m1-2024-01-01-2024-01-02.csv"
	path := writeDataFile(t, m, name, candleCSV)
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(candleCSV))
	checkedThrough := time.Now().UnixMilli()
	if err = writeFileMetadata(m.directory, name, fileMetadata{Verified: true, Bytes: original.Size(), Modified: original.ModTime().UnixNano(), Checksum: hex.EncodeToString(digest[:]), CheckedThrough: checkedThrough}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.StartEnrichment(EnrichmentRequest{Indicator: "sma", Parameters: map[string]float64{"period": 2}}); err != nil {
		t.Fatal(err)
	}
	if info := waitEnrichment(t, m, nil); info.Completed != 1 {
		t.Fatalf("initial enrichment: %+v", info)
	}
	metadata, err := readFileMetadata(m.directory, name)
	if err != nil || !metadata.Verified || metadata.CheckedThrough != checkedThrough {
		t.Fatalf("unchanged coverage lost while enriching columns: %+v %v", metadata, err)
	}
	current, _ := os.ReadFile(path)
	original, _ = os.Stat(path)
	changed := strings.Replace(string(current), "1704067260000", "1704067270000", 1)
	if err = os.WriteFile(path, []byte(changed), 0640); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err = m.StartEnrichment(EnrichmentRequest{Indicator: "sma", Parameters: map[string]float64{"period": 2}}); err != nil {
		t.Fatal(err)
	}
	info := waitEnrichment(t, m, nil)
	if info.Completed != 1 {
		t.Fatalf("enrichment failed: %+v", info)
	}
	metadata, err = readFileMetadata(m.directory, name)
	if err != nil || metadata.Verified || metadata.CheckedThrough != 0 {
		t.Fatalf("changed history was incorrectly reverified: %+v %v", metadata, err)
	}
	output, _ := os.ReadFile(path)
	calculated := sha256.Sum256(output)
	if metadata.Checksum != hex.EncodeToString(calculated[:]) {
		t.Fatal("enriched CSV checksum does not match its actual bytes")
	}
}
