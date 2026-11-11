//go:build bindings

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cryptodesk/internal/hyperliquid"
	"cryptodesk/internal/jforex"
)

func TestTradeHistoryFacadeMergesOnlyLocalClaimsAndPagesWithoutExchangeAccess(t *testing.T) {
	profile := t.TempDir()
	write := func(dir, id string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(profile, dir, id+".json")
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	address := "0x" + strings.Repeat("1", 40)
	writeHyper := func(id string, at int64, status string) {
		write("hyperliquid-orders", id, map[string]any{"preview": hyperliquid.Preview{ID: id, AccountAddress: address, Network: "mainnet", ValuationTime: at, Request: hyperliquid.OrderRequest{Market: "perpetual", Instrument: "BTC", Side: "buy", Quantity: "2"}}, "result": hyperliquid.OrderResult{Status: status, FilledQuantity: map[bool]string{true: "2", false: ""}[status == "filled"], AveragePrice: "100", OrderIDs: []int64{101}}})
	}
	older := strings.Repeat("a", 32)
	newer := strings.Repeat("b", 32)
	newest := strings.Repeat("c", 32)
	writeHyper(older, 1000, "filled")
	writeHyper(newest, 3000, "filled")
	writeHyper(strings.Repeat("d", 32), 4000, "rejected")
	write("dukascopy-trade-claims", newer, map[string]any{"id": newer, "createdAt": 2000, "environment": "demo", "status": "claimed", "request": jforex.TradeRequest{Instrument: "EUR/USD", Side: "sell", Quantity: 1000}})
	// Arbitrary downloaded/external history must never become platform-owned trades.
	write("hyperliquid-orders", "external-order", map[string]any{"orderId": 999, "status": "filled"})
	a := &App{hyperliquid: hyperliquid.NewStore(profile), jforex: jforex.New(profile)}
	defer a.jforex.Close()
	page, err := a.GetTradeHistory(0, 2)
	if err != nil || page.Total != 3 || len(page.Rows) != 2 || len(page.Errors) != 0 {
		t.Fatal(page, err)
	}
	if page.Rows[0].ID != "hyperliquid:"+newest || page.Rows[1].ID != "dukascopy:"+newer {
		t.Fatal("history is not sorted across providers", page.Rows)
	}
	if page.Rows[0].CanClose || page.Rows[0].BalanceAtOpen != "" {
		t.Fatal("legacy balance or closeability invented", page.Rows[0])
	}
	page, err = a.GetTradeHistory(2, 2)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].ID != "hyperliquid:"+older {
		t.Fatal(page, err)
	}
	page, err = a.GetTradeHistory(99, 2)
	if err != nil || len(page.Rows) != 0 || page.Rows == nil {
		t.Fatal(page, err)
	}
	for _, bounds := range [][2]int{{-1, 25}, {0, 0}, {0, 101}, {20001, 25}} {
		if _, err = a.GetTradeHistory(bounds[0], bounds[1]); err == nil {
			t.Fatal("unbounded page accepted", bounds)
		}
	}
	// A broken local claim is reported, with its original file preserved.
	broken := strings.Repeat("e", 32)
	write("hyperliquid-orders", broken, map[string]any{"garbled": true})
	page, err = a.GetTradeHistory(0, 25)
	if err != nil || page.Total != 3 || len(page.Errors) != 1 {
		t.Fatal(page, err)
	}
	if _, err = os.Stat(filepath.Join(profile, "hyperliquid-orders", broken+".json")); err != nil {
		t.Fatal("broken journal removed", err)
	}
}

func TestTradeHistoryFacadeRejectsUntrackedAndMalformedCloseActions(t *testing.T) {
	profile := t.TempDir()
	a := &App{hyperliquid: hyperliquid.NewStore(profile), jforex: jforex.New(profile)}
	defer a.jforex.Close()
	for _, id := range []string{"binance:" + strings.Repeat("a", 32), "hyperliquid:../../orders", "dukascopy:123", "hyperliquid:" + strings.Repeat("f", 32), "dukascopy:" + strings.Repeat("f", 32)} {
		if _, err := a.PreviewTradeClose(id); err == nil {
			t.Fatal("untracked trade accepted", id)
		}
		if _, err := a.ConfirmTradeClose(id); err == nil {
			t.Fatal("untracked confirmation accepted", id)
		}
	}
	if _, err := a.RefreshTradeHistory([]string{"binance:" + strings.Repeat("a", 32)}); err == nil {
		t.Fatal("unsupported history source accepted")
	}
	if _, err := a.RefreshTradeHistory(make([]string, 101)); err == nil {
		t.Fatal("unbounded refresh accepted")
	}
	a.closeTrading()
	if _, err := a.GetTradeHistory(0, 25); err == nil {
		t.Fatal("history started after shutdown")
	}
	if _, err := a.PreviewTradeClose("hyperliquid:" + strings.Repeat("a", 32)); err == nil {
		t.Fatal("close review started after shutdown")
	}
}
