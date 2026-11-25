//go:build sdkhistoryintegration

package main

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/screener"
)

// Explicit opt-in only. The source profile is read, never changed. Credentials
// stay in private Go/SDK storage; only public history and aggregate counts cross
// the worker pipe. Telegram settings and trading journals are never copied.
func TestLiveSDKUnderlyingTicksBuildAndSaveSyntheticHistory(t *testing.T) {
	sourceProfile := os.Getenv("CRYPTODESK_SDK_LIVE_PROFILE")
	if sourceProfile == "" {
		t.Fatal("set CRYPTODESK_SDK_LIVE_PROFILE for this explicit read-only live check")
	}
	profile := t.TempDir()
	// An optional private artifact directory retains only completed public-price
	// chunks between explicit smoke attempts. Settings and SDK sessions stay in
	// t.TempDir and are deleted, including after a failed or cancelled check.
	if cache := os.Getenv("CRYPTODESK_SDK_LIVE_CACHE"); cache != "" {
		quoteRoot := filepath.Join(profile, "screener-provider", "synthetic-quotes-v1")
		if err := copyLivePriceCache(cache, quoteRoot); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := copyLivePriceCache(quoteRoot, cache); err != nil {
				t.Error(err)
			}
		})
	}
	settings, err := os.ReadFile(filepath.Join(sourceProfile, "dukascopy-jforex.json"))
	if err != nil {
		t.Fatal("could not read the configured SDK connection settings")
	}
	if err := os.WriteFile(filepath.Join(profile, "dukascopy-jforex.json"), settings, 0600); err != nil {
		t.Fatal("could not create isolated SDK connection settings")
	}
	for i := range settings {
		settings[i] = 0
	}
	client := jforex.New(profile)
	defer client.Close()
	if _, configured := client.HistoricalSource(); !configured {
		t.Fatal("the source profile has no configured SDK history account")
	}
	observed := &liveSDKHistoryClient{dukascopyHistoryClient: client}
	manager := screener.NewWithDukascopyHistory(profile, dukascopyScreenerHistory{client: observed})
	defer manager.Close()
	// Reproduce the existing public denial without sending any public request.
	gate := filepath.Join(profile, "screener-provider")
	if err := os.MkdirAll(gate, 0700); err != nil {
		t.Fatal(err)
	}
	deny, err := json.Marshal(map[string]any{"version": 1, "nextAt": 0, "limitedTill": 0, "deniedTill": time.Now().Add(15 * time.Minute).UnixMilli(), "rateCount": 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gate, "rate-limit.json"), deny, 0600); err != nil {
		t.Fatal(err)
	}
	symbols := []string{"btcxau", "btcusd"}
	calculated := []string{"BTCXAU"}
	if os.Getenv("CRYPTODESK_SDK_LIVE_ALL") == "1" {
		symbols = []string{"btcxau", "btcusd", "xauspx", "ethbtc", "xauxag"}
		calculated = append(calculated, "XAUSPX", "ETHBTC")
	}
	_, err = manager.Start(screener.Request{DataSource: "dukascopy", Symbols: symbols, Timeframe: "d1", From: "2024-10-01", To: "2024-10-02", Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Minute)
	for manager.Info().Active && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	info := manager.Info()
	if info.Active {
		manager.Cancel()
		t.Fatal("bounded SDK history smoke exceeded its deadline")
	}
	t.Logf("Underlying SDK tick exports: %d", observed.tickExports.Load())
	for _, job := range info.Jobs {
		if job.Status != "completed" {
			t.Fatalf("SDK history %s did not publish: %s", job.Symbol, job.Message)
		}
	}
	files, err := manager.Files(0, 10)
	if err != nil || len(files.Files) != len(symbols) {
		t.Fatalf("SDK histories not saved: count=%d error=%v", len(files.Files), err)
	}
	for _, file := range files.Files {
		if file.Provenance == nil || file.Provenance.HistorySource != "jforex-sdk" {
			t.Fatal("saved history lost SDK provenance")
		}
		for _, field := range file.Fields {
			if strings.EqualFold(field, "volume") {
				t.Fatal("SDK price history invented volume")
			}
		}
	}
	for _, symbol := range calculated {
		bars, err := manager.SyntheticHistory(context.Background(), domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: symbol, Timeframe: "1d"}, 0, 0, 1000)
		if err != nil || len(bars) != 1 || !bars[0].Closed || !bars[0].Synthetic || !bars[0].VolumeUnavailable {
			t.Fatalf("saved %s chart unavailable: rows=%d error=%v", symbol, len(bars), err)
		}
	}
	if os.Getenv("CRYPTODESK_SDK_LIVE_EXPECT_CACHED") == "1" && observed.tickExports.Load() != 0 {
		t.Fatal("verified cached quote hours caused another SDK tick export")
	}
	t.Logf("Real SDK history built and saved all %d selected instruments; calculated D1 charts open from the published files.", len(symbols))
}

type liveSDKHistoryClient struct {
	dukascopyHistoryClient
	tickExports atomic.Int64
}

func (c *liveSDKHistoryClient) ExportTicks(ctx context.Context, source, symbol string, from, to int64, output string) (int64, error) {
	c.tickExports.Add(1)
	return c.dukascopyHistoryClient.ExportTicks(ctx, source, symbol, from, to, output)
}

func copyLivePriceCache(source, destination string) error {
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") || filepath.Ext(path) != ".csv" && filepath.Ext(path) != ".json" {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.CreateTemp(filepath.Dir(target), ".price-cache-")
		if err != nil {
			return err
		}
		defer os.Remove(output.Name())
		_, copyErr := io.Copy(output, input)
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Rename(output.Name(), target)
	})
}
