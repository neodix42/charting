//go:build sdkhistoryintegration

package main

import (
	"context"
	"encoding/json"
	"math"
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

func verifyLiveHourlyMonthlyCoverage(t *testing.T, profile string, client *jforex.Manager) {
	t.Helper()
	source, configured := client.HistoricalSource()
	if !configured {
		t.Fatal("live SDK source changed before coverage verification")
	}
	for _, symbol := range []string{"btcusd", "ethusd", "xauusd", "usa500idxusd"} {
		folder := filepath.Join(profile, "screener-provider", "synthetic-hourly-v1", source, "h1", symbol)
		entries, err := os.ReadDir(folder)
		if err != nil {
			t.Fatal(err)
		}
		months := map[string]int{}
		januaryRows := 0
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(folder, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var day struct {
				Day  int64 `json:"day"`
				Rows int   `json:"rows"`
			}
			if err := json.Unmarshal(data, &day); err != nil {
				t.Fatal(err)
			}
			if day.Rows > 0 {
				months[time.UnixMilli(day.Day).UTC().Format("2006-01")]++
			}
			if day.Day >= time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() && day.Day < time.Date(2022, 1, 29, 0, 0, 0, 0, time.UTC).UnixMilli() {
				januaryRows += day.Rows
			}
		}
		for month := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC); month.Before(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)); month = month.AddDate(0, 1, 0) {
			if months[month.Format("2006-01")] < 10 {
				t.Errorf("%s %s lost monthly observations: only %d source days", symbol, month.Format("2006-01"), months[month.Format("2006-01")])
			}
		}
		// Independently observed in the bounded January2022 SDK probes, before
		// the long-run regression exposed the successful-empty monthly cache.
		wanted := map[string]int{"btcusd": 658, "ethusd": 658, "xauusd": 455, "usa500idxusd": 436}[symbol]
		if januaryRows != wanted {
			t.Errorf("%s January1–28 reference differs: observed=%d independently checked=%d", symbol, januaryRows, wanted)
		}
		t.Logf("%s monthly observations: months=%d January1–28hourlyRows=%d", symbol, len(months), januaryRows)
	}
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

func TestLiveSDKLongHistoryInitialSourceDiagnostics(t *testing.T) {
	client := isolatedLiveHistoryClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	from := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	for _, symbol := range []string{"BTCUSD", "XAUUSD", "USA500IDXUSD"} {
		market := "cfd"
		if symbol == "XAUUSD" {
			market = "metal"
		}
		key := domain.FeedKey{Exchange: "dukascopy", Market: market, Instrument: symbol, Timeframe: "1h"}
		bars, err := client.DownloadHourlyHistory(ctx, key, from, from+28*86400, 1000, true)
		if err != nil {
			t.Errorf("%s initial28day error: %v", symbol, err)
			continue
		}
		t.Logf("%s initial28day observations=%d", symbol, len(bars))
	}
}

func TestLiveSDKHourlySourcePricesMatchNormalHistory(t *testing.T) {
	client := isolatedLiveHistoryClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	from := time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	comparedSymbols := 0
	for _, symbol := range []string{"BTCUSD", "ETHUSD", "XAUUSD", "USA500IDXUSD"} {
		market := "cfd"
		if symbol == "XAUUSD" {
			market = "metal"
		}
		key := domain.FeedKey{Exchange: "dukascopy", Market: market, Instrument: symbol, Timeframe: "1h"}
		source, err := client.DownloadHourlyHistory(ctx, key, from, from+86400, 1000, true)
		if err != nil && err.Error() == "JForex hourly history download timed out. Completed history is retained; retry to resume." {
			// Match the downloader's single typed SDK-timeout retry; this probe
			// bypasses Node only to compare normal SDK prices directly.
			t.Logf("%s typed SDK timeout; retrying the same closed source day once", symbol)
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				t.Fatal(ctx.Err())
			}
			source, err = client.DownloadHourlyHistory(ctx, key, from, from+86400, 1000, true)
		}
		if err != nil || len(source) == 0 {
			t.Fatalf("%s verified hourly source unavailable: rows=%d error=%v", symbol, len(source), err)
		}
		normal, err := client.History(ctx, key, from, from+86400, 1000)
		if err != nil || len(normal) == 0 {
			t.Logf("%s normal SDK reference unavailable: rows=%d error=%v", symbol, len(normal), err)
			continue
		}
		byTime := map[int64]domain.Candle{}
		for _, bar := range normal {
			byTime[bar.Time] = bar
		}
		matched := 0
		for _, bar := range source {
			other, ok := byTime[bar.Time]
			if !ok {
				continue
			}
			for index, value := range []float64{bar.Open, bar.High, bar.Low, bar.Close} {
				if math.Abs(value-[]float64{other.Open, other.High, other.Low, other.Close}[index]) > 1e-9 {
					t.Fatalf("%s source differs from the normal SDK BID price convention at %d", symbol, bar.Time)
				}
			}
			matched++
		}
		if matched < 10 {
			t.Fatalf("%s insufficient normal SDK prices for a meaningful comparison: %d", symbol, matched)
		}
		comparedSymbols++
		t.Logf("%s verified-source OHLC equals normal SDK BID OHLC for %d common closed hours", symbol, matched)
	}
	if comparedSymbols == 0 {
		t.Fatal("no normal SDK reference prices were available")
	}
}

func TestLiveSDKLongConcurrentInitialBatches(t *testing.T) {
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
	to := os.Getenv("CRYPTODESK_SDK_LIVE_LONG_TO")
	if to == "" {
		to = "2022-03-01"
	}
	observed := &liveSDKHistoryClient{dukascopyHistoryClient: client}
	manager := screener.NewWithDukascopyHistory(profile, dukascopyScreenerHistory{client: observed})
	defer manager.Close()
	_, err := manager.Start(screener.Request{DataSource: "dukascopy", Symbols: []string{"btcxau", "xauspx", "ethbtc"}, SyntheticSource: "h1-close", Timeframe: "d1", From: "2022-01-01", To: to, Concurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(40 * time.Minute)
	nextLog := time.Now().Add(30 * time.Second)
	for manager.Info().Active && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		if time.Now().After(nextLog) {
			for _, job := range manager.Info().Jobs {
				t.Logf("in progress %s: status=%s progress=%.4f", job.Symbol, job.Status, job.Progress)
			}
			t.Logf("SDK exports so far: hourly=%d ticks=%d", observed.hourlyExports.Load(), observed.tickExports.Load())
			nextLog = time.Now().Add(30 * time.Second)
		}
	}
	info := manager.Info()
	if info.Active {
		manager.Cancel()
		t.Fatal("bounded concurrent SDK check exceeded deadline")
	}
	for _, job := range info.Jobs {
		t.Logf("%s status=%s progress=%.4f message=%s", job.Symbol, job.Status, job.Progress, job.Message)
		if job.Status != "completed" {
			t.Fail()
		}
	}
	if to == "2026-11-25" {
		verifyLiveHourlyMonthlyCoverage(t, profile, client)
		if os.Getenv("CRYPTODESK_SDK_LIVE_EXPECT_HOURLY_CACHE") != "1" {
			from := time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC).Unix()
			info, err := client.InspectHourlyCandleData(context.Background(), domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCUSD", Timeframe: "1h"}, from, from+86400, "NO_FILTER")
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("SDK bounded recovery diagnostics: %v", info)
		}
	}
	files, err := manager.Files(0, 10)
	if err != nil || len(files.Files) != 3 {
		t.Fatalf("three requested histories were not published: error=%v", err)
	}
	for _, symbol := range []string{"BTCXAU", "XAUSPX", "ETHBTC"} {
		bars, err := manager.SyntheticHistory(context.Background(), domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: symbol, Timeframe: "1d"}, 0, 0, 5000)
		if err != nil || len(bars) < 30 {
			t.Fatalf("saved %s D1 chart unavailable: rows=%d error=%v", symbol, len(bars), err)
		}
		for _, bar := range bars {
			if !bar.Closed || !bar.Synthetic || !bar.Approximate || !bar.VolumeUnavailable {
				t.Fatalf("saved %s chart lost price quality metadata", symbol)
			}
		}
		requestedTo, err := time.Parse("2006-01-02", to)
		if err != nil || bars[0].Time > time.Date(2022, 1, 10, 0, 0, 0, 0, time.UTC).Unix() || bars[len(bars)-1].Time < requestedTo.AddDate(0, 0, -7).Unix() {
			t.Fatalf("saved %s D1 chart does not span the requested years", symbol)
		}
		t.Logf("saved %s D1 chart: rows=%d first=%s last=%s", symbol, len(bars), time.Unix(bars[0].Time, 0).UTC().Format("2006-01-02"), time.Unix(bars[len(bars)-1].Time, 0).UTC().Format("2006-01-02"))
	}
	t.Logf("SDKexports hourly=%d ticks=%d", observed.hourlyExports.Load(), observed.tickExports.Load())
	if observed.tickExports.Load() != 0 {
		t.Fatal("fast probe requested ticks")
	}
	if os.Getenv("CRYPTODESK_SDK_LIVE_EXPECT_HOURLY_CACHE") == "1" && observed.hourlyExports.Load() != 0 {
		t.Fatal("verified cached hourly sources caused another SDK export")
	}
}
