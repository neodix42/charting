package screener

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Fix only the execution cutoff and queue setup. The normal refresh pool,
// public-data transport, Node workers and durable publisher execute unchanged.
func startBoundedWeeklyRefresh(t *testing.T, m *Manager, names []string, asOf int64) []string {
	t.Helper()
	ids := make([]string, len(names))
	m.mu.Lock()
	for i, name := range names {
		ids[i] = dataFileID(m.directory, name)
		m.refreshJobs = append(m.refreshJobs, RefreshJob{ID: strings.Repeat(string(rune('a'+i)), 32), FileID: ids[i], fileIDAliases: []string{ids[i]}, Name: name, Status: "queued", Message: "Waiting for data refresh", Columns: []string{}})
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.refreshActive, m.refreshAsOf = true, asOf
	m.refreshCancel, m.refreshDone = cancel, make(chan struct{})
	m.wg.Add(1)
	go m.runRefresh(ctx, m.directory, asOf, MaxConcurrency)
	m.mu.Unlock()
	return ids
}

func TestWeeklyRefreshWaitsForDailyPublicationAndReplaysBothIndicatorFamilies(t *testing.T) {
	m, trace, gates := refreshManager(t, true)
	const dailyName = "eurusd-d1-2024-01-01-2024-01-15.csv"
	const weeklyName = "eurusd-w1-2024-01-01-2024-01-15.csv"
	daily := "timestamp,open,high,low,close,volume,sma_2\n1704067200000,10,11,9,10,10,\n1704240000000,50,51,49,50,10,30\n1704672000000,40,41,39,40,10,45\n1704758400000,50,51,49,50,10,45\n"
	weekly := "timestamp,open,high,low,close,volume,sma_2\n1704067200000,10,51,9,50,20,\n1704672000000,40,51,39,50,20,50\n"
	writeDataFile(t, m, dailyName, daily)
	weeklyPath := writeDataFile(t, m, weeklyName, weekly)
	// Put w1 first in the queue to ensure its position cannot bypass d1.
	ids := startBoundedWeeklyRefresh(t, m, []string{weeklyName, dailyName}, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC).UnixMilli())
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(gates, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daily refresh did not start its source request")
		}
		time.Sleep(5 * time.Millisecond)
	}
	info, err := m.Refresh(ids)
	if err != nil || len(info.Jobs) != 2 || info.Jobs[0].Status != "queued" || info.Jobs[1].Status != "running" {
		t.Fatalf("weekly refresh started before its daily prerequisite: %+v %v", info, err)
	}
	if data, err := os.ReadFile(weeklyPath); err != nil || string(data) != weekly {
		t.Fatalf("pending weekly history changed before daily publication: %s %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(gates, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	info = waitRefresh(t, m, ids)
	if info.Total != 2 || info.Completed != 2 || info.Failed != 0 || len(info.Jobs) != 2 || info.Jobs[0].UpdatedRows != 1 || info.Jobs[1].AddedRows != 1 {
		t.Fatalf("paired refresh did not update daily then weekly history: %+v", info)
	}
	weeklyData, err := os.ReadFile(filepath.Join(m.directory, info.Jobs[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(weeklyData))).ReadAll()
	want := [][]string{
		{"timestamp", "open", "high", "low", "close", "volume", "sma_2"},
		{"1704067200000", "10", "31", "9", "30", "30", ""},
		{"1704672000000", "40", "51", "39", "50", "20", "40"},
	}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("weekly refresh used stale daily bars or missed indicator replay: %s %v", weeklyData, err)
	}
	dailyData, err := os.ReadFile(filepath.Join(m.directory, info.Jobs[1].Name))
	if err != nil {
		t.Fatal(err)
	}
	dailyRows, err := csv.NewReader(strings.NewReader(string(dailyData))).ReadAll()
	if err != nil || len(dailyRows) != 6 || dailyRows[2][6] != "15" || dailyRows[3][6] != "25" || dailyRows[4][6] != "35" || dailyRows[5][6] != "45" {
		t.Fatalf("retained daily history lost rows or indicator replay: %s %v", dailyData, err)
	}
	page, err := m.Files(0, 20)
	if err != nil || page.Total != 2 || page.PeriodCounts["d1"] != 1 || page.PeriodCounts["w1"] != 1 {
		t.Fatalf("refresh removed retained daily or weekly family: %+v %v", page, err)
	}
	requests, err := os.ReadFile(trace)
	if err != nil || strings.Count(string(requests), "\n") != 1 {
		t.Fatalf("weekly refresh made an independent provider request: %s %v", requests, err)
	}
	for index, original := range []string{weekly, daily} {
		backup, err := os.ReadFile(info.Jobs[index].BackupPath)
		if err != nil || string(backup) != original {
			t.Fatalf("exact original backup missing: %s %v", backup, err)
		}
		metadata, err := readFileMetadata(m.directory, info.Jobs[index].Name)
		if err != nil || len(metadata.Recipes) != 1 || metadata.Recipes[0].Indicator != "sma" {
			t.Fatalf("refreshed recipe metadata missing: %+v %v", metadata, err)
		}
	}
	assertNoRefreshScratch(t, m.directory)
}

func TestWeeklyRefreshDailyFailurePreservesWeeklyDataAndMetadata(t *testing.T) {
	m, trace, gates := weeklyTransportManager(t, false)
	const dailyName = "eurusd-d1-2024-01-01-2024-01-15.csv"
	const weeklyName = "eurusd-w1-2024-01-01-2024-01-15.csv"
	writeDataFile(t, m, dailyName, weeklyDailyCSV(0, 14, true))
	weeklyPath := writeDataFile(t, m, weeklyName, "timestamp,open,high,low,close,volume,sma_2\n1704067200000,10,17,9,16,28,\n1704672000000,17,24,16,23,77,19.5\n")
	if err := writeFileMetadata(m.directory, weeklyName, fileMetadata{Recipes: []EnrichmentRequest{{Indicator: "sma", Parameters: map[string]float64{"period": 2}, Source: "close"}}}); err != nil {
		t.Fatal(err)
	}
	before := enrichmentTreeSnapshot(t, m.directory)
	if err := os.WriteFile(filepath.Join(gates, "fail"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ids := startBoundedWeeklyRefresh(t, m, []string{weeklyName, dailyName}, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC).UnixMilli())
	info := waitRefresh(t, m, ids)
	if info.Failed != 2 || info.Completed != 0 || len(info.Jobs) != 2 || !strings.Contains(info.Jobs[0].Message, "Daily history did not refresh") || !strings.Contains(info.Jobs[1].Message, "Injected daily source failure") {
		t.Fatalf("weekly conversion proceeded despite failed daily refresh: %+v", info)
	}
	after := enrichmentTreeSnapshot(t, m.directory)
	for _, path := range []string{weeklyPath, filepath.Join(m.directory, dailyName), historyMetadataPath(m.directory, weeklyName)} {
		if !reflect.DeepEqual(before[path], after[path]) {
			t.Fatalf("failed prerequisite changed data or metadata: %s", path)
		}
	}
	requests, err := os.ReadFile(trace)
	if err != nil || strings.Count(string(requests), "\n") != 1 {
		t.Fatalf("weekly refresh fetched despite failed prerequisite: %s %v", requests, err)
	}
	assertNoRefreshScratch(t, m.directory)
}

func TestWeeklyRefreshCancellationMarksBothPeriodsCancelledAndPreservesSources(t *testing.T) {
	m, _, gates := refreshManager(t, true)
	const dailyName = "eurusd-d1-2024-01-01-2024-01-15.csv"
	const weeklyName = "eurusd-w1-2024-01-01-2024-01-15.csv"
	dailyPath := writeDataFile(t, m, dailyName, weeklyDailyCSV(0, 14, true))
	weeklyPath := writeDataFile(t, m, weeklyName, "timestamp,open,high,low,close,volume,sma_2\n1704067200000,10,17,9,16,28,\n1704672000000,17,24,16,23,77,19.5\n")
	before := enrichmentTreeSnapshot(t, m.directory)
	ids := startBoundedWeeklyRefresh(t, m, []string{weeklyName, dailyName}, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC).UnixMilli())
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(gates, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daily refresh did not begin before cancellation")
		}
		time.Sleep(5 * time.Millisecond)
	}
	m.CancelRefresh()
	info := waitRefresh(t, m, ids)
	if info.Active || info.Cancelled != 2 || info.Failed != 0 || info.Completed != 0 || len(info.Jobs) != 2 || info.Jobs[0].Status != "cancelled" || info.Jobs[1].Status != "cancelled" {
		t.Fatalf("queued weekly derivative was not cancelled with its daily refresh: %+v", info)
	}
	after := enrichmentTreeSnapshot(t, m.directory)
	for _, path := range []string{dailyPath, weeklyPath} {
		if !reflect.DeepEqual(before[path], after[path]) {
			t.Fatalf("cancelled refresh changed original CSV: %s", path)
		}
	}
	assertNoRefreshScratch(t, m.directory)
}
