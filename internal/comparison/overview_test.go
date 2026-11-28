package comparison

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func TestOverviewReadsEachSourceOnceAndRebasesFourExactWindows(t *testing.T) {
	f := source()
	f.items["binance"] = []domain.Instrument{item("DCR", "spot", "DCRUSDT", "USDT")}
	type read struct {
		key      domain.FeedKey
		from, to int64
		limit    int
	}
	var mu sync.Mutex
	var reads []read
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		mu.Lock()
		reads = append(reads, read{key, from, to, limit})
		mu.Unlock()
		return candles(from, to, 100), nil
	}
	m := fixture(t, f)
	id := MarketID("binance", "spot", "DCRUSDT")
	view := View{Weeks: 26, Assets: []string{"BTC", id}}
	overview, err := m.Overview(context.Background(), "one-shared-snapshot", view)
	if err != nil || overview.RequestID != "one-shared-snapshot" || len(overview.Panels) != 4 {
		t.Fatal(overview, err)
	}
	first := overview.Panels[0]
	maxFrom := first.LastClosedTime - 84*day
	if len(reads) != 2 || len(f.historyCalls) != 2 {
		t.Fatalf("overview multiplied provider reads: %+v", reads)
	}
	for _, r := range reads {
		if r.from != maxFrom || r.to != first.LastClosedTime+day || r.limit != 85 {
			t.Fatalf("wrong largest shared UTC request: %+v", r)
		}
	}
	for index, weeks := range []int{2, 4, 8, 12} {
		panel := overview.Panels[index]
		if panel.RequestID != overview.RequestID || panel.Weeks != weeks || panel.AsOf != first.AsOf || panel.LastClosedTime != first.LastClosedTime || panel.BaselineTime != first.LastClosedTime-int64(weeks*7)*day || len(panel.Series) != 2 {
			t.Fatalf("window cutoff drifted: %+v", panel)
		}
		for assetIndex, s := range panel.Series {
			if s.Error != "" || len(s.Points) != weeks*7+1 || s.Points[0].Pct == nil || *s.Points[0].Pct != 0 || s.Points[0].Time != panel.BaselineTime || s.Asset.ID != view.Assets[assetIndex] {
				t.Fatalf("wrong independent series: %+v", s)
			}
			base := 100 + float64((panel.BaselineTime-maxFrom)/day)*10
			for pointIndex, p := range s.Points {
				price := base + float64(pointIndex)*10
				if p.Close == nil || p.Pct == nil || *p.Close != price || abs(*p.Pct-(price/base-1)*100) > 1e-10 {
					t.Fatalf("copied percentages or wrong native closes for%dweeks: %+v", weeks, p)
				}
			}
		}
	}
	for _, provider := range publicProviders {
		if f.catalogCalls[provider] != 1 {
			t.Fatalf("catalog discovered more than once per group: %s %d", provider, f.catalogCalls[provider])
		}
	}
	if _, err = m.Overview(context.Background(), "same-cutoff", view); err != nil || len(f.historyCalls) != 2 {
		t.Fatalf("shared complete range was not reused: %v reads%d", err, len(f.historyCalls))
	}
	// Panels cannot mutate one another's quote values or source identities.
	*overview.Panels[0].Series[0].Points[0].Close = 99999
	overview.Panels[0].Series[0].Asset.FeedKey.Instrument = "MUTATED"
	if *overview.Panels[1].Series[0].Points[0].Close == 99999 || overview.Panels[1].Series[0].Asset.FeedKey.Instrument == "MUTATED" {
		t.Fatal("panels share mutable point or source storage")
	}
}

func TestOverviewMissingOlderBaselineDoesNotSuppressShorterNativeHistory(t *testing.T) {
	f := source()
	last := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC).Unix()
	listed := last - 35*day
	gap := last - 10*day
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		bars := candles(listed, to, 100)
		out := bars[:0]
		for _, bar := range bars {
			if bar.Time != gap {
				out = append(out, bar)
			}
		}
		return out, nil
	}
	result, err := fixture(t, f).Overview(context.Background(), "listed-recently", View{Weeks: 104, Assets: []string{"BTC"}})
	if err != nil || len(result.Panels) != 4 || len(f.historyCalls) != 1 {
		t.Fatal(result, err)
	}
	for index, panel := range result.Panels {
		s := panel.Series[0]
		if index < 2 {
			if s.Error != "" || len(s.Points) != panel.Weeks*7+1 || s.Points[0].Pct == nil || *s.Points[0].Pct != 0 {
				t.Fatalf("valid shorter native window poisoned by older baseline: %+v", panel)
			}
			gaps := 0
			for _, point := range s.Points {
				if point.Time == gap {
					gaps++
					if point.Close != nil || point.Pct != nil {
						t.Fatal("missing native day fabricated")
					}
				}
			}
			if gaps != 1 {
				t.Fatal("native gap/calendar removed")
			}
		} else if s.Error == "" || len(s.Points) != 0 {
			t.Fatalf("unavailable longer baseline was rebased: %+v", panel)
		}
	}
}

func TestOverviewPropagatesSourceAndValidationErrorsWithoutPartialPrices(t *testing.T) {
	for _, mode := range []string{"provider", "invalid", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			f := source()
			f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
				if mode == "provider" {
					return nil, errors.New("provider unavailable")
				}
				bars := candles(from, to, 100)
				if mode == "invalid" {
					bars[0].Close = math.NaN()
				} else {
					bars = append(bars, bars[0])
				}
				return bars, nil
			}
			result, err := fixture(t, f).Overview(context.Background(), "broken-source", View{Weeks: 4, Assets: []string{"BTC"}})
			if err != nil || len(f.historyCalls) != 1 {
				t.Fatal(result, err)
			}
			for _, panel := range result.Panels {
				if panel.Series[0].Error == "" || len(panel.Series[0].Points) != 0 {
					t.Fatalf("bad source produced prices: %+v", panel)
				}
			}
		})
	}
}

func TestOverviewCancellationAndReplacementOwnTheWholeSnapshot(t *testing.T) {
	for _, mode := range []string{"caller", "explicit", "single replacement", "close"} {
		t.Run(mode, func(t *testing.T) {
			f := source()
			entered := make(chan struct{}, 1)
			f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
				entered <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			}
			m := fixture(t, f)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := m.Overview(ctx, "pending-overview", View{Weeks: 4, Assets: []string{"BTC"}})
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("overview did not start history")
			}
			switch mode {
			case "caller":
				cancel()
			case "explicit":
				m.Cancel("pending-overview")
			case "single replacement":
				if _, err := m.DailyChanges(context.Background(), "new-single", View{Weeks: 2, Assets: []string{}}); err != nil {
					t.Fatal(err)
				}
			case "close":
				m.Close()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("group ignored actual cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancelled overview retained source work")
			}
			if len(f.historyCalls) != 1 {
				t.Fatal("cancelled group started more source requests")
			}
		})
	}
}

func TestOverviewEmptyAndDuplicateSourceSelectionsPreserveExistingSemantics(t *testing.T) {
	f := source()
	m := fixture(t, f)
	empty, err := m.Overview(context.Background(), "empty", View{Weeks: 4, Assets: []string{}})
	if err != nil || len(empty.Panels) != 4 || len(f.catalogCalls) != 0 || len(f.historyCalls) != 0 {
		t.Fatal("empty overview touched providers", empty, err)
	}
	for _, panel := range empty.Panels {
		if len(panel.Series) != 0 {
			t.Fatal("empty selection acquired series")
		}
	}
	exact := MarketID("hyperliquid", "perpetual", "BTC")
	result, err := m.Overview(context.Background(), "duplicate-feed", View{Weeks: 4, Assets: []string{"BTC", exact, "UNKNOWN"}})
	if err != nil || len(f.historyCalls) != 1 {
		t.Fatal(result, err)
	}
	for _, panel := range result.Panels {
		if panel.Series[0].Error != "" || panel.Series[1].Asset.ID != exact || panel.Series[1].Error != "This source and market is already selected; remove the duplicate" || panel.Series[2].Asset.ID != "UNKNOWN" || panel.Series[2].Error == "" {
			t.Fatalf("source identity/error changed between panels: %+v", panel)
		}
	}
}
