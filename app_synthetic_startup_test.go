package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/screener"
)

func seedStartupRatio(t *testing.T, profile, symbol, period string) {
	t.Helper()
	directory := filepath.Join(profile, "screener")
	metadataDirectory := filepath.Join(directory, ".cryptodesk-screener-metadata")
	if err := os.MkdirAll(metadataDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	name := symbol + "-" + period + "-2024-01-01-2024-01-15.csv"
	content := []byte("timestamp,open,high,low,close\n1704067200000,5,7,4,6\n")
	filename := filepath.Join(directory, name)
	if err := os.WriteFile(filename, content, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := screener.SyntheticDefinitionFor(symbol)
	checksum := sha256.Sum256(content)
	metadata := map[string]any{
		"version": 1, "filename": name, "verified": true, "bytes": info.Size(), "modified": info.ModTime().UnixNano(), "checksum": hex.EncodeToString(checksum[:]),
		"provenance": screener.DataProvenance{Version: 1, Kind: "synchronized-bid-ratio", Numerator: definition.Numerator, Denominator: definition.Denominator, PriceType: "bid", SessionPolicy: "exact-timestamp-no-carry-v1", Volume: "unavailable", Approximate: true, ObservationResolution: "simultaneous millisecond BID ticks"},
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	id := sha256.Sum256([]byte(filename))
	if err := os.WriteFile(filepath.Join(metadataDirectory, hex.EncodeToString(id[:])+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// Exercise the production deferred bootstrap and its actual market factory,
// rather than the legacy synchronous initializer or a mocked frontend bridge.
func TestDeferredStartupRoutesCalculatedChartsToPublishedHistory(t *testing.T) {
	profile := t.TempDir()
	for _, symbol := range []string{"btcxau", "xauspx", "ethbtc"} {
		for _, period := range []string{"d1", "w1"} {
			seedStartupRatio(t, profile, symbol, period)
		}
	}
	a := newBootstrapApp(filepath.Join(profile, "workspace.sqlite"))
	defer a.shutdown(context.Background())
	a.beginBootstrap()
	if _, err := a.LoadWorkspace(); err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"BTCXAU", "XAUSPX", "ETHBTC"} {
		for _, interval := range []string{"1d", "1w"} {
			t.Run(symbol+interval, func(t *testing.T) {
				key := domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: symbol, Timeframe: interval}
				snapshot, err := a.AttachChart("startup-"+symbol+interval, 1, key, 1704067200, 1705276800)
				if err != nil {
					t.Fatal(err)
				}
				defer a.DetachChart(snapshot.LeaseID)
				deadline := time.Now().Add(3 * time.Second)
				for len(snapshot.Bars) == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
					snapshot, err = a.GetFeedSnapshot(snapshot.LeaseID, 0)
					if err != nil {
						t.Fatal(err)
					}
				}
				if len(snapshot.Bars) != 1 || snapshot.Bars[0].Close != 6 || !snapshot.Bars[0].Synthetic || !snapshot.Bars[0].Approximate || !snapshot.Bars[0].VolumeUnavailable {
					t.Fatalf("deferred chart bypassed calculated history: %+v", snapshot)
				}
				history, err := a.market.History(context.Background(), "startup-history", key, 1704067200, 1705276800, 10)
				if err != nil || len(history.Bars) != 1 || !history.Bars[0].Synthetic {
					t.Fatalf("deferred history bypassed calculated reader: %+v %v", history, err)
				}
			})
		}
	}
}

func TestDeferredStartupMissingRatioGivesDownloadGuidanceWithoutBrokerAccount(t *testing.T) {
	a := newBootstrapApp(filepath.Join(t.TempDir(), "workspace.sqlite"))
	defer a.shutdown(context.Background())
	a.beginBootstrap()
	if _, err := a.LoadWorkspace(); err != nil {
		t.Fatal(err)
	}
	key := domain.FeedKey{Exchange: "dukascopy", Market: "cfd", Instrument: "BTCXAU", Timeframe: "1d"}
	if err := a.awaitService("screener"); err != nil {
		t.Fatal(err)
	}
	_, err := a.market.History(context.Background(), "missing-ratio", key, 1704067200, 1705276800, 10)
	if !errors.Is(err, screener.ErrSyntheticHistoryUnavailable) || !strings.Contains(err.Error(), "Screener") {
		t.Fatalf("missing calculated history reached native broker validation: %v", err)
	}
}
