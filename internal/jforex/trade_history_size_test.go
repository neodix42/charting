package jforex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/trading"
)

func TestBrokerHistoryRequestedSizeRecoversLegacyPartialFillWithoutLoginOrWrites(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("CRYPTODESK_JFOREX_PROTOCOL_TRACE", trace)
	m, _ := testManager(t, "error")
	request := sampleTrade()
	request.Quantity = 100
	j := brokerJournal{ID: strings.Repeat("a", 32), Request: request, Environment: "demo", Account: accountKey(m.config), CreatedAt: time.Now().Add(-time.Minute).UnixMilli()}
	j.History = newBrokerHistory(j)
	if j.History.RequestedSize != "100" || j.History.Size != "" {
		t.Fatal("request presented as filled", j.History)
	}
	j.Result = TradeResult{Status: "filled", FilledQuantity: 58.8, EntryPrice: 3.5541, OrderID: "partial-order"}
	applyBrokerSubmission(&j)
	j.History.RequestedSize = ""
	if err := os.MkdirAll(filepath.Dir(m.tradeJournalPath(j.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := trading.WriteJournal(m.tradeJournalPath(j.ID), j, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(m.tradeJournalPath(j.ID))
	if err != nil {
		t.Fatal(err)
	}
	rows, issues := m.TradeHistory()
	if len(issues) != 0 || len(rows) != 1 || rows[0].RequestedSize != "100" || rows[0].Size != "58.8" || rows[0].RemainingSize != "58.8" {
		t.Fatal(rows, issues)
	}
	row, err := m.LocalTrade(j.ID)
	if err != nil || row.RequestedSize != "100" || row.Size != "58.8" {
		t.Fatal(row, err)
	}
	after, err := os.ReadFile(m.tradeJournalPath(j.ID))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("local read rewrote claim", err)
	}
	operations, _ := os.ReadFile(trace)
	if len(operations) > 0 {
		t.Fatal("local read contacted broker", string(operations))
	}
}

func TestBrokerHistoryRequestedAndActualSizeSurviveSnapshotsAndLegacyPendingRead(t *testing.T) {
	m, _ := testManager(t, "error")
	request := sampleTrade()
	request.Quantity = 100
	j := brokerJournal{ID: strings.Repeat("b", 32), Request: request, Environment: "demo", Account: accountKey(m.config), CreatedAt: time.Now().Add(-time.Minute).UnixMilli()}
	j.History = newBrokerHistory(j)
	j.History.RequestedSize = ""
	j.History.Size = "100"
	if err := os.MkdirAll(filepath.Dir(m.tradeJournalPath(j.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := trading.WriteJournal(m.tradeJournalPath(j.ID), j, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := m.readTradeJournal(j.ID)
	if err != nil || loaded.History.RequestedSize != "100" || loaded.History.Size != "" {
		t.Fatal(loaded, err)
	}
	order := brokerOrder{Intent: j.ID, OrderID: "partial-order", Instrument: request.Instrument, Side: request.Side, Status: "open", OriginalQuantity: 100, FilledQuantity: 58.8, RemainingQuantity: 20, EntryPrice: 3.5541, OpenDate: j.CreatedAt}
	if err := applyBrokerSnapshot(loaded, order); err != nil {
		t.Fatal(err)
	}
	if loaded.History.RequestedSize != "100" || loaded.History.Size != "58.8" || loaded.History.RemainingSize != "20" {
		t.Fatal(loaded.History)
	}
	order.Status = "closed"
	order.CloseDate = time.Now().Add(-time.Second).UnixMilli()
	order.RemainingQuantity = 0
	if err := applyBrokerSnapshot(loaded, order); err != nil {
		t.Fatal(err)
	}
	if loaded.History.RequestedSize != "100" || loaded.History.Size != "58.8" || loaded.History.RemainingSize != "0" {
		t.Fatal(loaded.History)
	}
}
