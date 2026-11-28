package main

import (
	"context"
	"math"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/comparison"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/market"
	"cryptodesk/internal/storage"
)

type overviewHistoryWindow struct {
	key      domain.FeedKey
	from, to int64
	limit    int
}
type comparisonOverviewAdapter struct {
	*comparisonVenueAdapter
	windows chan overviewHistoryWindow
}

func (a *comparisonOverviewAdapter) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	a.windows <- overviewHistoryWindow{key, from, to, limit}
	return a.comparisonVenueAdapter.History(ctx, key, from, to, limit)
}

func TestComparisonOverviewFacadeSharesNativeSourceSnapshotAcrossFourPanels(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	windows := make(chan overviewHistoryWindow, 8)
	reads := make(chan domain.FeedKey, 8)
	var streams atomic.Int32
	service := market.New(market.Options{Factory: func(provider string) (exchange.Adapter, error) {
		return &comparisonOverviewAdapter{comparisonVenueAdapter: &comparisonVenueAdapter{provider: provider, reads: reads, streams: &streams}, windows: windows}, nil
	}})
	defer service.Close()
	now := func() time.Time { return time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) }
	app := &App{store: store, market: service, ctx: context.Background()}
	app.comparison = comparison.NewWithOptions(comparison.Options{Profile: store.Directory(), Source: comparisonMarketSource{market: service}, Now: now})
	defer app.comparison.Close()
	view := comparison.View{Weeks: 26, Assets: []string{"coinbase|spot|TON-USD", "bybit|spot|GRAMUSDT", "hyperliquid|perpetual|GRAM"}}
	if _, err = app.SaveComparisonView(view); err != nil {
		t.Fatal(err)
	}
	result, err := app.GetComparisonOverview("overview-three-venues", view)
	if err != nil || result.RequestID != "overview-three-venues" || len(result.Panels) != 4 {
		t.Fatal(result, err)
	}
	last := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC).Unix()
	from := last - 84*86400
	for index, weeks := range []int{2, 4, 8, 12} {
		panel := result.Panels[index]
		if panel.Weeks != weeks || panel.LastClosedTime != last || panel.AsOf != now().Unix() || panel.BaselineTime != last-int64(weeks*7)*86400 || len(panel.Series) != 3 {
			t.Fatalf("facade returned inconsistent snapshot windows: %+v", panel)
		}
		for assetIndex, series := range panel.Series {
			if series.Asset.ID != view.Assets[assetIndex] || series.Error != "" || len(series.Points) != weeks*7+1 || series.Points[0].Pct == nil || *series.Points[0].Pct != 0 {
				t.Fatalf("facade changed venue or baseline: %+v", series)
			}
			point := series.Points[len(series.Points)-1]
			base := 100 + float64((12-weeks)*7)
			want := (184/base - 1) * 100
			if point.Close == nil || *point.Close != 184 || point.Pct == nil || math.Abs(*point.Pct-want) > 1e-10 {
				t.Fatalf("panels did not share actual native closes with separate rebasing: %+v", point)
			}
		}
	}
	for index := 0; index < 3; index++ {
		select {
		case request := <-windows:
			if request.from != from || request.to != last+86400 || request.limit != 85 {
				t.Fatalf("overview did not request one largest bounded range: %+v", request)
			}
		default:
			t.Fatal("source snapshot missing")
		}
	}
	if len(windows) != 0 || len(reads) != 3 || streams.Load() != 0 || service.Diagnostics().ActiveLeases != 0 {
		t.Fatal("overview multiplied source reads or opened streams")
	}
	saved, err := app.GetComparisonView()
	if err != nil || saved.Weeks != 26 {
		t.Fatal("overview overwrote saved single-period choice", saved, err)
	}
}
