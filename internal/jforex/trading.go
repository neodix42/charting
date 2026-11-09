package jforex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Quantity is the instrument's base units, not FX lots or SDK millions.
// Leverage is 0 to use the fixed account leverage; it cannot be changed per order.
type TradeRequest struct {
	Instrument string  `json:"instrument"`
	Side       string  `json:"side"`
	Quantity   float64 `json:"quantity"`
	StopLoss   float64 `json:"stopLoss"`
	TakeProfit float64 `json:"takeProfit"`
	Leverage   float64 `json:"leverage"`
}
type TradeEstimate struct {
	Instrument         string  `json:"instrument"`
	Side               string  `json:"side"`
	EntryPrice         float64 `json:"entryPrice"`
	AccountCurrency    string  `json:"accountCurrency"`
	ProfitPerPriceUnit float64 `json:"profitPerPriceUnit"`
	ValuationTime      int64   `json:"valuationTime"`
	ValuationError     string  `json:"valuationError,omitempty"`
	ValuationSource    string  `json:"valuationSource,omitempty"`
	ValuationNotice    string  `json:"valuationNotice,omitempty"`
}

type TradePreview struct {
	Metrics            *TradeMetrics `json:"metrics,omitempty"`
	AccountCurrency    string        `json:"accountCurrency,omitempty"`
	ProfitPerPriceUnit float64       `json:"profitPerPriceUnit,omitempty"`
	ValuationTime      int64         `json:"valuationTime,omitempty"`
	ValuationError     string        `json:"valuationError,omitempty"`
	ValuationSource    string        `json:"valuationSource,omitempty"`
	ValuationNotice    string        `json:"valuationNotice,omitempty"`
	ID                 string        `json:"id"`
	Instrument         string        `json:"instrument"`
	Side               string        `json:"side"`
	Quantity           float64       `json:"quantity"`
	StopLoss           float64       `json:"stopLoss"`
	TakeProfit         float64       `json:"takeProfit"`
	EntryPrice         float64       `json:"entryPrice"`
	AccountLeverage    float64       `json:"accountLeverage"`
	MinQuantity        float64       `json:"minQuantity"`
	QuantityStep       float64       `json:"quantityStep"`
	PriceStep          float64       `json:"priceStep"`
	ExpiresAt          int64         `json:"expiresAt"`
	Environment        string        `json:"environment"`
	Message            string        `json:"message"`
}
type TradeResult struct {
	Status              string  `json:"status"`
	OrderID             string  `json:"orderID"`
	FilledQuantity      float64 `json:"filledQuantity"`
	EntryPrice          float64 `json:"entryPrice"`
	ProtectionConfirmed bool    `json:"protectionConfirmed"`
	Message             string  `json:"message"`
}
type tradeConfirmation struct {
	preview TradePreview
	request TradeRequest
	config  configuration
	used    bool
	result  *TradeResult
}

func validateTradeRequest(request TradeRequest) error {
	if request.Instrument == "" || len(request.Instrument) > 128 || strings.ContainsFunc(request.Instrument, unicode.IsControl) || request.Side != "buy" && request.Side != "sell" {
		return errors.New("Select a valid Dukascopy instrument and Buy or Sell")
	}
	for _, value := range []float64{request.Quantity, request.StopLoss, request.TakeProfit, request.Leverage} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return errors.New("Enter finite nonnegative trade values")
		}
	}
	if request.Quantity <= 0 {
		return errors.New("Enter a positive trade quantity in base units")
	}
	return nil
}
func tradeRPC(request TradeRequest, op string) map[string]any {
	return map[string]any{"op": op, "instrument": request.Instrument, "side": request.Side, "quantity": request.Quantity, "stopLoss": request.StopLoss, "takeProfit": request.TakeProfit, "leverage": request.Leverage}
}
func verifyTradingBridge(ctx context.Context, process *bridgeProcess) error {
	var version bridgeVersion
	if err := process.call(ctx, map[string]any{"op": "version"}, &version); err != nil {
		return err
	}
	if version.Trading != "market-bracket-v1" {
		return errors.New("The installed JForex bridge does not support trading. Install this build's matching jforex-runtime using scripts/install-user.sh, then restart CryptoDesk")
	}
	return nil
}

// EstimateTrade reads a current executable quote and SDK account-currency
// valuation without creating a confirmation token, claim or order.
func (m *Manager) EstimateTrade(ctx context.Context, instrument, side string) (TradeEstimate, error) {
	if err := validateTradeRequest(TradeRequest{Instrument: instrument, Side: side, Quantity: 1}); err != nil {
		return TradeEstimate{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	process, release, err := m.begin(ctx, true, "")
	if err != nil {
		return TradeEstimate{}, err
	}
	defer release()
	var version bridgeVersion
	if err = process.call(ctx, map[string]any{"op": "version"}, &version); err != nil {
		return TradeEstimate{}, err
	}
	if version.TradeEstimates != "account-currency-pnl-v1" {
		return TradeEstimate{}, errors.New("The installed JForex bridge does not support account-currency trade estimates. Install this build's matching jforex-runtime, then restart CryptoDesk")
	}
	m.mu.Lock()
	config := m.config
	m.mu.Unlock()
	var estimate TradeEstimate
	if err = process.call(ctx, map[string]any{"op": "tradeEstimate", "instrument": instrument, "side": side}, &estimate); err != nil {
		return TradeEstimate{}, err
	}
	if estimate.Instrument != instrument || estimate.Side != side || estimate.EntryPrice <= 0 || math.IsNaN(estimate.EntryPrice) || math.IsInf(estimate.EntryPrice, 0) {
		return TradeEstimate{}, errors.New("The JForex bridge returned an invalid trade estimate")
	}
	normalizeValuation(&estimate.AccountCurrency, &estimate.ProfitPerPriceUnit, &estimate.ValuationTime, &estimate.ValuationError)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.config != config {
		return TradeEstimate{}, errors.New("Dukascopy account changed during valuation; refresh the estimate")
	}
	return estimate, nil
}
func normalizeValuation(currency *string, factor *float64, at *int64, problem *string) {
	if *problem != "" {
		*factor = 0
		*at = 0
		return
	}
	now := time.Now().UnixMilli()
	if len(*currency) < 3 || len(*currency) > 8 || strings.Trim(*currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" || *factor <= 0 || math.IsNaN(*factor) || math.IsInf(*factor, 0) || *at < now-30000 || *at > now+5000 {
		*factor = 0
		*at = 0
		*problem = "Account-currency valuation is unavailable or stale; no conversion estimate was assumed"
	}
}

func (m *Manager) PreviewTrade(ctx context.Context, request TradeRequest) (TradePreview, error) {
	if err := validateTradeRequest(request); err != nil {
		return TradePreview{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	process, release, err := m.begin(ctx, true, "")
	if err != nil {
		return TradePreview{}, err
	}
	defer release()
	if err = verifyTradingBridge(ctx, process); err != nil {
		return TradePreview{}, err
	}
	m.mu.Lock()
	config := m.config
	m.mu.Unlock()
	var preview TradePreview
	if err = process.call(ctx, tradeRPC(request, "tradePreview"), &preview); err != nil {
		return TradePreview{}, err
	}
	if preview.Instrument != request.Instrument || preview.Side != request.Side || preview.Quantity != request.Quantity || preview.StopLoss != request.StopLoss || preview.TakeProfit != request.TakeProfit || preview.EntryPrice <= 0 || math.IsNaN(preview.EntryPrice) || math.IsInf(preview.EntryPrice, 0) || preview.AccountLeverage <= 0 || preview.MinQuantity <= 0 || preview.QuantityStep <= 0 || preview.PriceStep <= 0 {
		return TradePreview{}, errors.New("The JForex bridge returned an invalid trade preview")
	}
	normalizeValuation(&preview.AccountCurrency, &preview.ProfitPerPriceUnit, &preview.ValuationTime, &preview.ValuationError)
	if preview.Metrics != nil {
		normalizeTradeMetrics(preview.Metrics)
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return TradePreview{}, err
	}
	preview.ID = hex.EncodeToString(token[:])
	preview.ExpiresAt = time.Now().Add(time.Minute).UnixMilli()
	preview.Environment = config.Environment
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.config != config {
		return TradePreview{}, errors.New("Dukascopy credentials changed during preview; prepare the trade again")
	}
	if m.tradePreviews == nil {
		m.tradePreviews = map[string]*tradeConfirmation{}
	}
	for id, item := range m.tradePreviews {
		if item.preview.ExpiresAt < time.Now().UnixMilli() {
			delete(m.tradePreviews, id)
		}
	}
	if len(m.tradePreviews) >= 100 {
		return TradePreview{}, errors.New("Too many pending trade previews")
	}
	m.tradePreviews[preview.ID] = &tradeConfirmation{preview: preview, request: request, config: config}
	m.holdUntil = time.Now().Add(2 * time.Minute)
	return preview, nil
}
func (m *Manager) SubmitTrade(ctx context.Context, id string) (TradeResult, error) {
	m.mu.Lock()
	confirmation := m.tradePreviews[id]
	if confirmation == nil || confirmation.preview.ExpiresAt < time.Now().UnixMilli() || confirmation.config != m.config || m.closed {
		m.mu.Unlock()
		return TradeResult{}, errors.New("Trade confirmation expired or the account changed; prepare the trade again")
	}
	if confirmation.used {
		result := confirmation.result
		m.mu.Unlock()
		if result != nil {
			return *result, nil
		}
		return TradeResult{}, errors.New("This confirmation was already submitted; inspect the account before another trade")
	}
	confirmation.used = true
	request := confirmation.request
	m.mu.Unlock()
	// Reserve the intent durably before any order RPC. No automatic replay follows
	// an interrupted process or timeout, and confirmation tokens do not survive restart.
	directory := filepath.Join(filepath.Dir(m.path), "dukascopy-trade-claims")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return TradeResult{}, errors.New("Could not save the trade intent; no order was submitted")
	}
	claim, err := os.OpenFile(filepath.Join(directory, id+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return TradeResult{}, errors.New("Could not reserve a unique trade intent; no order was submitted")
	}
	encoded, _ := json.Marshal(map[string]any{"id": id, "request": request, "environment": confirmation.preview.Environment, "createdAt": time.Now().UnixMilli(), "status": "claimed"})
	_, writeErr := claim.Write(encoded)
	err = errors.Join(writeErr, claim.Sync(), claim.Close())
	if err != nil {
		return TradeResult{}, errors.New("Could not persist the trade intent; no order was submitted")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	process, release, err := m.begin(ctx, true, "")
	if err != nil {
		return TradeResult{}, err
	}
	defer release()
	if err = verifyTradingBridge(ctx, process); err != nil {
		return TradeResult{}, err
	}
	m.mu.Lock()
	sameAccount := m.config == confirmation.config && !m.closed && confirmation.preview.ExpiresAt >= time.Now().UnixMilli()
	m.mu.Unlock()
	if !sameAccount {
		return TradeResult{}, errors.New("Dukascopy account changed; no order was submitted")
	}
	rpc := tradeRPC(request, "tradeSubmit")
	rpc["intent"] = id
	var result TradeResult
	if err = process.call(ctx, rpc, &result); err != nil {
		result = TradeResult{Status: "unknown", Message: "JForex did not confirm the submission outcome. Inspect account orders labelled CD_" + id + " before another trade. No automatic retry was made."}
	} else if result.Status != "filled" && result.Status != "partial" && result.Status != "submitted" && result.Status != "rejected" && result.Status != "unknown" {
		result = TradeResult{Status: "unknown", Message: "JForex returned an unrecognized submission outcome; inspect the account. No automatic retry was made."}
	}
	m.mu.Lock()
	confirmation.result = &result
	m.mu.Unlock()
	return result, nil
}
