package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/hyperliquid"
	"cryptodesk/internal/trading"
)

func TestTradingFacadeCapabilitiesAndSecretIsolation(t *testing.T) {
	a := &App{hyperliquid: hyperliquid.NewStore(t.TempDir())}
	key := domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1h"}
	before, err := a.GetTradingStatus(key)
	if err != nil || before.Enabled {
		t.Fatal(before, err)
	}
	secret := strings.Repeat("0", 63) + "1"
	info, err := a.ConfigureHyperliquid(hyperliquid.Settings{AccountAddress: "0x" + strings.Repeat("2", 40), PrivateKey: secret, Network: "testnet"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(info)
	if strings.Contains(string(encoded), secret) || !info.PrivateKeyConfigured {
		t.Fatal("credential Info exposed signing key or lost configuration")
	}
	status, err := a.GetTradingStatus(key)
	if err != nil || !status.Enabled || !status.CanBuy || !status.CanSell || !status.LeverageSupported || !status.ProtectiveOrdersSupported || status.Environment != "testnet" {
		t.Fatal(status, err)
	}
	key.Market, key.Instrument = "spot", "@268"
	status, err = a.GetTradingStatus(key)
	if err != nil || status.Enabled || status.CanBuy || status.CanSell || status.LeverageSupported || status.ProtectiveOrdersSupported || status.MaxLeverage != 1 {
		t.Fatal(status, err)
	}
	key.Exchange, key.Instrument = "binance", "BTCUSDT"
	status, err = a.GetTradingStatus(key)
	if err != nil || status.Enabled {
		t.Fatal(status, err)
	}
	if _, err = a.PreviewTrade(trading.Request{FeedKey: key, Side: "buy", Quantity: "1"}); err == nil {
		t.Fatal("unsupported exchange accepted a trade")
	}
	if _, err = a.SubmitTrade("binance:any"); err == nil {
		t.Fatal("unsupported confirmation routed to a provider")
	}
	if err = a.OpenHyperliquidHelp("https://untrusted.example"); err == nil {
		t.Fatal("arbitrary browser URL accepted")
	}
}

func TestTradingShutdownDrainsAndRejectsNewWork(t *testing.T) {
	a := &App{}
	if err := a.beginTradeWork(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.closeTrading(); close(done) }()
	deadline := time.Now().Add(time.Second)
	for {
		a.tradingMu.Lock()
		closed := a.tradingClosed
		a.tradingMu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("trading shutdown did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if err := a.beginTradeWork(); err == nil {
		t.Fatal("new work accepted during shutdown")
	}
	select {
	case <-done:
		t.Fatal("shutdown lost active order work")
	default:
	}
	a.tradingWG.Done()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not drain")
	}
	if _, err := a.SubmitTrade("hyperliquid:any"); err == nil {
		t.Fatal("submission started after shutdown")
	}
}

func TestHyperliquidExecutionResultsRetainActualOutcome(t *testing.T) {
	for input, want := range map[string]string{"filled": "submitted", "partial": "partial", "rejected": "rejected", "not-filled": "uncertain", "uncertain": "uncertain", "unknown-new-response": "uncertain"} {
		got := hyperliquidTradeResult(hyperliquid.OrderResult{Status: input, OrderIDs: []int64{9007199254740993}, Message: "provider result"})
		if got.Status != want || len(got.OrderIDs) != 1 || got.OrderIDs[0] != "9007199254740993" || got.Message != "provider result" {
			t.Fatal(got)
		}
	}
}
