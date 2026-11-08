package screener

import (
	"context"
	"crypto/sha256"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type hyperliquidDownloadAdapter struct {
	mu      sync.Mutex
	calls   int
	spans   []dataRange
	hold    chan struct{}
	started chan struct{}
	empty   bool
}

func (a *hyperliquidDownloadAdapter) Instruments(context.Context) ([]domain.Instrument, error) {
	return []domain.Instrument{
		{NativeID: "BTC", Market: "perpetual", Availability: "available"},
		{NativeID: "@107", Market: "spot", Availability: "available", ProviderMetadata: map[string]string{"displaySymbol": "HYPE/USDC"}},
		{NativeID: "xyz:BTC", Market: "perpetual", Availability: "available"},
		{NativeID: "OLD", Market: "perpetual", Availability: "delisted"},
	}, nil
}
func (a *hyperliquidDownloadAdapter) History(ctx context.Context, k domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	a.mu.Lock()
	a.calls++
	a.spans = append(a.spans, dataRange{From: from, To: to})
	a.mu.Unlock()
	if a.hold != nil {
		if a.started != nil {
			select {
			case a.started <- struct{}{}:
			default:
			}
		}
		select {
		case <-a.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if a.empty {
		return nil, nil
	}
	if k.Exchange != "hyperliquid" || k.Timeframe != "1d" {
		return nil, errors.New("wrong download source/interval")
	}
	start := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC).Unix()
	out := []domain.Candle{}
	for i := range 30 {
		ts := start + int64(i)*86400
		if ts >= from && ts < to {
			v := float64(i + 1)
			out = append(out, domain.Candle{Time: ts, Open: v, High: v + 2, Low: v, Close: v + 1, Volume: float64(i + 2), Closed: true, Quality: "authoritative"})
		}
	}
	return out, nil
}
func (a *hyperliquidDownloadAdapter) Stream(context.Context, domain.FeedKey, func([]domain.Candle, domain.Health)) error {
	return errors.New("not used")
}
func hyperliquidDownloadManager(t *testing.T) *Manager {
	m := sourceEnrichmentManager(t, false)
	m.hyperliquidAdapter = &hyperliquidDownloadAdapter{}
	return m
}
func waitHyperliquidDownload(t *testing.T, m *Manager) Info {
	t.Helper()
	m.mu.Lock()
	done := m.batchDone
	m.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(90 * time.Second):
			t.Fatal("Hyperliquid download timed out")
		}
	}
	info := m.Info()
	for _, job := range info.Jobs {
		if job.Status != "completed" {
			t.Fatalf("download failed: %+v", job)
		}
	}
	return info
}
func TestHyperliquidDownloaderCatalogIdentitySourceAndWeekly(t *testing.T) {
	m := hyperliquidDownloadManager(t)
	catalog, err := m.CatalogForSource(context.Background(), "hyperliquid")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Groups) != 2 || len(catalog.Groups[0].Instruments) != 1 || len(catalog.Groups[1].Instruments) != 2 {
		t.Fatalf("complete market catalog: %+v", catalog)
	}
	id := "hyperliquid-" + publicAlertSymbol("perpetual", "BTC")
	if _, err = m.Start(Request{DataSource: "hyperliquid", Symbols: []string{id}, Timeframe: "w1", From: "2000-01-01", To: "2024-01-20"}); err != nil {
		t.Fatal(err)
	}
	info := waitHyperliquidDownload(t, m)
	if len(info.Jobs) != 2 {
		t.Fatal("weekly must retain daily prerequisite")
	}
	page, err := m.FilesForSource(0, 50, "all", "hyperliquid")
	if err != nil || page.Total != 2 {
		t.Fatalf("source inventory: %+v %v", page, err)
	}
	for _, f := range page.Files {
		if f.DataSource != "hyperliquid" || f.Description != "BTC" || f.From < "2024-01-01" {
			t.Fatalf("availability/identity: %+v", f)
		}
		if !strings.HasPrefix(f.Name, "hyperliquid_") {
			t.Fatal(f.Name)
		}
	}
	dukas, err := m.FilesForSource(0, 50, "all", "dukascopy")
	if err != nil || dukas.Total != 0 {
		t.Fatal("source inventory leaked")
	}
	daily, err := readHyperliquidCSV(info.Jobs[0].OutputPath, 86400)
	if err != nil || len(daily) != 17 {
		t.Fatalf("daily available range: %d %v", len(daily), err)
	}
	weekly, err := readHyperliquidCSV(info.Jobs[1].OutputPath, 604800)
	if err != nil || len(weekly) != 3 {
		t.Fatalf("weekly count %d %v", len(weekly), err)
	}
	verifiedWeeks, err := readHyperliquidVerifiedCSV(m.directory, filepath.Base(info.Jobs[1].OutputPath), 604800, info.Jobs[1].AsOf)
	if err != nil || len(verifiedWeeks) != 3 || verifiedWeeks[0].Closed || !verifiedWeeks[1].Closed || verifiedWeeks[2].Closed {
		t.Fatalf("weekly proof did not survive publication: %+v %v", verifiedWeeks, err)
	}
	// First partial listing week Jan3-7 uses the actual first open, last close and summed volume.
	if weekly[0].Time != time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix() || weekly[0].Open != 1 || weekly[0].Close != 6 || weekly[0].High != 7 || weekly[0].Volume != 20 {
		t.Fatalf("incorrect Monday OHLCV: %+v", weekly[0])
	}
	a := m.hyperliquidAdapter.(*hyperliquidDownloadAdapter)
	a.mu.Lock()
	calls := a.calls
	a.mu.Unlock()
	if calls != 1 {
		t.Fatalf("weekly refetched daily: calls=%d", calls)
	}
	restored := newManager(m.profile, func() (workerRuntime, error) { return m.runtime, nil })
	defer restored.Close()
	if restored.Info().Error != "" || len(restored.Info().Jobs) != 2 {
		t.Fatalf("source queue roundtrip: %+v", restored.Info())
	}
}
func TestHyperliquidEnrichmentAndExtensionPreserveHistoryAndIndicators(t *testing.T) {
	m := hyperliquidDownloadManager(t)
	id := "hyperliquid-" + publicAlertSymbol("spot", "@107")
	run := func(from, to string) {
		t.Helper()
		if _, err := m.Start(Request{DataSource: "hyperliquid", Symbols: []string{id}, Timeframe: "d1", From: from, To: to}); err != nil {
			t.Fatal(err)
		}
		waitHyperliquidDownload(t, m)
	}
	run("2024-01-01", "2024-01-20")
	// A same-period Dukascopy CSV must not be part of a Hyperliquid enrichment batch.
	untouched := filepath.Join(m.directory, "btcusd-d1-2024-01-01-2024-01-20.csv")
	original := []byte("timestamp,open,high,low,close,volume\n1704240000000,1,2,1,2,3\n")
	if err := os.WriteFile(untouched, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartEnrichment(EnrichmentRequest{DataSource: "hyperliquid", Period: "d1", Indicator: "sma", Parameters: map[string]float64{"period": 3}}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	done := m.enrichmentDone
	m.mu.Unlock()
	<-done
	enrichment, err := m.Enrichment(nil)
	if err != nil || enrichment.Total != 1 || enrichment.Completed != 1 {
		t.Fatalf("source enrichment: %+v %v", enrichment, err)
	}
	run("2024-01-20", "2024-01-25")
	page, err := m.FilesForSource(0, 50, "d1", "hyperliquid")
	if err != nil || page.Total != 1 {
		t.Fatalf("one merged history: %+v %v", page, err)
	}
	f := page.Files[0]
	data, err := os.ReadFile(filepath.Join(m.directory, f.Name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sma_3") || !strings.Contains(string(data), ",22,24,22,23,23,22\n") {
		t.Fatalf("indicator was not replayed through appended history: %s", data)
	}
	rows, err := readHyperliquidCSV(filepath.Join(m.directory, f.Name), 86400)
	if err != nil || len(rows) != 22 {
		t.Fatalf("old rows lost: %d %v", len(rows), err)
	}
	adapter := m.hyperliquidAdapter.(*hyperliquidDownloadAdapter)
	adapter.mu.Lock()
	calls := adapter.calls
	last := adapter.spans[len(adapter.spans)-1]
	adapter.mu.Unlock()
	if calls != 2 || last.From != time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("extension downloaded covered history: calls=%d last=%+v", calls, last)
	}
	run("2024-01-01", "2024-01-25")
	adapter.mu.Lock()
	repeatCalls := adapter.calls
	adapter.mu.Unlock()
	if repeatCalls != calls {
		t.Fatal("covered repeat refetched provider history")
	}

	got, _ := os.ReadFile(untouched)
	if string(got) != string(original) {
		t.Fatal("other source changed")
	}
}
func TestHyperliquidSelectionValidationAndCancel(t *testing.T) {
	m := hyperliquidDownloadManager(t)
	id := "hyperliquid-" + publicAlertSymbol("perpetual", "BTC")
	view, _ := m.View()
	view.DataSource = "hyperliquid"
	view.HyperliquidSymbols = []string{id}
	view.HyperliquidSelectionInitialized = true
	if _, err := m.SaveView(view); err != nil {
		t.Fatal(err)
	}
	if err := m.loadView(); err != nil {
		t.Fatal(err)
	}
	saved, _ := m.View()
	if saved.DataSource != "hyperliquid" || len(saved.HyperliquidSymbols) != 1 || !saved.HyperliquidSelectionInitialized {
		t.Fatal(saved)
	}
	if _, err := m.Start(Request{DataSource: "hyperliquid", Symbols: []string{id}, Timeframe: "h1", From: "2024-01-01", To: "2024-01-20"}); err == nil {
		t.Fatal("unsupported timeframe accepted")
	}
	a := m.hyperliquidAdapter.(*hyperliquidDownloadAdapter)
	a.hold = make(chan struct{})
	a.started = make(chan struct{}, 1)
	if _, err := m.Start(Request{DataSource: "hyperliquid", Symbols: []string{id}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-20"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.started:
	case <-time.After(5 * time.Second):
		t.Fatal("download never reached cancellable provider")
	}
	cancelled := m.Cancel()
	if cancelled.Active || cancelled.Jobs[0].Status != "cancelled" {
		t.Fatal(cancelled)
	}
	files, _ := m.FilesForSource(0, 10, "all", "hyperliquid")
	if files.Total != 0 {
		t.Fatal("cancelled download was published")
	}
}

func TestHyperliquidDownloaderLiveSmoke(t *testing.T) {
	if os.Getenv("CRYPTODESK_LIVE_HYPERLIQUID_DOWNLOAD") != "1" {
		t.Skip("opt in with CRYPTODESK_LIVE_HYPERLIQUID_DOWNLOAD=1; two markets, real public data, temporary profile")
	}
	m := sourceEnrichmentManager(t, false)
	actual, err := exchange.New("hyperliquid")
	if err != nil {
		t.Fatal(err)
	}
	m.hyperliquidAdapter = publicAlertLiveSmokeAdapter{Adapter: actual}
	now := time.Now().UTC().Truncate(24 * time.Hour)
	from := now.AddDate(0, 0, -90).Format("2006-01-02")
	to := now.Format("2006-01-02")
	ids := []string{"hyperliquid-" + publicAlertSymbol("spot", "@107"), "hyperliquid-" + publicAlertSymbol("perpetual", "BTC")}
	if _, err = m.Start(Request{DataSource: "hyperliquid", Symbols: ids, Timeframe: "w1", From: from, To: to}); err != nil {
		t.Fatal(err)
	}
	waitHyperliquidDownload(t, m)
	if _, err = m.StartEnrichment(EnrichmentRequest{DataSource: "hyperliquid", Period: "d1", Indicator: "sma", Parameters: map[string]float64{"period": 3}}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	done := m.enrichmentDone
	m.mu.Unlock()
	<-done
	enriched, _ := m.Enrichment(nil)
	if enriched.Completed != 2 {
		t.Fatalf("actual calculator enrichment: %+v", enriched)
	}
	if _, err = m.Start(Request{DataSource: "hyperliquid", Symbols: ids, Timeframe: "d1", From: now.AddDate(0, 0, -100).Format("2006-01-02"), To: to}); err != nil {
		t.Fatal(err)
	}
	waitHyperliquidDownload(t, m)
	page, err := m.FilesForSource(0, 20, "all", "hyperliquid")
	if err != nil || page.Total != 4 {
		t.Fatalf("live source inventory: %+v %v", page, err)
	}
	for _, f := range page.Files {
		if f.Error != "" {
			t.Fatal(f.Error)
		}
		t.Logf("real %s: %s to %s, %d bytes, fields=%v", f.Description, f.From, f.To, f.Bytes, f.Fields)
	}
}

func TestHyperliquidVisibleCoverageNeverPromotesUnverifiedSnapshots(t *testing.T) {
	directory := t.TempDir()
	name := "hyperliquid-" + publicAlertSymbol("perpetual", "BTC") + "-d1-2024-01-01-2024-01-03.csv"
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	bars := []domain.Candle{{Time: start, Open: 1, High: 2, Low: 1, Close: 2, Volume: 1, Closed: true, Quality: "authoritative"}, {Time: start + 86400, Open: 2, High: 3, Low: 2, Close: 3, Volume: 1, Quality: "provisional"}}
	data, err := publicAlertRawCSV(bars, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	asOf := (start + 10*86400) * 1000
	// Missing metadata/checksum must neither prove rows nor suppress downloads.
	rows, err := readHyperliquidVerifiedCSV(directory, name, 86400, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Closed || rows[1].Closed {
		t.Fatal("unverified timestamps were certified")
	}
	spans, err := hyperliquidCoveredRanges([]downloadSource{{Input: path, Name: name}}, asOf)
	if err != nil || len(spans) != 0 {
		t.Fatalf("unverified CSV suppressed download: %+v %v", spans, err)
	}
	digest := sha256.Sum256(data)
	metadata := fileMetadata{Checksum: hex.EncodeToString(digest[:]), Coverage: []dataRange{{From: start * 1000, To: (start + 86400) * 1000}}}
	if err = writeFileMetadata(directory, name, metadata); err != nil {
		t.Fatal(err)
	}
	rows, err = readHyperliquidVerifiedCSV(directory, name, 86400, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if !rows[0].Closed || rows[1].Closed {
		t.Fatalf("elapsed time promoted old partial candle: %+v", rows)
	}
	// External edits invalidate the entire old provenance, while preserving bytes.
	metadata.Checksum = strings.Repeat("0", 64)
	if err = writeFileMetadata(directory, name, metadata); err != nil {
		t.Fatal(err)
	}
	rows, err = readHyperliquidVerifiedCSV(directory, name, 86400, asOf)
	if err != nil || rows[0].Closed {
		t.Fatalf("changed CSV was trusted: %+v %v", rows, err)
	}
}

func TestHyperliquidEmptyRepairKeepsOldPartialUnverified(t *testing.T) {
	m := hyperliquidDownloadManager(t)
	m.hyperliquidAdapter.(*hyperliquidDownloadAdapter).empty = true
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	id := "hyperliquid-" + publicAlertSymbol("perpetual", "BTC")
	name := id + "-d1-2024-01-01-2024-01-03.csv"
	bars := []domain.Candle{{Time: start, Open: 1, High: 2, Low: 1, Close: 2, Volume: 1, Closed: true, Quality: "authoritative"}, {Time: start + 86400, Open: 2, High: 3, Low: 2, Close: 3, Volume: 1, Quality: "provisional"}}
	data, _ := publicAlertRawCSV(bars, false)
	path := filepath.Join(m.directory, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	metadata := fileMetadata{Checksum: hex.EncodeToString(digest[:]), Coverage: []dataRange{{From: start * 1000, To: (start + 86400) * 1000}}}
	if err := writeFileMetadata(m.directory, name, metadata); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(Request{DataSource: "hyperliquid", Symbols: []string{id}, Timeframe: "d1", From: "2024-01-01", To: "2024-01-03"}); err != nil {
		t.Fatal(err)
	}
	info := waitHyperliquidDownload(t, m)
	output := info.Jobs[0].OutputPath
	verified, err := readHyperliquidVerifiedCSV(m.directory, filepath.Base(output), 86400, time.Now().UnixMilli())
	if err != nil || len(verified) != 2 || verified[1].Closed {
		t.Fatalf("empty repair certified retained partial: %+v %v", verified, err)
	}
	raw, _, _, err := readHyperliquidRaw(hyperliquidHistoryRoot(m.profile), publicAlertAsset{Market: "perpetual", NativeID: "BTC"}, "d1")
	if err != nil || len(raw) != 1 || raw[0].Time != start {
		t.Fatalf("stale partial entered chart/alert history: %+v %v", raw, err)
	}
}

func TestHyperliquidReplayResumesWithInterruptedScratch(t *testing.T) {
	m := hyperliquidDownloadManager(t)
	work := t.TempDir()
	input := filepath.Join(work, "data.part")
	base := "timestamp,open,high,low,close,volume\n1704067200000,1,2,1,2,1\n1704153600000,2,3,2,3,1\n1704240000000,3,4,3,4,1\n"
	if err := os.WriteFile(input, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(m.directory, "hyperliquid-"+publicAlertSymbol("perpetual", "BTC")+"-d1-2024-01-01-2024-01-04.csv")
	enriched := strings.Replace(base, "volume\n", "volume,sma_3\n", 1)
	enriched = strings.Replace(enriched, ",2,1\n", ",2,1,\n", 1)
	enriched = strings.Replace(enriched, ",3,1\n", ",3,1,\n", 1)
	enriched = strings.Replace(enriched, ",4,1\n", ",4,1,3\n", 1)
	if err := os.WriteFile(source, []byte(enriched), 0600); err != nil {
		t.Fatal(err)
	}
	interrupted := filepath.Join(work, "replayed.csv")
	if err := os.WriteFile(interrupted, []byte("old interrupted replay"), 0600); err != nil {
		t.Fatal(err)
	}
	data, recipes, err := m.replayHyperliquidIndicators(context.Background(), work, input, []downloadSource{{Input: source, Name: filepath.Base(source)}}, nil)
	if err != nil || len(recipes) != 1 || !strings.Contains(string(data), "1704240000000,3,4,3,4,1,3\n") {
		t.Fatalf("retry after interrupted replay: %s %+v %v", data, recipes, err)
	}
	retained, _ := os.ReadFile(interrupted)
	if string(retained) != "old interrupted replay" {
		t.Fatal("retry modified unrelated older scratch")
	}
	entries, _ := os.ReadDir(work)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".replay-") {
			t.Fatal("replay attempt scratch leaked")
		}
	}
}

func TestHyperliquidBaseOnlyExtensionNeedsNoIndicatorRuntime(t *testing.T) {
	work := t.TempDir()
	source := filepath.Join(work, "source.csv")
	input := filepath.Join(work, "data.part")
	data := []byte("timestamp,open,high,low,close,volume\n1704067200000,1,2,1,2,1\n")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{runtimeError: "explicitly unavailable"}
	got, recipes, err := m.replayHyperliquidIndicators(context.Background(), work, input, []downloadSource{{Input: source, Name: filepath.Base(source)}}, nil)
	if err != nil || len(recipes) != 0 || string(got) != string(data) {
		t.Fatalf("base-only extension required Node: %s %+v %v", got, recipes, err)
	}
	unknown := strings.Replace(string(data), "volume\n", "volume,unknown\n", 1)
	unknown = strings.Replace(unknown, ",2,1\n", ",2,1,999\n", 1)
	if err := os.WriteFile(source, []byte(unknown), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.replayHyperliquidIndicators(context.Background(), work, input, []downloadSource{{Input: source, Name: filepath.Base(source)}}, nil); err == nil {
		t.Fatal("unknown derived column was silently discarded")
	}
}
