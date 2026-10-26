package screener

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type filePoolJob struct {
	Name, Status, BackupPath string
}

type filePoolState struct {
	Active                       bool
	Completed, Failed, Cancelled int
	Progress                     float64
	Jobs                         []filePoolJob
}

// These tests run the real Go scheduler and production Node workers. The
// preload only gates source/output I/O and substitutes Dukascopy's HTTP
// transport; CSV validation, indicator calculation and publication stay real.
func filePoolManager(t *testing.T, operation string, count int, failFirst bool) (*Manager, string, []string, map[string]string) {
	t.Helper()
	m := sourceEnrichmentManager(t, false)
	gates := t.TempDir()
	names, originals := make([]string, count), map[string]string{}
	for index := range count {
		name := fmt.Sprintf("eurusd-d1-2024-01-01-2024-02-%02d.csv", index+1)
		text := refreshCSV
		if operation == "enrichment" {
			text = candleCSV
			if failFirst && index == 0 {
				text = strings.Replace(text, "close", "missingClose", 1)
			}
		}
		writeDataFile(t, m, name, text)
		names[index], originals[name] = name, text
	}
	settings, _ := json.Marshal(struct {
		Directory, Gates, Operation, Module, Fail string
	}{m.directory, gates, operation, filepath.Join(filepath.Dir(m.runtime.worker), "node_modules", "dukascopy-node"), names[0]})
	bootstrap := fmt.Sprintf(`
const fs=require('node:fs'),fsp=require('node:fs/promises'),path=require('node:path');
const config=%s;
let source;
const createReadStream=fs.createReadStream;
fs.createReadStream=function(filename,...args){
  if(path.dirname(filename)===config.Directory)source=path.basename(filename);
  return createReadStream.call(this,filename,...args);
};
async function gate(){
  if(!source)throw Error('Test source was not discovered');
  await fsp.writeFile(path.join(config.Gates,source+'.started'),JSON.stringify({directory:process.cwd(),pid:process.pid}));
  while(true){
    try{await fsp.stat(path.join(config.Gates,source+'.release'));return;}
    catch{await new Promise(resolve=>setTimeout(resolve,5));}
  }
}
if(config.Operation==='enrichment'){
  const open=fsp.open;
  fsp.open=async function(filename,...args){if(path.basename(filename)==='enriched.csv')await gate();return open.call(this,filename,...args);};
}else{
  const api=require(config.Module);
  api.BufferFetcher.prototype.fetchBuffer=async function(url){
    // Let the first shared-session probe finish, then hold subsequent source
    // fetches to prove all twenty production workers progress concurrently.
    let initial=false;
    try{await fsp.writeFile(path.join(config.Gates,'session-ready'),'',{flag:'wx'});initial=true;}
    catch(error){if(error.code!=='EEXIST')throw error;}
    if(!initial)await gate();
    if(!initial&&%t&&source===config.Fail)throw Error('Independent test upstream failure');
    const parsed=new URL(url),last=Number(parsed.pathname.split('/').at(-1));
    const year=Number.isFinite(last)?last:new Date(Number(parsed.searchParams.get('from'))).getUTCFullYear();
    return Buffer.from(JSON.stringify({timestamp:Date.UTC(year,0,1),shift:86400000,multiplier:1,open:10,high:11,low:9,close:10,
      times:[0,1,1],opens:[0,10,10],highs:[0,10,10],lows:[0,10,10],closes:[0,10,10],volumes:[10,10,10]}));
  };
}
`, settings, failFirst)
	preload := filepath.Join(gates, "transport.cjs")
	if err := os.WriteFile(preload, []byte(bootstrap), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	return m, gates, names, originals
}

func startFileBatch(t *testing.T, m *Manager, operation string) {
	t.Helper()
	var err error
	if operation == "enrichment" {
		_, err = m.StartEnrichment(EnrichmentRequest{Indicator: "sma", Parameters: map[string]float64{"period": 3}})
	} else {
		_, err = m.StartRefresh()
	}
	if err != nil {
		t.Fatal(err)
	}
}

func filePoolSnapshot(t *testing.T, m *Manager, operation string, names []string) filePoolState {
	t.Helper()
	ids := make([]string, len(names))
	for index, name := range names {
		ids[index] = dataFileID(m.directory, name)
	}
	if operation == "enrichment" {
		info, err := m.Enrichment(ids)
		if err != nil {
			t.Fatal(err)
		}
		state := filePoolState{Active: info.Active, Completed: info.Completed, Failed: info.Failed, Cancelled: info.Cancelled, Progress: info.Progress}
		for _, job := range info.Jobs {
			state.Jobs = append(state.Jobs, filePoolJob{job.Name, job.Status, job.BackupPath})
		}
		return state
	}
	info, err := m.Refresh(ids)
	if err != nil {
		t.Fatal(err)
	}
	state := filePoolState{Active: info.Active, Completed: info.Completed, Failed: info.Failed, Cancelled: info.Cancelled, Progress: info.Progress}
	for _, job := range info.Jobs {
		state.Jobs = append(state.Jobs, filePoolJob{job.Name, job.Status, job.BackupPath})
	}
	return state
}

func waitFileBatch(t *testing.T, m *Manager, operation string, names []string) filePoolState {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		state := filePoolSnapshot(t, m, operation, names)
		if !state.Active {
			return state
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("file worker pool did not drain")
	return filePoolState{}
}

func waitFileGate(t *testing.T, gates, name string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(gates, name+".started"))
		var child struct{ Directory string }
		if err == nil && json.Unmarshal(data, &child) == nil {
			return child.Directory
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file worker did not start: %s", name)
	return ""
}

func TestEnrichmentAndRefreshRunTwentyRealWorkersAndRefillAfterIndependentFailure(t *testing.T) {
	for _, operation := range []string{"enrichment", "refresh"} {
		t.Run(operation, func(t *testing.T) {
			m, gates, names, originals := filePoolManager(t, operation, 22, true)
			startFileBatch(t, m, operation)
			private := map[string]bool{}
			for _, name := range names[:20] {
				directory := waitFileGate(t, gates, name)
				if private[directory] {
					t.Fatal("parallel file workers share a scratch or WAF directory")
				}
				private[directory] = true
			}
			state := filePoolSnapshot(t, m, operation, names)
			for index, job := range state.Jobs {
				if job.Name != names[index] || (index < 20 && job.Status != "running") || (index >= 20 && job.Status != "queued") {
					t.Fatalf("twenty-worker cap/order was not retained: %+v", state)
				}
			}
			assertNotStarted(t, gates, names[20])
			assertNotStarted(t, gates, names[21])
			releaseGate(t, gates, names[0])
			waitFileGate(t, gates, names[20])
			state = filePoolSnapshot(t, m, operation, names)
			if state.Failed != 1 || state.Jobs[21].Status != "queued" {
				t.Fatalf("a per-file failure did not refill exactly one worker: %+v", state)
			}
			releaseGate(t, gates, names[1])
			waitFileGate(t, gates, names[21])
			for _, name := range names[2:] {
				releaseGate(t, gates, name)
			}
			state = waitFileBatch(t, m, operation, names)
			if state.Completed != 21 || state.Failed != 1 || state.Cancelled != 0 || state.Progress != 100 {
				t.Fatalf("independent final states: %+v", state)
			}
			for index, job := range state.Jobs {
				data, err := os.ReadFile(filepath.Join(m.directory, job.Name))
				if err != nil {
					t.Fatal(err)
				}
				if index == 0 {
					if string(data) != originals[job.Name] {
						t.Fatal("failed file was overwritten")
					}
					continue
				}
				backup, err := os.ReadFile(job.BackupPath)
				if err != nil || string(backup) != originals[job.Name] {
					t.Fatalf("parallel publication did not preserve exact original: %s %v", job.Name, err)
				}
				rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
				if err != nil || len(rows) < 4 {
					t.Fatalf("invalid published CSV: %q %v", data, err)
				}
				if operation == "enrichment" && rows[3][6] != "2" {
					t.Fatalf("real library values were not independently calculated: %q", data)
				}
				if operation == "refresh" && (rows[2][6] != "15" || rows[3][6] != "25") {
					t.Fatalf("parallel refresh did not repair and replay indicators: %q", data)
				}
				metadata, err := readFileMetadata(m.directory, job.Name)
				if err != nil || len(metadata.Recipes) != 1 {
					t.Fatalf("parallel recipe metadata missing: %s %+v %v", job.Name, metadata, err)
				}
				if operation == "refresh" {
					info, err := m.Refresh(nil)
					if err != nil || info.AsOf == 0 || metadata.CheckedThrough != info.AsOf {
						t.Fatalf("active and queued files used different execution cutoffs: %s %+v %+v %v", job.Name, metadata, info, err)
					}
				}
			}
			assertNoEnrichmentScratch(t, m.directory)
			assertNoRefreshScratch(t, m.directory)
		})
	}
}

func TestEnrichmentAndRefreshCancelOrCloseDrainTwentyWorkersAndCancelQueuedFiles(t *testing.T) {
	for _, operation := range []string{"enrichment", "refresh"} {
		for _, shutdown := range []string{"cancel", "close"} {
			t.Run(operation+"/"+shutdown, func(t *testing.T) {
				m, gates, names, originals := filePoolManager(t, operation, 22, false)
				startFileBatch(t, m, operation)
				for _, name := range names[:20] {
					waitFileGate(t, gates, name)
				}
				assertNotStarted(t, gates, names[20])
				// Keep a committed result while cancelling twenty running workers
				// and a queued file. Their teardown must not undo earlier commits.
				releaseGate(t, gates, names[0])
				waitFileGate(t, gates, names[20])
				published, err := os.ReadFile(filepath.Join(m.directory, names[0]))
				if err != nil || string(published) == originals[names[0]] {
					t.Fatalf("first result did not publish before cancelling: %v", err)
				}
				assertNotStarted(t, gates, names[21])
				if shutdown == "close" {
					m.Close()
				} else if operation == "enrichment" {
					m.CancelEnrichment()
				} else {
					m.CancelRefresh()
				}
				state := waitFileBatch(t, m, operation, names)
				if state.Cancelled != len(names)-1 || state.Completed != 1 || state.Failed != 0 || state.Progress != 100 {
					t.Fatalf("cancellation did not drain every queued/running file: %+v", state)
				}
				assertNotStarted(t, gates, names[21])
				for index, job := range state.Jobs {
					data, err := os.ReadFile(filepath.Join(m.directory, job.Name))
					if index == 0 {
						if err != nil || job.Status != "completed" || string(data) != string(published) {
							t.Fatalf("cancellation changed a committed result: %+v %v", job, err)
						}
						backup, err := os.ReadFile(job.BackupPath)
						if err != nil || string(backup) != originals[job.Name] {
							t.Fatalf("cancellation changed its exact original backup: %+v %v", job, err)
						}
						continue
					}
					if err != nil || job.Status != "cancelled" || string(data) != originals[job.Name] {
						t.Fatalf("cancelled file was changed or left active: %+v %v", job, err)
					}
				}
				assertNoEnrichmentScratch(t, m.directory)
				assertNoRefreshScratch(t, m.directory)
			})
		}
	}
}

func TestFileBatchProgressIncludesAllRunningAndTerminalJobs(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	m.mu.Lock()
	m.enrichmentJobs = []EnrichmentJob{{Status: "running", Progress: 30}, {Status: "running", Progress: 60}, {Status: "completed", Progress: 100}, {Status: "failed"}, {Status: "cancelled"}, {Status: "queued"}}
	m.refreshJobs = []RefreshJob{{Status: "running", Progress: 30}, {Status: "running", Progress: 60}, {Status: "completed", Progress: 100}, {Status: "failed"}, {Status: "cancelled"}, {Status: "queued"}}
	enrichment, refresh := m.enrichmentInfoLocked(nil), m.refreshInfoLocked(nil)
	m.mu.Unlock()
	if math.Abs(enrichment.Progress-65) > 1e-9 || math.Abs(refresh.Progress-65) > 1e-9 {
		t.Fatalf("aggregate progress excluded running/terminal workers: %+v %+v", enrichment, refresh)
	}
}
