//go:build bindings

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cryptodesk/internal/hyperliquid"
	"cryptodesk/internal/trading"
)

func TestTradeScreenGalleryFacadeReadsAllLocalPagesAndRemovesOnlySelectedOriginal(t *testing.T) {
	a, profile, ids, _, original := nativeTradeScreenApp(t)
	address := "0x" + strings.Repeat("1", 40)
	var oldestID, noImageID, oldestPath string
	for index := 1; index <= 30; index++ {
		id := fmt.Sprintf("%032x", index)
		trade := trading.Trade{ID: "hyperliquid:" + id, Provider: "hyperliquid", Account: address, Environment: "mainnet", Instrument: "BTC", Market: "perpetual", Direction: "long", SubmittedAt: 10000 + int64(index), Status: "closed", CloseDate: 1700000000000, ProfitLossState: "closed", ProfitLoss: "2", ProfitLossCurrency: "USDC"}
		journal := map[string]any{"preview": hyperliquid.Preview{ID: id, AccountAddress: address, Network: "mainnet", Request: hyperliquid.OrderRequest{Market: "perpetual", Instrument: "BTC", Side: "buy", Quantity: "1"}}, "history": trade}
		data, err := json.Marshal(journal)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(profile, "hyperliquid-orders", id+".json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if index == 1 {
			oldestID, oldestPath = trade.ID, path
		}
		if index == 2 {
			noImageID = trade.ID
		}
	}
	if _, err := a.SetTradeEntryCondition(oldestID, "Outside the first history page"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetTradeEntryCondition(noImageID, "Condition without screenshots"); err != nil {
		t.Fatal(err)
	}
	screen, err := a.AddTradeScreen(oldestID, "older.png", original)
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.AddTradeScreen(ids[1], "dukas.png", original)
	if err != nil {
		t.Fatal(err)
	}
	journalBefore, err := os.ReadFile(oldestPath)
	if err != nil {
		t.Fatal(err)
	}
	history, err := a.GetTradeHistory(0, 25)
	if err != nil || len(history.Rows) != 25 || history.Total != 32 {
		t.Fatal("fixture must have a full first history page", history, err)
	}
	for _, row := range history.Rows {
		if row.ID == oldestID || row.ID == noImageID {
			t.Fatal("gallery fixture accidentally appeared on the visible first page")
		}
	}
	page, err := a.GetTradeScreenGallery(trading.TradeScreenGalleryQuery{})
	if err != nil || page.Total != 0 || len(page.Items) != 0 || !slices.Contains(page.Conditions, "Outside the first history page") || !slices.Contains(page.Conditions, "Condition without screenshots") {
		t.Fatal("empty gallery must retain all local condition facets", page, err)
	}
	page, err = a.GetTradeScreenGallery(trading.TradeScreenGalleryQuery{Conditions: []string{"Outside the first history page"}, Directions: []string{"long"}, Providers: []string{"hyperliquid"}, Outcomes: []string{"won"}})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Screen.ID != screen.ID || page.Items[0].Outcome != "won" || len(page.Errors) != 0 {
		t.Fatal("gallery omitted the older matching journal/image", page, err)
	}
	if after, _ := os.ReadFile(oldestPath); !bytes.Equal(journalBefore, after) {
		t.Fatal("gallery read modified exchange-owned journal")
	}
	if _, err := a.RemoveTradeScreen(ids[1], screen.ID); err == nil {
		t.Fatal("facade removal crossed trade boundary")
	}
	if _, err := a.GetTradeScreen(oldestID, screen.ID); err != nil {
		t.Fatal("failed cross-trade removal changed the original", err)
	}
	if removed, err := a.RemoveTradeScreen(oldestID, screen.ID); err != nil || !removed.Removed || removed.Warning != "" {
		t.Fatal(err)
	}
	if _, err := a.GetTradeScreen(oldestID, screen.ID); err == nil {
		t.Fatal("facade still exposes removed image")
	}
	page, err = a.GetTradeScreenGallery(trading.TradeScreenGalleryQuery{Providers: []string{"dukascopy", "hyperliquid"}})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Screen.ID != other.ID {
		t.Fatal("removal changed another trade's gallery images", page, err)
	}
	if after, _ := os.ReadFile(oldestPath); !bytes.Equal(journalBefore, after) {
		t.Fatal("image removal modified exchange-owned journal")
	}
	notes, err := a.entryConditionStore().Snapshot()
	if err != nil || notes[oldestID] != "Outside the first history page" {
		t.Fatal("image removal erased the entry condition", notes, err)
	}
}
