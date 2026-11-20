package screener

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func batchIDs(info Info) []string {
	ids := make([]string, len(info.Jobs))
	for index, job := range info.Jobs {
		ids[index] = job.ID
	}
	return ids
}

func TestDailyCompanionBatchScopesJobsAndWritesBothHistories(t *testing.T) {
	m := testManager(t)
	request := validRequest()
	request.Timeframe = "h1"
	started, err := m.StartWithDailyCompanion(request)
	if err != nil || len(started.Jobs) != 2 {
		t.Fatal("start paired repair", started, err)
	}
	for index, period := range []string{"d1", "h1"} {
		job := started.Jobs[index]
		if job.Timeframe != period || job.Symbol != "btcusd" || normalizeDataSource(job.DataSource) != "dukascopy" || job.From != request.From || job.To != request.To || job.AsOf <= 0 || job.AsOf != started.Jobs[0].AsOf {
			t.Fatal("repair touched another asset, period, source or cutoff", job)
		}
	}
	finished := waitFinished(t, m)
	for _, job := range finished.Jobs {
		if job.Status != "completed" {
			t.Fatal("paired download failed", finished)
		}
	}
	for _, job := range finished.Jobs {
		if data, err := os.ReadFile(job.OutputPath); err != nil || len(data) == 0 {
			t.Fatal("paired history was not published", job, err)
		}
	}

}

func TestDailyCompanionDownloadRetainsProductionIncrementalCoverage(t *testing.T) {
	m, trace, _ := refreshManager(t, false)
	writeDataFile(t, m, "eurusd-d1-2024-01-02-2024-01-04.csv", "timestamp,open,high,low,close,volume,sma_2\n1704153600000,20,21,19,20,10,\n1704240000000,30,31,29,30,10,25\n")
	request := Request{Symbols: []string{"eurusd"}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-06"}
	if _, err := m.StartWithDailyCompanion(request); err != nil {
		t.Fatal(err)
	}
	first := waitFinished(t, m)
	if first.Jobs[0].Status != "completed" {
		t.Fatal(first)
	}
	data, err := os.ReadFile(first.Jobs[0].OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	requests, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(requests), "\n") != 2 {
		t.Fatal("repair did not fetch the missing prefix and suffix only", string(requests))
	}
	if _, err := m.StartWithDailyCompanion(request); err != nil {
		t.Fatal(err)
	}
	repeated := waitFinished(t, m)
	if repeated.Jobs[0].Status != "completed" || !strings.Contains(repeated.Jobs[0].Message, "already available") {
		t.Fatal(repeated)
	}
	after, err := os.ReadFile(repeated.Jobs[0].OutputPath)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("covered repair changed candles or indicators", err)
	}
	repeatedRequests, err := os.ReadFile(trace)
	if err != nil || !bytes.Equal(requests, repeatedRequests) {
		t.Fatal("covered repair issued another provider request", err)
	}
}

func TestDailyCompanionHyperliquidRepairKeepsNativeIdentityAndReusesCoverage(t *testing.T) {
	m := hyperliquidDownloadManager(t)
	id := "hyperliquid-" + publicAlertSymbol("perpetual", "BTC")
	request := Request{DataSource: "hyperliquid", Symbols: []string{id}, Timeframe: "h1", From: "2024-01-03", To: "2024-01-05"}
	started, err := m.StartWithDailyCompanion(request)
	if err != nil || len(started.Jobs) != 2 {
		t.Fatal("Hyperliquid hourly repair did not include its daily companion", started, err)
	}
	for _, job := range waitHyperliquidDownload(t, m).Jobs {
		if job.DataSource != "hyperliquid" || job.Symbol != id || job.NativeID != "BTC" || job.Market != "perpetual" || (job.Timeframe != "h1" && job.Timeframe != "d1") {
			t.Fatal("repair fetched another native market", job)
		}
	}
	adapter := m.hyperliquidAdapter.(*hyperliquidDownloadAdapter)
	adapter.mu.Lock()
	before := adapter.calls
	for _, key := range adapter.keys {
		if key.Instrument != "BTC" || key.Exchange != "hyperliquid" || key.Market != "perpetual" {
			t.Fatal("repair requested another asset or data source", key)
		}
	}
	adapter.mu.Unlock()
	if _, err := m.StartWithDailyCompanion(request); err != nil {
		t.Fatal(err)
	}
	waitHyperliquidDownload(t, m)
	adapter.mu.Lock()
	after := adapter.calls
	adapter.mu.Unlock()
	if before == 0 || after != before {
		t.Fatal("covered Hyperliquid repair redownloaded retained source candles", before, after)
	}
}

func TestDailyCompanionQueueRestoresAndResumesItsExactPair(t *testing.T) {
	m := testManager(t)
	t.Setenv("CRYPTODESK_SCREENER_TEST_MODE", "hold")
	request := validRequest()
	request.Timeframe = "h1"
	started, err := m.StartWithDailyCompanion(request)
	if err != nil {
		t.Fatal(err)
	}
	waitRunning(t, m)
	m.Close()
	t.Setenv("CRYPTODESK_SCREENER_TEST_MODE", "")
	restored := reopenTestManager(t, m.profile)
	finished := waitFinished(t, restored)
	if !reflect.DeepEqual(batchIDs(finished), batchIDs(started)) || len(finished.Jobs) != 2 {
		t.Fatal("paired repair queue changed after restart", finished)
	}
	for index, period := range []string{"d1", "h1"} {
		if finished.Jobs[index].Timeframe != period || finished.Jobs[index].Status != "completed" {
			t.Fatal("paired repair failed to resume", finished)
		}
	}
	if _, err := downloadBatchRequest([]Job{started.Jobs[1], started.Jobs[0]}, 5); err == nil {
		t.Fatal("unordered companion queue was accepted")
	}
}

func TestDownloadBatchStatusAndCancellationNeverAffectReplacementJobs(t *testing.T) {
	m := testManager(t)
	t.Setenv("CRYPTODESK_SCREENER_TEST_MODE", "hold")
	request := validRequest()
	request.Timeframe = "h1"
	started, err := m.StartWithDailyCompanion(request)
	if err != nil {
		t.Fatal(err)
	}
	ids := batchIDs(started)
	waitRunning(t, m)
	for _, invalid := range [][]string{nil, ids[:1], {ids[0], ids[0]}, {ids[0], strings.Repeat("f", 32)}} {
		if _, err := m.DownloadBatch(invalid); err == nil {
			t.Fatal("status accepted incomplete or replaced IDs", invalid)
		}
		if _, err := m.CancelDownloadBatch(invalid); err == nil {
			t.Fatal("cancellation accepted incomplete or replaced IDs", invalid)
		}
		if !m.Info().Active {
			t.Fatal("identity failure cancelled someone else's download")
		}
	}
	if status, err := m.DownloadBatch([]string{ids[1], ids[0]}); err != nil || !status.Active {
		t.Fatal("status rejected a valid unordered ID set", status, err)
	}
	if cancelled, err := m.CancelDownloadBatch(ids); err != nil || cancelled.Active || cancelled.Jobs[0].Status != "cancelled" || cancelled.Jobs[1].Status != "cancelled" {
		t.Fatal("owned cancellation failed", cancelled, err)
	}
	request.Symbols = []string{"ethusd"}
	replacement, err := m.StartWithDailyCompanion(request)
	if err != nil {
		t.Fatal(err)
	}
	waitRunning(t, m)
	if _, err := m.DownloadBatch(ids); err == nil {
		t.Fatal("old batch status masqueraded as the replacement")
	}
	if _, err := m.CancelDownloadBatch(ids); err == nil || !m.Info().Active || !reflect.DeepEqual(batchIDs(m.Info()), batchIDs(replacement)) {
		t.Fatal("stale cancellation touched the replacement", err)
	}
	if _, err := m.CancelDownloadBatch(batchIDs(replacement)); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadBatchCancellationRevalidatesIdentityAfterDrain(t *testing.T) {
	m := testManager(t)
	job := Job{ID: strings.Repeat("a", 32), Symbol: "btcusd", Timeframe: "d1", From: "2024-01-01", To: "2024-01-02", Status: "running"}
	m.jobs, m.batchDirectory, m.publication = []Job{job}, m.directory, map[string]string{}
	m.active = true
	done := make(chan struct{})
	m.batchDone = done
	cancelled := make(chan struct{}, 1)
	m.cancel = func() { cancelled <- struct{}{} }
	result := make(chan error, 1)
	go func() { _, err := m.CancelDownloadBatch([]string{job.ID}); result <- err }()
	<-cancelled
	m.mu.Lock()
	close(done)
	replacement := job
	replacement.ID, replacement.Symbol, replacement.Status = strings.Repeat("b", 32), "ethusd", "queued"
	m.jobs, m.cancel = []Job{replacement}, nil
	m.mu.Unlock()
	if err := <-result; err == nil {
		t.Fatal("cancellation returned a replacement batch as owned work")
	}
	if got := m.Info(); len(got.Jobs) != 1 || got.Jobs[0].ID != replacement.ID || got.Jobs[0].Status != "queued" {
		t.Fatal("cancellation cleaned up or cancelled replacement work", got)
	}
	m.active, m.jobs = false, nil
}

func TestDailyCompanionRepairPreservesActiveAndPendingWorkOnRejection(t *testing.T) {
	for _, activity := range []string{"download", "live", "enrichment", "refresh", "analysis", "pending"} {
		t.Run(activity, func(t *testing.T) {
			m := testManager(t)
			m.jobs = []Job{{ID: strings.Repeat("a", 32), Symbol: "ethusd", Timeframe: "d1", Status: "completed"}}
			switch activity {
			case "download":
				m.active = true
			case "live":
				m.liveActive = true
			case "enrichment":
				m.enrichmentActive = true
			case "refresh":
				m.refreshActive = true
			case "analysis":
				m.analysis = &analysisState{active: true}
			case "pending":
				m.jobs[0].Status = "queued"
			}
			before := append([]Job(nil), m.jobs...)
			request := validRequest()
			request.Timeframe = "h1"
			if _, err := m.StartWithDailyCompanion(request); err == nil || !reflect.DeepEqual(before, m.jobs) {
				t.Fatal("repair stole existing work", err, m.jobs)
			}
			m.active, m.liveActive, m.enrichmentActive, m.refreshActive = false, false, false, false
			m.analysis = nil
			m.jobs = nil
			if _, err := os.Stat(filepath.Join(m.profile, "screener-downloads.json")); !os.IsNotExist(err) {
				t.Fatal("rejected repair rewrote the saved queue", err)
			}
		})
	}
}
