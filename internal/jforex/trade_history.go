package jforex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"cryptodesk/internal/trading"
	"encoding/hex"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type brokerOrder struct {
	Intent              string   `json:"intent"`
	OrderID             string   `json:"orderID"`
	Instrument          string   `json:"instrument"`
	Side                string   `json:"side"`
	Status              string   `json:"status"`
	OriginalQuantity    float64  `json:"originalQuantity"`
	FilledQuantity      float64  `json:"filledQuantity"`
	RemainingQuantity   float64  `json:"remainingQuantity"`
	EntryPrice          float64  `json:"entryPrice"`
	ExitPrice           float64  `json:"exitPrice,omitempty"`
	StopLoss            float64  `json:"stopLoss"`
	TakeProfit          float64  `json:"takeProfit"`
	OpenDate            int64    `json:"openDate"`
	CloseDate           int64    `json:"closeDate"`
	AccountCurrency     string   `json:"accountCurrency,omitempty"`
	ProfitPerPriceUnit  *float64 `json:"profitPerPriceUnit,omitempty"`
	ValuationTime       int64    `json:"valuationTime,omitempty"`
	ValuationError      string   `json:"valuationError,omitempty"`
	ProfitLoss          *float64 `json:"profitLoss,omitempty"`
	ProfitLossTime      int64    `json:"profitLossTime,omitempty"`
	ProfitLossError     string   `json:"profitLossError,omitempty"`
	PipSize             float64  `json:"pipSize,omitempty"`
	ProfitLossPips      *float64 `json:"profitLossPips,omitempty"`
	ProfitLossPipsTime  int64    `json:"profitLossPipsTime,omitempty"`
	ProfitLossPipsError string   `json:"profitLossPipsError,omitempty"`
}
type brokerJournal struct {
	ID          string                 `json:"id"`
	Request     TradeRequest           `json:"request"`
	Environment string                 `json:"environment"`
	Account     string                 `json:"account,omitempty"`
	CreatedAt   int64                  `json:"createdAt"`
	Status      string                 `json:"status"`
	Preview     TradePreview           `json:"preview"`
	Result      TradeResult            `json:"result"`
	History     trading.Trade          `json:"history"`
	Closes      []trading.CloseAttempt `json:"closes,omitempty"`
}
type closeConfirmation struct {
	Preview trading.ClosePreview
	Config  configuration
	EntryID string
}

func accountKey(config configuration) string {
	sum := sha256.Sum256([]byte(config.Username))
	return hex.EncodeToString(sum[:16])
}
func numberText(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return ""
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
func optionalPrice(value float64) string {
	if value <= 0 {
		return ""
	}
	return numberText(value)
}
func (m *Manager) lockTradeHistory(id string) func() {
	value, _ := m.historyLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
func (m *Manager) tradeJournalPath(id string) string {
	return filepath.Join(filepath.Dir(m.path), "dukascopy-trade-claims", id+".json")
}
func newBrokerHistory(j brokerJournal) trading.Trade {
	direction := "long"
	if j.Request.Side == "sell" {
		direction = "short"
	}
	r := trading.Trade{ID: "dukascopy:" + j.ID, Provider: "dukascopy", Environment: j.Environment, Account: j.Account, Instrument: j.Request.Instrument, Market: "jforex", Direction: direction, SubmittedAt: j.CreatedAt, Size: numberText(j.Request.Quantity), StopLoss: optionalPrice(j.Request.StopLoss), TakeProfit: optionalPrice(j.Request.TakeProfit), Currency: j.Preview.AccountCurrency, Status: "unconfirmed", CloseReason: "Refresh to verify this platform position", MarginBasis: "JForex does not expose reliable per-position margin"}
	if r.StopLoss != "" {
		r.StopLossSource = "submitted"
	}
	if r.TakeProfit != "" {
		r.TakeProfitSource = "submitted"
	}
	return r
}

// Terminal SDK orders can report zero once protective orders have disappeared.
// Keep recorded levels for history, while a zero on a live order means removed.
func applyBrokerProtection(price, source *string, current float64, closed bool) {
	if current > 0 {
		*price, *source = optionalPrice(current), "exchange"
	} else if !closed {
		*price, *source = "", ""
	} else if *price != "" && *source != "submitted" {
		*source = "recorded"
	}
}

func recoverClosedBrokerProtection(j *brokerJournal) {
	r := &j.History
	if r.Status != "closed" || r.CloseDate <= 0 {
		return
	}
	if r.StopLoss == "" && j.Request.StopLoss > 0 {
		r.StopLoss, r.StopLossSource = optionalPrice(j.Request.StopLoss), "submitted"
	}
	if r.TakeProfit == "" && j.Request.TakeProfit > 0 {
		r.TakeProfit, r.TakeProfitSource = optionalPrice(j.Request.TakeProfit), "submitted"
	}
}
func applyBrokerSubmission(j *brokerJournal) {
	r := &j.History
	result := j.Result
	if result.Status == "rejected" {
		r.Status = "rejected"
		return
	}
	if result.FilledQuantity > 0 {
		r.Size = numberText(result.FilledQuantity)
		r.RemainingSize = r.Size
		r.EntryPrice = optionalPrice(result.EntryPrice)
		r.Status = "open"
	}
	if result.OrderID != "" {
		r.OrderID = result.OrderID
	}
	if metrics := result.ExecutionMetrics; metrics != nil {
		normalizeTradeMetrics(metrics)
		r.BalanceCurrency = metrics.AccountCurrency
		if metrics.AccountCurrency != "" {
			r.Currency = metrics.AccountCurrency
		}
		r.BalanceTime = metrics.AccountTime
		r.BalanceBasis = metrics.AccountNote
		if metrics.Balance != nil {
			r.BalanceAtOpen = numberText(*metrics.Balance)
		}
	}
	if result.History != nil {
		_ = applyBrokerSnapshot(j, *result.History)
	}
	factor := numberText(j.Preview.ProfitPerPriceUnit)
	if j.Preview.ValuationError != "" || r.Currency != j.Preview.AccountCurrency {
		factor = ""
	}
	trading.ApplyRisk(r, factor)
}
func applyBrokerSnapshot(j *brokerJournal, order brokerOrder) error {
	if order.Intent != j.ID || order.OrderID == "" || j.History.OrderID != "" && j.History.OrderID != order.OrderID || order.Instrument != j.Request.Instrument || order.Side != j.Request.Side || math.Abs(order.OriginalQuantity-j.Request.Quantity) > math.Max(1e-7, j.Request.Quantity*1e-8) {
		return errors.New("JForex order does not match the local platform submission")
	}
	for _, value := range []float64{order.OriginalQuantity, order.FilledQuantity, order.RemainingQuantity, order.EntryPrice, order.StopLoss, order.TakeProfit} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return errors.New("Invalid JForex order values")
		}
	}
	if order.FilledQuantity > j.Request.Quantity+math.Max(1e-7, j.Request.Quantity*1e-8) || order.RemainingQuantity > j.Request.Quantity+math.Max(1e-7, j.Request.Quantity*1e-8) {
		return errors.New("JForex order size differs from the platform request")
	}
	if order.OpenDate < 0 || order.CloseDate < 0 || order.OpenDate > time.Now().Add(5*time.Second).UnixMilli() || order.CloseDate > time.Now().Add(5*time.Second).UnixMilli() {
		return errors.New("Invalid JForex execution timestamps")
	}
	switch order.Status {
	case "open", "closed", "submitted", "cancelled", "closing":
	default:
		return errors.New("Unrecognized JForex position status")
	}
	r := &j.History
	previousStatus := r.Status
	r.OrderID = order.OrderID
	r.OpenDate = order.OpenDate
	r.CloseDate = order.CloseDate
	r.Status = order.Status
	r.RemainingSize = numberText(order.RemainingQuantity)
	r.EntryPrice = optionalPrice(order.EntryPrice)
	if order.Status != "closed" || previousStatus != "closed" {
		r.ExitPrice = ""
	}
	if order.Status == "closed" && order.CloseDate > 0 && order.ExitPrice > 0 && !math.IsInf(order.ExitPrice, 0) && !math.IsNaN(order.ExitPrice) {
		r.ExitPrice = optionalPrice(order.ExitPrice)
	}
	applyBrokerProtection(&r.StopLoss, &r.StopLossSource, order.StopLoss, order.Status == "closed")
	applyBrokerProtection(&r.TakeProfit, &r.TakeProfitSource, order.TakeProfit, order.Status == "closed")
	recoverClosedBrokerProtection(j)
	if order.FilledQuantity > 0 {
		r.Size = numberText(order.FilledQuantity)
	}
	factor := r.ProfitPerPriceUnit
	if factor == "" && j.Preview.ValuationError == "" && r.Currency == j.Preview.AccountCurrency {
		factor = numberText(j.Preview.ProfitPerPriceUnit)
	}
	currencyKnown := len(order.AccountCurrency) >= 3 && order.AccountCurrency[0] >= 'A' && order.AccountCurrency[0] <= 'Z' && len(order.AccountCurrency) <= 8 && strings.Trim(order.AccountCurrency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") == ""
	if currencyKnown && (order.ProfitPerPriceUnit != nil || order.ValuationTime != 0 || order.ValuationError != "") {
		previousCurrency := r.Currency
		currency, amount, at, problem := order.AccountCurrency, 0.0, order.ValuationTime, order.ValuationError
		if order.ProfitPerPriceUnit != nil {
			amount = *order.ProfitPerPriceUnit
		}
		normalizeValuation(&currency, &amount, &at, &problem)
		if currency != "" {
			r.Currency = currency
		}
		r.ValuationTime, r.ValuationError = at, problem
		if amount > 0 && problem == "" {
			factor = numberText(amount)
		} else if currency != previousCurrency {
			factor = ""
		}
	}
	if currencyKnown && order.AccountCurrency != r.Currency {
		r.Currency = order.AccountCurrency
		factor = ""
	}
	trading.ApplyRisk(r, factor)
	if order.ProfitLoss != nil && !math.IsNaN(*order.ProfitLoss) && !math.IsInf(*order.ProfitLoss, 0) && currencyKnown && r.Currency == order.AccountCurrency && order.ProfitLossTime >= time.Now().Add(-30*time.Second).UnixMilli() && order.ProfitLossTime <= time.Now().Add(5*time.Second).UnixMilli() && (order.Status == "open" || order.Status == "closed" || order.Status == "closing") {
		r.ProfitLoss = numberText(*order.ProfitLoss)
		r.ProfitLossCurrency = order.AccountCurrency
		r.ProfitLossTime = order.ProfitLossTime
		r.ProfitLossState = order.Status
		r.ProfitLossBasis = "JForex SDK reported profit/loss in account currency"
		r.ProfitLossError = ""
	} else if r.Status != "closed" || previousStatus != "closed" || r.ProfitLoss == "" {
		r.ProfitLoss = ""
		r.ProfitLossState = ""
		r.ProfitLossError = order.ProfitLossError
		if r.ProfitLossError == "" {
			r.ProfitLossError = "Refresh using the matching updated JForex bridge to retrieve position profit/loss"
		}
	}
	if order.PipSize > 0 && !math.IsInf(order.PipSize, 0) && !math.IsNaN(order.PipSize) {
		r.PipSize = numberText(order.PipSize)
	}
	if order.ProfitLossPips != nil && !math.IsNaN(*order.ProfitLossPips) && !math.IsInf(*order.ProfitLossPips, 0) && order.ProfitLossPipsTime >= time.Now().Add(-30*time.Second).UnixMilli() && order.ProfitLossPipsTime <= time.Now().Add(5*time.Second).UnixMilli() && (order.Status == "open" || order.Status == "closing" || order.Status == "closed" && order.CloseDate > 0) {
		r.ProfitLossPips = numberText(*order.ProfitLossPips)
		r.ProfitLossPipsUnit = "pips"
		r.ProfitLossPipsTime = order.ProfitLossPipsTime
		r.ProfitLossPipsState = order.Status
		r.ProfitLossPipsBasis = "JForex SDK reported profit/loss in instrument pips (rounded by the broker to 0.1 pip)"
		r.ProfitLossPipsError = ""
	} else if r.Status != "closed" || previousStatus != "closed" || r.ProfitLossPips == "" {
		r.ProfitLossPips = ""
		r.ProfitLossPipsState = ""
		r.ProfitLossPipsError = order.ProfitLossPipsError
		if r.ProfitLossPipsError == "" {
			r.ProfitLossPipsError = "Refresh using the matching updated JForex bridge to retrieve instrument pips"
		}
	}
	return nil
}
func (m *Manager) readTradeJournal(id string) (*brokerJournal, error) {
	if !trading.ValidIntent(id) {
		return nil, errors.New("Invalid platform trade identity")
	}
	var j brokerJournal
	if err := trading.ReadJournal(m.tradeJournalPath(id), &j); err != nil {
		return nil, err
	}
	if j.ID != id || j.Environment != "demo" && j.Environment != "live" || validateTradeRequest(j.Request) != nil || j.CreatedAt <= 0 {
		return nil, errors.New("Invalid Dukascopy trade journal; original preserved")
	}
	if j.History.ID == "" {
		j.History = newBrokerHistory(j)
		applyBrokerSubmission(&j)
	}
	if j.History.ID != "dukascopy:"+id || j.History.Environment != j.Environment || j.History.Account != j.Account || j.History.Instrument != j.Request.Instrument || len(j.Closes) > 100 {
		return nil, errors.New("Dukascopy journal identity mismatch; original preserved")
	}
	// Repair old rows in memory before login/network work, without rewriting the
	// claim on a read. The next ordinary successful refresh persists the repair.
	if j.History.Status == "closed" && j.History.CloseDate > 0 {
		recoverClosedBrokerProtection(&j)
		factor := j.History.ProfitPerPriceUnit
		if factor == "" && j.History.Currency != "" && j.History.Currency == j.Preview.AccountCurrency && j.Preview.ValuationError == "" {
			factor = numberText(j.Preview.ProfitPerPriceUnit)
		}
		trading.ApplyRisk(&j.History, factor)
	}
	return &j, nil
}
func (m *Manager) TradeHistory() ([]trading.Trade, []string) {
	ids, err := trading.JournalIDs(filepath.Join(filepath.Dir(m.path), "dukascopy-trade-claims"))
	if err != nil {
		return nil, []string{err.Error()}
	}
	rows := []trading.Trade{}
	problems := []string{}
	m.mu.Lock()
	config := m.config
	m.mu.Unlock()
	for _, id := range ids {
		unlock := m.lockTradeHistory(id)
		j, e := m.readTradeJournal(id)
		unlock()
		if e != nil {
			problems = append(problems, "Dukascopy journal "+id+": "+e.Error())
			continue
		}
		r := j.History
		if r.Status == "rejected" || r.Status == "cancelled" && r.OpenDate == 0 {
			continue
		}
		if j.Account != accountKey(config) || j.Environment != config.Environment || config.Password == "" {
			r.CanClose = false
			r.CloseReason = "Configure the matching JForex account/environment and refresh to verify this position"
		} else if time.Now().UnixMilli()-r.CheckedAt > 30000 {
			r.CanClose = false
			r.CloseReason = "Refresh exchange status before closing"
		}
		if j.Account == accountKey(config) && j.Environment == config.Environment {
			r.Account = config.Username
		}
		rows = append(rows, r)
	}
	return rows, problems
}

// The journal is atomically published; local attachments do not wait on the
// exchange refresh lock or initiate an account connection.
func (m *Manager) LocalTrade(id string) (trading.Trade, error) {
	j, err := m.readTradeJournal(id)
	if err != nil {
		return trading.Trade{}, err
	}
	return j.History, nil
}
func verifyHistoryBridge(ctx context.Context, p *bridgeProcess) error {
	var version bridgeVersion
	if err := p.call(ctx, map[string]any{"op": "version"}, &version); err != nil {
		return err
	}
	if version.TradeHistory != "platform-orders-v1" {
		return errors.New("Install this build's matching JForex bridge and restart CryptoDesk to read or close platform trades")
	}
	return nil
}
func (m *Manager) refreshBroker(ctx context.Context, j *brokerJournal, includeValuation bool) error {
	r := &j.History
	r.CanClose = false
	m.mu.Lock()
	config := m.config
	m.mu.Unlock()
	if config.Environment != j.Environment || j.Account != "" && j.Account != accountKey(config) {
		return errors.New("Configure the JForex account/environment that opened this trade")
	}
	p, release, err := m.begin(ctx, true, "")
	if err != nil {
		return err
	}
	defer release()
	if err = verifyHistoryBridge(ctx, p); err != nil {
		return err
	}
	request := tradeRPC(j.Request, "tradeHistory")
	request["intent"] = j.ID
	request["includeValuation"] = includeValuation
	if r.OrderID != "" {
		request["orderID"] = r.OrderID
	}
	request["from"] = j.CreatedAt
	var order brokerOrder
	if err = p.call(ctx, request, &order); err != nil {
		return err
	}
	if order.Status == "not-found" {
		return errors.New("This platform order was not found in the connected JForex account")
	}
	if err = applyBrokerSnapshot(j, order); err != nil {
		return err
	}
	m.mu.Lock()
	same := m.config == config && !m.closed
	confirmation := m.tradePreviews[j.ID]
	submitting := confirmation != nil && confirmation.used && confirmation.result == nil
	m.mu.Unlock()
	if !same {
		return errors.New("JForex account changed during trade refresh")
	}
	if j.Account == "" {
		j.Account = accountKey(config)
		r.Account = j.Account
	}
	r.CheckedAt = time.Now().UnixMilli()
	r.CloseReason = "Position is already closed or has not filled"
	if r.Status == "open" && order.RemainingQuantity > 0 {
		if submitting {
			r.CloseReason = "The opening order is still being confirmed; refresh before closing"
			return nil
		}
		for _, attempt := range j.Closes {
			if attempt.Status == "claimed" || attempt.Status == "uncertain" || attempt.Status == "closing" {
				r.CloseReason = "A close is awaiting confirmation; inspect the exact broker position before another attempt"
				return nil
			}
		}
		r.CanClose = true
		r.CloseReason = "Verified platform order ID and label"
	}
	return nil
}
func (m *Manager) RefreshTrade(ctx context.Context, id string) (trading.Trade, error) {
	unlock := m.lockTradeHistory(id)
	defer unlock()
	j, err := m.readTradeJournal(id)
	if err != nil {
		return trading.Trade{}, err
	}
	err = m.refreshBroker(ctx, j, true)
	if err != nil {
		j.History.CanClose = false
		j.History.Error = err.Error()
		j.History.CloseReason = err.Error()
		if j.History.Status != "closed" {
			j.History.ProfitLoss = ""
			j.History.ProfitLossError = err.Error()
			j.History.ProfitLossPips = ""
			j.History.ProfitLossPipsError = err.Error()
		}
	} else {
		j.History.Error = ""
	}
	if err = trading.WriteJournal(m.tradeJournalPath(id), j, false); err != nil {
		return j.History, err
	}
	row := j.History
	m.mu.Lock()
	if j.Account == accountKey(m.config) && j.Environment == m.config.Environment {
		row.Account = m.config.Username
	}
	m.mu.Unlock()
	return row, nil
}
func (m *Manager) PreviewClose(ctx context.Context, id string) (trading.ClosePreview, error) {
	unlock := m.lockTradeHistory(id)
	defer unlock()
	j, err := m.readTradeJournal(id)
	if err != nil {
		return trading.ClosePreview{}, err
	}
	if err = m.refreshBroker(ctx, j, false); err != nil {
		return trading.ClosePreview{}, err
	}
	if !j.History.CanClose {
		return trading.ClosePreview{}, errors.New(j.History.CloseReason)
	}
	if err = trading.WriteJournal(m.tradeJournalPath(id), j, false); err != nil {
		return trading.ClosePreview{}, err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return trading.ClosePreview{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j.Account != accountKey(m.config) || j.Environment != m.config.Environment {
		return trading.ClosePreview{}, errors.New("JForex account changed; review again")
	}
	if m.closePreviews == nil {
		m.closePreviews = map[string]closeConfirmation{}
	}
	for key, value := range m.closePreviews {
		if value.Preview.ExpiresAt < time.Now().UnixMilli() {
			delete(m.closePreviews, key)
		}
	}
	if len(m.closePreviews) >= 100 {
		return trading.ClosePreview{}, errors.New("Too many pending close confirmations")
	}
	preview := trading.ClosePreview{ID: hex.EncodeToString(token[:]), TradeID: j.History.ID, Provider: "dukascopy", Environment: j.Environment, Account: m.config.Username, Instrument: j.Request.Instrument, OrderID: j.History.OrderID, Quantity: j.History.RemainingSize, ExpiresAt: time.Now().Add(time.Minute).UnixMilli(), Warnings: []string{"Closes only this verified JForex position ID at market. Final execution price is determined by the broker."}}
	m.closePreviews[preview.ID] = closeConfirmation{Preview: preview, Config: m.config, EntryID: id}
	return preview, nil
}
func (m *Manager) ConfirmClose(ctx context.Context, token string) (trading.Trade, error) {
	m.mu.Lock()
	plan, ok := m.closePreviews[token]
	delete(m.closePreviews, token)
	same := m.config == plan.Config && !m.closed
	m.mu.Unlock()
	if !ok || !same || time.Now().UnixMilli() >= plan.Preview.ExpiresAt {
		return trading.Trade{}, errors.New("Close confirmation expired, was used, or the account changed")
	}
	unlock := m.lockTradeHistory(plan.EntryID)
	defer unlock()
	j, err := m.readTradeJournal(plan.EntryID)
	if err != nil {
		return trading.Trade{}, err
	}
	if err = m.refreshBroker(ctx, j, false); err != nil {
		return j.History, err
	}
	if !j.History.CanClose || j.History.RemainingSize != plan.Preview.Quantity {
		return j.History, errors.New("Position changed; refresh and review again")
	}
	p, release, err := m.begin(ctx, true, "")
	if err != nil {
		return j.History, err
	}
	defer release()
	if err = verifyHistoryBridge(ctx, p); err != nil {
		return j.History, err
	}
	m.mu.Lock()
	same = m.config == plan.Config && !m.closed
	m.mu.Unlock()
	if !same || time.Now().UnixMilli() >= plan.Preview.ExpiresAt {
		return j.History, errors.New("Account changed or close review expired")
	}
	if len(j.Closes) >= 100 {
		return j.History, errors.New("Too many close attempts for this position")
	}
	j.Closes = append(j.Closes, trading.CloseAttempt{ID: token, Quantity: plan.Preview.Quantity, CreatedAt: time.Now().UnixMilli(), Status: "claimed"})
	attempt := &j.Closes[len(j.Closes)-1]
	j.History.CanClose = false
	j.History.Status = "closing"
	j.History.CloseReason = "Awaiting broker close confirmation"
	if err = trading.WriteJournal(m.tradeJournalPath(plan.EntryID), j, false); err != nil {
		return j.History, errors.New("Could not persist close intent; no close request sent")
	}
	request := tradeRPC(j.Request, "tradeClose")
	request["intent"] = j.ID
	request["orderID"] = j.History.OrderID
	request["closeIntent"] = token
	remaining, _ := strconv.ParseFloat(plan.Preview.Quantity, 64)
	request["remainingQuantity"] = remaining
	request["from"] = j.CreatedAt
	var order brokerOrder
	if err = p.call(ctx, request, &order); err != nil {
		attempt.Status = "uncertain"
		j.History.Error = "Broker close outcome is uncertain; no automatic resend will be made"
	} else if err = applyBrokerSnapshot(j, order); err != nil {
		attempt.Status = "uncertain"
		j.History.Error = err.Error()
	} else {
		attempt.Status = order.Status
		if order.Status == "closed" {
			attempt.Time = order.CloseDate
		}
		j.History.Error = ""
	}
	if err = trading.WriteJournal(m.tradeJournalPath(plan.EntryID), j, false); err != nil {
		j.History.Error += " Close result could not be saved; inspect the broker before another action"
	}
	row := j.History
	row.Account = plan.Config.Username
	return row, nil
}
