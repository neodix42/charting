package hyperliquid

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"cryptodesk/internal/trading"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

func (s *Store) PreviewClose(ctx context.Context, id string) (trading.ClosePreview, error) {
	unlock := s.lockHistory(id)
	defer unlock()
	j, err := s.readOrderJournal(id)
	if err != nil {
		return trading.ClosePreview{}, err
	}
	if err = s.refreshOrder(ctx, id, j); err != nil {
		return trading.ClosePreview{}, err
	}
	if !j.History.CanClose {
		return trading.ClosePreview{}, errors.New(j.History.CloseReason)
	}
	if err = trading.WriteJournal(s.journalPath(id), j, false); err != nil {
		return trading.ClosePreview{}, err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return trading.ClosePreview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings.AccountAddress != j.Preview.AccountAddress || s.settings.Network != j.Preview.Network {
		return trading.ClosePreview{}, errors.New("Hyperliquid account changed; review again")
	}
	if s.closePreviews == nil {
		s.closePreviews = map[string]closePlan{}
	}
	for key, value := range s.closePreviews {
		if value.Preview.ExpiresAt < s.now().UnixMilli() {
			delete(s.closePreviews, key)
		}
	}
	if len(s.closePreviews) >= 100 {
		return trading.ClosePreview{}, errors.New("Too many pending close confirmations")
	}
	preview := trading.ClosePreview{ID: hex.EncodeToString(token[:]), TradeID: j.History.ID, Provider: "hyperliquid", Environment: j.History.Environment, Account: j.History.Account, Instrument: j.History.Instrument, OrderID: j.History.OrderID, Quantity: j.History.RemainingSize, ExpiresAt: s.now().Add(time.Minute).UnixMilli(), Warnings: []string{"Market exit uses a reduce-only IOC order capped at 1% slippage; partial fills are possible.", "Hyperliquid maintains a net position. Concurrent trading outside CryptoDesk can change the position during execution."}}
	s.closePreviews[preview.ID] = closePlan{Preview: preview, Settings: s.settings, Quantity: preview.Quantity, EntryID: id}
	return preview, nil
}

func (s *Store) ConfirmClose(ctx context.Context, token string) (trading.Trade, error) {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return trading.Trade{}, ctx.Err()
	}
	s.mu.Lock()
	plan, ok := s.closePreviews[token]
	delete(s.closePreviews, token)
	same := plan.Settings == s.settings
	s.mu.Unlock()
	if !ok || !same || s.now().UnixMilli() >= plan.Preview.ExpiresAt {
		return trading.Trade{}, errors.New("Close confirmation expired, was used, or the account changed")
	}
	unlock := s.lockHistory(plan.EntryID)
	defer unlock()
	j, err := s.readOrderJournal(plan.EntryID)
	if err != nil {
		return trading.Trade{}, err
	}
	if err = s.refreshOrder(ctx, plan.EntryID, j); err != nil {
		return j.History, err
	}
	if !j.History.CanClose || j.History.RemainingSize != plan.Quantity {
		return j.History, errors.New("Position changed; refresh and review the close again")
	}
	if err = s.checkAgent(ctx, plan.Settings); err != nil {
		return j.History, err
	}
	request := j.Preview.Request
	request.StopLoss = ""
	request.TakeProfit = ""
	request.Quantity = plan.Quantity
	if request.Side == "buy" {
		request.Side = "sell"
	} else {
		request.Side = "buy"
	}
	m, err := s.market(ctx, plan.Settings, request)
	if err != nil {
		return j.History, err
	}
	quote, err := s.quote(ctx, plan.Settings, request)
	if err != nil {
		return j.History, err
	}
	quantity, qErr := positive(plan.Quantity)
	if qErr != nil || !quantity.Equal(quantity.Round(int32(m.szDecimals))) {
		return j.History, errors.New("Remaining size does not match current exchange size rules")
	}
	limit := quote.Mul(decimal.NewFromFloat(0.99))
	buy := request.Side == "buy"
	if buy {
		limit = quote.Mul(decimal.NewFromFloat(1.01))
	}
	precision := priceDecimals(limit, m.szDecimals, false)
	if buy {
		limit = limit.RoundFloor(precision)
	} else {
		limit = limit.RoundCeil(precision)
	}
	s.mu.Lock()
	same = s.settings == plan.Settings
	s.mu.Unlock()
	if !same || s.now().UnixMilli() >= plan.Preview.ExpiresAt {
		return j.History, errors.New("Account changed or close review expired; review again")
	}
	if len(j.Closes) >= 100 {
		return j.History, errors.New("Too many close attempts for this trade")
	}
	j.Closes = append(j.Closes, trading.CloseAttempt{ID: token, Quantity: plan.Quantity, CreatedAt: s.now().UnixMilli(), Status: "claimed"})
	attempt := &j.Closes[len(j.Closes)-1]
	j.History.CanClose = false
	j.History.Status = "closing"
	j.History.CloseReason = "Awaiting exchange close confirmation"
	if err = trading.WriteJournal(s.journalPath(plan.EntryID), j, false); err != nil {
		return j.History, errors.New("Could not persist the close intent; no close order sent")
	}
	response, callErr := s.exchange(ctx, plan.Settings, wire{{"type", "order"}, {"orders", []wire{orderWire(m, buy, limit.String(), plan.Quantity, true, wire{{"limit", wire{{"tif", "Ioc"}}}}, "0x"+token)}}, {"grouping", "na"}}, uint64(plan.Preview.ExpiresAt))
	attempt.Status = "uncertain"
	j.History.Error = "Close submission outcome is uncertain; no automatic resend will be made"
	if callErr == nil {
		var out struct {
			Type string `json:"type"`
			Data struct {
				Statuses []json.RawMessage `json:"statuses"`
			} `json:"data"`
		}
		if json.Unmarshal(response, &out) == nil && out.Type == "order" && len(out.Data.Statuses) == 1 {
			var status struct {
				Error  string `json:"error"`
				Filled *struct {
					OID     int64  `json:"oid"`
					TotalSz string `json:"totalSz"`
					AvgPx   string `json:"avgPx"`
				} `json:"filled"`
			}
			if json.Unmarshal(out.Data.Statuses[0], &status) == nil {
				if status.Error != "" {
					attempt.Status = "rejected"
					attempt.Message = status.Error
					j.History.Status = "open"
					j.History.Error = "Close rejected: " + status.Error
				}
				if status.Filled != nil {
					filled, fErr := positive(status.Filled.TotalSz)
					_, priceErr := positive(status.Filled.AvgPx)
					if fErr == nil && priceErr == nil && filled.LessThanOrEqual(quantity) && status.Filled.OID > 0 {
						attempt.Status = "filled"
						attempt.OrderID = strconv.FormatInt(status.Filled.OID, 10)
						attempt.Filled = filled.String()
						j.History.RemainingSize = quantity.Sub(filled).String()
						j.History.Error = ""
						j.History.CloseReason = "Close fill confirmed; refreshing exchange close time"
						if filled.Equal(quantity) {
							if cancelErr := s.cancelOwnProtection(ctx, plan.Settings, j, m, uint64(plan.Preview.ExpiresAt)); cancelErr != nil {
								attempt.Message = "Close filled, but protective-order cancellation was not confirmed: " + cancelErr.Error()
								j.History.Error = attempt.Message
								j.ProtectionWarning = attempt.Message
							}
						}
					}
				}
			}
		}
	}
	if err = trading.WriteJournal(s.journalPath(plan.EntryID), j, false); err != nil {
		j.History.Error += " The close result could not be saved; inspect the exchange before any further action"
	}
	return j.History, nil
}
func (s *Store) cancelOwnProtection(ctx context.Context, v Settings, j *orderJournal, m instrument, expiry uint64) error {
	cancels := []wire{}
	for _, value := range []struct{ price, kind string }{{j.Preview.Request.StopLoss, "sl"}, {j.Preview.Request.TakeProfit, "tp"}} {
		if value.price == "" {
			continue
		}
		sum := sha256.Sum256([]byte(j.Preview.ID + value.kind))
		cancels = append(cancels, wire{{"asset", m.asset}, {"cloid", "0x" + hex.EncodeToString(sum[:16])}})
	}
	if len(cancels) == 0 {
		return nil
	}
	raw, err := s.exchange(ctx, v, wire{{"type", "cancelByCloid"}, {"cancels", cancels}}, expiry)
	if err != nil {
		return err
	}
	var out struct {
		Type string `json:"type"`
		Data struct {
			Statuses []json.RawMessage `json:"statuses"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Type != "cancel" || len(out.Data.Statuses) != len(cancels) {
		return errors.New("unrecognized cancellation response")
	}
	for _, status := range out.Data.Statuses {
		var text string
		if json.Unmarshal(status, &text) != nil || text != "success" {
			return fmt.Errorf("verify protective orders on the exchange")
		}
	}
	return nil
}
