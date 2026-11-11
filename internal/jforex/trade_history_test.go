package jforex

import (
	"context"
	"cryptodesk/internal/trading"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
