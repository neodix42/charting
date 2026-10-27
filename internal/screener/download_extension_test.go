package screener

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoveredDownloadRejectsAnotherHistoryIntroducedAfterPlanning(t *testing.T) {
	m := testManager(t)
	if err := checkDirectory(m.directory); err != nil {
		t.Fatal(err)
	}
	name := "eurusd-d1-2024-01-01-2024-01-04.csv"
	contents := "timestamp,open,high,low,close,volume\n1704067200000,10,11,9,10,1\n"
	full := writeDataFile(t, m, name, contents)
	job := Job{ID: strings.Repeat("a", 32), Symbol: "eurusd", Timeframe: "d1", From: "2024-01-01", To: "2024-01-04", AsOf: time.Now().UnixMilli(), Status: "running"}
	work := t.TempDir()
	plan, originals, err := prepareDownloadPlan(job, m.directory, work)
	if err != nil {
		t.Fatal(err)
	}
	_, checksum, err := historyFingerprint(full)
	if err != nil {
		t.Fatal(err)
	}
	other := writeDataFile(t, m, "eurusd-d1-2024-01-02-2024-01-04.csv", contents)
	m.jobs, m.publication = []Job{job}, map[string]string{}
	_, _, err = m.publishExtendedDownload(context.Background(), 0, job, m.directory, work, filepath.Join(work, "data.part"), plan, originals, workerEvent{NoChange: true, Checksum: checksum, FirstTimestamp: 1704067200000, LastTimestamp: 1704067200000})
	if err == nil || !strings.Contains(err.Error(), "another history range appeared") {
		t.Fatalf("covered download ignored an unplanned history: %v", err)
	}
	for _, path := range []string{full, other} {
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != contents {
			t.Fatalf("existing history changed: %q %v", data, readErr)
		}
	}
}

func TestFailedIncrementalDownloadRetainsCompletedRangesForResume(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	const name = "eurusd-d1-2024-01-02-2024-01-04.csv"
	original := "timestamp,open,high,low,close,volume,sma_2\n1704153600000,20,21,19,20,10,\n1704240000000,30,31,29,30,10,25\n"
	full := writeDataFile(t, m, name, original)
	folder := t.TempDir()
	trace, release := filepath.Join(folder, "trace"), filepath.Join(folder, "release")
	module, _ := json.Marshal(filepath.Join(filepath.Dir(m.runtime.worker), "node_modules", "dukascopy-node"))
	traceJS, _ := json.Marshal(trace)
	releaseJS, _ := json.Marshal(release)
	preload := filepath.Join(folder, "transport.cjs")
	bootstrap := fmt.Sprintf(`const fs=require('node:fs');const api=require(%s);let calls=0;
api.BufferFetcher.prototype.fetchBuffer=async function(url){fs.appendFileSync(%s,url+'\n');calls++;if(calls===2&&!fs.existsSync(%s))throw Error('Interrupted suffix download');
return Buffer.from(JSON.stringify({timestamp:1704067200000,shift:86400000,multiplier:1,open:10,high:11,low:9,close:10,times:[0,1,1,1],opens:[0,10,10,10],highs:[0,10,10,10],lows:[0,10,10,10],closes:[0,10,10,10],volumes:[10,10,10,10]}));};`, module, traceJS, releaseJS)
	if err := os.WriteFile(preload, []byte(bootstrap), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	request := Request{Symbols: []string{"eurusd"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-05", Concurrency: 20}
	if _, err := m.Start(request); err != nil {
		t.Fatal(err)
	}
	info := waitFinished(t, m)
	if info.Jobs[0].Status != "failed" || !strings.Contains(info.Jobs[0].Message, "Interrupted") {
		t.Fatalf("missing independent suffix failure: %+v", info)
	}
	if _, err := os.Stat(filepath.Join(m.jobDirectory(info.Jobs[0]), "range-0", "checkpoint.json")); err != nil {
		t.Fatal("completed prefix checkpoint was deleted", err)
	}
	data, _ := os.ReadFile(full)
	if string(data) != original {
		t.Fatal("failed suffix published an incomplete extension")
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Resume(); err != nil {
		t.Fatal(err)
	}
	info = waitFinished(t, m)
	full = filepath.Join(m.directory, "eurusd-d1-2024-01-01-2024-01-05.csv")
	if info.Jobs[0].Status != "completed" || info.Jobs[0].OutputPath != full {
		t.Fatalf("resume: %+v", info)
	}
	requests, _ := os.ReadFile(trace)
	if strings.Count(string(requests), "\n") != 3 {
		t.Fatalf("completed prefix was fetched again: %s", requests)
	}
	data, _ = os.ReadFile(full)
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(rows) != 5 || rows[2][6] != "15" || rows[4][6] != "35" {
		t.Fatalf("resumed data/indicators: %s %v", data, err)
	}
}

func TestCrashAfterReplacingExistingDownloadRecoversCommittedFileAndRetainsBackup(t *testing.T) {
	m := testManager(t)
	job := Job{ID: strings.Repeat("b", 32), Symbol: "btcusd", Timeframe: "d1", From: "2023-12-31", To: "2024-01-03", AsOf: time.Now().UnixMilli(), Status: "running", Progress: 95}
	m.batchDirectory = m.directory
	m.jobs = []Job{job}
	m.concurrency = 1
	name := "btcusd-d1-2024-01-01-2024-01-02.csv"
	m.publication = map[string]string{job.ID: name}
	if err := os.MkdirAll(m.directory, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("timestamp,open,high,low,close,volume\n1704067200000,10,11,9,10,1\n")
	full := writeDataFile(t, m, name, string(original))
	work, err := m.prepareJob(job)
	if err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(work, "data.part")
	updated := []byte("timestamp,open,high,low,close,volume\n1703980800000,9,10,8,9,1\n1704067200000,10,11,9,10,1\n1704153600000,11,12,10,11,1\n")
	if err := os.WriteFile(partial, updated, 0600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(m.directory, ".original.csv")
	if err := os.Link(full, backup); err != nil {
		t.Fatal(err)
	}
	if err := m.persistDownloadsLocked(); err != nil {
		t.Fatal(err)
	}
	publication := filepath.Join(work, "publish.csv")
	if err := os.Link(partial, publication); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(publication, full); err != nil {
		t.Fatal(err)
	}
	// The process died after replacement but before the completed journal write.
	restarted := reopenTestManager(t, m.profile)
	info := restarted.Info()
	if info.Active || info.Jobs[0].Status != "completed" || info.Jobs[0].OutputPath != full {
		t.Fatalf("atomic extension was not recovered: %+v", info)
	}
	data, err := os.ReadFile(full)
	if err != nil || string(data) != string(updated) {
		t.Fatal("committed extension changed", err)
	}
	data, err = os.ReadFile(backup)
	if err != nil || string(data) != string(original) {
		t.Fatal("exact original backup changed", err)
	}
	files, err := csvNames(m.directory)
	if err != nil || len(files) != 1 {
		t.Fatalf("recovery created duplicate histories: %v %v", files, err)
	}
}

func TestIncrementalDownloadCanonicalizesUppercaseFilenameAcrossRestartAndRefresh(t *testing.T) {
	m, trace, _ := refreshManager(t, false)
	const name = "EURUSD-D1-2024-01-01-2024-01-03.CSV"
	full := writeDataFile(t, m, name, "timestamp,open,high,low,close,volume\n1704067200000,10,11,9,10,10\n1704153600000,20,21,19,20,10\n")
	request := Request{Symbols: []string{"eurusd"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-04"}
	if _, err := m.Start(request); err != nil {
		t.Fatal(err)
	}
	info := waitFinished(t, m)
	full = filepath.Join(m.directory, "eurusd-d1-2024-01-01-2024-01-04.csv")
	if info.Jobs[0].Status != "completed" || info.Jobs[0].OutputPath != full {
		t.Fatalf("uppercase existing filename: %+v", info)
	}
	before, _ := os.ReadFile(trace)
	m.Close()
	restarted := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
	defer restarted.Close()
	info = restarted.Info()
	if info.Error != "" || info.Active || info.Jobs[0].Status != "completed" || info.Jobs[0].OutputPath != full {
		t.Fatalf("uppercase publication did not restore: %+v", info)
	}
	after, _ := os.ReadFile(trace)
	if string(before) != string(after) {
		t.Fatal("completed extension ran again after restart")
	}
	if _, err := restarted.StartRefresh(); err != nil {
		t.Fatal(err)
	}
	if info := waitRefresh(t, restarted, nil); info.Failed != 0 || info.Completed != 1 {
		t.Fatalf("Refresh rejected the retained filename: %+v", info)
	}
}

func TestDownloadPrependsAndAppendsExistingHistoryAndRecalculatesSavedIndicators(t *testing.T) {
	m, trace, _ := refreshManager(t, false)
	const name = "eurusd-d1-2024-01-02-2024-01-04.csv"
	original := "timestamp,open,high,low,close,volume,sma_2\n1704153600000,20,21,19,20,10,\n1704240000000,30,31,29,30,10,25\n"
	full := writeDataFile(t, m, name, original)
	request := Request{Symbols: []string{"eurusd"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-06", Concurrency: 20}
	if _, err := m.Start(request); err != nil {
		t.Fatal(err)
	}
	info := waitFinished(t, m)
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Fatalf("old range remained: %v", err)
	}
	full = filepath.Join(m.directory, "eurusd-d1-2024-01-01-2024-01-06.csv")
	if info.Jobs[0].Status != "completed" || info.Jobs[0].OutputPath != full {
		t.Fatalf("existing history was not updated: %+v", info)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(rows) != 4 || rows[2][6] != "15" || rows[3][6] != "25" {
		t.Fatalf("prepend/warm-up context: %s %v", data, err)
	}
	requests, _ := os.ReadFile(trace)
	if strings.Count(string(requests), "\n") != 2 {
		t.Fatalf("prefix and suffix were not fetched independently: %s", requests)
	}
	backups, err := filepath.Glob(filepath.Join(m.directory, ".cryptodesk-history-publications", "*", "source-0000.csv"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("original backup: %v %v", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil || string(backup) != original {
		t.Fatalf("original changed: %q %v", backup, err)
	}
	metadata, err := readFileMetadata(m.directory, filepath.Base(full))
	if err != nil || len(metadata.Coverage) != 1 || metadata.Coverage[0].From != 1704067200000 || metadata.Coverage[0].To != 1704499200000 {
		t.Fatalf("queried coverage was not retained: %+v %v", metadata, err)
	}
	before, _ := os.Stat(full)
	if _, err = m.Start(request); err != nil {
		t.Fatal(err)
	}
	info = waitFinished(t, m)
	if info.Jobs[0].Status != "completed" || !strings.Contains(info.Jobs[0].Message, "already available") {
		t.Fatalf("already covered request: %+v", info)
	}
	after, _ := os.Stat(full)
	repeated, _ := os.ReadFile(trace)
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || string(repeated) != string(requests) {
		t.Fatal("covered history was fetched or rewritten again")
	}
	page, err := m.Files(0, 20)
	if err != nil || page.Total != 1 || page.Files[0].FirstTimestamp == nil || *page.Files[0].FirstTimestamp != 1704067200000 {
		t.Fatalf("updated file period: %+v %v", page, err)
	}
	// Refresh must retain the prepended rows after the range filename changes.
	if _, err = m.StartRefresh(); err != nil {
		t.Fatal(err)
	}
	if refreshed := waitRefresh(t, m, nil); refreshed.Completed != 1 || refreshed.Failed != 0 {
		t.Fatalf("refresh after a prepend: %+v", refreshed)
	}
}

func TestIncrementalDownloadCancelAndRestartKeepOriginalAndSavedRangeCheckpoints(t *testing.T) {
	for _, mode := range []string{"cancel", "restart", "changed-source"} {
		t.Run(mode, func(t *testing.T) {
			m, _, gates := refreshManager(t, true)
			full := writeDataFile(t, m, refreshName, refreshCSV)
			request := Request{Symbols: []string{"eurusd"}, Timeframe: "d1", From: "2023-01-01", To: "2024-01-06", Concurrency: 20}
			if _, err := m.Start(request); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(gates, "started")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("incremental transport did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if _, err := m.StartEnrichment(EnrichmentRequest{Indicator: "sma"}); err == nil {
				t.Fatal("enrichment mutated an active download source")
			}
			if _, err := m.StartAnalysis(AnalysisRequest{Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(0))}}); err == nil {
				t.Fatal("Analysis read an active incremental download")
			}
			if mode == "changed-source" {
				if err := os.WriteFile(full, []byte(refreshCSV+"1704326400000,40,41,39,40,10,35\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(gates, "release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				info := waitFinished(t, m)
				if info.Jobs[0].Status != "failed" || !strings.Contains(info.Jobs[0].Message, "changed") {
					t.Fatalf("source mutation: %+v", info)
				}
				return
			}
			if mode == "cancel" {
				m.Cancel()
			} else {
				m.Close()
			}
			data, err := os.ReadFile(full)
			if err != nil || string(data) != refreshCSV {
				t.Fatal("unfinished extension changed the original")
			}
			if mode == "restart" {
				if err := os.WriteFile(filepath.Join(gates, "release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				restarted := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
				defer restarted.Close()
				info := waitFinished(t, restarted)
				full = filepath.Join(m.directory, "eurusd-d1-2023-01-01-2024-01-06.csv")
				if info.Jobs[0].Status != "completed" || info.Jobs[0].OutputPath != full {
					t.Fatalf("restart did not resume extension: %+v", info)
				}
			}
		})
	}
}

func TestDownloadConsolidatesLegacySubsetWithoutFetchingOrLosingIndicators(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	name := "eurusd-d1-2024-01-01-2024-01-04.csv"
	first := "timestamp,open,high,low,close,volume,sma_2\n1704067200000,10,11,9,10,10,\n1704153600000,20,21,19,20,10,15\n1704240000000,30,31,29,30,10,25\n"
	writeDataFile(t, m, name, first)
	old := writeDataFile(t, m, "eurusd-d1-2024-01-02-2024-01-04.csv", "timestamp,open,high,low,close,volume,weekday\n1704153600000,20,21,19,20,10,2\n1704240000000,30,31,29,30,10,3\n")
	// The saved spans fully cover this request; the real incremental worker
	// must consolidate locally and never invoke Dukascopy.
	preload := filepath.Join(t.TempDir(), "no-network.cjs")
	module, _ := json.Marshal(filepath.Join(filepath.Dir(m.runtime.worker), "node_modules", "dukascopy-node"))
	if err := os.WriteFile(preload, []byte(fmt.Sprintf("require(%s).BufferFetcher.prototype.fetchBuffer=async()=>{throw Error('Unexpected provider request');};", module)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_OPTIONS", "--require="+preload)
	request := Request{Symbols: []string{"eurusd"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-04"}
	if _, err := m.Start(request); err != nil {
		t.Fatal(err)
	}
	info := waitFinished(t, m)
	if info.Jobs[0].Status != "completed" {
		t.Fatalf("merge failed: %+v", info)
	}
	names, err := csvNames(m.directory)
	if err != nil || len(names) != 1 || names[0] != name {
		t.Fatalf("duplicate histories remain: %v %v", names, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("superseded file remains: %v", err)
	}
	data, err := os.ReadFile(info.Jobs[0].OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(rows) != 4 || strings.Join(rows[0][6:], ",") != "sma_2,weekday" || rows[1][7] != "1" || rows[3][6] != "25" {
		t.Fatalf("history or indicators lost: %s %v", data, err)
	}
	metadata, err := readFileMetadata(m.directory, name)
	if err != nil || len(metadata.Recipes) != 2 {
		t.Fatalf("indicator recipes lost: %+v %v", metadata, err)
	}
	m.Close()
	restarted := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
	defer restarted.Close()
	if page, err := restarted.Files(0, 20); err != nil || page.Total != 1 {
		t.Fatalf("duplicates returned after restart: %+v %v", page, err)
	}
}
