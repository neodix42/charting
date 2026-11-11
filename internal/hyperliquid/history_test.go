package hyperliquid

import (
	"context"
	"cryptodesk/internal/trading"
	"encoding/json"
	"fmt"
	"github.com/shopspring/decimal"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type historyFixture struct {
	store            *Store
	mock             *tradingMock
	id               string
	fills            []historyFill
	position         string
	nextID           int64
	partial          string
	exitPrice        string
	uncertain        bool
	entryAt, closeAt int64
	cloids           map[string]int64
}

func newHistoryFixture(t *testing.T) *historyFixture {
	return newHistoryFixtureSide(t, "buy")
}
func newHistoryFixtureSide(t *testing.T, side string) *historyFixture {
	t.Helper()
	s, m := mockStore(t)
	m.collateralToken = intPointer(0)
	request := baseRequest()
	request.Side = side
	entrySide, closeSide, position := "B", "A", "1"
	if side == "sell" {
		request.StopLoss, request.TakeProfit = "110", "90"
		entrySide, closeSide, position = "A", "B", "-1"
	}
	p, err := s.Preview(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Submit(context.Background(), p.ID)
	if err != nil || result.FilledQuantity != "1" {
		t.Fatal(result, err)
	}
	f := &historyFixture{store: s, mock: m, id: p.ID, position: position, nextID: 200, entryAt: s.now().Add(-time.Second).UnixMilli(), cloids: map[string]int64{}}
	f.fills = []historyFill{{Coin: p.Request.Instrument, OID: 101, Side: entrySide, Sz: "1", Px: "100", StartPosition: "0", Time: f.entryAt, TID: 1}, {Coin: "ETH", OID: 900, Side: "B", Sz: "2", Px: "200", StartPosition: "0", Time: f.entryAt, TID: 2}}
	m.positions = json.RawMessage(fmt.Sprintf(`[{"position":{"coin":%q,"szi":%q,"marginUsed":"5","unrealizedPnl":"2.5"}}]`, p.Request.Instrument, position))
	m.historyHandler = func(w http.ResponseWriter, input map[string]json.RawMessage) bool {
		var kind string
		_ = json.Unmarshal(input["type"], &kind)
		switch kind {
		case "orderStatus":
			var cid string
			_ = json.Unmarshal(input["oid"], &cid)
			oid := int64(101)
			side := entrySide
			reduce := false
			if cid != "0x"+f.id {
				oid = f.cloids[cid]
				side = closeSide
				reduce = true
			}
			if oid == 0 {
				fmt.Fprint(w, `{"status":"unknownOid"}`)
				return true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "order", "order": map[string]any{"status": "filled", "statusTimestamp": f.entryAt, "order": map[string]any{"coin": p.Request.Instrument, "side": side, "oid": oid, "cloid": cid, "timestamp": f.entryAt, "origSz": "1", "reduceOnly": reduce}}})
			return true
		case "userFillsByTime":
			var from, to int64
			_ = json.Unmarshal(input["startTime"], &from)
			_ = json.Unmarshal(input["endTime"], &to)
			rows := []historyFill{}
			for _, fill := range f.fills {
				if fill.Time >= from && fill.Time <= to {
					rows = append(rows, fill)
				}
			}
			_ = json.NewEncoder(w).Encode(rows)
			return true
		}
		if _, ok := input["action"]; !ok {
			return false
		}
		var action map[string]any
		_ = json.Unmarshal(input["action"], &action)
		if action["type"] == "cancelByCloid" {
			m.actions = append(m.actions, action)
			statuses := []string{}
			for range action["cancels"].([]any) {
				statuses = append(statuses, "success")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "response": map[string]any{"type": "cancel", "data": map[string]any{"statuses": statuses}}})
			return true
		}
		if action["type"] != "order" {
			return false
		}
		orders := action["orders"].([]any)
		if len(orders) != 1 {
			return false
		}
		order := orders[0].(map[string]any)
		if order["r"] != true {
			return false
		}
		if order["b"] != (closeSide == "B") {
			t.Error("close direction must reduce the original position")
		}
		m.actions = append(m.actions, action)
		if f.uncertain {
			w.WriteHeader(503)
			fmt.Fprint(w, `synthetic uncertain close`)
			return true
		}
		requested := order["s"].(string)
		filled := requested
		if f.partial != "" {
			filled = f.partial
			f.partial = ""
		}
		old, _ := trading.Numeric(f.position)
		quantity, _ := trading.Numeric(filled)
		f.nextID++
		f.closeAt = s.now().UnixMilli()
		exit := "99"
		if f.exitPrice != "" {
			exit = f.exitPrice
		}
		exitPx, _ := trading.Numeric(exit)
		closedPnl := quantity.Mul(exitPx.Sub(decimal.NewFromInt(100)))
		if closeSide == "B" {
			closedPnl = closedPnl.Neg()
		}
		f.fills = append(f.fills, historyFill{Coin: p.Request.Instrument, OID: f.nextID, Side: closeSide, ClosedPnL: closedPnl.String(), Sz: filled, Px: exit, StartPosition: f.position, Time: f.closeAt, TID: uint64(f.nextID)})
		if closeSide == "A" {
			f.position = old.Sub(quantity).String()
		} else {
			f.position = old.Add(quantity).String()
		}
		f.cloids[order["c"].(string)] = f.nextID
		m.positions = json.RawMessage(fmt.Sprintf(`[{"position":{"coin":%q,"szi":%q,"marginUsed":"3","unrealizedPnl":%q}}]`, p.Request.Instrument, f.position, func() string {
			left, _ := trading.Numeric(f.position)
			return left.Abs().Mul(decimal.NewFromFloat(2.5)).String()
		}()))
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "response": map[string]any{"type": "order", "data": map[string]any{"statuses": []any{map[string]any{"filled": map[string]any{"oid": f.nextID, "totalSz": filled, "avgPx": exit}}}}}})
		return true
	}
	return f
}
func TestPlatformHistorySnapshotsReconcilesAndClosesOnlyItsOwnExposure(t *testing.T) {
	f := newHistoryFixture(t)
	ctx := context.Background()
	rows, issues := f.store.TradeHistory()
	if len(issues) != 0 || len(rows) != 1 || rows[0].BalanceAtOpen != "1000" || rows[0].BalanceTime <= 0 || rows[0].SLAmount != "-10" || rows[0].TPAmount != "10" {
		t.Fatal(rows, issues)
	}
	row, err := f.store.RefreshTrade(ctx, f.id)
	if err != nil || row.Error != "" || !row.CanClose || row.OpenDate != f.entryAt || row.OrderID != "101" || row.MarginUsed != "5" {
		t.Fatal(row, err)
	}
	before := len(f.mock.actions)
	preview, err := f.store.PreviewClose(ctx, f.id)
	if err != nil || preview.Quantity != "1" || len(f.mock.actions) != before {
		t.Fatal(preview, err)
	}
	row, err = f.store.ConfirmClose(ctx, preview.ID)
	if err != nil || row.CloseDate != 0 || row.Status != "closing" {
		t.Fatal(row, err)
	}
	if len(f.mock.actions) != before+2 {
		t.Fatal("expected one reduce-only IOC and own bracket cancellation", f.mock.actions)
	}
	row, err = f.store.RefreshTrade(ctx, f.id)
	if err != nil || row.Error != "" || row.Status != "closed" || row.CloseDate != f.closeAt || row.CanClose {
		t.Fatal(row, err)
	}
	if _, err = f.store.ConfirmClose(ctx, preview.ID); err == nil || len(f.mock.actions) != before+2 {
		t.Fatal("close confirmation replayed", err)
	}
	restored := NewStore(f.store.profile)
	rows, issues = restored.TradeHistory()
	if len(issues) != 0 || len(rows) != 1 || rows[0].CloseDate != f.closeAt || rows[0].BalanceAtOpen != "1000" {
		t.Fatal("restart lost history", rows, issues)
	}
	if len(f.fills) != 3 {
		t.Fatal(f.fills)
	}
}
func TestHistoryMixedPositionAndUnknownLocalIDsCannotCloseOutsideTrades(t *testing.T) {
	f := newHistoryFixture(t)
	f.mock.mu.Lock()
	f.fills = append(f.fills, historyFill{Coin: "BTC", OID: 999, Side: "B", Sz: "1", Px: "100", StartPosition: "1", Time: f.entryAt + 1, TID: 999})
	f.mock.positions = json.RawMessage(`[{"position":{"coin":"BTC","szi":"2","marginUsed":"10"}}]`)
	f.mock.mu.Unlock()
	row, err := f.store.RefreshTrade(context.Background(), f.id)
	if err != nil || row.Error != "" || row.CanClose || !strings.Contains(row.CloseReason, "Other executions") {
		t.Fatal(row, err)
	}
	before := len(f.mock.actions)
	if _, err = f.store.PreviewClose(context.Background(), f.id); err == nil {
		t.Fatal("mixed position was closeable")
	}
	if _, err = f.store.PreviewClose(context.Background(), strings.Repeat("f", 32)); err == nil {
		t.Fatal("foreign order accepted without journal")
	}
	rows, issues := f.store.TradeHistory()
	if len(rows) != 1 || len(issues) != 0 || len(f.mock.actions) != before {
		t.Fatal(rows, issues, f.mock.actions)
	}
}
func TestPartialCloseKeepsResidualAndUncertainCloseCannotReplay(t *testing.T) {
	t.Run("partial", func(t *testing.T) {
		f := newHistoryFixture(t)
		f.partial = "0.4"
		preview, err := f.store.PreviewClose(context.Background(), f.id)
		if err != nil {
			t.Fatal(err)
		}
		before := len(f.mock.actions)
		row, err := f.store.ConfirmClose(context.Background(), preview.ID)
		if err != nil || row.RemainingSize != "0.6" || len(f.mock.actions) != before+1 {
			t.Fatal(row, err)
		}
		row, err = f.store.RefreshTrade(context.Background(), f.id)
		if err != nil || row.Error != "" || !row.CanClose || row.RemainingSize != "0.6" {
			t.Fatal(row, err)
		}
		preview, err = f.store.PreviewClose(context.Background(), f.id)
		if err != nil || preview.Quantity != "0.6" {
			t.Fatal(preview, err)
		}
		_, err = f.store.ConfirmClose(context.Background(), preview.ID)
		if err != nil {
			t.Fatal(err)
		}
		row, err = f.store.RefreshTrade(context.Background(), f.id)
		if err != nil || row.Status != "closed" || row.CloseDate == 0 {
			t.Fatal(row, err)
		}
	})
	t.Run("uncertain", func(t *testing.T) {
		f := newHistoryFixture(t)
		f.uncertain = true
		preview, err := f.store.PreviewClose(context.Background(), f.id)
		if err != nil {
			t.Fatal(err)
		}
		before := len(f.mock.actions)
		row, err := f.store.ConfirmClose(context.Background(), preview.ID)
		if err != nil || row.Error == "" || row.Status != "closing" {
			t.Fatal(row, err)
		}
		if _, err = f.store.PreviewClose(context.Background(), f.id); err == nil {
			t.Fatal("uncertain close was repeated")
		}
		if len(f.mock.actions) != before+1 {
			t.Fatal("unexpected automatic resend")
		}
		restored := NewStore(f.store.profile)
		var saved orderJournal
		if err = trading.ReadJournal(filepath.Join(restored.profile, "hyperliquid-orders", f.id+".json"), &saved); err != nil || saved.Closes[0].Status != "uncertain" {
			t.Fatal(saved, err)
		}
	})
}

func TestShortHistoryCloseBuysToReduceAndRecordsServerCloseTime(t *testing.T) {
	f := newHistoryFixtureSide(t, "sell")
	ctx := context.Background()
	row, err := f.store.RefreshTrade(ctx, f.id)
	if err != nil || !row.CanClose || row.Direction != "short" || row.RemainingSize != "1" || row.SLAmount != "-10" || row.TPAmount != "10" {
		t.Fatal(row, err)
	}
	preview, err := f.store.PreviewClose(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ConfirmClose(ctx, preview.ID); err != nil {
		t.Fatal(err)
	}
	row, err = f.store.RefreshTrade(ctx, f.id)
	if err != nil || row.Status != "closed" || row.RemainingSize != "0" || row.CloseDate != f.closeAt || f.position != "0" {
		t.Fatal(row, err)
	}
}

func TestHistoryRecoversSettlementRiskAndTracksRealizedPlusFloatingProfit(t *testing.T) {
	f := newHistoryFixture(t)
	ctx := context.Background()
	j, err := f.store.readOrderJournal(f.id)
	if err != nil {
		t.Fatal(err)
	}
	j.Preview.AccountCurrency = ""
	j.Preview.ProfitPerPriceUnit = ""
	j.History.Currency = ""
	j.History.SLAmount = ""
	j.History.TPAmount = ""
	if err = trading.WriteJournal(f.store.journalPath(f.id), j, false); err != nil {
		t.Fatal(err)
	}
	row, err := f.store.RefreshTrade(ctx, f.id)
	if err != nil || row.Currency != "USDC" || row.SLAmount != "-10" || row.TPAmount != "10" || row.ProfitLoss != "2.5" || row.ProfitLossState != "open" || row.ExitPrice != "" || row.ProfitLossPips != "250" || row.ProfitLossPipsUnit != "ticks" || row.PipSize != "0.01" {
		t.Fatal(row, err)
	}
	f.partial = "0.4"
	plan, err := f.store.PreviewClose(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ConfirmClose(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	row, err = f.store.RefreshTrade(ctx, f.id)
	if err != nil || row.RemainingSize != "0.6" || row.SLAmount != "-6" || row.TPAmount != "6" || row.ProfitLoss != "1.1" || row.ProfitLossPips != "110" || row.ExitPrice != "" {
		t.Fatal("remaining risk and realized/floating total", row, err)
	}
	plan, err = f.store.PreviewClose(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ConfirmClose(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	row, err = f.store.RefreshTrade(ctx, f.id)
	if err != nil || row.ProfitLoss != "-1" || row.ProfitLossCurrency != "USDC" || row.ProfitLossState != "closed" || row.ExitPrice != "99" || row.ProfitLossPips != "-100" || row.ProfitLossPipsState != "closed" {
		t.Fatal(row, err)
	}
	// Confirmed final values survive unavailable history and restart.
	restored := NewStore(f.store.profile)
	rows, issues := restored.TradeHistory()
	if len(issues) != 0 || len(rows) != 1 || rows[0].ProfitLoss != "-1" || rows[0].ExitPrice != "99" || rows[0].ProfitLossPips != "-100" {
		t.Fatal(rows, issues)
	}
}
func TestHistoryDoesNotInventProfitForMissingOrMixedExchangeData(t *testing.T) {
	f := newHistoryFixture(t)
	f.mock.mu.Lock()
	f.mock.positions = json.RawMessage(`[{"position":{"coin":"BTC","szi":"1","marginUsed":"5","unrealizedPnl":"0"}}]`)
	f.mock.mu.Unlock()
	row, err := f.store.RefreshTrade(context.Background(), f.id)
	if err != nil || row.ProfitLoss != "0" || row.ProfitLossPips != "0" {
		t.Fatal("real zero lost", row, err)
	}
	f.mock.mu.Lock()
	f.mock.positions = json.RawMessage(`[{"position":{"coin":"BTC","szi":"1","marginUsed":"5"}}]`)
	f.mock.mu.Unlock()
	row, err = f.store.RefreshTrade(context.Background(), f.id)
	if err != nil || row.ProfitLoss != "" || row.ProfitLossError == "" || row.ProfitLossPips != "" {
		t.Fatal("missing field became zero", row, err)
	}
	f.mock.mu.Lock()
	f.mock.collateralToken = intPointer(9999)
	f.mock.positions = json.RawMessage(`[{"position":{"coin":"BTC","szi":"1","unrealizedPnl":"100"}}]`)
	f.mock.mu.Unlock()
	row, err = f.store.RefreshTrade(context.Background(), f.id)
	if err != nil || row.ProfitLossPips != "" {
		t.Fatal("unknown current settlement currency reused an old price conversion", row, err)
	}
	f.mock.mu.Lock()
	f.mock.collateralToken = intPointer(0)
	f.fills = append(f.fills, historyFill{Coin: "BTC", OID: 999, Side: "B", Sz: "1", Px: "100", StartPosition: "1", Time: f.entryAt + 1, TID: 999})
	f.mock.positions = json.RawMessage(`[{"position":{"coin":"BTC","szi":"2","unrealizedPnl":"100"}}]`)
	f.mock.mu.Unlock()
	row, err = f.store.RefreshTrade(context.Background(), f.id)
	if err != nil || row.ProfitLoss != "" || row.SLAmount != "" || row.CanClose || row.ProfitLossPips != "" || row.ExitPrice != "" {
		t.Fatal("mixed account P/L attributed to one entry", row, err)
	}
}

func TestHistoryWeightsExitFillsAndBackfillsLegacyClosedTicks(t *testing.T) {
	for _, side := range []string{"buy", "sell"} {
		t.Run(side, func(t *testing.T) {
			f := newHistoryFixtureSide(t, side)
			ctx := context.Background()
			f.partial = "0.4"
			plan, err := f.store.PreviewClose(ctx, f.id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.ConfirmClose(ctx, plan.ID); err != nil {
				t.Fatal(err)
			}
			f.exitPrice = "101"
			plan, err = f.store.PreviewClose(ctx, f.id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.ConfirmClose(ctx, plan.ID); err != nil {
				t.Fatal(err)
			}
			row, err := f.store.RefreshTrade(ctx, f.id)
			want := "20"
			if side == "sell" {
				want = "-20"
			}
			if err != nil || row.ExitPrice != "100.2" || row.ProfitLossPips != want || row.ProfitLossPipsState != "closed" {
				t.Fatal(row, err)
			}
			j, err := f.store.readOrderJournal(f.id)
			if err != nil {
				t.Fatal(err)
			}
			// Existing final monetary results must not prevent new-field backfill.
			j.History.ExitPrice, j.History.PipSize, j.History.ProfitLossPips, j.History.ProfitLossPipsState = "", "", "", ""
			if err = trading.WriteJournal(f.store.journalPath(f.id), j, false); err != nil {
				t.Fatal(err)
			}
			row, err = f.store.RefreshTrade(ctx, f.id)
			if err != nil || row.ExitPrice != "100.2" || row.ProfitLossPips != want {
				t.Fatal("legacy closed values not recovered", row, err)
			}
		})
	}
}

func TestHistoryTicksUseEntryPrecisionAndCapReversalClosingSize(t *testing.T) {
	f := newHistoryFixture(t)
	ctx := context.Background()
	f.mock.mu.Lock()
	f.fills[0].Px = "99.99"
	f.mock.positions = json.RawMessage(`[{"position":{"coin":"BTC","szi":"1","unrealizedPnl":"0.02"}}]`)
	f.mock.mu.Unlock()
	row, err := f.store.RefreshTrade(ctx, f.id)
	if err != nil || row.PipSize != "0.001" || row.ProfitLossPips != "20" || row.ExitPrice != "" {
		t.Fatal(row, err)
	}
	f.mock.mu.Lock()
	// The live price has crossed 100; retain the entry tick rather than the
	// coarser increment at the new price. A reversal closes only the original 1.
	f.fills = append(f.fills, historyFill{Coin: "BTC", Side: "A", Px: "101", Sz: "2", StartPosition: "1", ClosedPnL: "1.01", OID: 999, Time: f.entryAt + 1, TID: 999})
	f.mock.mu.Unlock()
	row, err = f.store.RefreshTrade(ctx, f.id)
	if err != nil || row.PipSize != "0.001" || row.ExitPrice != "101" || row.ProfitLossPips != "1010" || row.ProfitLossPipsState != "closed" {
		t.Fatal(row, err)
	}
}
