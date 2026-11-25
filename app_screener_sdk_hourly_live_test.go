//go:build sdkhistoryintegration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/screener"
)

// Explicit read-only check; no alert state, Telegram configuration or journals
// enter this temporary profile. Only public-market counts/errors are logged.
func isolatedLiveHistoryClient(t *testing.T) *jforex.Manager {
	client, _ := isolatedLiveHistoryProfile(t)
	return client
}

func isolatedLiveHistoryProfile(t *testing.T) (*jforex.Manager, string) {
	t.Helper()
	source := os.Getenv("CRYPTODESK_SDK_LIVE_PROFILE")
	if source == "" {
		t.Fatal("set CRYPTODESK_SDK_LIVE_PROFILE for the explicit SDK history check")
	}
	profile := t.TempDir()
	settings, err := os.ReadFile(filepath.Join(source, "dukascopy-jforex.json"))
	if err != nil {
		t.Fatal("could not read SDK settings")
	}
	if err := os.WriteFile(filepath.Join(profile, "dukascopy-jforex.json"), settings, 0600); err != nil {
		t.Fatal("could not isolate SDK settings")
	}
	for i := range settings {
		settings[i] = 0
	}
	client := jforex.New(profile)
	t.Cleanup(client.Close)
	return client, profile
}

func TestLiveSDKFastSyntheticHistoryDoesNotDownloadTicks(t *testing.T) {
	client, profile := isolatedLiveHistoryProfile(t)
	if cache := os.Getenv("CRYPTODESK_SDK_LIVE_HOURLY_CACHE"); cache != "" {
		quoteRoot := filepath.Join(profile, "screener-provider", "synthetic-hourly-v1")
		if err := copyLivePriceCache(cache, quoteRoot); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := copyLivePriceCache(quoteRoot, cache); err != nil {
				t.Error(err)
			}
		})
	}
	observed := &liveSDKHistoryClient{dukascopyHistoryClient: client}
	manager := screener.NewWithDukascopyHistory(profile, dukascopyScreenerHistory{client: observed})
	defer manager.Close()
	_, err := manager.Start(screener.Request{DataSource: "dukascopy", Symbols: []string{"btcxau"}, SyntheticSource: "h1-close", Timeframe: "w1", From: "2024-09-30", To: "2024-10-28", Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Minute)
	for manager.Info().Active && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	info := manager.Info()
	if info.Active {
		manager.Cancel()
		t.Fatal("bounded real fast-history check exceeded its deadline")
	}
	for _, job := range info.Jobs {
		if job.Status != "completed" {
			t.Fatalf("fast %s history failed: %s", job.Timeframe, job.Message)
		}
	}
	for _, period := range []string{"1d", "1w"} {
		bars, err := manager.SyntheticHistory(context.Background(), domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCXAU", Timeframe: period}, 0, 0, 1000)
		if err != nil || len(bars) == 0 {
			t.Fatalf("real fast %s published chart unavailable: rows=%d error=%v", period, len(bars), err)
		}
		for _, bar := range bars {
			if !bar.Closed || !bar.Synthetic || !bar.Approximate || !bar.VolumeUnavailable || period == "1w" && time.Unix(bar.Time, 0).UTC().Weekday() != time.Monday {
				t.Fatal("fast observation quality/boundary flags were lost")
			}
		}
		t.Logf("Real fast BTCXAU %s: %d saved closed candles", period, len(bars))
	}
	if observed.tickExports.Load() != 0 {
		t.Fatal("fast synthetic history downloaded ticks")
	}
	if os.Getenv("CRYPTODESK_SDK_LIVE_EXPECT_HOURLY_CACHE") == "1" && observed.hourlyExports.Load() != 0 {
		t.Fatal("fast repeat refetched compatible H1 observations")
	}
	t.Logf("Actual SDK exports: ticks=%d hourly=%d", observed.tickExports.Load(), observed.hourlyExports.Load())
}

func TestLiveSDKHistoricalCalendarReadiness(t *testing.T) {
	client := isolatedLiveHistoryClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	from := time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	for _, symbol := range []string{"BTCUSD", "ETHUSD"} {
		key := domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: symbol, Timeframe: "1h"}
		var result map[string]any
		for attempt := 0; attempt < 3; attempt++ {
			var err error
			result, err = client.InspectHistoricalCalendar(ctx, key, from, from+86400)
			if err != nil {
				t.Fatal(err)
			}
			if result["readSchedule"] == true {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s public historical calendar: %s", symbol, encoded)
	}
}

func TestLiveSDKHourlySamplingAndOrdinaryD1History(t *testing.T) {
	client := isolatedLiveHistoryClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	from := time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	for _, symbol := range []string{"BTCUSD", "ETHUSD", "XAUUSD", "USA500IDXUSD"} {
		market := "cfd"
		if symbol == "XAUUSD" {
			market = "metal"
		}
		bars, err := client.DownloadHourlyHistory(ctx, domain.FeedKey{Exchange: "dukascopy", Market: market, Instrument: symbol, Timeframe: "1h"}, from, from+86400, 1000, true)
		if err != nil {
			t.Errorf("%s verified H1: %v", symbol, err)
			continue
		}
		if len(bars) == 0 {
			t.Errorf("%s returned no historical H1 observations", symbol)
		}
		t.Logf("%s: %d verified full-session closed hourly observations", symbol, len(bars))
	}
	day := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC).Unix()
	bars, err := client.DownloadHistory(ctx, domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "0700HKHKD", Timeframe: "1d"}, day, day+86400, 1000)
	if err != nil || len(bars) != 1 || !bars[0].Closed {
		t.Errorf("ordinary 0700HKHKD latest D1 unavailable: rows=%d error=%v", len(bars), err)
	} else {
		t.Log("0700HKHKD latest D1 is available through authenticated SDK history")
	}
}

func TestLiveSDKHourlyBatchRangeConsistency(t *testing.T) {
	client := isolatedLiveHistoryClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	from := time.Date(2024, 9, 30, 0, 0, 0, 0, time.UTC).Unix()
	to := from + 28*86400
	day := from + 86400
	for _, symbol := range []string{"BTCUSD", "XAUUSD"} {
		market := "cfd"
		if symbol == "XAUUSD" {
			market = "metal"
		}
		key := domain.FeedKey{Exchange: "dukascopy", Market: market, Instrument: symbol, Timeframe: "1h"}
		for _, sample := range []struct {
			name     string
			from, to int64
		}{{"cold672h", from, to}, {"single24h", day, day + 86400}, {"warm672h", from, to}} {
			bars, err := client.DownloadHourlyHistory(ctx, key, sample.from, sample.to, 1000, true)
			if err != nil {
				t.Errorf("%s %s: %v", symbol, sample.name, err)
				continue
			}
			var first, last int64
			if len(bars) > 0 {
				first, last = bars[0].Time, bars[len(bars)-1].Time
			}
			t.Logf("%s %s requested=%d..%d rows=%d first=%d last=%d", symbol, sample.name, sample.from, sample.to, len(bars), first, last)
			if len(bars) == 0 {
				t.Errorf("%s %s returned no observations", symbol, sample.name)
			}
		}
	}
}

func TestLiveSDKHourlyProviderFilterAndRangeDiagnostics(t *testing.T) {
	for _, sample := range []struct {
		name, filter string
		days         int
	}{{"day-first", "WEEKENDS", 1}, {"wed-ending", "WEEKENDS", 2}, {"mon-ending", "WEEKENDS", 28}, {"no-filter", "NO_FILTER", 28}} {
		t.Run(sample.name, func(t *testing.T) {
			client := isolatedLiveHistoryClient(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			from := time.Date(2024, 9, 30, 0, 0, 0, 0, time.UTC).Unix()
			for _, symbol := range []string{"BTCUSD", "XAUUSD"} {
				market := "cfd"
				if symbol == "XAUUSD" {
					market = "metal"
				}
				result, err := client.InspectHourlyCandleData(ctx, domain.FeedKey{Exchange: "dukascopy", Market: market, Instrument: symbol, Timeframe: "1h"}, from, from+int64(sample.days)*86400, sample.filter)
				if err != nil {
					t.Errorf("%s: %v", symbol, err)
					continue
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("%s: %s", symbol, encoded)
			}
		})
	}
}

func TestLiveSDKHourlySlicedBatchAndOrdinaryDailyRange(t *testing.T) {
	client := isolatedLiveHistoryClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	from := time.Date(2024, 9, 30, 0, 0, 0, 0, time.UTC).Unix()
	to := from + 28*86400
	for _, symbol := range []string{"BTCUSD", "XAUUSD"} {
		market := "cfd"
		if symbol == "XAUUSD" {
			market = "metal"
		}
		bars, err := client.DownloadHourlyHistory(ctx, domain.FeedKey{Exchange: "dukascopy", Market: market, Instrument: symbol, Timeframe: "1h"}, from, to, 1000, true)
		if err != nil {
			t.Fatalf("%s daily-sliced28day source: %v", symbol, err)
		}
		seen := map[int64]int{}
		for _, bar := range bars {
			seen[bar.Time/86400*86400]++
		}
		if len(bars) == 0 || seen[from] == 0 || seen[from+86400] == 0 {
			t.Fatalf("%s lost known first/second source days: rows=%d", symbol, len(bars))
		}
		if symbol == "BTCUSD" && len(bars) != 672 {
			t.Fatalf("BTCUSD lost observations in the verified open28day period: rows=%d", len(bars))
		}
		t.Logf("%s daily-sliced28day source: rows=%d observedDays=%d first=%d last=%d", symbol, len(bars), len(seen), bars[0].Time, bars[len(bars)-1].Time)
	}
	day := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC).Unix()
	bars, err := client.DownloadHistory(ctx, domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "0700HKHKD", Timeframe: "1d"}, day, day+14*86400, 1000)
	if err != nil || len(bars) < 8 || bars[len(bars)-1].Time != day+13*86400 || !bars[len(bars)-1].Closed {
		t.Fatalf("ordinary0700two-week D1 range lost latestclosedNov24: rows=%d error=%v", len(bars), err)
	}
	t.Logf("0700HKHKD native two-weekD1 source: rows=%d first=%d last=%d", len(bars), bars[0].Time, bars[len(bars)-1].Time)
}

func TestLiveSDKOrdinaryDailyRangeLatestClosed(t *testing.T) {
	client := isolatedLiveHistoryClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	from := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC).Unix()
	bars, err := client.DownloadHistory(ctx, domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "0700HKHKD", Timeframe: "1d"}, from, from+14*86400, 1000)
	if err != nil || len(bars) < 8 || bars[len(bars)-1].Time != from+13*86400 || !bars[len(bars)-1].Closed {
		t.Fatalf("ordinary0700two-week D1 source missing latest closedNov24: rows=%d error=%v", len(bars), err)
	}
	t.Logf("0700HKHKD two-weekD1: rows=%d first=%d last=%d", len(bars), bars[0].Time, bars[len(bars)-1].Time)
}
