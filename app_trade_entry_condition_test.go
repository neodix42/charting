//go:build bindings

package main

import (
	"cryptodesk/internal/storage"
	"cryptodesk/internal/trading"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTradeEntryConditionsStayLocalSurviveRefreshAndDoNotCreateTrades(t *testing.T) {
	profile := t.TempDir()
	store, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &App{store: store}
	id := "dukascopy:" + strings.Repeat("a", 32)
	if saved, err := a.SetTradeEntryCondition(id, "Long after range breakout"); err != nil || saved != "Long after range breakout" {
		t.Fatal(saved, err)
	}
	if page, err := a.GetTradeHistory(0, 25); err != nil || page.Total != 0 || len(page.Rows) != 0 {
		t.Fatal("annotations created a platform trade", page, err)
	}
	// Provider refreshes own the prices/status, while annotations are merged last.
	rows := []trading.Trade{{ID: id, EntryPrice: "123", EntryCondition: "stale exchange value"}}
	if err = a.annotateTradeHistory(rows); err != nil || rows[0].EntryCondition != "Long after range breakout" || rows[0].EntryPrice != "123" {
		t.Fatal(rows, err)
	}
	reopened := &App{store: store}
	rows[0].EntryCondition = ""
	if err = reopened.annotateTradeHistory(rows); err != nil || rows[0].EntryCondition != "Long after range breakout" {
		t.Fatal("annotation was not restored", rows, err)
	}
	if _, err = reopened.SetTradeEntryCondition(id, ""); err != nil {
		t.Fatal(err)
	}
	if err = reopened.annotateTradeHistory(rows); err != nil || rows[0].EntryCondition != "" {
		t.Fatal("cleared annotation restored", rows, err)
	}
	for _, path := range []string{"dukascopy-trade-claims", "hyperliquid-orders"} {
		if _, err = os.Stat(filepath.Join(profile, path)); !os.IsNotExist(err) {
			t.Fatal("annotation wrote an order journal", path, err)
		}
	}
	if _, err = a.SetTradeEntryCondition("dukascopy:../../outside", "note"); err == nil {
		t.Fatal("malformed ID accepted")
	}
	a.closeTrading()
	if _, err = a.SetTradeEntryCondition(id, "after shutdown"); err == nil {
		t.Fatal("save started after shutdown")
	}
}
