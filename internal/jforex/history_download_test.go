package jforex

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func downloadKey() domain.FeedKey {
	return domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCUSD", Timeframe: "1d"}
}

type downloadRequest struct {
	From, To      int64
	Download      bool
	FeedKey       domain.FeedKey
	FullOpenHours *bool
}

func downloadTrace(t *testing.T, path string) []downloadRequest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result := []downloadRequest{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var request downloadRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		result = append(result, request)
	}
	return result
}

func TestDownloadHistoryUsesScopedDownloadFlagAndSharesChartCache(t *testing.T) {
	for _, instrument := range []string{"BTCUSD", "XAUXAG", "0700HKHKD", "EURUSD", "XAUUSD"} {
		t.Run(instrument, func(t *testing.T) {
			trace := filepath.Join(t.TempDir(), "requests")
			t.Setenv("CRYPTODESK_JFOREX_DOWNLOAD_TRACE", trace)
			m, starts := tickExportManager(t, "daily")
			key := downloadKey()
			key.Instrument = instrument
			if instrument == "XAUXAG" || instrument == "XAUUSD" {
				key.Market = "metal"
			} else if instrument == "EURUSD" {
				key.Market = "forex"
			}
			from, to := exportTestFrom/1000, exportTestFrom/1000+3*86400
			for _, query := range []func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error){m.DownloadHistory, m.History, m.DownloadHistory} {
				bars, err := query(context.Background(), key, from, to, 1000)
				if err != nil || len(bars) != 3 || bars[0].Time != from || bars[2].Time != to-86400 {
					t.Fatalf("native history lost actual daily rows: %+v %v", bars, err)
				}
			}
			requests := downloadTrace(t, trace)
			if len(requests) != 1 || !requests[0].Download || requests[0].FeedKey != key || requests[0].From != from || requests[0].To != to || starts.Load() != 1 {
				t.Fatalf("download scope/cache identity changed: %+v starts=%d", requests, starts.Load())
			}
		})
	}
}

func TestDownloadHistoryAlignsMissingSpansAfterPartialChartCoverage(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "requests")
	t.Setenv("CRYPTODESK_JFOREX_DOWNLOAD_TRACE", trace)
	m, _ := tickExportManager(t, "daily")
	key := downloadKey()
	from, to := exportTestFrom/1000, exportTestFrom/1000+3*86400
	// A genuine preceding chart query proves midday-to-midday coverage and
	// retains the native day whose opening timestamp lies inside that range.
	if bars, err := m.History(context.Background(), key, from+43200, from+86400+43200, 1000); err != nil || len(bars) != 1 || bars[0].Time != from+86400 {
		t.Fatal("partial chart query failed", bars, err)
	}
	bars, err := m.DownloadHistory(context.Background(), key, from, to, 1000)
	if err != nil || len(bars) != 3 {
		t.Fatalf("daily download rejected or lost partial-cache rows: %+v %v", bars, err)
	}
	requests := downloadTrace(t, trace)
	if len(requests) != 3 || requests[0].Download || requests[0].From != from+43200 || requests[0].To != from+86400+43200 || !requests[1].Download || requests[1].From != from+86400 || requests[1].To != to || !requests[2].Download || requests[2].From != from || requests[2].To != from+86400 {
		t.Fatalf("wrong expanded native intervals or changed chart request: %+v", requests)
	}
	if _, err := m.DownloadHistory(context.Background(), key, from, to, 1000); err != nil || len(downloadTrace(t, trace)) != 3 {
		t.Fatal("complete mixed cache was downloaded again", err)
	}
}

func TestDownloadHistoryRejectsNonDownloadRangesBeforeLogin(t *testing.T) {
	for _, kind := range []string{"instrument", "market", "timeframe", "negative", "unaligned-from", "unaligned-to", "oversized", "forming", "limit-zero", "limit-large", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			m, starts := tickExportManager(t, "daily")
			key, from, to, limit := downloadKey(), exportTestFrom/1000, exportTestFrom/1000+86400, 1000
			ctx := context.Background()
			switch kind {
			case "instrument":
				key.Instrument = ""
			case "market":
				key.Market = "spot"
			case "timeframe":
				key.Timeframe = "1w"
			case "negative":
				from = -86400
			case "unaligned-from":
				from++
			case "unaligned-to":
				to++
			case "oversized":
				to = from + 367*86400
			case "forming":
				from = time.Now().Unix() / 86400 * 86400
				to = from + 86400
			case "limit-zero":
				limit = 0
			case "limit-large":
				limit = 1001
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			if bars, err := m.DownloadHistory(ctx, key, from, to, limit); err == nil || len(bars) != 0 || starts.Load() != 0 {
				t.Fatalf("invalid native download reached SDK: %+v starts=%d err=%v", bars, starts.Load(), err)
			}
		})
	}
}

func TestDownloadHistoryFailureDoesNotClaimCoverageOrKillSession(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "requests")
	t.Setenv("CRYPTODESK_JFOREX_DOWNLOAD_TRACE", trace)
	m, starts := tickExportManager(t, "daily-failure")
	for range 2 {
		if bars, err := m.DownloadHistory(context.Background(), downloadKey(), exportTestFrom/1000, exportTestFrom/1000+86400, 1000); err == nil || len(bars) != 0 {
			t.Fatal("timed-out native query invented data or coverage", bars, err)
		}
	}
	if len(downloadTrace(t, trace)) != 2 || starts.Load() != 1 || !m.Info().Connected {
		t.Fatal("native timeout hid missing data or reset SDK session")
	}
}
