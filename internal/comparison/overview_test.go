package comparison

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
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

func TestOverviewAtMovesAllUTCWindowsTogetherAndReusesVisitedRanges(t *testing.T) {
	f := source()
	f.items["bybit"] = []domain.Instrument{item("GRAM", "spot", "GRAMUSDT", "USDT")}
	type bounds struct {
		from, to int64
		limit    int
	}
	var calls []bounds
	epoch := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		calls = append(calls, bounds{from, to, limit})
		bars := []domain.Candle{}
		for at := from; at < to; at += day {
			price := 100 + float64((at-epoch)/day)
			bars = append(bars, domain.Candle{Time: at, Open: price, High: price, Low: price, Close: price, Closed: true, Quality: "authoritative"})
		}
		return bars, nil
	}
	now := time.Date(2026, 11, 28, 1, 30, 0, 0, time.FixedZone("Dubai", 4*3600))
	m := NewWithOptions(Options{Profile: t.TempDir(), Source: f, Now: func() time.Time { return now }})
	defer m.Close()
	view := View{Weeks: 26, Assets: []string{MarketID("bybit", "spot", "GRAMUSDT")}}
	if _, err := m.SaveView(view); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(m.viewPath())
	if err != nil {
		t.Fatal(err)
	}
	// The user's local date is November 28; the latest closed UTC day is Nov 26.
	latest := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC).Unix()
	cases := []struct {
		id        string
		end, want int64
	}{{"today", 0, latest}, {"prev", latest - day, latest - day}, {"prev-again", latest - 2*day, latest - 2*day}, {"next", latest - day, latest - day}, {"today-again", 0, latest}}
	observed := map[int64]float64{}
	for _, test := range cases {
		result, err := m.OverviewAt(context.Background(), test.id, view, test.end)
		if err != nil || len(result.Panels) != 4 {
			t.Fatal(result, err)
		}
		for index, weeks := range []int{2, 4, 8, 12} {
			panel := result.Panels[index]
			s := panel.Series[0]
			if panel.LastClosedTime != test.want || panel.BaselineTime != test.want-int64(weeks*7)*day || panel.AsOf != now.UTC().Unix() || s.Asset.ID != view.Assets[0] || s.Error != "" || len(s.Points) != weeks*7+1 || s.Points[0].Pct == nil || *s.Points[0].Pct != 0 {
				t.Fatalf("navigation changed duration/source/cutoff: %+v", panel)
			}
			for _, p := range s.Points {
				if p.Close == nil {
					t.Fatal("real closed quote missing")
				}
				if old, ok := observed[p.Time]; ok && old != *p.Close {
					t.Fatal("navigation changed a native close at the same UTC time")
				}
				observed[p.Time] = *p.Close
			}
		}
	}
	if len(calls) != 3 {
		t.Fatalf("Prev/Next/Today redownloaded visited ranges: %+v", calls)
	}
	for index, call := range calls {
		want := latest - int64(index)*day
		if call.from != want-84*day || call.to != want+day || call.limit != 85 {
			t.Fatalf("navigation escaped one bounded maximum window: %+v", call)
		}
	}
	now = now.Add(24 * time.Hour)
	pinned, err := m.OverviewAt(context.Background(), "pinned-after-midnight", view, latest-day)
	if err != nil {
		t.Fatal(err)
	}
	for _, panel := range pinned.Panels {
		if panel.LastClosedTime != latest-day || panel.AsOf != now.UTC().Unix() {
			t.Fatal("historical pin moved on UTC rollover")
		}
	}
	current, err := m.OverviewAt(context.Background(), "today-after-midnight", view, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, panel := range current.Panels {
		if panel.LastClosedTime != latest+day {
			t.Fatal("Today did not resolve the new latest closed UTC day")
		}
	}
	after, err := os.ReadFile(m.viewPath())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("date navigation rewrote private Comparison preferences", err)
	}
}

func TestOverviewAtRejectsInvalidDatesWithoutCancellingCurrentWork(t *testing.T) {
	f := source()
	entered := make(chan context.Context, 1)
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		entered <- ctx
		<-ctx.Done()
		return nil, ctx.Err()
	}
	m := fixture(t, f)
	view := View{Weeks: 4, Assets: []string{"BTC"}}
	done := make(chan error, 1)
	go func() { _, err := m.Overview(context.Background(), "healthy-pending", view); done <- err }()
	var owner context.Context
	select {
	case owner = <-entered:
	case <-time.After(time.Second):
		t.Fatal("overview did not begin")
	}
	latest := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC).Unix()
	for _, end := range []int64{-day, -1, latest + 1, latest + day, latest + day + 1, day} {
		if _, err := m.OverviewAt(context.Background(), "invalid-navigation", view, end); err == nil {
			t.Fatalf("accepted invalid UTC end date %d", end)
		}
	}
	select {
	case <-owner.Done():
		t.Fatal("invalid date cancelled the current valid snapshot")
	case <-time.After(30 * time.Millisecond):
	}
	if len(f.historyCalls) != 1 {
		t.Fatal("invalid navigation started provider work")
	}
	m.Cancel("healthy-pending")
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("actual owner cancellation stopped working")
	}
}

func TestOverviewAtPreservesHistoricalMissingBaselinesAndBoundedCache(t *testing.T) {
	f := source()
	latest := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC).Unix()
	listed := latest - 28*day
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		return candles(listed, to, 100), nil
	}
	m := fixture(t, f)
	view := View{Weeks: 4, Assets: []string{"BTC"}}
	today, err := m.OverviewAt(context.Background(), "listing-today", view, latest)
	if err != nil || today.Panels[1].Series[0].Error != "" {
		t.Fatal(today, err)
	}
	prev, err := m.OverviewAt(context.Background(), "listing-prev", view, latest-day)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Panels[0].Series[0].Error != "" || len(prev.Panels[0].Series[0].Points) != 15 || prev.Panels[1].Series[0].Error == "" || len(prev.Panels[1].Series[0].Points) != 0 {
		t.Fatal("historical date navigation rebased missing history or poisoned a valid shorter window", prev)
	}
	complete := source()
	manager := fixture(t, complete)
	for offset := int64(0); offset < 50; offset++ {
		if _, err := manager.OverviewAt(context.Background(), fmt.Sprintf("bounded-%d", offset), view, latest-offset*day); err != nil {
			t.Fatal(err)
		}
	}
	if len(manager.pages) != 40 || len(complete.historyCalls) != 50 {
		t.Fatalf("daily navigation escaped bounded range cache: pages%d reads%d", len(manager.pages), len(complete.historyCalls))
	}
}

func TestDailyChangesAtKeepsSinglePeriodAndNavigationSupersedesTheOldGroup(t *testing.T) {
	f := source()
	m := fixture(t, f)
	end := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC).Unix()
	single, err := m.DailyChangesAt(context.Background(), "single-past", View{Weeks: 1, Assets: []string{"BTC"}}, end)
	if err != nil || single.Weeks != 1 || single.LastClosedTime != end || single.BaselineTime != end-7*day || len(single.Series[0].Points) != 8 {
		t.Fatal(single, err)
	}
	entered := make(chan struct{}, 1)
	f.mu.Lock()
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	f.mu.Unlock()
	pending := make(chan error, 1)
	go func() {
		_, err := m.OverviewAt(context.Background(), "old-date", View{Weeks: 4, Assets: []string{"BTC"}}, end)
		pending <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("historical request did not start")
	}
	next, err := m.OverviewAt(context.Background(), "new-date", View{Weeks: 4, Assets: []string{}}, end+day)
	if err != nil || next.Panels[0].LastClosedTime != end+day {
		t.Fatal(next, err)
	}
	select {
	case err := <-pending:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("new date did not supersede old grouped work", err)
		}
	case <-time.After(time.Second):
		t.Fatal("obsolete date snapshot retained work")
	}
}
