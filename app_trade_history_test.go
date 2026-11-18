//go:build bindings

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/hyperliquid"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/storage"
	"cryptodesk/internal/trading"
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
	local, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	a := &App{store: local, hyperliquid: hyperliquid.NewStore(profile), jforex: jforex.New(profile)}
	defer a.jforex.Close()
	claimPath := filepath.Join(profile, "hyperliquid-orders", newest+".json")
	claimBefore, _ := os.ReadFile(claimPath)
	if _, err = a.SetTradeEntryCondition("hyperliquid:"+newest, "Breakout"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.SetTradeEntryCondition("dukascopy:"+newer, "Retest"); err != nil {
		t.Fatal(err)
	}
	const originalImage = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aGU8AAAAASUVORK5CYII="
	var firstScreen trading.TradeScreen
	for _, tradeID := range []string{"hyperliquid:" + newest, "hyperliquid:" + newest, "dukascopy:" + newer} {
		screen, err := a.AddTradeScreen(tradeID, "screen.png", originalImage)
		if err != nil || screen.TradeID != tradeID || screen.Width != 1 || screen.Height != 1 {
			t.Fatal("offline attachment failed", screen, err)
		}
		if firstScreen.ID == "" {
			firstScreen = screen
		}
	}
	if _, err := a.AddTradeScreen("hyperliquid:"+strings.Repeat("f", 32), "untracked.png", originalImage); err == nil {
		t.Fatal("an image created an untracked platform trade")
	}
	if image, err := a.GetTradeScreen(firstScreen.TradeID, firstScreen.ID); err != nil || image != originalImage {
		t.Fatal("original image was changed by the facade", err)
	}
	if _, err := a.GetTradeScreen("dukascopy:"+newer, firstScreen.ID); err == nil {
		t.Fatal("the facade exposed an image from another trade")
	}
	if screens, err := a.QueryTradeScreens(trading.TradeScreenQuery{Provider: "hyperliquid", EntryCondition: "Breakout"}); err != nil || screens.Total != 2 {
		t.Fatal("trade images cannot be queried by local metadata", screens, err)
	}
	page, err := a.GetTradeHistory(0, 2)
	if err != nil || page.Total != 3 || len(page.Rows) != 2 || len(page.Errors) != 0 {
		t.Fatal(page, err)
	}
	if page.Rows[0].ID != "hyperliquid:"+newest || page.Rows[1].ID != "dukascopy:"+newer {
		t.Fatal("history is not sorted across providers", page.Rows)
	}
	if page.Rows[0].EntryCondition != "Breakout" || page.Rows[1].EntryCondition != "Retest" {
		t.Fatal("local conditions were not merged into provider rows", page.Rows)
	}
	if len(page.Rows[0].Screens) != 2 || len(page.Rows[1].Screens) != 1 {
		t.Fatal("local image metadata did not merge with both providers", page.Rows)
	}
	if after, _ := os.ReadFile(claimPath); !bytes.Equal(claimBefore, after) {
		t.Fatal("editing an entry condition changed the provider claim")
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
	if _, err := a.QueryTradeScreens(trading.TradeScreenQuery{}); err == nil {
		t.Fatal("image work started after shutdown")
	}
	if _, err := a.GetTradeHistory(0, 25); err == nil {
		t.Fatal("history started after shutdown")
	}
	if _, err := a.PreviewTradeClose("hyperliquid:" + strings.Repeat("a", 32)); err == nil {
		t.Fatal("close review started after shutdown")
	}
}

func TestChartTradeCandidatesRequireMatchingSourceMarketAccountAndEnvironment(t *testing.T) {
	profile := t.TempDir()
	a := &App{hyperliquid: hyperliquid.NewStore(profile), jforex: jforex.New(profile)}
	defer a.jforex.Close()
	address := "0x" + strings.Repeat("1", 40)
	if _, err := a.hyperliquid.Configure(hyperliquid.Settings{AccountAddress: address, Network: "testnet"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.jforex.Configure(jforex.Settings{Username: "offline-chart-user", Password: "offline-chart-password", Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	write := func(dir, id string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(profile, dir, id+".json")
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i, tc := range []struct{ account, network, market, symbol string }{{address, "testnet", "perpetual", "BTC"}, {address, "mainnet", "perpetual", "BTC"}, {"0x" + strings.Repeat("2", 40), "testnet", "perpetual", "BTC"}, {address, "testnet", "spot", "@107"}, {address, "testnet", "perpetual", "xyz:BTC"}} {
		id := strings.Repeat(string(rune('a'+i)), 32)
		write("hyperliquid-orders", id, map[string]any{"preview": hyperliquid.Preview{ID: id, AccountAddress: tc.account, Network: tc.network, ValuationTime: 1000, Request: hyperliquid.OrderRequest{Instrument: tc.symbol, Market: tc.market, Side: "buy", Quantity: "1"}}, "result": hyperliquid.OrderResult{Status: "filled", FilledQuantity: "1", AveragePrice: "61000", OrderIDs: []int64{101}}})
	}
	duka := strings.Repeat("f", 32)
	write("dukascopy-trade-claims", duka, map[string]any{"id": duka, "createdAt": 1000, "environment": "demo", "status": "claimed", "request": jforex.TradeRequest{Instrument: "EURUSD", Side: "buy", Quantity: 1000}, "result": jforex.TradeResult{Status: "filled", FilledQuantity: 1000, EntryPrice: 1.1, OrderID: "D-101"}})
	keys := []domain.FeedKey{{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1h"}, {Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1d"}, {Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Timeframe: "1h"}, {Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1h"}}
	page, err := a.GetChartTrades(keys)
	if err != nil || len(page.Rows) != 2 || len(page.Errors) != 0 {
		t.Fatal(page, err)
	}
	for _, row := range page.Rows {
		if row.Instrument != "BTC" && row.Instrument != "EURUSD" {
			t.Fatal("unrelated trade leaked", row)
		}
	}
	page, err = a.GetChartTrades([]domain.FeedKey{{Exchange: "hyperliquid", Market: "spot", Instrument: "@107", Timeframe: "1h"}})
	if err != nil || len(page.Rows) != 0 {
		t.Fatal("spot executions are not open positions", page, err)
	}
	if _, err = a.GetChartTrades(make([]domain.FeedKey, 10)); err == nil {
		t.Fatal("too many chart keys accepted")
	}
	if _, err = a.GetChartTrades([]domain.FeedKey{{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "nonsense"}}); err == nil {
		t.Fatal("invalid chart identity accepted")
	}
}

func TestChartTradesRestoreNewestConfirmedOpeningIncludingClosedAndImmediateFills(t *testing.T) {
	profile := t.TempDir()
	a := &App{hyperliquid: hyperliquid.NewStore(profile), jforex: jforex.New(profile)}
	defer a.jforex.Close()
	account := "0x" + strings.Repeat("1", 40)
	if _, err := a.hyperliquid.Configure(hyperliquid.Settings{AccountAddress: account, Network: "testnet"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.jforex.Configure(jforex.Settings{Username: "local-position-user", Password: "offline-password", Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	write := func(folder, id string, value any) {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(profile, folder, id+".json")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, encoded, 0600); err != nil {
			t.Fatal(err)
		}
		before[path] = encoded
	}
	hyper := func(n int, status string, submitted, opened int64, qty, entry, address, network, symbol string) string {
		id := fmt.Sprintf("%032x", n)
		p := hyperliquid.Preview{ID: id, AccountAddress: address, Network: network, ValuationTime: submitted, Request: hyperliquid.OrderRequest{Market: "perpetual", Instrument: symbol, Side: "buy", Quantity: "100"}}
		r := trading.Trade{ID: "hyperliquid:" + id, Provider: "hyperliquid", Environment: network, Account: address, Instrument: symbol, Market: "perpetual", SubmittedAt: submitted, OpenDate: opened, Size: qty, EntryPrice: entry, Direction: "long", OrderID: fmt.Sprint(n), Status: status, StopLoss: "60000", TakeProfit: "62000"}
		if status == "closed" {
			r.CloseDate = opened + 10
			r.RemainingSize = "0"
		} else {
			r.RemainingSize = qty
		}
		write("hyperliquid-orders", id, map[string]any{"preview": p, "history": r})
		return r.ID
	}
	// A large number of old orders must not exceed an open-order refresh cap;
	// only the latest actual opening is needed for each of the at-most-nine feeds.
	for n := 1; n <= 120; n++ {
		hyper(n, "open", int64(n), int64(n), "1", "61000", account, "testnet", "BTC")
	}
	closedID := hyper(121, "closed", 1000, 2000, "58.8", "61001", account, "testnet", "BTC")
	hyper(122, "unconfirmed", 9000, 0, "", "", account, "testnet", "BTC")
	hyper(123, "open", 9000, 9000, "1", "61000", "0x"+strings.Repeat("2", 40), "testnet", "BTC")
	hyper(124, "open", 9000, 9000, "1", "61000", account, "mainnet", "BTC")
	hyper(125, "open", 9000, 9000, "1", "61000", account, "testnet", "xyz:BTC")
	hyper(126, "open", 9000, 9000, "0", "61000", account, "testnet", "BTC")
	hyper(127, "open", 9000, 9000, "1", "NaN", account, "testnet", "BTC")
	hyper(128, "rejected", 9000, 9000, "1", "61000", account, "testnet", "BTC")

	broker := func(n int, status, acct, environment string, submitted, opened int64, qty, entry string) string {
		id := fmt.Sprintf("%032x", n)
		r := trading.Trade{ID: "dukascopy:" + id, Provider: "dukascopy", Account: acct, Environment: environment, Instrument: "EURUSD", Market: "jforex", SubmittedAt: submitted, OpenDate: opened, Size: qty, EntryPrice: entry, Direction: "long", OrderID: fmt.Sprint(n), Status: status, StopLoss: "1.09", TakeProfit: "1.12"}
		if status == "closed" {
			r.CloseDate, r.RemainingSize = opened+10, "0"
		} else {
			r.RemainingSize = qty
		}
		write("dukascopy-trade-claims", id, map[string]any{"id": id, "createdAt": submitted, "account": acct, "environment": environment, "status": "claimed", "request": jforex.TradeRequest{Instrument: "EURUSD", Side: "buy", Quantity: 1000}, "history": r})
		return r.ID
	}
	broker(1, "open", "", "demo", 1000, 1100, "1000", "1.1")
	brokerID := broker(2, "closed", "", "demo", 2000, 2100, "1000", "1.11")
	broker(3, "unconfirmed", "", "demo", 9000, 0, "", "")
	broker(4, "open", "other-account", "demo", 9000, 9000, "1000", "1.1")
	broker(5, "open", "", "live", 9000, 9000, "1000", "1.1")
	keys := []domain.FeedKey{{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1h"}, {Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Timeframe: "1h"}}
	assertRows := func(hyperID string) {
		t.Helper()
		page, err := a.GetChartTrades(keys)
		if err != nil || len(page.Rows) != 2 || page.Total != 2 || page.Limit != 9 || len(page.Errors) > 0 {
			t.Fatal(page, err)
		}
		found := map[string]bool{}
		for _, row := range page.Rows {
			found[row.ID] = true
		}
		if !found[hyperID] || !found[brokerID] {
			t.Fatal("wrong latest opening restored", found)
		}
	}
	assertRows(closedID)
	// A confirmed submission has actual fill data before the first exchange
	// history refresh supplies OpenDate; restore it from SubmittedAt immediately.
	immediateID := hyper(129, "open", 3000, 0, "2", "61002", account, "testnet", "BTC")
	assertRows(immediateID)
	// Equal timestamps use a stable identity tie-break independent of file order.
	tiedID := hyper(130, "open", 3000, 0, "3", "61003", account, "testnet", "BTC")
	assertRows(tiedID)
	for path, original := range before {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, after) {
			t.Fatal("restoring chart levels rewrote the original trade journal", path, err)
		}
	}
}
