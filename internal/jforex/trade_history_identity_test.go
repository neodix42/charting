package jforex

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/trading"
)

func partiallyFilledBrokerJournal() brokerJournal {
	j := brokerJournal{ID: strings.Repeat("d", 32), Environment: "demo", CreatedAt: time.Now().Add(-time.Hour).UnixMilli(), Request: TradeRequest{Instrument: "EURUSD", Side: "buy", Quantity: 500, StopLoss: 1.09, TakeProfit: 1.12}}
	j.History = newBrokerHistory(j)
	j.Result = TradeResult{Status: "partial", OrderID: "offline-partial-26", FilledQuantity: 26, EntryPrice: 1.1003, ProtectionConfirmed: true}
	applyBrokerSubmission(&j)
	return j
}

func copiedPartialBrokerSnapshot(j brokerJournal) brokerOrder {
	return brokerOrder{Intent: j.ID, OrderID: j.History.OrderID, Instrument: j.Request.Instrument, Side: j.Request.Side, Status: "open", OriginalQuantity: 0, FilledQuantity: 26, RemainingQuantity: 26, EntryPrice: 1.1003, StopLoss: 1.09, TakeProfit: 1.12, OpenDate: j.CreatedAt}
}

func TestBrokerHistoryRecoversCopiedPartialOrderWithoutReplacingRequestedSize(t *testing.T) {
	for _, original := range []float64{0, 26, 500} {
		t.Run(numberText(original), func(t *testing.T) {
			j := partiallyFilledBrokerJournal()
			o := copiedPartialBrokerSnapshot(j)
			o.OriginalQuantity = original
			if err := applyBrokerSnapshot(&j, o); err != nil || j.History.Status != "open" || j.History.Size != "26" || j.History.RequestedSize != "500" || j.History.RemainingSize != "26" {
				t.Fatal(j.History, err)
			}
			o.Status, o.RemainingQuantity, o.CloseDate = "closed", 0, time.Now().Add(-time.Second).UnixMilli()
			o.StopLoss, o.TakeProfit = 0, 0
			if err := applyBrokerSnapshot(&j, o); err != nil || j.History.Status != "closed" || j.History.RemainingSize != "0" || j.History.CloseDate == 0 || j.History.StopLoss != "1.09" || j.History.StopLossSource != "recorded" {
				t.Fatal("broker closure was hidden by reconstructed quantity", j.History, err)
			}
		})
	}
}

func TestBrokerCopiedOrderRecoveryRequiresExactIdentityAndDurableActualFill(t *testing.T) {
	cases := map[string]func(*brokerJournal, *brokerOrder){
		"no fill witness":             func(j *brokerJournal, _ *brokerOrder) { j.Result.FilledQuantity = 0 },
		"requested size is not proof": func(j *brokerJournal, _ *brokerOrder) { j.Result = TradeResult{}; j.History.Size = "500" },
		"unconfirmed result":          func(j *brokerJournal, _ *brokerOrder) { j.Result.Status = "unknown" },
		"other acknowledged id":       func(j *brokerJournal, _ *brokerOrder) { j.Result.OrderID = "other" },
		"other native id":             func(_ *brokerJournal, o *brokerOrder) { o.OrderID = "other" },
		"other intent":                func(_ *brokerJournal, o *brokerOrder) { o.Intent = strings.Repeat("e", 32) },
		"other instrument":            func(_ *brokerJournal, o *brokerOrder) { o.Instrument = "GBPUSD" },
		"other side":                  func(_ *brokerJournal, o *brokerOrder) { o.Side = "sell" },
		"unknown original":            func(_ *brokerJournal, o *brokerOrder) { o.OriginalQuantity = 25 },
		"negative original":           func(_ *brokerJournal, o *brokerOrder) { o.OriginalQuantity = -1 },
		"invalid original":            func(_ *brokerJournal, o *brokerOrder) { o.OriginalQuantity = math.NaN() },
		"changed actual fill":         func(_ *brokerJournal, o *brokerOrder) { o.FilledQuantity = 27 },
		"remaining exceeds fill":      func(_ *brokerJournal, o *brokerOrder) { o.RemainingQuantity = 27 },
		"invalid actual fill":         func(_ *brokerJournal, o *brokerOrder) { o.FilledQuantity = math.Inf(1) },
		"witness exceeds request":     func(j *brokerJournal, _ *brokerOrder) { j.Result.FilledQuantity = 501 },
		"witness has no pinned id":    func(j *brokerJournal, _ *brokerOrder) { j.History.OrderID = "" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			j := partiallyFilledBrokerJournal()
			o := copiedPartialBrokerSnapshot(j)
			change(&j, &o)
			before := j.History
			if err := applyBrokerSnapshot(&j, o); err == nil {
				t.Fatal("unproven order accepted", j.History)
			}
			if !reflect.DeepEqual(j.History, before) {
				t.Fatal("rejected snapshot changed saved history")
			}
		})
	}
}

func TestBrokerFillWitnessUsesLatestVerifiedActualSizeAndNeverRequestedSize(t *testing.T) {
	j := partiallyFilledBrokerJournal()
	j.History.CheckedAt = time.Now().Add(-time.Hour).UnixMilli()
	j.History.OpenDate = j.CreatedAt
	j.History.Size = "30"
	if got := knownBrokerFilledQuantity(&j); got != 30 {
		t.Fatal(got)
	}
	j.Result = TradeResult{}
	j.History.CheckedAt = 0
	if got := knownBrokerFilledQuantity(&j); got != 0 {
		t.Fatal("unverified display size became ownership proof", got)
	}
	j.History.CheckedAt = time.Now().UnixMilli()
	j.History.Size = "500"
	j.History.Status = "submitted"
	if got := knownBrokerFilledQuantity(&j); got != 0 {
		t.Fatal("a successfully checked pending order supplied a filled-size witness", got)
	}
	j.History.Status, j.History.OpenDate = "open", 0
	if got := knownBrokerFilledQuantity(&j); got != 0 {
		t.Fatal("missing execution date supplied a filled-size witness", got)
	}
}

func TestBrokerCopiedPartialHistoryPreviewAndExactCloseUseDurableWitness(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "closed"}[terminal], func(t *testing.T) {
			trace := filepath.Join(t.TempDir(), "trace")
			t.Setenv("CRYPTODESK_JFOREX_PROTOCOL_TRACE", trace)
			scenario := "trade-history-copied-partial"
			if terminal {
				scenario += "-closed"
			}
			m, _ := testManager(t, scenario)
			ctx := context.Background()
			p, err := m.PreviewTrade(ctx, sampleTrade())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.SubmitTrade(ctx, p.ID); err != nil {
				t.Fatal(err)
			}
			row, err := m.RefreshTrade(ctx, p.ID)
			if err != nil || row.Error != "" || row.Size != "26" || row.RequestedSize != "1000" {
				t.Fatal(row, err)
			}
			if terminal {
				if row.Status != "closed" || row.CanClose || row.CloseDate == 0 {
					t.Fatal(row)
				}
				if _, err = m.PreviewClose(ctx, p.ID); err == nil {
					t.Fatal("already closed order was closeable")
				}
				return
			}
			if !row.CanClose {
				t.Fatal(row)
			}
			plan, err := m.PreviewClose(ctx, p.ID)
			if err != nil || plan.Quantity != "26" {
				t.Fatal(plan, err)
			}
			operations, _ := os.ReadFile(trace)
			if strings.Contains(string(operations), "tradeClose") {
				t.Fatal("read-only refresh or preview traded")
			}
			row, err = m.ConfirmClose(ctx, plan.ID)
			if err != nil || row.Status != "closed" || row.RemainingSize != "0" {
				t.Fatal(row, err)
			}
			operations, _ = os.ReadFile(trace)
			if strings.Count(string(operations), "tradeClose\n") != 1 {
				t.Fatal(string(operations))
			}
			j, err := m.readTradeJournal(p.ID)
			if err != nil || len(j.Closes) != 1 || j.Closes[0].Quantity != "26" || j.Closes[0].Status != "closed" {
				t.Fatal(j, err)
			}
			if _, err = m.ConfirmClose(ctx, plan.ID); err == nil {
				t.Fatal("close confirmation replay accepted")
			}
			// Retained closed history survives a normal local read.
			var restored brokerJournal
			if err = trading.ReadJournal(m.tradeJournalPath(p.ID), &restored); err != nil || restored.History.Status != "closed" {
				t.Fatal(restored, err)
			}
		})
	}
}
