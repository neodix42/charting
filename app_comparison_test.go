package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/comparison"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/market"
	"cryptodesk/internal/storage"
)

func TestComparisonSnapshotFacadeExportsWithoutStartingProviders(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var factoryCalls atomic.Int32
	service := market.New(market.Options{Factory: func(string) (exchange.Adapter, error) {
		factoryCalls.Add(1)
		return nil, errors.New("snapshot must not request an adapter")
	}})
	defer service.Close()
	app := &App{store: store, market: service, ctx: context.Background()}
	directory, err := app.GetComparisonSnapshotDirectory()
	if err != nil || directory != filepath.Join(store.Directory(), "snapshots") {
		t.Fatal(directory, err)
	}
	defer app.comparison.Close()
	directory = filepath.Join(t.TempDir(), "chosen")
	if _, err = app.ConfigureComparisonSnapshotDirectory(directory); err != nil {
		t.Fatal(err)
	}
	last := time.Now().UTC().Unix()/86400*86400 - 86400
	zero := 0.0
	panel := comparison.Result{Weeks: 2, BaselineTime: last - 14*86400, LastClosedTime: last, Series: []comparison.Series{{Asset: comparison.Asset{ID: "BTC", Symbol: "BTC"}, Points: []comparison.Point{{Time: last - 14*86400, Pct: &zero}}}}}
	paths, err := app.ExportComparisonSnapshots([]comparison.Result{panel})
	if err != nil || len(paths) != 1 || filepath.Dir(paths[0]) != directory {
		t.Fatal(paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil || !strings.HasPrefix(string(data), "Date\tBTC\n") || !strings.HasSuffix(string(data), "\tNA\n") {
		t.Fatal(string(data), err)
	}
	if factoryCalls.Load() != 0 || service.Diagnostics().HistoryRequests != 0 {
		t.Fatal("snapshot accessed provider services")
	}
}

type comparisonDCRAdapter struct {
	provider         string
	streams          *atomic.Int32
	started, stopped chan struct{}
}

func (a *comparisonDCRAdapter) Instruments(context.Context) ([]domain.Instrument, error) {
	base, id, quote, kind := "BTC", "BTC", "USDC", "perpetual"
	if a.provider == "binance" {
		base, id, quote, kind = "DCR", "DCRUSDT", "USDT", "spot"
	}
	return []domain.Instrument{{NativeID: id, BaseAsset: base, QuoteAsset: quote, Market: kind, Availability: "available", SupportedIntervals: []string{"1d"}}}, nil
}
func (a *comparisonDCRAdapter) Stream(ctx context.Context, _ domain.FeedKey, _ func([]domain.Candle, domain.Health)) error {
	a.streams.Add(1)
	close(a.started)
	<-ctx.Done()
	close(a.stopped)
	return ctx.Err()
}
func (a *comparisonDCRAdapter) History(ctx context.Context, _ domain.FeedKey, from, to int64, _ int) ([]domain.Candle, error) {
	// Keep the read pending long enough to observe asynchronous cancellation
	// inherited from the already detached chart in the original implementation.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	bars := []domain.Candle{}
	for at := from; at < to; at += 86400 {
		value := 100 + float64((at-from)/86400)
		bars = append(bars, domain.Candle{Time: at, Open: value, High: value, Low: value, Close: value, Closed: true, Quality: "authoritative"})
	}
	return bars, nil
}

func TestComparisonFacadeLoadsBinanceDCRAfterItsChartHasStopped(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var streams atomic.Int32
	started, stopped := make(chan struct{}), make(chan struct{})
	service := market.New(market.Options{Grace: time.Millisecond, Factory: func(provider string) (exchange.Adapter, error) {
		return &comparisonDCRAdapter{provider: provider, streams: &streams, started: started, stopped: stopped}, nil
	}})
	defer service.Close()
	key := domain.FeedKey{Exchange: "binance", Market: "spot", Instrument: "DCRUSDT", Timeframe: "1d"}
	lease, err := service.Attach("dcr-chart", 1, key, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("chart did not start")
	}
	service.Detach(lease.LeaseID)
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("chart did not stop")
	}
	app := &App{store: store, market: service, ctx: context.Background()}
	result, err := app.GetComparisonDailyChanges("dcr-after-chart", comparison.View{Weeks: 4, Assets: []string{"DCR"}})
	if app.comparison != nil {
		defer app.comparison.Close()
	}
	if err != nil || len(result.Series) != 1 {
		t.Fatalf("DCR comparison failed: %+v %v", result, err)
	}
	series := result.Series[0]
	if series.Error != "" || len(series.Points) != 29 || series.Asset.FeedKey == nil || *series.Asset.FeedKey != key || series.Asset.QuoteAsset != "USDT" {
		t.Fatalf("Binance DCR did not load after chart shutdown: %+v", series)
	}
	if series.Points[0].Pct == nil || *series.Points[0].Pct != 0 || series.Points[28].Pct == nil || *series.Points[28].Pct < 27.999999 || *series.Points[28].Pct > 28.000001 {
		t.Fatal("DCR did not retain its common baseline and actual price changes")
	}
	if streams.Load() != 1 || service.Diagnostics().ActiveLeases != 0 {
		t.Fatal("Comparison restarted an unrelated chart stream")
	}
}

func TestComparisonFacadeKeepsEmptySavedViewWithoutStartingProviders(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// An empty selection must never invoke the real adapter factory.
	service := market.New(market.Options{})
	defer service.Close()
	app := &App{store: store, market: service, ctx: context.Background()}
	initial, err := app.GetComparisonView()
	if err != nil || initial.Weeks != 4 || len(initial.Assets) != 7 {
		t.Fatalf("initial view %+v %v", initial, err)
	}
	view := comparison.View{Weeks: 12, Assets: []string{}}
	if _, err = app.SaveComparisonView(view); err != nil {
		t.Fatal(err)
	}
	result, err := app.GetComparisonDailyChanges("empty-facade", view)
	if err != nil || result.Weeks != 12 || len(result.Series) != 0 || service.Diagnostics().HistoryRequests != 0 {
		t.Fatalf("empty Comparison fetched data %+v %v", result, err)
	}
	app.CancelComparisonRequest("empty-facade")
	app.comparison.Close()
	app.comparison = nil
	restored, err := app.GetComparisonView()
	if err != nil || restored.Weeks != 12 || len(restored.Assets) != 0 {
		t.Fatalf("empty view not durable %+v %v", restored, err)
	}
	app.comparison.Close()
	app.comparisonClosed = true
	if _, err = app.GetComparisonView(); err == nil {
		t.Fatal("Comparison reopened after app shutdown")
	}
}
