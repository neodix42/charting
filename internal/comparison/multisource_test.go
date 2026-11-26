package comparison

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func TestExplicitMarketsRetainAllSourcesAndSameAssetSelections(t *testing.T) {
	f := source()
	f.items["hyperliquid"] = []domain.Instrument{item("BTC", "perpetual", "BTC", "USDC"), item("GRAM", "perpetual", "GRAM", "USDC")}
	f.items["binance"] = []domain.Instrument{item("GRAM", "spot", "GRAMUSDT", "USDT"), item("GRAM", "perpetual", "GRAMUSDT", "USDT")}
	f.items["bybit"] = []domain.Instrument{item("GRAM", "spot", "GRAMUSDT", "USDT"), item("GRAM", "perpetual", "GRAMUSDT", "USDT")}
	f.items["coinbase"] = []domain.Instrument{item("TON", "spot", "TON-USD", "USD")}
	m := fixture(t, f)
	catalog, err := m.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]Asset{}
	for _, a := range catalog.Markets {
		if a.ID == "" || seen[a.ID].ID != "" {
			t.Fatalf("missing/duplicate exact market identity: %+v", a)
		}
		seen[a.ID] = a
	}
	selections := []string{MarketID("hyperliquid", "perpetual", "GRAM"), MarketID("binance", "spot", "GRAMUSDT"), MarketID("bybit", "spot", "GRAMUSDT"), MarketID("bybit", "perpetual", "GRAMUSDT"), MarketID("coinbase", "spot", "TON-USD")}
	for _, id := range selections {
		if seen[id].Symbol != "TON" {
			t.Fatalf("canonical TON source missing: %s %+v", id, seen[id])
		}
	}
	if len(seen) != 7 || len(catalog.Assets) != 2 {
		t.Fatalf("explicit sources were deduplicated or changed automatic choices: %+v", catalog)
	}
	result, err := m.DailyChanges(context.Background(), "all-ton-sources", View{Weeks: 4, Assets: selections})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 0 || len(result.Series) != 5 || len(f.historyCalls) != 5 {
		t.Fatalf("normal source notices or missing history: %+v", result)
	}
	for i, s := range result.Series {
		if s.Asset.ID != selections[i] || s.Error != "" || len(s.Points) != 29 || s.Asset.FeedKey == nil || *s.Asset.FeedKey != *seen[selections[i]].FeedKey {
			t.Fatalf("explicit source was replaced: %+v", s)
		}
	}
	// Both catalog collections have independent feed-key copies.
	catalog.Markets[0].FeedKey.Instrument = "MUTATED"
	catalog.Assets[0].FeedKey.Instrument = "MUTATED"
	again, err := m.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range append(again.Markets, again.Assets...) {
		if a.FeedKey.Instrument == "MUTATED" {
			t.Fatal("caller mutated retained catalog")
		}
	}
}
func TestExplicitSourceWorksDuringOtherProviderFailuresWithoutAutoFallback(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "partial"}[partial], func(t *testing.T) {
			f := source()
			if partial {
				f.items["hyperliquid"][0].ProviderMetadata = map[string]string{"catalogWarning": "spot unavailable"}
			} else {
				f.errors["hyperliquid"] = errors.New("unreachable")
			}
			f.errors["bybit"] = errors.New("unreachable")
			f.items["coinbase"] = []domain.Instrument{item("TON", "spot", "TON-USD", "USD")}
			f.items["binance"] = []domain.Instrument{item("BTC", "spot", "BTCUSDT", "USDT")}
			m := fixture(t, f)
			c, err := m.Catalog(context.Background())
			if err != nil || c.AutoError == "" || len(c.Assets) != 0 || len(c.Markets) == 0 || len(c.Warnings) != 2 {
				t.Fatalf("healthy explicit discovery erased: %+v %v", c, err)
			}
			id := MarketID("coinbase", "spot", "TON-USD")
			r, err := m.DailyChanges(context.Background(), "partial", View{Weeks: 1, Assets: []string{"BTC", id}})
			if err != nil || len(r.Series) != 2 || !strings.Contains(r.Series[0].Error, "choose an explicit source") || r.Series[1].Error != "" || r.Series[1].Asset.ID != id || len(f.historyCalls) != 1 || f.historyCalls[0].Exchange != "coinbase" {
				t.Fatalf("outage caused an implicit source switch or prevented explicit history: %+v %v", r, err)
			}
		})
	}
}
func TestAllProviderFailuresRemainVisible(t *testing.T) {
	f := source()
	for _, provider := range publicProviders {
		f.errors[provider] = errors.New("offline")
	}
	if _, err := fixture(t, f).Catalog(context.Background()); err == nil || !strings.Contains(err.Error(), "No public daily markets") {
		t.Fatal("all-provider failure hidden", err)
	}
}

func TestAutomaticAndExplicitAliasesCannotRequestTheSameFeedTwice(t *testing.T) {
	f := source()
	f.items["hyperliquid"] = []domain.Instrument{item("GRAM", "perpetual", "GRAM", "USDC")}
	f.items["coinbase"] = []domain.Instrument{item("TON", "spot", "TON-USD", "USD")}
	f.items["bybit"] = []domain.Instrument{item("GRAM", "spot", "GRAMUSDT", "USDT")}
	ids := []string{"TON", MarketID("hyperliquid", "perpetual", "GRAM"), MarketID("coinbase", "spot", "TON-USD"), MarketID("bybit", "spot", "GRAMUSDT")}
	result, err := fixture(t, f).DailyChanges(context.Background(), "duplicate-native-feed", View{Weeks: 4, Assets: ids})
	if err != nil || len(result.Series) != 4 {
		t.Fatal("selection identities were collapsed", result, err)
	}
	for index, series := range result.Series {
		if series.Asset.ID != ids[index] || series.Asset.Symbol != "TON" {
			t.Fatalf("duplicate handling replaced or erased a saved selection: %+v", series)
		}
		if index == 1 {
			if series.Error != "This source and market is already selected; remove the duplicate" || len(series.Points) != 0 || series.Asset.FeedKey == nil || series.Asset.FeedKey.Instrument != "GRAM" {
				t.Fatalf("duplicate native source was plotted or silently switched: %+v", series)
			}
		} else if series.Error != "" || len(series.Points) != 29 {
			t.Fatalf("distinct venue was rejected: %+v", series)
		}
	}
	counts := map[domain.FeedKey]int{}
	for _, key := range f.historyCalls {
		counts[key]++
	}
	if len(f.historyCalls) != 3 || len(counts) != 3 {
		t.Fatalf("duplicate caused extra source work or other venues were lost: %+v", f.historyCalls)
	}
	for key, count := range counts {
		if count != 1 {
			t.Fatalf("same source was requested more than once: %+v count%d", key, count)
		}
	}
}

func TestMissingExplicitMarketKeepsReadableSelectionWithoutInventingAvailability(t *testing.T) {
	f := source()
	id := MarketID("coinbase", "spot", "TON-USD")
	r, err := fixture(t, f).DailyChanges(context.Background(), "delisted-source", View{Weeks: 26, Assets: []string{id}})
	if err != nil || len(r.Series) != 1 {
		t.Fatal(r, err)
	}
	s := r.Series[0]
	if s.Asset.ID != id || s.Asset.Symbol != "TON-USD" || s.Asset.Exchange != "coinbase" || s.Asset.Market != "spot" || s.Asset.NativeID != "TON-USD" || s.Asset.FeedKey != nil || s.Asset.QuoteAsset != "" || len(s.Points) != 0 || s.Error == "" || len(f.historyCalls) != 0 {
		t.Fatalf("missing market was fabricated, substituted or displayed as a storage key: %+v", s)
	}
}
func TestMarketSelectionsValidateCanonicalEncodingAndPreserveNativeCase(t *testing.T) {
	valid := []string{MarketID("coinbase", "spot", "TON-USD"), MarketID("hyperliquid", "perpetual", "xyz:aapl"), MarketID("hyperliquid", "spot", "@123"), MarketID("binance", "spot", "币安人生USDT")}
	for _, id := range valid {
		view, err := normalizeView(View{Weeks: 1, Assets: []string{id}})
		if err != nil || view.Assets[0] != id {
			t.Fatalf("valid exact case/encoding changed: %q %+v %v", id, view, err)
		}
	}
	invalid := []string{"evil|spot|TON-USD", "coinbase|perpetual|TON-USD", "binance|spot|https%3A%2F%2Fexample.com", "hyperliquid|spot|%40123|USDC", "hyperliquid|spot|%40123%", "hyperliquid|spot|%40123%20", "hyperliquid|spot|@123", "binance|spot|" + strings.Repeat("A", 513)}
	for _, id := range invalid {
		if _, err := normalizeView(View{Weeks: 1, Assets: []string{id}}); err == nil {
			t.Fatalf("accepted malformed market selection %q", id)
		}
	}
	if _, err := normalizeView(View{Weeks: 1, Assets: []string{valid[0], valid[0]}}); err == nil {
		t.Fatal("duplicate exact sources accepted")
	}
	view, err := normalizeView(View{Weeks: 1, Assets: []string{" btc ", valid[0]}})
	if err != nil || view.Assets[0] != "BTC" || view.Assets[1] != valid[0] {
		t.Fatal(view, err)
	}
}
func TestMarketSelectionStorageMigratesOnlyOnSaveAndPreservesNativeIdentity(t *testing.T) {
	profile := t.TempDir()
	m := New(profile, nil)
	defer m.Close()
	legacy := []byte(`{"version":1,"view":{"weeks":26,"assets":["BTC","TON"]}}`)
	if err := os.WriteFile(m.viewPath(), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	view, err := m.View()
	if err != nil || !reflect.DeepEqual(view, View{Weeks: 26, Assets: []string{"BTC", "TON"}}) {
		t.Fatal(view, err)
	}
	unchanged, _ := os.ReadFile(m.viewPath())
	if string(unchanged) != string(legacy) {
		t.Fatal("reading legacy settings rewrote user bytes")
	}
	view.Assets = append(view.Assets, MarketID("coinbase", "spot", "TON-USD"), MarketID("hyperliquid", "perpetual", "xyz:aapl"))
	if _, err = m.SaveView(view); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(m.viewPath())
	var stored savedView
	if err = json.Unmarshal(raw, &stored); err != nil || stored.Version != 2 {
		t.Fatalf("new source format not versioned: %s %v", raw, err)
	}
	restored := New(profile, nil)
	defer restored.Close()
	got, err := restored.View()
	if err != nil || !reflect.DeepEqual(got, view) {
		t.Fatal(got, err)
	}
	for _, bad := range []string{`{"version":1,"view":{"weeks":1,"assets":["coinbase|spot|TON-USD"]}}`, `{"version":3,"view":{"weeks":1,"assets":[]}}`, `{"version":2,"view":{"weeks":1,"assets":["evil|spot|TON-USD"]}}`} {
		if err = os.WriteFile(m.viewPath(), []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = m.SaveView(view); err == nil {
			t.Fatalf("overwrote unrecognized saved bytes: %s", bad)
		}
		after, _ := os.ReadFile(m.viewPath())
		if string(after) != bad {
			t.Fatal("invalid original changed")
		}
	}
}
func TestMissingBaselineIdentifiesActualRequestedRangeAndNeverChangesSource(t *testing.T) {
	f := source()
	f.items["hyperliquid"] = []domain.Instrument{item("GRAM", "perpetual", "GRAM", "USDC")}
	f.items["coinbase"] = []domain.Instrument{item("TON", "spot", "TON-USD", "USD")}
	listed := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC).Unix()
	f.history = func(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
		if key.Exchange == "hyperliquid" {
			from = listed
		}
		return candles(from, to, 100), nil
	}
	m := fixture(t, f)
	id := MarketID("hyperliquid", "perpetual", "GRAM")
	r, err := m.DailyChanges(context.Background(), "26-weeks", View{Weeks: 26, Assets: []string{id}})
	if err != nil || len(r.Series) != 1 || len(r.Series[0].Points) != 0 || !strings.Contains(r.Series[0].Error, "2026-05-26") || !strings.Contains(r.Series[0].Error, "2026-07-02") || len(f.historyCalls) != 1 || f.historyCalls[0].Exchange != "hyperliquid" {
		t.Fatalf("missing baseline rebased/stiched/switched: %+v %v", r, err)
	}
	c := MarketID("coinbase", "spot", "TON-USD")
	r, err = m.DailyChanges(context.Background(), "same-26-weeks", View{Weeks: 26, Assets: []string{c}})
	if err != nil || r.Series[0].Error != "" || len(r.Series[0].Points) != 183 || r.Series[0].Asset.ID != c || len(f.historyCalls) != 2 || f.historyCalls[1].Exchange != "coinbase" {
		t.Fatalf("explicit longer-history source unusable: %+v %v", r, err)
	}
}
func TestVerifiedBybitRenameUsesOnlyExactNativeMarketsAfterItsEffectiveDate(t *testing.T) {
	for _, test := range []struct {
		at   time.Time
		item domain.Instrument
		want bool
	}{
		{time.Date(2026, 6, 21, 23, 59, 59, 0, time.UTC), item("GRAM", "spot", "GRAMUSDT", "USDT"), false},
		{time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC), item("GRAM", "spot", "GRAMUSDT", "USDT"), true},
		{time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC), item("GRAM", "perpetual", "GRAMUSDT", "USDT"), true},
		{time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC), item("GRAM", "spot", "GRAMBTC", "BTC"), false},
		{time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC), item("UTON", "spot", "UTONUSDT", "USDT"), false},
	} {
		if got := renamedToncoin(test.item, "bybit", test.at); got != test.want {
			t.Fatalf("unverified or wrong-date alias: %+v got%v", test, got)
		}
	}
}
