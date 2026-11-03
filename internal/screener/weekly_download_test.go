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
	"time"
)

func weeklyRequest() Request {
	return Request{Symbols: []string{"eurusd"}, Timeframe: "w1", From: "2024-01-01", To: "2024-01-15", Concurrency: 20}
}

func weeklyDailyCSV(first, last int, enriched bool) string {
	var text strings.Builder
	text.WriteString("timestamp,open,high,low,close,volume")
	if enriched {
		text.WriteString(",sma_2")
	}
	text.WriteByte('\n')
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for i := first; i < last; i++ {
		fmt.Fprintf(&text, "%d,%d,%d,%d,%d,%d", base+int64(i)*86_400_000, i+10, i+11, i+9, i+10, i+1)
		if enriched {
			if i == first {
				text.WriteByte(',')
			} else {
				fmt.Fprintf(&text, ",%g", float64(i)+9.5)
			}
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func assertWeeklyDownloadRows(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	want := [][]string{
		{"timestamp", "open", "high", "low", "close", "volume"},
		{"1704067200000", "10", "17", "9", "16", "28"},
		{"1704672000000", "17", "24", "16", "23", "77"},
	}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("incorrect weekly OHLCV: %s %v", data, err)
	}
}

func TestWeeklyDownloadReusesCoveredDailyHistoryWithoutNetworkAndRestoresQueue(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	name := "eurusd-d1-2024-01-01-2024-01-15.csv"
	original := weeklyDailyCSV(0, 14, true)
	full := writeDataFile(t, m, name, original)
	before, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	preload := filepath.Join(t.TempDir(), "no-network.cjs")
	module, _ := json.Marshal(filepath.Join(filepath.Dir(m.runtime.worker), "node_modules", "dukascopy-node"))
	if err := os.WriteFile(preload, []byte(fmt.Sprintf("require(%s).BufferFetcher.prototype.fetchBuffer=async()=>{throw Error('Unexpected provider request');};", module)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	if _, err := m.Start(weeklyRequest()); err != nil {
		t.Fatal(err)
	}
	info := waitFinished(t, m)
	if len(info.Jobs) != 2 || info.Jobs[0].Timeframe != "d1" || info.Jobs[1].Timeframe != "w1" || info.Jobs[0].Status != "completed" || info.Jobs[1].Status != "completed" {
		t.Fatalf("covered weekly batch did not complete both retained periods: %+v", info)
	}
	data, err := os.ReadFile(full)
	after, statErr := os.Stat(full)
	if err != nil || statErr != nil || string(data) != original || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("covered daily history or enriched columns were rewritten: %s %v %v", data, err, statErr)
	}
	assertWeeklyDownloadRows(t, info.Jobs[1].OutputPath)
	m.Close()
	restarted := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
	defer restarted.Close()
	restored := restarted.Info()
	if restored.Error != "" || restored.Active || len(restored.Jobs) != 2 || restored.Jobs[0].Status != "completed" || restored.Jobs[1].Status != "completed" || restored.Jobs[1].OutputPath != info.Jobs[1].OutputPath {
		t.Fatalf("paired weekly queue did not restore: %+v", restored)
	}
	page, err := restarted.Files(0, 20)
	if err != nil || page.Total != 2 || page.PeriodCounts["d1"] != 1 || page.PeriodCounts["w1"] != 1 {
		t.Fatalf("daily or weekly inventory lost on restart: %+v %v", page, err)
	}
}

// Replace only the pinned public source transport. All source planning,
// incremental merging, weekly aggregation and publication execute normally.
func weeklyTransportManager(t *testing.T, gated bool) (*Manager, string, string) {
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
	bootstrap := fmt.Sprintf(`const fs=require('node:fs/promises'),path=require('node:path');
const api=require(%s),trace=%s,gates=%s,gated=%t;
api.BufferFetcher.prototype.fetchBuffer=async function(url){
 await fs.appendFile(trace,url+'\n');await fs.writeFile(path.join(gates,'started'),'');
 if(gated){while(true){try{await fs.stat(path.join(gates,'release'));break;}catch{await new Promise(resolve=>setTimeout(resolve,5));}}}
 try{await fs.stat(path.join(gates,'fail'));throw Error('Injected daily source failure');}catch(error){if(error.code!=='ENOENT')throw error;}
 return Buffer.from(JSON.stringify({timestamp:Date.UTC(2024,0,1),shift:86400000,multiplier:1,open:10,high:11,low:9,close:10,
 times:[0,...Array(13).fill(1)],opens:[0,...Array(13).fill(1)],highs:[0,...Array(13).fill(1)],lows:[0,...Array(13).fill(1)],closes:[0,...Array(13).fill(1)],volumes:Array.from({length:14},(_,i)=>i+1)}));
};`, module, traceJS, gatesJS, gated)
	preload := filepath.Join(folder, "transport.cjs")
	if err := os.WriteFile(preload, []byte(bootstrap), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	return m, trace, gates
}

func TestWeeklyDownloadExtendsDailyHistoryAndPreservesDailyIndicators(t *testing.T) {
	m, trace, _ := weeklyTransportManager(t, false)
	original := weeklyDailyCSV(2, 7, true)
	writeDataFile(t, m, "eurusd-d1-2024-01-03-2024-01-08.csv", original)
	if _, err := m.Start(weeklyRequest()); err != nil {
		t.Fatal(err)
	}
	info := waitFinished(t, m)
	if len(info.Jobs) != 2 || info.Jobs[0].Status != "completed" || info.Jobs[1].Status != "completed" {
		t.Fatalf("weekly prerequisite extension failed: %+v", info)
	}
	data, err := os.ReadFile(info.Jobs[0].OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(rows) != 15 || rows[0][6] != "sma_2" || rows[1][6] != "" || rows[2][6] != "10.5" || rows[14][6] != "22.5" {
		t.Fatalf("daily source extension lost rows or indicator values: %s %v", data, err)
	}
	assertWeeklyDownloadRows(t, info.Jobs[1].OutputPath)
	requests, err := os.ReadFile(trace)
	if err != nil || strings.Count(string(requests), "\n") != 2 {
		t.Fatalf("daily prefix/suffix were not fetched once: %s %v", requests, err)
	}
}

func TestWeeklyDownloadFailedDailyPrerequisiteBlocksConversionUntilResume(t *testing.T) {
	m, _, gates := weeklyTransportManager(t, false)
	if err := os.WriteFile(filepath.Join(gates, "fail"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(weeklyRequest()); err != nil {
		t.Fatal(err)
	}
	info := waitFinished(t, m)
	if len(info.Jobs) != 2 || info.Jobs[0].Status != "failed" || info.Jobs[1].Status != "failed" || !strings.Contains(info.Jobs[1].Message, "Daily prerequisite") || info.Jobs[1].OutputPath != "" {
		t.Fatalf("failed daily prerequisite published weekly history: %+v", info)
	}
	if names, err := csvNames(m.directory); err != nil || len(names) != 0 {
		t.Fatalf("failed daily source left a published history: %v %v", names, err)
	}
	if err := os.Remove(filepath.Join(gates, "fail")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Resume(); err != nil {
		t.Fatal(err)
	}
	info = waitFinished(t, m)
	if info.Jobs[0].Status != "completed" || info.Jobs[1].Status != "completed" {
		t.Fatalf("weekly retry did not rerun its daily prerequisite: %+v", info)
	}
	assertWeeklyDownloadRows(t, info.Jobs[1].OutputPath)
}

func TestWeeklyDownloadCancelOrRestartDuringDailyFetchPreservesSource(t *testing.T) {
	for _, mode := range []string{"cancel", "restart"} {
		t.Run(mode, func(t *testing.T) {
			m, trace, gates := weeklyTransportManager(t, true)
			original := weeklyDailyCSV(2, 7, true)
			full := writeDataFile(t, m, "eurusd-d1-2024-01-03-2024-01-08.csv", original)
			if _, err := m.Start(weeklyRequest()); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(gates, "started")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("daily source request did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if mode == "cancel" {
				info := m.Cancel()
				if info.Jobs[0].Status != "cancelled" || info.Jobs[1].Status != "cancelled" {
					t.Fatalf("cancel did not stop the paired batch: %+v", info)
				}
			}
			m.Close()
			data, err := os.ReadFile(full)
			if err != nil || string(data) != original {
				t.Fatalf("unfinished daily prerequisite changed original source: %s %v", data, err)
			}
			before, _ := os.ReadFile(trace)
			if err := os.WriteFile(filepath.Join(gates, "release"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			restarted := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
			defer restarted.Close()
			info := waitFinished(t, restarted)
			if info.Error != "" || len(info.Jobs) != 2 {
				t.Fatalf("weekly queue failed to restore: %+v", info)
			}
			if mode == "restart" {
				if info.Jobs[0].Status != "completed" || info.Jobs[1].Status != "completed" {
					t.Fatalf("restart did not finish weekly prerequisites and conversion: %+v", info)
				}
				assertWeeklyDownloadRows(t, info.Jobs[1].OutputPath)
			} else {
				after, _ := os.ReadFile(trace)
				if info.Active || info.Jobs[0].Status != "cancelled" || info.Jobs[1].Status != "cancelled" || string(before) != string(after) {
					t.Fatalf("cancelled weekly batch resumed after restart: %+v", info)
				}
			}
		})
	}
}
