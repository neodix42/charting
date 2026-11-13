package screener

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func publicProgressContext(l *LiveAlerts) context.Context {
	const id = "public-step-alert"
	l.mu.Lock()
	l.checkProgress = map[string]liveAlertProgress{id: {state: "running", revision: l.configRevision}}
	l.mu.Unlock()
	return context.WithValue(context.Background(), liveAlertProgressKey{}, id)
}
func publicProgressPhase(l *LiveAlerts) liveAlertProgress {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.checkProgress["public-step-alert"]
}
func awaitPublicPhase(t *testing.T, l *LiveAlerts, phase string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if strings.Contains(publicProgressPhase(l).phase, phase) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("public progress did not report %q: %+v", phase, publicProgressPhase(l))
		case <-ticker.C:
		}
	}
}

func TestPublicAlertStepsIdentifyWorkerAndSharedHistoryWaits(t *testing.T) {
	for _, wait := range []string{"worker", "catalog", "series"} {
		t.Run(wait, func(t *testing.T) {
			l, _, _, _ := parallelPublicFixture(t, 1)
			ctx, cancel := context.WithCancel(publicProgressContext(l))
			defer cancel()
			data := l.publicAlertDataState()
			releases := []func(){}
			defer func() {
				for _, release := range releases {
					release()
				}
			}()
			phase := "Waiting for public data worker"
			if wait == "worker" {
				for range publicAlertConcurrency {
					release, err := data.acquireSlot(ctx)
					if err != nil {
						t.Fatal(err)
					}
					releases = append(releases, release)
				}
			} else {
				key := "catalog/hyperliquid"
				phase = "Waiting for shared market discovery"
				if wait == "series" {
					key = "series/hyperliquid/" + publicAlertSymbol("spot", "@0") + "/m1"
					phase = "Waiting for shared history"
				}
				release, err := data.acquireKey(ctx, key)
				if err != nil {
					t.Fatal(err)
				}
				releases = append(releases, release)
			}
			c := publicAlertTestCondition()
			c.Request.Conditions = c.Request.Conditions[:1]
			result := runParallelPublic(l, ctx, c)
			awaitPublicPhase(t, l, phase)
			cancel()
			finished := awaitParallelPublic(t, result)
			if !errors.Is(finished.err, context.Canceled) {
				t.Fatalf("cancelled wait was not drained: %v", finished.err)
			}
		})
	}
}

func TestPublicAlertStepsShowOneNamedConcurrentDownloadAndRemoveCompletedWorkers(t *testing.T) {
	l, fake, adapter, gate := parallelPublicFixture(t, 4)
	for index := range fake.items {
		fake.items[index].ProviderMetadata = map[string]string{"displaySymbol": []string{"CAPPY/USDC", "SELL/USDC", "xyz:AAPL", "BTC"}[index]}
	}
	ctx, cancel := context.WithCancel(publicProgressContext(l))
	defer cancel()
	c := publicAlertTestCondition()
	c.Request.Conditions = c.Request.Conditions[:1]
	result := runParallelPublic(l, ctx, c)
	awaitParallelPublicStarts(t, adapter, publicAlertConcurrency)
	current := publicProgressPhase(l)
	if !strings.HasPrefix(current.phase, "Downloading ") || strings.Count(current.phase, "Downloading") != 1 || current.asset != "" {
		t.Fatalf("concurrent downloads were repeated instead of summarized: %+v", current)
	}
	name := strings.TrimPrefix(current.phase, "Downloading ")
	if name != "CAPPY/USDC" && name != "SELL/USDC" && name != "xyz:AAPL" && name != "BTC" {
		t.Fatalf("download label lost the actual asset's display name: %+v", current)
	}
	if current.percent != 0 {
		t.Fatal("active worker labels advanced unfinished series count", current)
	}
	close(gate)
	r := awaitParallelPublic(t, result)
	if r.err != nil {
		t.Fatal(r.err)
	}
	current = publicProgressPhase(l)
	if current.phase != "Finishing public data preparation" || current.asset != "" || current.percent != 70 {
		t.Fatalf("finished task left a stale active step: %+v", current)
	}
}

func TestPublicAlertSeriesReportsActualEnrichmentAndCacheReuse(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker, plans, err := startPublicAlertWorker(ctx, m.runtime, map[string]publicAlertPlan{"m1": {Fields: []string{"close", "sma_3"}, Extra: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.close()
	root := t.TempDir()
	directory := filepath.Join(root, "data")
	for _, dir := range []string{directory, filepath.Join(root, "raw")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fake := &publicAlertFakeAdapter{price: 100}
	asset := publicAlertAsset{Symbol: publicAlertSymbol("spot", "@0"), NativeID: "@0", Market: "spot", Name: "Example"}
	stages := []string{}
	report := func(stage string) { stages = append(stages, stage) }
	cutoff := time.Date(2026, 11, 7, 18, 0, 5, 0, time.UTC).UnixMilli()
	if _, err = preparePublicAlertSeries(ctx, fake, worker, root, directory, "hyperliquid", asset, "m1", plans["m1"], cutoff, report); err != nil {
		t.Fatal(err)
	}
	expected := []string{"Validating cached history", "Downloading history (provider pacing and network)", "Saving verified candle history", "Checking reusable indicators", "Enriching indicators", "Saving enriched history"}
	if !reflect.DeepEqual(stages, expected) {
		t.Fatalf("enrichment stage ordering incorrect: %v", stages)
	}
	stages = nil
	if _, err = preparePublicAlertSeries(ctx, fake, worker, root, directory, "hyperliquid", asset, "m1", plans["m1"], cutoff, report); err != nil {
		t.Fatal(err)
	}
	expected = []string{"Validating cached history", "Reusing verified candle history", "Checking reusable indicators", "Reusing verified indicators"}
	if !reflect.DeepEqual(stages, expected) {
		t.Fatalf("cache reuse reported enrichment/downloads it did not run: %v", stages)
	}
}

func TestPublicAlertStepsAggregateDifferentActiveStages(t *testing.T) {
	l, _, _, _ := parallelPublicFixture(t, 1)
	p := newPublicAlertPreparationProgress(l, publicProgressContext(l), 3)
	p.step(0, "Downloading history", "BTC")
	p.step(1, "Enriching indicators", "ETH")
	current := publicProgressPhase(l)
	if current.phase != "Downloading BTC" || current.asset != "" {
		t.Fatalf("active download was not concise: %+v", current)
	}
	p.complete(0)
	current = publicProgressPhase(l)
	if current.phase != "Enriching indicators" || current.asset != "ETH: Enriching indicators" {
		t.Fatalf("completed task masks remaining enrichment: %+v", current)
	}
	p.step(2, "Enriching indicators", "SOL")
	if got := publicProgressPhase(l).phase; got != "Enriching indicators (2)" {
		t.Fatalf("matching steps not counted: %s", got)
	}
}

func TestPublicAlertStepsChooseOneDownloadAndSwitchWhenItsWorkerFinishes(t *testing.T) {
	l, _, _, _ := parallelPublicFixture(t, 1)
	p := newPublicAlertPreparationProgress(l, publicProgressContext(l), 4)
	p.step(3, "Downloading latest closed candle", "hyperliquid SELL/USDC spot d1", "SELL/USDC")
	p.step(1, "Downloading older indicator warm-up history", "hyperliquid CAPPY/USDC spot d1", "CAPPY/USDC")
	p.step(2, "Downloading history (provider pacing and network)", "hyperliquid BTC perpetual d1", "BTC")
	p.step(0, "Enriching indicators", "hyperliquid ETH perpetual d1", "ETH")
	assertDownload := func(name string, percent float64) {
		t.Helper()
		current := publicProgressPhase(l)
		if current.phase != "Downloading "+name || current.asset != "" || current.percent != percent {
			t.Fatalf("unstable/duplicated active download or incorrect completion count: %+v", current)
		}
	}
	assertDownload("CAPPY/USDC", 0)
	// Reporting a higher worker again cannot replace the selected lower-index
	// download, and a finishing series advances percentage exactly once.
	p.step(3, "Downloading latest closed candle", "hyperliquid SELL/USDC spot d1", "SELL/USDC")
	assertDownload("CAPPY/USDC", 0)
	p.complete(1)
	assertDownload("BTC", 17.5)
	p.remove(2)
	assertDownload("SELL/USDC", 17.5)
	p.step(3, "Enriching indicators", "hyperliquid SELL/USDC spot d1", "SELL/USDC")
	current := publicProgressPhase(l)
	if current.phase != "Enriching indicators (2)" || !strings.Contains(current.asset, "ETH") || !strings.Contains(current.asset, "SELL/USDC") || current.percent != 17.5 {
		t.Fatal("completed download hid remaining enrichment", current)
	}
	p.remove(0)
	p.complete(3)
	current = publicProgressPhase(l)
	if current.phase != "Finishing public data preparation" || current.asset != "" || current.percent != 35 {
		t.Fatal("removed workers left stale download text or changed series counts", current)
	}
}

func TestPublicAlertProgressNameUsesDisplayNameWithSymbolFallback(t *testing.T) {
	for _, test := range []struct {
		asset publicAlertAsset
		want  string
	}{
		{asset: publicAlertAsset{Name: " CAPPY/USDC ", Symbol: "internal-spot-id"}, want: "CAPPY/USDC"},
		{asset: publicAlertAsset{Symbol: "xyz:BTC"}, want: "xyz:BTC"},
		{asset: publicAlertAsset{Name: " ", Symbol: "BTC"}, want: "BTC"},
	} {
		if got := publicAlertProgressName(test.asset); got != test.want {
			t.Fatalf("asset display name=%q, want %q", got, test.want)
		}
	}
}

func TestPublicAlertSeriesReportsWarmupAndLatestCandleRepair(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker, plans, err := startPublicAlertWorker(ctx, m.runtime, map[string]publicAlertPlan{"m1": {Fields: []string{"close", "sma_3"}, Extra: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.close()
	for _, test := range []struct {
		name, phase    string
		short, pending bool
	}{
		{name: "short listing", phase: "Downloading older indicator warm-up history", short: true},
		{name: "missing latest candle", phase: "Downloading latest closed candle", pending: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "data")
			for _, dir := range []string{directory, filepath.Join(root, "raw")} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			fake := &publicAlertFakeAdapter{price: 100, short: test.short, pending: test.pending}
			asset := publicAlertAsset{Symbol: publicAlertSymbol("spot", "@0"), NativeID: "@0", Market: "spot"}
			stages := []string{}
			_, err := preparePublicAlertSeries(ctx, fake, worker, root, directory, "hyperliquid", asset, "m1", plans["m1"], time.Date(2026, 11, 7, 18, 0, 5, 0, time.UTC).UnixMilli(), func(stage string) { stages = append(stages, stage) })
			if err == nil {
				t.Fatal("incomplete actual history was accepted")
			}
			found := false
			for _, stage := range stages {
				if stage == test.phase {
					found = true
				}
				if stage == "Enriching indicators" {
					t.Fatal("failed history falsely reported enrichment", stages)
				}
			}
			if !found {
				t.Fatalf("repair step was hidden: %v", stages)
			}
		})
	}
}
