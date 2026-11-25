package screener

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func TestSyntheticSourceDefaultViewAndLegacyQueueRemainDistinct(t *testing.T) {
	if defaultView().SyntheticSource != "h1-close" || copyView(ViewState{}).SyntheticSource != "h1-close" {
		t.Fatal("fresh Downloader selection did not default to fast history")
	}
	if effectiveSyntheticSource("") != "ticks" || effectiveSyntheticSource("ticks") != "ticks" {
		t.Fatal("legacy job or protocol changed its frozen tick input")
	}
	jobs := []Job{{DataSource: "dukascopy", Symbol: "btcxau", Timeframe: "d1", From: "2024-01-01", To: "2024-01-03"}}
	legacy, err := downloadBatchRequest(jobs, 1)
	if err != nil || legacy.SyntheticSource != "ticks" {
		t.Fatal("legacy queue source changed", legacy, err)
	}
	jobs[0].SyntheticSource = "h1-close"
	fast, err := downloadBatchRequest(jobs, 1)
	if err != nil || fast.SyntheticSource != "h1-close" {
		t.Fatal("saved fast queue lost its source", fast, err)
	}
	jobs = append(jobs, Job{DataSource: "dukascopy", Symbol: "ethbtc", Timeframe: "d1", From: jobs[0].From, To: jobs[0].To, SyntheticSource: "ticks"})
	if _, err := downloadBatchRequest(jobs, 1); err == nil {
		t.Fatal("mixed frozen source modes were accepted as one queue")
	}
	fast.SyntheticSource = "unknown"
	if err := validateRequest(fast, time.Now()); err == nil {
		t.Fatal("unsupported source mode accepted")
	}
	if syntheticSourceFromProvenance(&DataProvenance{SyntheticSource: "h1-close", Kind: "native", ObservationResolution: "Native daily candles"}) != "h1-close" {
		t.Fatal("native daily preference erased the fast fallback policy")
	}
}

func TestFastSyntheticRealWorkerBatchesCachesAndPublishesClosedHistory(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	disallowSDKTestHTTP(t)
	provider := newFakeSDKHistory()
	provider.export = func(_ context.Context, call sdkHistoryCall) (int64, error) {
		if call.timeframe != "h1" || call.to-call.from > 1000*sdkHistoryHour {
			return 0, fmt.Errorf("fast ratio requested an unexpected source: %s", call.timeframe)
		}
		var data strings.Builder
		data.WriteString("timestamp,open,high,low,close\n")
		var count int64
		for at := call.from; at < call.to; at += sdkHistoryHour {
			price := float64(2)
			if call.symbol == "btcusd" {
				price = 10 + float64((at-1704067200000)/sdkHistoryHour)
			}
			fmt.Fprintf(&data, "%d,%g,%g,%g,%g\n", at, price, price+1, price/2, price)
			count++
		}
		return count, os.WriteFile(call.output, []byte(data.String()), 0600)
	}
	ctx, cleanup, err := withProviderSessionWithHistory(context.Background(), m.profile, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	state := sessionFromContext(ctx)
	state.limiter.interval = 0
	state.limiter.state.DeniedTill = time.Now().Add(time.Hour).UnixMilli()
	job := Job{ID: strings.Repeat("c", 32), SyntheticSource: "h1-close", DataSource: "dukascopy", Symbol: "btcxau", Timeframe: "d1", From: "2024-01-01", To: "2024-03-01", AsOf: 1709251200000, Status: "running"}
	m.jobs, m.publication, m.batchDirectory = []Job{job}, map[string]string{}, m.directory
	output, _, err := m.download(ctx, 0, job, m.directory, 1)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := readFileMetadata(m.directory, filepath.Base(output))
	if err != nil || metadata.Provenance == nil || metadata.Provenance.SyntheticSource != "h1-close" || metadata.Provenance.MaxQuoteAgeMs != sdkHistoryHour {
		t.Fatal("published H1 input provenance was lost", metadata.Provenance, err)
	}
	bars, err := m.SyntheticHistory(context.Background(), domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCXAU", Timeframe: "1d"}, 0, 0, 1000)
	if err != nil || len(bars) != 60 || bars[0].Open != 5 || bars[0].High != 16.5 || !bars[0].Approximate || !bars[0].VolumeUnavailable {
		t.Fatal("saved fast chart lost observed ratios", len(bars), err)
	}
	provider.mu.Lock()
	calls := append([]sdkHistoryCall(nil), provider.calls...)
	provider.mu.Unlock()
	if len(calls) != 6 {
		t.Fatalf("sixty days should require six28-day source requests, got%d", len(calls))
	}
	job.ID, job.Status = strings.Repeat("d", 32), "running"
	m.jobs = []Job{job}
	if _, _, err := m.download(ctx, 0, job, m.directory, 1); err != nil {
		t.Fatal("cached repeat failed", err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.calls) != len(calls) {
		t.Fatal("repeat redownloaded compatible source history")
	}
}
