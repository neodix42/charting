package comparison

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/market"
)

type fakeSource struct {
	mu           sync.Mutex
	items        map[string][]domain.Instrument
	errors       map[string]error
	catalogCalls map[string]int
	historyCalls []domain.FeedKey
	history      func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error)
}

func (f *fakeSource) Instruments(ctx context.Context, provider string) ([]domain.Instrument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.catalogCalls[provider]++
	return f.items[provider], f.errors[provider]
}
func (f *fakeSource) History(ctx context.Context, id string, key domain.FeedKey, from, to int64, limit int) (market.History, error) {
	f.mu.Lock()
	f.historyCalls = append(f.historyCalls, key)
	read := f.history
	f.mu.Unlock()
	bars, err := read(ctx, key, from, to, limit)
	return market.History{Bars: bars}, err
}
func item(base, market, id, quote string) domain.Instrument {
	return domain.Instrument{NativeID: id, BaseAsset: base, Market: market, QuoteAsset: quote, Availability: "available", SupportedIntervals: []string{"1d"}}
}
func source() *fakeSource {
	return &fakeSource{items: map[string][]domain.Instrument{"hyperliquid": {item("BTC", "perpetual", "BTC", "USDC"), item("ETH", "perpetual", "ETH", "USDC")}, "binance": {}}, errors: map[string]error{}, catalogCalls: map[string]int{}, history: func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		return candles(from, to, 100), nil
	}}
}
func candles(from, to int64, base float64) []domain.Candle {
	var result []domain.Candle
	for ts := from; ts < to; ts += day {
		price := base + float64((ts-from)/day)*10
		result = append(result, domain.Candle{Time: ts, Open: price, High: price, Low: price, Close: price, Closed: true, Quality: "authoritative"})
	}
	return result
}
func fixture(t *testing.T, s Source) *Manager {
	t.Helper()
	m := NewWithOptions(Options{Profile: t.TempDir(), Source: s, Now: func() time.Time { return time.Date(2026, 11, 25, 5, 50, 0, 0, time.FixedZone("Dubai", 4*3600)) }})
	t.Cleanup(m.Close)
	return m
}

func TestCommonClosedUTCBaselineAndCumulativeChanges(t *testing.T) {
	f := source()
	m := fixture(t, f)
	result, err := m.DailyChanges(context.Background(), "math", View{Weeks: 4, Assets: []string{"BTC", "ETH"}})
	if err != nil {
		t.Fatal(err)
	}
	last := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC).Unix()
	if result.LastClosedTime != last || result.BaselineTime != last-28*day || result.AsOf != time.Date(2026, 11, 25, 1, 50, 0, 0, time.UTC).Unix() {
		t.Fatalf("wrong UTC window: %+v", result)
	}
	for _, series := range result.Series {
		if series.Error != "" || len(series.Points) != 29 {
			t.Fatalf("invalid series %+v", series)
		}
		for i, p := range series.Points {
			if p.Time != result.BaselineTime+int64(i)*day || p.Close == nil || p.Pct == nil || *p.Close != 100+float64(i*10) || abs(*p.Pct-float64(i*10)) > 1e-10 {
				t.Fatalf("wrong cumulative point %d: %+v", i, p)
			}
		}
	}
	if result.Series[0].Points[0].Pct == result.Series[1].Points[0].Pct {
		t.Fatal("series share mutable point storage")
	}
}
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestCatalogPrefersExactHyperIdentityAndTrueBinanceAbsence(t *testing.T) {
	f := source()
	f.items["hyperliquid"] = []domain.Instrument{item("BTC", "spot", "@2", "USDC"), item("BTC", "perpetual", "xyz:BTC", "USDC"), item("BTC", "perpetual", "BTC", "USDC"), item("HYPE", "spot", "@107", "USDC"), item("HYPE", "spot", "@999", "USDT"), item("GRAM", "perpetual", "GRAM", "USDC"), item("UTON", "spot", "@7", "USDC"), item("ZEN", "perpetual", "xyz:ZEN", "USDC")}
	f.items["binance"] = []domain.Instrument{item("BTC", "spot", "BTCUSDT", "USDT"), item("TON", "spot", "TONUSDT", "USDT"), item("ZEN", "spot", "ZENUSDT", "USDT"), item("NO", "spot", "NOBTC", "BTC"), item("FUTURE", "perpetual", "FUTUREUSDT", "USDT")}
	catalog, err := fixture(t, f).Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string]Asset{}
	for _, asset := range catalog.Assets {
		if _, exists := assets[asset.Symbol]; exists {
			t.Fatal("duplicate symbol")
		}
		assets[asset.Symbol] = asset
	}
	for symbol, want := range map[string]string{"BTC": "hyperliquid/perpetual/BTC", "HYPE": "hyperliquid/spot/@107", "TON": "hyperliquid/perpetual/GRAM", "ZEN": "hyperliquid/perpetual/xyz:ZEN"} {
		asset := assets[symbol]
		if got := asset.Exchange + "/" + asset.Market + "/" + asset.NativeID; got != want || asset.FeedKey == nil || asset.FeedKey.Instrument != asset.NativeID {
			t.Fatalf("%s: %+v want %s", symbol, asset, want)
		}
	}
	if _, ok := assets["NO"]; ok {
		t.Fatal("non-USDT Binance pair was admitted")
	}
	if assets["TON"].Name != "Gram (formerly Toncoin)" || assets["TON"].DisplaySymbol != "TON (GRAM)" {
		t.Fatal("verified TON rename was not disclosed")
	}
	if _, duplicate := assets["GRAM"]; duplicate || assets["UTON"].NativeID != "@7" {
		t.Fatal("renamed TON was duplicated or wrapped UTON was aliased")
	}
}

func TestVerifiedToncoinRenameDateAndProviderPriority(t *testing.T) {
	for _, test := range []struct {
		name       string
		at         time.Time
		hyper      []domain.Instrument
		binance    []domain.Instrument
		wantNative string
		wantSource string
		wantRename bool
	}{
		{
			name:       "before effective date preserves active TON",
			at:         time.Date(2026, 7, 1, 23, 59, 59, 0, time.UTC),
			hyper:      []domain.Instrument{item("TON", "perpetual", "TON", "USDC"), item("GRAM", "perpetual", "GRAM", "USDC")},
			binance:    []domain.Instrument{item("GRAM", "spot", "GRAMUSDT", "USDT")},
			wantNative: "TON", wantSource: "hyperliquid",
		},
		{
			name:       "active native GRAM beats Binance successor",
			at:         time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
			hyper:      []domain.Instrument{item("GRAM", "perpetual", "GRAM", "USDC")},
			binance:    []domain.Instrument{item("GRAM", "spot", "GRAMUSDT", "USDT")},
			wantNative: "GRAM", wantSource: "hyperliquid", wantRename: true,
		},
		{
			name:       "Binance successor used only when no eligible Hyper successor",
			at:         time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC),
			hyper:      []domain.Instrument{item("BTC", "perpetual", "BTC", "USDC"), item("GRAM", "perpetual", "GRAM", "ETH")},
			binance:    []domain.Instrument{item("GRAM", "spot", "GRAMUSDT", "USDT")},
			wantNative: "GRAMUSDT", wantSource: "binance", wantRename: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := source()
			f.items["hyperliquid"], f.items["binance"] = test.hyper, test.binance
			m := NewWithOptions(Options{Profile: t.TempDir(), Source: f, Now: func() time.Time { return test.at }})
			defer m.Close()
			catalog, err := m.Catalog(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var ton Asset
			for _, asset := range catalog.Assets {
				if asset.Symbol == "TON" {
					ton = asset
				}
				if test.wantRename && asset.Symbol == "GRAM" {
					t.Fatal("one renamed asset was exposed as both TON and GRAM")
				}
			}
			if ton.NativeID != test.wantNative || ton.Exchange != test.wantSource || ton.FeedKey == nil || ton.FeedKey.Instrument != test.wantNative {
				t.Fatalf("wrong actual source for canonical TON: %+v", ton)
			}
			if test.wantRename && (ton.DisplaySymbol != "TON (GRAM)" || ton.Name != "Gram (formerly Toncoin)") || !test.wantRename && (ton.DisplaySymbol != "" || ton.Name != "Toncoin") {
				t.Fatalf("rename disclosure or date boundary wrong: %+v", ton)
			}
		})
	}
}

func TestToncoinNeverResolvesWrappedOrUnverifiedSimilarTokens(t *testing.T) {
	f := source()
	f.items["hyperliquid"] = []domain.Instrument{item("UTON", "spot", "@7", "USDC"), item("TONCOIN", "perpetual", "TONCOIN", "USDC"), item("GRAM", "spot", "@999", "USDC"), item("GRAM", "perpetual", "xyz:GRAM", "USDC")}
	f.items["binance"] = []domain.Instrument{item("UTON", "spot", "UTONUSDT", "USDT"), item("GRAM", "spot", "GRAMBTC", "BTC")}
	result, err := fixture(t, f).DailyChanges(context.Background(), "unverified-ton", View{Weeks: 1, Assets: []string{"TON"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Series[0].Asset.FeedKey != nil || result.Series[0].Error == "" || len(f.historyCalls) != 0 {
		t.Fatalf("unverified token guessed as TON: %+v", result.Series[0])
	}
}

func TestRenamedToncoinUsesActualGRAMHistoryAndNeverStitchesMissingBaseline(t *testing.T) {
	f := source()
	f.items["hyperliquid"] = []domain.Instrument{item("GRAM", "perpetual", "GRAM", "USDC")}
	f.items["binance"] = []domain.Instrument{item("TON", "spot", "TONUSDT", "USDT"), item("GRAM", "spot", "GRAMUSDT", "USDT")}
	m := fixture(t, f)
	view := View{Weeks: 4, Assets: []string{"TON"}}
	result, err := m.DailyChanges(context.Background(), "renamed-ton", view)
	if err != nil {
		t.Fatal(err)
	}
	series := result.Series[0]
	if series.Error != "" || series.Asset.Symbol != "TON" || series.Asset.DisplaySymbol != "TON (GRAM)" || len(series.Points) != 29 || *series.Points[0].Pct != 0 || abs(*series.Points[28].Pct-280) > 1e-10 {
		t.Fatalf("wrong renamed comparison or common-baseline math: %+v", series)
	}
	if len(f.historyCalls) != 1 || f.historyCalls[0].Instrument != "GRAM" || f.historyCalls[0].Exchange != "hyperliquid" {
		t.Fatalf("canonical TON requested retired or fallback history: %+v", f.historyCalls)
	}
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		return candles(from, to, 100)[1:], nil
	}
	// A longer requested period must not stitch old TON, use Binance, or rebase
	// the first GRAM candle to hide a missing common UTC baseline.
	view.Weeks = 104
	result, err = m.DailyChanges(context.Background(), "renamed-ton-old-baseline", view)
	if err != nil || !strings.Contains(result.Series[0].Error, "common UTC baseline") || len(result.Series[0].Points) != 0 || len(f.historyCalls) != 2 || f.historyCalls[1].Instrument != "GRAM" {
		t.Fatalf("missing successor baseline was substituted: %+v %v", result, err)
	}
}

func TestUSDQuotedMarketsExcludeCrossPricesAndDiscloseEligibleFallback(t *testing.T) {
	f := source()
	f.items["hyperliquid"] = []domain.Instrument{
		item("BTC", "perpetual", "BTC", "USDC"), item("ALT", "spot", "@1", "BTC"), item("ALT", "spot", "@100", "USDC"),
		item("CROSS", "spot", "@2", "BTC"), item("NONUSD", "perpetual", "NONUSD", "ETH"), item("UNLISTEDQUOTE", "spot", "@3", "USDE"),
		item("RANK", "spot", "@4", "USD"), item("RANK", "spot", "@5", "USDH"), item("RANK", "spot", "@6", "USDT"), item("RANK", "spot", "@7", "USDC"),
	}
	f.items["binance"] = []domain.Instrument{item("CROSS", "spot", "CROSSUSDT", "USDT"), item("NONUSD", "spot", "NONUSDUSDT", "USDT"), item("UNLISTEDQUOTE", "spot", "UNLISTEDQUOTEUSDT", "USDT")}
	m := fixture(t, f)
	catalog, err := m.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string]Asset{}
	for _, a := range catalog.Assets {
		assets[a.Symbol] = a
	}
	if assets["ALT"].NativeID != "@100" || assets["ALT"].QuoteAsset != "USDC" || assets["RANK"].NativeID != "@7" {
		t.Fatalf("cross quote or nonpreferred USD quote selected: %+v", assets)
	}
	for _, symbol := range []string{"CROSS", "NONUSD", "UNLISTEDQUOTE"} {
		if assets[symbol].Exchange != "binance" {
			t.Fatalf("%s did not use eligible Binance market: %+v", symbol, assets[symbol])
		}
	}
	result, err := m.DailyChanges(context.Background(), "usd", View{Weeks: 1, Assets: []string{"ALT", "CROSS"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "CROSS: using Binance spot CROSSUSDT (USDT)" || result.Series[1].Asset.Exchange != "binance" || result.Series[1].Asset.QuoteAsset != "USDT" {
		t.Fatalf("fallback basis missing: %+v", result.Warnings)
	}
	for _, key := range f.historyCalls {
		if key.Instrument == "@1" || key.Instrument == "@2" {
			t.Fatal("cross-price history entered comparison")
		}
	}
}

func TestHyperFailureNeverAuthorizesBinanceFallback(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			f := source()
			if partial {
				f.items["hyperliquid"][0].ProviderMetadata = map[string]string{"catalogWarning": "Spot discovery failed"}
			} else {
				f.errors["hyperliquid"] = errors.New("provider offline")
			}
			f.items["binance"] = []domain.Instrument{item("BTC", "spot", "BTCUSDT", "USDT")}
			_, err := fixture(t, f).DailyChanges(context.Background(), "outage", View{Weeks: 1, Assets: []string{"BTC"}})
			if err == nil || f.catalogCalls["binance"] != 0 || len(f.historyCalls) != 0 {
				t.Fatalf("outage fell back: %v %+v", err, f)
			}
		})
	}
	f := source()
	f.items["binance"] = []domain.Instrument{item("BTC", "spot", "BTCUSDT", "USDT")}
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		return nil, errors.New("Hyperliquid history refused")
	}
	result, err := fixture(t, f).DailyChanges(context.Background(), "history-error", View{Weeks: 1, Assets: []string{"BTC"}})
	if err != nil || len(result.Series) != 1 || !strings.Contains(result.Series[0].Error, "refused") || len(f.historyCalls) != 1 || f.historyCalls[0].Exchange != "hyperliquid" {
		t.Fatalf("history outage masked: %+v %v", result, err)
	}
}

func TestGapsRemainNullAndMissingBaselineNeverRebases(t *testing.T) {
	f := source()
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		bars := candles(from, to, 100)
		if key.Instrument == "BTC" {
			bars[2].Closed = false
			bars[3].Quality = "provisional"
			return append(bars[:4], bars[5:]...), nil
		}
		return bars[1:], nil
	}
	result, err := fixture(t, f).DailyChanges(context.Background(), "gaps", View{Weeks: 1, Assets: []string{"BTC", "ETH", "UNKNOWN"}})
	if err != nil {
		t.Fatal(err)
	}
	btc := result.Series[0]
	if len(btc.Points) != 8 || btc.Points[2].Close != nil || btc.Points[2].Pct != nil || btc.Points[3].Pct != nil || btc.Points[4].Pct != nil || abs(*btc.Points[5].Pct-50) > 1e-10 {
		t.Fatalf("gap was filled/rebased: %+v", btc)
	}
	if result.Series[1].Error == "" || len(result.Series[1].Points) != 0 {
		t.Fatal("missing baseline was rebased")
	}
	if got := result.Series[2]; got.Asset.Symbol != "UNKNOWN" || got.Asset.Name != "UNKNOWN" || got.Error == "" || got.Asset.FeedKey != nil {
		t.Fatalf("unknown asset was removed/guessed: %+v", got)
	}
}

func TestClosedCompletePagesReuseAndIncompletePagesRetry(t *testing.T) {
	f := source()
	m := fixture(t, f)
	view := View{Weeks: 1, Assets: []string{"BTC"}}
	for i := 0; i < 2; i++ {
		if _, err := m.DailyChanges(context.Background(), fmt.Sprint(i), view); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.historyCalls) != 1 || f.catalogCalls["hyperliquid"] != 1 {
		t.Fatalf("complete data not reused: %+v", f)
	}
	f2 := source()
	var reads atomic.Int32
	f2.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		bars := candles(from, to, 100)
		if reads.Add(1) == 1 {
			return bars[:len(bars)-1], nil
		}
		return bars, nil
	}
	m2 := fixture(t, f2)
	first, _ := m2.DailyChanges(context.Background(), "first", view)
	second, _ := m2.DailyChanges(context.Background(), "second", view)
	if first.Series[0].Points[7].Pct != nil || second.Series[0].Points[7].Pct == nil || reads.Load() != 2 {
		t.Fatal("incomplete page prevented publication retry")
	}
}

func TestExpiredCacheRefreshesAndUTCDateChangeUsesNewCommonWindow(t *testing.T) {
	f := source()
	var clock atomic.Int64
	clock.Store(time.Date(2026, 11, 25, 23, 59, 0, 0, time.UTC).Unix())
	m := NewWithOptions(Options{Profile: t.TempDir(), Source: f, Now: func() time.Time { return time.Unix(clock.Load(), 0) }})
	defer m.Close()
	view := View{Weeks: 1, Assets: []string{"BTC"}}
	first, err := m.DailyChanges(context.Background(), "first", view)
	if err != nil {
		t.Fatal(err)
	}
	clock.Add(61)
	second, err := m.DailyChanges(context.Background(), "refresh", view)
	if err != nil {
		t.Fatal(err)
	}
	if second.BaselineTime != first.BaselineTime+day || second.LastClosedTime != first.LastClosedTime+day {
		t.Fatal("midnight did not move shared window")
	}
	clock.Add(61)
	if _, err = m.DailyChanges(context.Background(), "expired", view); err != nil {
		t.Fatal(err)
	}
	if _, err = m.DailyChanges(context.Background(), "reused-after-refresh", view); err != nil {
		t.Fatal(err)
	}
	if len(f.historyCalls) != 3 {
		t.Fatalf("expired refresh was not reused: %d", len(f.historyCalls))
	}
}

func TestInvalidDailyPricesAndDuplicateCandlesCannotProducePercentages(t *testing.T) {
	for _, mode := range []string{"invalid", "duplicate", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			f := source()
			f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
				bars := candles(from, to, 100)
				switch mode {
				case "invalid":
					bars[2].Low = 10000
				case "duplicate":
					bars = append(bars, bars[0])
				case "overflow":
					bars[0].Open = 1e-300
					bars[0].High = 1e-300
					bars[0].Low = 1e-300
					bars[0].Close = 1e-300
					bars[1].Open = 1e300
					bars[1].High = 1e300
					bars[1].Low = 1e300
					bars[1].Close = 1e300
				}
				return bars, nil
			}
			result, err := fixture(t, f).DailyChanges(context.Background(), "bad", View{Weeks: 1, Assets: []string{"BTC"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Series[0].Error == "" || len(result.Series[0].Points) != 0 {
				t.Fatalf("bad data produced points: %+v", result.Series[0])
			}
		})
	}
}

func TestEmptySelectionsNeverDiscoverOrFetchAndBoundsAreExplicit(t *testing.T) {
	f := source()
	m := fixture(t, f)
	result, err := m.DailyChanges(context.Background(), "empty", View{Weeks: 4, Assets: []string{}})
	if err != nil || len(result.Series) != 0 || len(f.catalogCalls) != 0 || len(f.historyCalls) != 0 {
		t.Fatalf("empty view fetched data: %+v %v", result, err)
	}
	for _, view := range []View{{Weeks: 0}, {Weeks: 105}, {Weeks: 1, Assets: []string{"btc", "BTC"}}, {Weeks: 1, Assets: []string{"GRAM:TON"}}, {Weeks: 1, Assets: make([]string, 21)}} {
		if _, err = m.DailyChanges(context.Background(), "invalid", view); err == nil {
			t.Fatalf("accepted invalid view %+v", view)
		}
	}
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		if limit != 729 || to-from != 729*day {
			t.Errorf("unbounded range %d %d", limit, to-from)
		}
		return candles(from, to, 100), nil
	}
	if _, err = m.DailyChanges(context.Background(), "max", View{Weeks: 104, Assets: []string{"BTC"}}); err != nil {
		t.Fatal(err)
	}
}

func TestCancellationAndNewSelectionPreventLatePublication(t *testing.T) {
	f := source()
	entered, release := make(chan struct{}), make(chan struct{})
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		close(entered)
		<-release
		return candles(from, to, 100), nil
	}
	m := fixture(t, f)
	finished := make(chan error, 1)
	go func() {
		_, err := m.DailyChanges(context.Background(), "old", View{Weeks: 1, Assets: []string{"BTC"}})
		finished <- err
	}()
	<-entered
	if _, err := m.DailyChanges(context.Background(), "new-empty", View{Weeks: 2, Assets: []string{}}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("late result published: %v", err)
	}
	secondEntered := make(chan struct{})
	f.mu.Lock()
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		close(secondEntered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	f.mu.Unlock()
	go func() {
		_, err := m.DailyChanges(context.Background(), "explicit", View{Weeks: 1, Assets: []string{"BTC"}})
		finished <- err
	}()
	<-secondEntered
	m.Cancel("explicit")
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	m.Close()
	if _, err := m.DailyChanges(context.Background(), "after-close", DefaultView()); err == nil {
		t.Fatal("accepted request after close")
	}
}

func TestViewPersistenceDefaultsOnlyOnMissingAndPreservesCorruption(t *testing.T) {
	profile := t.TempDir()
	m := New(profile, nil)
	defer m.Close()
	view, err := m.View()
	if err != nil || !reflect.DeepEqual(view, DefaultView()) {
		t.Fatalf("defaults: %+v %v", view, err)
	}
	if _, err = os.Stat(m.viewPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reading defaults wrote a file")
	}
	want := View{Weeks: 8, Assets: []string{}}
	if _, err = m.SaveView(want); err != nil {
		t.Fatal(err)
	}
	other := New(profile, nil)
	defer other.Close()
	got, err := other.View()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("empty view lost: %+v %v", got, err)
	}
	if st, _ := os.Stat(m.viewPath()); st.Mode().Perm() != 0600 {
		t.Fatalf("view permission %v", st.Mode())
	}
	for _, data := range []string{"{bad", `{"version":2,"view":{"weeks":4,"assets":[]}}`, `{"version":1,"view":{"weeks":0,"assets":[]}}`, `{"version":1,"view":{"weeks":4,"assets":[]},"extra":true}`} {
		if err = os.WriteFile(m.viewPath(), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = m.View(); err == nil {
			t.Fatalf("accepted bad saved bytes %s", data)
		}
		if _, err = m.SaveView(DefaultView()); err == nil {
			t.Fatal("overwrote corrupt view")
		}
		saved, _ := os.ReadFile(m.viewPath())
		if string(saved) != data {
			t.Fatal("corrupt bytes changed")
		}
	}
	if pending, _ := filepath.Glob(filepath.Join(profile, ".comparison-save-*")); len(pending) > 0 {
		t.Fatal("temporary writes leaked")
	}
}
