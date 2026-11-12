package trading

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestTradeScreenGalleryCombinesFiltersAcrossAllLocalTrades(t *testing.T) {
	store := NewEntryConditions(t.TempDir())
	_, url := screenFixture(t)
	type fixture struct {
		provider, direction, condition, status, state, pnl string
		images                                             int
	}
	fixtures := []fixture{
		{"dukascopy", "long", "Condition X", "closed", "closed", "10", 2},
		{"dukascopy", "short", "Condition X", "closed", "closed", "-2", 1},
		{"hyperliquid", "buy", "Condition X", "closed", "closed", "-1", 1},
		{"hyperliquid", "long", "Condition Y", "closed", "closed", "3", 1},
		{"hyperliquid", "sell", "Condition Z", "open", "open", "100", 1},
		{"hyperliquid", "long", "Condition Y", "closed", "closed", "0", 1},
		{"dukascopy", "long", "Condition X", "closed", "closed", "NaN", 1},
		{"hyperliquid", "long", "Tiny profit", "closed", "closed", "0.000000000000000000000001", 1},
		{"dukascopy", "short", "Without images", "closed", "closed", "1", 0},
	}
	trades := []Trade{}
	for index, item := range fixtures {
		trade := Trade{ID: fmt.Sprintf("%s:%032x", item.provider, index+1), Provider: item.provider, Direction: item.direction, Status: item.status, CloseDate: 1700000000000, ProfitLossState: item.state, ProfitLoss: item.pnl, ProfitLossCurrency: "USD"}
		trades = append(trades, trade)
		if _, err := store.Set(trade.ID, item.condition); err != nil {
			t.Fatal(err)
		}
		for n := 0; n < item.images; n++ {
			if _, err := store.AddScreen(trade, fmt.Sprintf("%d-%d.png", index, n), url); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Annotations whose platform journal is missing must not become external
	// trades or condition facets merely because their attachment files exist.
	orphan := Trade{ID: "hyperliquid:" + strings.Repeat("f", 32), Provider: "hyperliquid", Direction: "long"}
	if _, err := store.Set(orphan.ID, "Orphan condition"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddScreen(orphan, "orphan.png", url); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		query TradeScreenGalleryQuery
		want  int
	}{
		{"clear", TradeScreenGalleryQuery{}, 0},
		{"all providers", TradeScreenGalleryQuery{Providers: []string{"dukascopy", "hyperliquid"}}, 9},
		{"long condition", TradeScreenGalleryQuery{Conditions: []string{"Condition X"}, Directions: []string{"long"}}, 4},
		{"long condition won", TradeScreenGalleryQuery{Conditions: []string{"Condition X"}, Directions: []string{"long"}, Outcomes: []string{"won"}}, 2},
		{"short lost", TradeScreenGalleryQuery{Directions: []string{"short"}, Outcomes: []string{"lost"}}, 1},
		{"hyperliquid lost", TradeScreenGalleryQuery{Providers: []string{"hyperliquid"}, Outcomes: []string{"lost"}}, 1},
		{"won", TradeScreenGalleryQuery{Outcomes: []string{"won"}}, 4},
		{"lost", TradeScreenGalleryQuery{Outcomes: []string{"lost"}}, 2},
		{"both outcomes", TradeScreenGalleryQuery{Outcomes: []string{"won", "lost"}}, 6},
		{"same group OR", TradeScreenGalleryQuery{Conditions: []string{"Condition X", "Condition Y"}, Directions: []string{"short"}}, 1},
		{"exact condition case", TradeScreenGalleryQuery{Conditions: []string{"condition x"}}, 0},
		{"open short included without result", TradeScreenGalleryQuery{Directions: []string{"short"}}, 2},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			page, err := store.ScreenGallery(trades, test.query)
			if err != nil || page.Total != test.want || len(page.Items) != test.want {
				t.Fatalf("filter combination: got %d/%d want %d, %v", page.Total, len(page.Items), test.want, err)
			}
			wantFacets := []string{"Condition X", "Condition Y", "Condition Z", "Tiny profit", "Without images"}
			if !reflect.DeepEqual(page.Conditions, wantFacets) {
				t.Fatal("facets omitted another page/no-image trade or adopted an orphan", page.Conditions)
			}
			for _, item := range page.Items {
				if item.Screen.Direction != "long" && item.Screen.Direction != "short" || item.EntryCondition == "" || item.Screen.TradeID == orphan.ID {
					t.Fatal("gallery lost trade association or canonical direction", item)
				}
			}
		})
	}
	// Pagination is applied after the combined filters. Duplicate input trades
	// (e.g. a caller merging pages) cannot duplicate attachment results.
	trades = append(trades, trades[0])
	all, err := store.ScreenGallery(trades, TradeScreenGalleryQuery{Providers: []string{"dukascopy", "hyperliquid"}})
	if err != nil {
		t.Fatal(err)
	}
	var collected []string
	for offset := 0; offset < all.Total; offset += 2 {
		page, err := store.ScreenGallery(trades, TradeScreenGalleryQuery{Providers: []string{"dukascopy", "hyperliquid"}, Offset: offset, Limit: 2})
		if err != nil || page.Total != all.Total {
			t.Fatal(page, err)
		}
		for _, item := range page.Items {
			collected = append(collected, item.Screen.ID)
		}
	}
	var expected []string
	for _, item := range all.Items {
		expected = append(expected, item.Screen.ID)
	}
	if !reflect.DeepEqual(collected, expected) || all.Total != 9 {
		t.Fatal("gallery pagination duplicated, omitted or reordered results", collected, expected)
	}
	// A note edit is visible on the next query without modifying the trade or
	// rewriting attachments to retain a stale copied condition name.
	if _, err := store.Set(trades[0].ID, "Renamed condition"); err != nil {
		t.Fatal(err)
	}
	page, err := store.ScreenGallery(trades, TradeScreenGalleryQuery{Conditions: []string{"Renamed condition"}})
	if err != nil || page.Total != 2 || !sort.StringsAreSorted(page.Conditions) {
		t.Fatal("gallery did not use current entry-condition annotations", page, err)
	}
}

func TestTradeScreenGalleryRejectsInvalidQueriesAndUnconfirmedOutcomes(t *testing.T) {
	store := NewEntryConditions(t.TempDir())
	for _, query := range []TradeScreenGalleryQuery{
		{Offset: -1}, {Offset: 1000001}, {Limit: -1}, {Limit: 101},
		{Conditions: []string{""}}, {Conditions: []string{"line\nbreak"}},
		{Directions: []string{"buy"}}, {Outcomes: []string{"Won"}},
		{Providers: []string{"binance"}},
	} {
		if _, err := store.ScreenGallery(nil, query); err == nil {
			t.Fatal("invalid query accepted", query)
		}
	}
	base := Trade{Status: "closed", CloseDate: 1700000000000, ProfitLossState: "closed", ProfitLoss: "1", ProfitLossCurrency: "USDC"}
	for _, field := range []string{"status", "date", "state", "amount", "currency", "zero"} {
		trade := base
		switch field {
		case "status":
			trade.Status = "open"
		case "date":
			trade.CloseDate = 0
		case "state":
			trade.ProfitLossState = "open"
		case "amount":
			trade.ProfitLoss = "unknown"
		case "currency":
			trade.ProfitLossCurrency = ""
		case "zero":
			trade.ProfitLoss = "0"
		}
		if got := galleryOutcome(trade); got != "" {
			t.Fatal("unconfirmed, open or breakeven trade was called won/lost", field, got)
		}
	}
}
