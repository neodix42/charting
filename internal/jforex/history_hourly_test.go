package jforex

import (
	"context"
	"path/filepath"
	"testing"
)

func TestHourlyDownloadUsesExplicitSessionCapabilityWithoutPoisoningChartCache(t *testing.T) {
	for _, full := range []bool{true, false} {
		t.Run(map[bool]string{true: "verified", false: "native"}[full], func(t *testing.T) {
			trace := filepath.Join(t.TempDir(), "hourly-requests")
			t.Setenv("CRYPTODESK_JFOREX_DOWNLOAD_TRACE", trace)
			m, starts := tickExportManager(t, "hourly")
			key := downloadKey()
			key.Timeframe = "1h"
			from, to := exportTestFrom/1000, exportTestFrom/1000+3*3600
			bars, err := m.DownloadHourlyHistory(context.Background(), key, from, to, 1000, full)
			if err != nil || len(bars) != 3 {
				t.Fatal("hourly download failed", bars, err)
			}
			if _, err := m.History(context.Background(), key, from, to, 1000); err != nil {
				t.Fatal(err)
			}
			if _, err := m.DownloadHourlyHistory(context.Background(), key, from, to, 1000, full); err != nil {
				t.Fatal(err)
			}
			requests := downloadTrace(t, trace)
			if len(requests) != 3 || !requests[0].Download || requests[0].FullOpenHours == nil || *requests[0].FullOpenHours != full || requests[1].Download || requests[1].FullOpenHours != nil || !requests[2].Download || starts.Load() != 1 {
				t.Fatal("filtered source shared chart-cache coverage or lost explicit session policy", requests, starts.Load())
			}
		})
	}
}

func TestHourlyDownloadRejectsUnknownCapabilityAndInvalidBounds(t *testing.T) {
	key := downloadKey()
	key.Timeframe = "1h"
	from := exportTestFrom / 1000
	m, _ := tickExportManager(t, "legacy")
	if _, err := m.DownloadHourlyHistory(context.Background(), key, from, from+3600, 1000, true); err == nil {
		t.Fatal("legacy bridge was trusted to verify historical calendar coverage")
	}
	for _, kind := range []string{"native-other", "open", "unaligned", "oversized", "period", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			m, starts := tickExportManager(t, "hourly")
			key := key
			start, end, full := from, from+3600, true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "native-other":
				key.Instrument, key.Market, full = "XAUUSD", "metal", false
			case "open":
				end = 32503680000
			case "unaligned":
				start++
			case "oversized":
				end = start + 1001*3600
			case "period":
				key.Timeframe = "1d"
			case "canceled":
				cancel()
			}
			if _, err := m.DownloadHourlyHistory(ctx, key, start, end, 1000, full); err == nil || starts.Load() != 0 {
				t.Fatal("invalid hourly source reached the SDK", starts.Load(), err)
			}
		})
	}
}
