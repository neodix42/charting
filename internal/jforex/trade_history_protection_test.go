package jforex

import (
	"bytes"
	"context"
	"cryptodesk/internal/trading"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func protectionJournal() brokerJournal {
	j := brokerJournal{ID: strings.Repeat("c", 32), Request: sampleTrade(), Environment: "demo", CreatedAt: time.Now().Add(-time.Hour).UnixMilli()}
	j.History = newBrokerHistory(j)
	j.History.Status, j.History.OrderID = "open", "owned-protection-test"
	j.History.EntryPrice, j.History.RemainingSize = "1.1003", "1000"
	j.History.Currency, j.History.ProfitPerPriceUnit = "EUR", ".9"
	return j
}

func protectionSnapshot(j brokerJournal, status string) brokerOrder {
	order := brokerOrder{Intent: j.ID, OrderID: j.History.OrderID, Instrument: j.Request.Instrument, Side: j.Request.Side, Status: status, OriginalQuantity: 1000, FilledQuantity: 1000, EntryPrice: 1.1003, OpenDate: j.CreatedAt}
	if status == "closed" {
		order.CloseDate = time.Now().Add(-time.Second).UnixMilli()
	} else {
		order.RemainingQuantity = 1000
	}
	return order
}

func TestClosedBrokerSnapshotRetainsLastObservedProtection(t *testing.T) {
	j := protectionJournal()
	j.History.StopLoss, j.History.TakeProfit = "1.095", "1.125"
	j.History.StopLossSource, j.History.TakeProfitSource = "exchange", "exchange"
	if err := applyBrokerSnapshot(&j, protectionSnapshot(j, "closed")); err != nil {
		t.Fatal(err)
	}
	if j.History.StopLoss != "1.095" || j.History.TakeProfit != "1.125" || j.History.SLAmount != "-4.77" || j.History.TPAmount != "22.23" || j.History.StopLossSource != "recorded" || j.History.TakeProfitSource != "recorded" {
		t.Fatalf("closed SDK zero levels erased the last observed protection: %+v", j.History)
	}
}

func TestLegacyClosedProtectionRecoversWithoutLoginOrJournalMutation(t *testing.T) {
	m, _ := testManager(t, "error")
	j := protectionJournal()
	j.Account = accountKey(m.config)
	j.History.Account = j.Account
	j.History.Status, j.History.CloseDate, j.History.RemainingSize = "closed", time.Now().Add(-time.Minute).UnixMilli(), "0"
	j.History.StopLoss, j.History.TakeProfit = "", ""
	path := m.tradeJournalPath(j.ID)
	if err := trading.WriteJournal(path, j, true); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	rows, problems := m.TradeHistory()
	if len(problems) != 0 || len(rows) != 1 {
		t.Fatal(rows, problems)
	}
	r := rows[0]
	if r.StopLoss != "1.09" || r.TakeProfit != "1.12" || r.SLAmount != "-9.27" || r.TPAmount != "17.73" || r.Currency != "EUR" || r.StopLossSource != "submitted" || r.TakeProfitSource != "submitted" {
		t.Fatalf("submitted levels were not recovered from the original claim: %+v", r)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("listing trade history rewrote the original journal")
	}
}

func TestOpenBrokerSnapshotRespectsRemovedProtection(t *testing.T) {
	j := protectionJournal()
	if err := applyBrokerSnapshot(&j, protectionSnapshot(j, "open")); err != nil {
		t.Fatal(err)
	}
	if j.History.StopLoss != "" || j.History.TakeProfit != "" || j.History.SLAmount != "" || j.History.TPAmount != "" {
		t.Fatalf("removed active protection was restored from the submission: %+v", j.History)
	}
}

func TestClosedBrokerProtectionKeepsIndependentExchangeAndSubmittedLevels(t *testing.T) {
	j := protectionJournal()
	j.History.StopLoss, j.History.TakeProfit = "", ""
	order := protectionSnapshot(j, "closed")
	order.StopLoss = 1.095
	if err := applyBrokerSnapshot(&j, order); err != nil {
		t.Fatal(err)
	}
	if j.History.StopLoss != "1.095" || j.History.StopLossSource != "exchange" || j.History.TakeProfit != "1.12" || j.History.TakeProfitSource != "submitted" || j.History.SLAmount != "-4.77" || j.History.TPAmount != "17.73" {
		t.Fatal(j.History)
	}
}

func TestClosedBrokerProtectionDoesNotInventLevelsOrCurrencyConversion(t *testing.T) {
	j := protectionJournal()
	j.History.StopLoss, j.History.TakeProfit, j.History.ProfitPerPriceUnit = "", "", ""
	if err := applyBrokerSnapshot(&j, protectionSnapshot(j, "closed")); err != nil {
		t.Fatal(err)
	}
	if j.History.StopLoss != "1.09" || j.History.TakeProfit != "1.12" || j.History.SLAmount != "" || j.History.TPAmount != "" {
		t.Fatal("missing FX was assumed", j.History)
	}
	j = protectionJournal()
	j.Request.StopLoss, j.Request.TakeProfit = 0, 0
	j.History.StopLoss, j.History.TakeProfit = "", ""
	if err := applyBrokerSnapshot(&j, protectionSnapshot(j, "closed")); err != nil {
		t.Fatal(err)
	}
	if j.History.StopLoss != "" || j.History.TakeProfit != "" || j.History.SLAmount != "" || j.History.TPAmount != "" {
		t.Fatal("nonexistent protection was invented", j.History)
	}
}

func TestBrokerProtectionSurvivesCloseRefreshAndRestartWithZeroTerminalLevels(t *testing.T) {
	m, _ := testManager(t, "trade-history-empty-protection")
	ctx := context.Background()
	preview, err := m.PreviewTrade(ctx, sampleTrade())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.SubmitTrade(ctx, preview.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RefreshTrade(ctx, preview.ID); err != nil {
		t.Fatal(err)
	}
	plan, err := m.PreviewClose(ctx, preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	row, err := m.ConfirmClose(ctx, plan.ID)
	if err != nil || row.Status != "closed" || row.StopLoss != "1.09" || row.TakeProfit != "1.12" || row.SLAmount != "-9.27" || row.TPAmount != "17.73" {
		t.Fatal(row, err)
	}
	row, err = m.RefreshTrade(ctx, preview.ID)
	if err != nil || row.StopLoss != "1.09" || row.TakeProfit != "1.12" || row.SLAmount != "-9.27" || row.TPAmount != "17.73" || row.StopLossSource != "recorded" {
		t.Fatal(row, err)
	}
	profile := filepath.Dir(m.path)
	m.Close()
	restored, _ := restoredCatalogManager(t, profile, "error")
	rows, issues := restored.TradeHistory()
	if len(issues) != 0 || len(rows) != 1 || rows[0].StopLoss != row.StopLoss || rows[0].TakeProfit != row.TakeProfit || rows[0].SLAmount != row.SLAmount || rows[0].TPAmount != row.TPAmount {
		t.Fatal(rows, issues)
	}
}
