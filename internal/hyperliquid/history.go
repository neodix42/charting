package hyperliquid

import (
	"context"
	"cryptodesk/internal/trading"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/shopspring/decimal"
)

type orderJournal struct {
	Preview           Preview                `json:"preview"`
	Result            OrderResult            `json:"result"`
	History           trading.Trade          `json:"history"`
	Closes            []trading.CloseAttempt `json:"closes,omitempty"`
	ProtectionWarning string                 `json:"protectionWarning,omitempty"`
}
type closePlan struct {
	Preview           trading.ClosePreview
	Settings          Settings
	Quantity, EntryID string
}

func (s *Store) lockHistory(id string) func() {
	value, _ := s.historyLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
func (s *Store) journalPath(id string) string {
	return filepath.Join(s.profile, "hyperliquid-orders", id+".json")
}
func newHistoryTrade(p Preview, metrics Metrics, at int64) trading.Trade {
	direction := p.Request.Side
	if p.Request.Market == "perpetual" {
		if direction == "buy" {
			direction = "long"
		} else {
			direction = "short"
		}
	}
	return trading.Trade{ID: "hyperliquid:" + p.ID, Provider: "hyperliquid", Account: p.AccountAddress, Environment: p.Network, Instrument: p.Request.Instrument, Market: p.Request.Market, Direction: direction, SubmittedAt: at, Size: p.Request.Quantity, StopLoss: p.Request.StopLoss, TakeProfit: p.Request.TakeProfit, Currency: p.AccountCurrency, BalanceAtOpen: metrics.AccountBalance, BalanceCurrency: metrics.AvailableBalanceCurrency, BalanceTime: metrics.MetricsTime, BalanceBasis: metrics.AvailableBalanceBasis, Status: "unconfirmed", CloseReason: "Refresh exchange status before closing"}
}
func applySubmission(j *orderJournal) {
	r := &j.History
	if q, ok := trading.Numeric(j.Result.FilledQuantity); ok && q.IsPositive() {
		r.Size = q.String()
		r.RemainingSize = q.String()
		r.EntryPrice = j.Result.AveragePrice
		r.Status = "open"
		if j.Preview.Request.Market == "spot" {
			r.Status = "filled"
			r.RemainingSize = ""
		}
		if len(j.Result.OrderIDs) > 0 {
			r.OrderID = strconv.FormatInt(j.Result.OrderIDs[0], 10)
		}
		trading.ApplyRisk(r, j.Preview.ProfitPerPriceUnit)
	} else if j.Result.Status == "rejected" || j.Result.Status == "failed" {
		r.Status = "rejected"
	}
}
func (s *Store) readOrderJournal(id string) (*orderJournal, error) {
	if !trading.ValidIntent(id) {
		return nil, errors.New("Invalid platform trade ID")
	}
	var j orderJournal
	if err := trading.ReadJournal(s.journalPath(id), &j); err != nil {
		return nil, err
	}
	if j.Preview.ID == "" {
		if err := trading.ReadJournal(s.journalPath(id), &j.Preview); err != nil {
			return nil, err
		}
	}
	p := j.Preview
	if p.ID != id || !addressPattern.MatchString(p.AccountAddress) || p.Network != "mainnet" && p.Network != "testnet" || p.Request.Market != "spot" && p.Request.Market != "perpetual" || p.Request.Side != "buy" && p.Request.Side != "sell" || p.Request.Instrument == "" || len(p.Request.Instrument) > 128 {
		return nil, errors.New("Invalid Hyperliquid platform journal; original preserved")
	}
	if j.History.ID == "" {
		at := p.MetricsTime
		if at == 0 {
			at = p.ValuationTime
		}
		if at == 0 {
			at = p.ExpiresAt - 120000
		}
		j.History = newHistoryTrade(p, Metrics{}, at)
		applySubmission(&j)
	}
	if j.History.ID != "hyperliquid:"+id || j.History.Account != p.AccountAddress || j.History.Environment != p.Network || j.History.Instrument != p.Request.Instrument {
		return nil, errors.New("Hyperliquid journal identity mismatch; original preserved")
	}
	if len(j.Closes) > 100 {
		return nil, errors.New("Too many close attempts in the local trade journal")
	}
	return &j, nil
}
func (s *Store) TradeHistory() ([]trading.Trade, []string) {
	ids, err := trading.JournalIDs(filepath.Join(s.profile, "hyperliquid-orders"))
	if err != nil {
		return nil, []string{err.Error()}
	}
	rows := []trading.Trade{}
	problems := []string{}
	s.mu.Lock()
	v := s.settings
	s.mu.Unlock()
	for _, id := range ids {
		unlock := s.lockHistory(id)
		j, err := s.readOrderJournal(id)
		unlock()
		if err != nil {
			problems = append(problems, "Hyperliquid journal "+id+": "+err.Error())
			continue
		}
		row := j.History
		if row.Status == "rejected" {
			continue
		}
		if v.AccountAddress != row.Account || v.Network != row.Environment || v.PrivateKey == "" {
			row.CanClose = false
			row.CloseReason = "Configure this trade's account and network to update or close it"
		} else if s.now().UnixMilli()-row.CheckedAt > 30000 {
			row.CanClose = false
			row.CloseReason = "Refresh exchange status before closing"
		}
		rows = append(rows, row)
	}
	return rows, problems
}

type historyFill struct {
	Coin          string `json:"coin"`
	Side          string `json:"side"`
	Px            string `json:"px"`
	Sz            string `json:"sz"`
	StartPosition string `json:"startPosition"`
	ClosedPnL     string `json:"closedPnl"`
	OID           int64  `json:"oid"`
	Time          int64  `json:"time"`
	TID           uint64 `json:"tid"`
	Hash          string `json:"hash"`
}

func (s *Store) historyFills(ctx context.Context, v Settings, from int64) ([]historyFill, error) {
	all := []historyFill{}
	seen := map[string]bool{}
	end := s.now().UnixMilli()
	cursor := max(int64(0), from)
	for page := 0; page < 32; page++ {
		var fills []historyFill
		if err := s.post(ctx, v, "/info", map[string]any{"type": "userFillsByTime", "user": v.AccountAddress, "startTime": cursor, "endTime": end, "aggregateByTime": false}, &fills); err != nil {
			return nil, err
		}
		if len(fills) > 2000 {
			return nil, errors.New("Hyperliquid fill response exceeds its limit")
		}
		if len(fills) == 0 {
			return all, nil
		}
		last := cursor
		for _, fill := range fills {
			if fill.Time < cursor || fill.Time > end || fill.OID <= 0 || fill.Side != "B" && fill.Side != "A" {
				return nil, errors.New("Invalid Hyperliquid fill history")
			}
			q, ok := trading.Numeric(fill.Sz)
			p, pOK := trading.Numeric(fill.Px)
			_, startOK := trading.Numeric(fill.StartPosition)
			if !ok || !pOK || !startOK || !q.IsPositive() || !p.IsPositive() {
				return nil, errors.New("Invalid Hyperliquid execution values")
			}
			key := fmt.Sprintf("%d/%d/%s/%d/%s/%s/%s", fill.OID, fill.Time, fill.Hash, fill.TID, fill.Sz, fill.Px, fill.StartPosition)
			if !seen[key] {
				seen[key] = true
				all = append(all, fill)
			}
			last = max(last, fill.Time)
		}
		if len(all) >= 10000 {
			return nil, errors.New("Hyperliquid exposes only the latest 10,000 fills; complete position attribution is unavailable")
		}
		if last == cursor {
			if len(fills) >= 2000 {
				return nil, errors.New("Fill pagination cannot prove complete coverage at this timestamp")
			}
			cursor++
		} else {
			cursor = last
		}
		if cursor > end {
			return all, nil
		}
	}
	return nil, errors.New("Fill history exceeded its bounded pagination limit")
}

type queriedOrder struct {
	Status string `json:"status"`
	Order  struct {
		Order struct {
			Coin       string `json:"coin"`
			Side       string `json:"side"`
			OID        int64  `json:"oid"`
			Cloid      string `json:"cloid"`
			Timestamp  int64  `json:"timestamp"`
			ReduceOnly bool   `json:"reduceOnly"`
			OrigSz     string `json:"origSz"`
		} `json:"order"`
		Status string `json:"status"`
		Time   int64  `json:"statusTimestamp"`
	} `json:"order"`
}

func (s *Store) historyOrder(ctx context.Context, v Settings, cloid string) (queriedOrder, error) {
	var out queriedOrder
	err := s.post(ctx, v, "/info", map[string]any{"type": "orderStatus", "user": v.AccountAddress, "oid": cloid}, &out)
	return out, err
}
func (s *Store) refreshOrder(ctx context.Context, id string, j *orderJournal) error {
	s.mu.Lock()
	v := s.settings
	s.mu.Unlock()
	r := &j.History
	p := j.Preview
	r.CanClose = false
	r.CloseReason = "Position ownership is not yet verified"
	if v.AccountAddress != p.AccountAddress || v.Network != p.Network {
		return errors.New("Configure the account/network that opened this trade")
	}
	if r.CloseDate > 0 && r.Status == "closed" && r.ProfitLoss != "" && r.ProfitLossState == "closed" {
		r.CheckedAt = s.now().UnixMilli()
		return nil
	}
	status, err := s.historyOrder(ctx, v, "0x"+id)
	if err != nil {
		return err
	}
	o := status.Order.Order
	original, origOK := trading.Numeric(o.OrigSz)
	expectedSize, requestOK := trading.Numeric(p.Request.Quantity)
	if status.Status != "order" {
		return errors.New("The exchange has not confirmed this platform order ID")
	}
	if o.Coin != p.Request.Instrument || o.Cloid != "0x"+id || o.Side != map[string]string{"buy": "B", "sell": "A"}[p.Request.Side] || o.OID <= 0 || !origOK || !requestOK || !original.Equal(expectedSize) {
		return errors.New("Exchange order does not match the platform submission")
	}
	oid := o.OID
	if r.OrderID != "" && r.OrderID != strconv.FormatInt(oid, 10) {
		return errors.New("Exchange and local order IDs do not match")
	}
	r.OrderID = strconv.FormatInt(oid, 10)
	if strings.Contains(strings.ToLower(status.Order.Status), "reject") {
		r.Status = "rejected"
		return nil
	}
	fills, err := s.historyFills(ctx, v, max(int64(0), r.SubmittedAt-120000))
	if err != nil {
		return err
	}
	sort.SliceStable(fills, func(i, j int) bool { return fills[i].Time < fills[j].Time })
	quantity, cost := decimal.Zero, decimal.Zero
	first := int64(0)
	for _, fill := range fills {
		if fill.OID == oid && fill.Coin == p.Request.Instrument {
			if fill.Side != map[string]string{"buy": "B", "sell": "A"}[p.Request.Side] {
				return errors.New("Entry fill direction mismatch")
			}
			q, _ := trading.Numeric(fill.Sz)
			px, _ := trading.Numeric(fill.Px)
			quantity = quantity.Add(q)
			cost = cost.Add(q.Mul(px))
			if first == 0 {
				first = fill.Time
			}
		}
	}
	if !quantity.IsPositive() {
		return errors.New("Entry execution is outside available fill history or has not filled")
	}
	requested, ok := trading.Numeric(p.Request.Quantity)
	if !ok || quantity.GreaterThan(requested) {
		return errors.New("Filled quantity does not match the platform order")
	}
	if known, kOK := trading.Numeric(j.Result.FilledQuantity); kOK && known.IsPositive() && !known.Equal(quantity) {
		return errors.New("Complete entry fill history is unavailable")
	}
	r.Size = quantity.String()
	r.EntryPrice = cost.Div(quantity).String()
	r.OpenDate = first
	m, marketErr := s.market(ctx, v, p.Request)
	factor := p.ProfitPerPriceUnit
	if marketErr == nil && profitFactor(m) != "" {
		if p.AccountCurrency == "" || p.AccountCurrency == m.accountCurrency {
			r.Currency = m.accountCurrency
			factor = profitFactor(m)
			r.ValuationTime = s.now().UnixMilli()
			r.ValuationError = ""
		} else {
			factor = ""
			r.ValuationError = "Settlement currency differs from the recorded trade"
		}
	}
	r.ProfitLoss = ""
	r.ProfitLossState = ""
	r.ProfitLossError = "Position profit/loss is not yet verified"
	trading.ApplyRisk(r, factor)
	if p.Request.Market == "spot" {
		r.Status = "filled"
		r.CloseReason = "Spot orders are executions, not separately closeable positions"
		r.ProfitLossError = "Spot executions have no separately attributable position profit/loss"
		r.CheckedAt = s.now().UnixMilli()
		return nil
	}
	position := decimal.Zero
	mixed := false
	started := false
	closedAt := int64(0)
	realized, realizedKnown := decimal.Zero, true
	for _, fill := range fills {
		if fill.Coin != p.Request.Instrument || fill.Time < first {
			continue
		}
		if !started && fill.OID != oid {
			continue
		}
		start, _ := trading.Numeric(fill.StartPosition)
		if !started {
			position = start
			started = true
			if !position.IsZero() {
				mixed = true
			}
		}
		if !start.Equal(position) {
			return errors.New("Fill history has a position gap; closing is unavailable")
		}
		delta, _ := trading.Numeric(fill.Sz)
		if fill.Side == "A" {
			delta = delta.Neg()
		}
		if fill.OID != oid && position.Sign() == delta.Sign() {
			mixed = true
		}
		if !position.IsZero() && position.Sign() != delta.Sign() {
			value, known := trading.Numeric(fill.ClosedPnL)
			if !known {
				realizedKnown = false
			} else {
				realized = realized.Add(value)
			}
		}
		next := position.Add(delta)
		if !position.IsZero() && (next.IsZero() || next.Sign() != position.Sign()) {
			if !mixed {
				closedAt = fill.Time
			}
			r.Status = "closed"
			r.CloseDate = closedAt
			r.RemainingSize = "0"
			r.CheckedAt = s.now().UnixMilli()
			r.CloseReason = "This trade's exposure is closed"
			trading.ApplyRisk(r, factor)
			if !mixed && realizedKnown && r.Currency != "" {
				r.ProfitLoss = realized.String()
				r.ProfitLossCurrency = r.Currency
				r.ProfitLossTime = s.now().UnixMilli()
				r.ProfitLossState = "closed"
				r.ProfitLossBasis = "Realized Hyperliquid fill P/L; excludes fees and funding"
				r.ProfitLossError = ""
			} else {
				r.ProfitLossError = "Complete individually attributable closing fills or settlement currency are unavailable"
			}
			if mixed {
				r.CloseReason = "The net position closed; its individual close time is unavailable after mixed executions"
			}
			return nil
		}
		position = next
	}
	if marketErr != nil {
		return marketErr
	}
	var state clearinghouseState
	if err = s.post(ctx, v, "/info", map[string]string{"type": "clearinghouseState", "user": v.AccountAddress, "dex": m.dex}, &state); err != nil {
		return err
	}
	live := decimal.Zero
	margin, unrealized := "", ""
	if state.AssetPositions == nil {
		return errors.New("Current position list is unavailable")
	}
	if state.Time != nil && !s.freshObservation(*state.Time) {
		return errors.New("Current position snapshot is stale")
	}
	found := false
	for _, asset := range *state.AssetPositions {
		if asset.Position.Coin == p.Request.Instrument {
			if found {
				return errors.New("Duplicate current position")
			}
			found = true
			unrealized = asset.Position.UnrealizedPnL
			var valid bool
			live, valid = trading.Numeric(asset.Position.Size)
			if !valid {
				return errors.New("Invalid current position")
			}
			if amount, ok := trading.Numeric(asset.Position.MarginUsed); ok && !amount.IsNegative() {
				margin = amount.String()
			}
		}
	}
	if !live.Equal(position) {
		return errors.New("Current position differs from the verified fill history")
	}
	r.Status = "open"
	r.RemainingSize = position.Abs().String()
	r.CheckedAt = s.now().UnixMilli()
	if position.IsZero() {
		r.Status = "closed"
		r.CloseReason = "Position is flat; closing time unavailable"
		return nil
	}
	if mixed {
		r.MarginUsed = ""
		r.RemainingSize = ""
		r.SLAmount = ""
		r.TPAmount = ""
		r.ValuationError = "Other executions share this net position; the trade's remaining size cannot be attributed"
		r.CloseReason = "Other executions share this net position; an individual platform trade cannot be closed safely"
		r.ProfitLossError = "Other executions share this net position; individual trade P/L is unavailable"
		return nil
	}
	r.MarginUsed = margin
	r.MarginCurrency = m.accountCurrency
	r.MarginBasis = "Last observed exchange margin for the verified position"
	trading.ApplyRisk(r, factor)
	if floating, valid := trading.Numeric(unrealized); valid && realizedKnown && r.Currency != "" && r.Currency == m.accountCurrency {
		r.ProfitLoss = realized.Add(floating).String()
		r.ProfitLossCurrency = r.Currency
		r.ProfitLossTime = s.now().UnixMilli()
		r.ProfitLossState = "open"
		r.ProfitLossBasis = "Realized fill P/L plus current unrealized position P/L; excludes fees and funding"
		r.ProfitLossError = ""
	} else {
		r.ProfitLossError = "The exchange has not supplied complete position/fill P/L or settlement currency"
	}
	for _, attempt := range j.Closes {
		if attempt.Status == "filled" {
			total := decimal.Zero
			for _, fill := range fills {
				if strconv.FormatInt(fill.OID, 10) == attempt.OrderID && fill.Coin == p.Request.Instrument {
					q, _ := trading.Numeric(fill.Sz)
					total = total.Add(q)
				}
			}
			expected, valid := trading.Numeric(attempt.Filled)
			if !valid || total.LessThan(expected) {
				r.CloseReason = "Waiting for the previous close fills to reconcile"
				return nil
			}
		}
	}
	for i := range j.Closes {
		close := &j.Closes[i]
		if close.Status == "claimed" || close.Status == "uncertain" || close.Status == "closing" {
			status, e := s.historyOrder(ctx, v, "0x"+close.ID)
			if e != nil {
				return e
			}
			if status.Status == "order" && (status.Order.Order.Cloid != "0x"+close.ID || status.Order.Order.Coin != p.Request.Instrument || !status.Order.Order.ReduceOnly) {
				return errors.New("Close order ownership could not be verified")
			}
			if status.Status == "order" && (status.Order.Status == "filled" || strings.Contains(status.Order.Status, "Canceled") || status.Order.Status == "canceled") {
				close.Status = "confirmed"
			} else {
				r.CloseReason = "A previous close has an uncertain outcome; inspect the exchange before another attempt"
				return nil
			}
		}
	}
	if v.PrivateKey == "" {
		r.CloseReason = "Configure the trading API wallet to close this position"
		return nil
	}
	r.CanClose = true
	r.CloseReason = "Verified CryptoDesk-only remaining exposure"
	return nil
}
func (s *Store) RefreshTrade(ctx context.Context, id string) (trading.Trade, error) {
	unlock := s.lockHistory(id)
	defer unlock()
	j, err := s.readOrderJournal(id)
	if err != nil {
		return trading.Trade{}, err
	}
	err = s.refreshOrder(ctx, id, j)
	if err != nil {
		j.History.CanClose = false
		j.History.Error = err.Error()
		j.History.CloseReason = err.Error()
		if j.History.Status != "closed" {
			j.History.ProfitLoss = ""
			j.History.ProfitLossError = err.Error()
		}
	} else {
		j.History.Error = j.ProtectionWarning
	}
	if writeErr := trading.WriteJournal(s.journalPath(id), j, false); writeErr != nil {
		return j.History, writeErr
	}
	return j.History, nil
}
