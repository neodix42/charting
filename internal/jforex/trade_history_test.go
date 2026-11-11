package jforex

import (
	"context"
	"cryptodesk/internal/trading"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrokerPlatformHistoryCapturesExecutionBalanceAndClosesVerifiedOrder(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("CRYPTODESK_JFOREX_PROTOCOL_TRACE", trace)
	m, _ := testManager(t, "trade-history")
	ctx := context.Background()
	p, err := m.PreviewTrade(ctx, sampleTrade())
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.SubmitTrade(ctx, p.ID)
	if err != nil || result.Status != "filled" {
		t.Fatal(result, err)
	}
	rows, issues := m.TradeHistory()
	if len(issues) != 0 || len(rows) != 1 || rows[0].BalanceAtOpen != "900" || rows[0].BalanceCurrency != "EUR" || rows[0].SLAmount != "-9.27" || rows[0].TPAmount != "17.73" || rows[0].MarginUsed != "" {
		t.Fatal(rows, issues)
	}
	row, err := m.RefreshTrade(ctx, p.ID)
	if err != nil || row.Error != "" || !row.CanClose || row.OpenDate <= 0 {
		t.Fatal(row, err)
	}
	closePreview, err := m.PreviewClose(ctx, p.ID)
	if err != nil || closePreview.Quantity != "1000" {
		t.Fatal(closePreview, err)
	}
	operations, _ := os.ReadFile(trace)
	if strings.Contains(string(operations), "tradeClose") {
		t.Fatal("preview closed a position")
	}
	row, err = m.ConfirmClose(ctx, closePreview.ID)
	if err != nil || row.Status != "closed" || row.CloseDate <= 0 || row.CanClose {
		t.Fatal(row, err)
	}
	if _, err = m.ConfirmClose(ctx, closePreview.ID); err == nil {
		t.Fatal("close replay accepted")
	}
	operations, _ = os.ReadFile(trace)
	if strings.Count(string(operations), "tradeClose\n") != 1 {
		t.Fatal(string(operations))
	}
	profile := filepath.Dir(m.path)
	m.Close()
	restored, _ := restoredCatalogManager(t, profile, "error")
	rows, issues = restored.TradeHistory()
	if len(issues) != 0 || len(rows) != 1 || rows[0].CloseDate != row.CloseDate || rows[0].BalanceAtOpen != "900" {
		t.Fatal(rows, issues)
	}
	raw, _ := os.ReadFile(restored.tradeJournalPath(p.ID))
	if strings.Contains(string(raw), testPassword) || strings.Contains(string(raw), testUsername) {
		t.Fatal("journal leaked credentials")
	}
}
func TestBrokerHistoryCannotCloseForeignOrChangedPositions(t *testing.T) {
	for _, scenario := range []string{"trade-history-outside", "trade-history-changed", "trade-history-uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			trace := filepath.Join(t.TempDir(), "trace")
			t.Setenv("CRYPTODESK_JFOREX_PROTOCOL_TRACE", trace)
			m, _ := testManager(t, scenario)
			ctx := context.Background()
			p, err := m.PreviewTrade(ctx, sampleTrade())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.SubmitTrade(ctx, p.ID); err != nil {
				t.Fatal(err)
			}
			plan, err := m.PreviewClose(ctx, p.ID)
			if scenario == "trade-history-outside" {
				if err == nil {
					t.Fatal("foreign order accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			row, err := m.ConfirmClose(ctx, plan.ID)
			operations, _ := os.ReadFile(trace)
			if scenario == "trade-history-changed" {
				if err == nil || strings.Contains(string(operations), "tradeClose") {
					t.Fatal("changed quantity closed", row, err, string(operations))
				}
				return
			}
			if err != nil || row.Error == "" {
				t.Fatal(row, err)
			}
			if _, err = m.PreviewClose(ctx, p.ID); err == nil {
				t.Fatal("uncertain close could be resubmitted")
			}
			operations, _ = os.ReadFile(trace)
			if strings.Count(string(operations), "tradeClose\n") != 1 {
				t.Fatal("automatic resend", string(operations))
			}
			var record brokerJournal
			if err = trading.ReadJournal(m.tradeJournalPath(p.ID), &record); err != nil || record.Closes[0].Status != "uncertain" {
				t.Fatal(record, err)
			}
		})
	}
}

func TestBrokerHistoryRecoversLegacyRiskAndReportedOpenAndClosedProfit(t *testing.T) {
	m, _ := testManager(t, "trade-history")
	ctx := context.Background()
	preview, err := m.PreviewTrade(ctx, sampleTrade())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.SubmitTrade(ctx, preview.ID); err != nil {
		t.Fatal(err)
	}
	j, err := m.readTradeJournal(preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Old claims have prices/size but no preview currency or conversion snapshot.
	j.Preview.AccountCurrency = ""
	j.Preview.ProfitPerPriceUnit = 0
	j.Preview.ValuationError = "Missing original conversion"
	j.History.Currency = ""
	j.History.SLAmount = ""
	j.History.TPAmount = ""
	if err = trading.WriteJournal(m.tradeJournalPath(preview.ID), j, false); err != nil {
		t.Fatal(err)
	}
	row, err := m.RefreshTrade(ctx, preview.ID)
	if err != nil || row.Currency != "EUR" || row.SLAmount != "-9.27" || row.TPAmount != "17.73" || row.ProfitLoss != "12.5" || row.ProfitLossCurrency != "EUR" || row.ProfitLossState != "open" || row.ProfitLossPips != "125.5" || row.ProfitLossPipsUnit != "pips" || row.ExitPrice != "" {
		t.Fatal(row, err)
	}
	plan, err := m.PreviewClose(ctx, preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	row, err = m.ConfirmClose(ctx, plan.ID)
	if err != nil || row.Status != "closed" || row.ProfitLoss != "" || row.ProfitLossPips != "" {
		t.Fatal("open P/L must not be presented as final", row, err)
	}
	row, err = m.RefreshTrade(ctx, preview.ID)
	if err != nil || row.ProfitLoss != "-3.75" || row.ProfitLossState != "closed" || row.CloseDate == 0 || row.ExitPrice != "1.096548" || row.ProfitLossPips != "-37.5" || row.ProfitLossPipsState != "closed" {
		t.Fatal(row, err)
	}
	rows, issues := m.TradeHistory()
	if len(issues) != 0 || len(rows) != 1 || rows[0].ProfitLoss != "-3.75" || rows[0].ExitPrice != "1.096548" || rows[0].ProfitLossPips != "-37.5" {
		t.Fatal(rows, issues)
	}
}

func TestBrokerPipsAreIndependentOfCurrencyAndNeverReuseOpenResultAsFinal(t *testing.T) {
	now := time.Now().UnixMilli()
	j := brokerJournal{ID: strings.Repeat("a", 32), Request: sampleTrade()}
	zero := 0.0
	order := brokerOrder{Intent: j.ID, OrderID: "owned", Instrument: j.Request.Instrument, Side: j.Request.Side, Status: "open", OriginalQuantity: j.Request.Quantity, FilledQuantity: j.Request.Quantity, RemainingQuantity: j.Request.Quantity, EntryPrice: 1.1003, OpenDate: now - 1000, PipSize: .0001, ProfitLossPips: &zero, ProfitLossPipsTime: now}
	if err := applyBrokerSnapshot(&j, order); err != nil || j.History.ProfitLossPips != "0" || j.History.ProfitLossPipsUnit != "pips" || j.History.Currency != "" {
		t.Fatal(j.History, err)
	}
	order.ProfitLossPipsTime = now - 60000
	if err := applyBrokerSnapshot(&j, order); err != nil || j.History.ProfitLossPips != "" {
		t.Fatal("stale native pips displayed", j.History, err)
	}
	order.ProfitLossPipsTime = now
	if err := applyBrokerSnapshot(&j, order); err != nil {
		t.Fatal(err)
	}
	order.Status, order.CloseDate, order.RemainingQuantity = "closed", now, 0
	order.ExitPrice = 1.096548
	order.ProfitLossPips = nil
	if err := applyBrokerSnapshot(&j, order); err != nil || j.History.ExitPrice != "1.096548" || j.History.ProfitLossPips != "" {
		t.Fatal("open pip result reused as final", j.History, err)
	}
	final := -37.5
	order.ProfitLossPips = &final
	if err := applyBrokerSnapshot(&j, order); err != nil || j.History.ProfitLossPips != "-37.5" || j.History.ProfitLossPipsState != "closed" {
		t.Fatal(j.History, err)
	}
	order.ProfitLossPips, order.ExitPrice = nil, 0
	if err := applyBrokerSnapshot(&j, order); err != nil || j.History.ExitPrice != "1.096548" || j.History.ProfitLossPips != "-37.5" {
		t.Fatal("confirmed final values lost", j.History, err)
	}
}
