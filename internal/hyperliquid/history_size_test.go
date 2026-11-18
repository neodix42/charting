package hyperliquid

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/trading"
)

func TestHistoryRequestedSizeRecoversLegacyPartialFillWithoutAccountAccessOrWrites(t *testing.T) {
	profile := t.TempDir()
	s := NewStore(profile)
	id := strings.Repeat("a", 32)
	p := Preview{ID: id, AccountAddress: "0x" + strings.Repeat("1", 40), Network: "mainnet", Request: OrderRequest{Market: "perpetual", Instrument: "xyz:NATGAS", Side: "buy", Quantity: "100"}}
	j := orderJournal{Preview: p}
	j.History = newHistoryTrade(p, Metrics{}, time.Now().UnixMilli())
	if j.History.RequestedSize != "100" || j.History.Size != "" {
		t.Fatal("request presented as filled", j.History)
	}
	j.Result = OrderResult{Status: "partial", FilledQuantity: "58.8", AveragePrice: "3.5541", OrderIDs: []int64{608848221881}}
	applySubmission(&j)
	j.History.RequestedSize = "" // Existing journals predate the requested-size field.
	if err := os.MkdirAll(profile+"/hyperliquid-orders", 0700); err != nil {
		t.Fatal(err)
	}
	if err := trading.WriteJournal(s.journalPath(id), j, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.journalPath(id))
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*Store{s, NewStore(profile)} {
		rows, issues := store.TradeHistory()
		if len(issues) != 0 || len(rows) != 1 || rows[0].RequestedSize != "100" || rows[0].Size != "58.8" || rows[0].RemainingSize != "58.8" {
			t.Fatal(rows, issues)
		}
		row, err := store.LocalTrade(id)
		if err != nil || row.RequestedSize != "100" || row.Size != "58.8" {
			t.Fatal(row, err)
		}
	}
	after, err := os.ReadFile(s.journalPath(id))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("local read rewrote claim", err)
	}
}

func TestHistoryUnconfirmedLegacyRequestDoesNotBecomeActualSize(t *testing.T) {
	s := NewStore(t.TempDir())
	id := strings.Repeat("b", 32)
	p := Preview{ID: id, AccountAddress: "0x" + strings.Repeat("1", 40), Network: "mainnet", Request: OrderRequest{Market: "perpetual", Instrument: "xyz:NATGAS", Side: "buy", Quantity: "100"}}
	j := orderJournal{Preview: p, History: newHistoryTrade(p, Metrics{}, time.Now().UnixMilli())}
	j.History.RequestedSize = ""
	j.History.Size = "100" // Old constructors used the request before any fill.
	if err := os.MkdirAll(s.profile+"/hyperliquid-orders", 0700); err != nil {
		t.Fatal(err)
	}
	if err := trading.WriteJournal(s.journalPath(id), j, true); err != nil {
		t.Fatal(err)
	}
	row, err := s.LocalTrade(id)
	if err != nil || row.RequestedSize != "100" || row.Size != "" || row.RemainingSize != "" {
		t.Fatal(row, err)
	}
}

func TestHistoryRequestedAndActualOpenedSizeSurviveRefreshAndPartialExit(t *testing.T) {
	f := newHistoryFixture(t)
	row, err := f.store.RefreshTrade(context.Background(), f.id)
	if err != nil || row.RequestedSize != "1" || row.Size != "1" {
		t.Fatal(row, err)
	}
	f.partial = "0.4"
	plan, err := f.store.PreviewClose(context.Background(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ConfirmClose(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	row, err = f.store.RefreshTrade(context.Background(), f.id)
	if err != nil || row.RequestedSize != "1" || row.Size != "1" || row.RemainingSize != "0.6" {
		t.Fatal(row, err)
	}
}
