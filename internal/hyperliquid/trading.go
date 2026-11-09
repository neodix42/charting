package hyperliquid

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type OrderRequest struct {
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Side       string `json:"side"`
	Quantity   string `json:"quantity"`
	StopLoss   string `json:"stopLoss"`
	TakeProfit string `json:"takeProfit"`
	Leverage   int    `json:"leverage"`
}
type Estimate struct {
	ReferencePrice     string `json:"referencePrice"`
	AccountCurrency    string `json:"accountCurrency,omitempty"`
	ProfitPerPriceUnit string `json:"profitPerPriceUnit,omitempty"`
	ValuationTime      int64  `json:"valuationTime"`
	ValuationError     string `json:"valuationError,omitempty"`
	MaxLeverage        int    `json:"maxLeverage"`
}

type Preview struct {
	Metrics
	AccountCurrency    string       `json:"accountCurrency,omitempty"`
	ProfitPerPriceUnit string       `json:"profitPerPriceUnit,omitempty"`
	ValuationTime      int64        `json:"valuationTime"`
	ValuationError     string       `json:"valuationError,omitempty"`
	ID                 string       `json:"id"`
	Request            OrderRequest `json:"request"`
	ReferencePrice     string       `json:"referencePrice"`
	LimitPrice         string       `json:"limitPrice"`
	ExpiresAt          int64        `json:"expiresAt"`
	Network            string       `json:"network"`
	AccountAddress     string       `json:"accountAddress"`
	MaxLeverage        int          `json:"maxLeverage"`
	Warnings           []string     `json:"warnings"`
}
type OrderResult struct {
	Status         string  `json:"status"`
	Message        string  `json:"message"`
	OrderIDs       []int64 `json:"orderIds"`
	ClientOrderID  string  `json:"clientOrderId"`
	FilledQuantity string  `json:"filledQuantity,omitempty"`
	AveragePrice   string  `json:"averagePrice,omitempty"`
}
type instrument struct {
	asset, szDecimals, maxLeverage, baseToken int
	dex                                       string
	accountCurrency, valuationError           string
	collateralToken                           *int
	balanceToken                              int
	balanceCurrency                           string
	marginTiers                               []marginTier
	marginTableError                          string
}
type prepared struct {
	Preview
	settings   Settings
	instrument instrument
}

func (s *Store) market(ctx context.Context, v Settings, r OrderRequest) (instrument, error) {
	if r.Market == "spot" {
		var meta struct {
			Tokens   []spotToken `json:"tokens"`
			Universe []struct {
				Name   string `json:"name"`
				Index  int    `json:"index"`
				Tokens []int  `json:"tokens"`
			} `json:"universe"`
		}
		if err := s.post(ctx, v, "/info", map[string]string{"type": "spotMeta"}, &meta); err != nil {
			return instrument{}, err
		}
		for _, pair := range meta.Universe {
			if pair.Name == r.Instrument && len(pair.Tokens) == 2 {
				for _, token := range meta.Tokens {
					if token.Index != nil && *token.Index == pair.Tokens[0] && token.SzDecimals >= 0 && token.SzDecimals <= 8 {
						currency, valuationErr := tokenCurrency(meta.Tokens, pair.Tokens[1])
						balanceToken, balanceCurrency := pair.Tokens[1], currency
						if r.Side == "sell" {
							balanceToken = pair.Tokens[0]
							balanceCurrency, _ = tokenCurrency(meta.Tokens, pair.Tokens[0])
						}
						return instrument{asset: 10000 + pair.Index, szDecimals: token.SzDecimals, maxLeverage: 1, baseToken: *token.Index, accountCurrency: currency, valuationError: valuationErr, balanceToken: balanceToken, balanceCurrency: balanceCurrency, collateralToken: &pair.Tokens[1]}, nil
					}
				}
			}
		}
		return instrument{}, errors.New("Spot pair is not available on the selected Hyperliquid network")
	}
	if r.Market != "perpetual" {
		return instrument{}, errors.New("Choose a Hyperliquid spot or perpetual market")
	}
	dex := ""
	offset := 0
	if prefix, _, ok := strings.Cut(r.Instrument, ":"); ok {
		dex = prefix
		var dexs []struct {
			Name string `json:"name"`
		}
		if err := s.post(ctx, v, "/info", map[string]string{"type": "perpDexs"}, &dexs); err != nil {
			return instrument{}, err
		}
		for i, d := range dexs {
			if d.Name == dex && i > 0 {
				offset = 100000 + i*10000
				break
			}
		}
		if offset == 0 {
			return instrument{}, errors.New("Builder DEX is not available on the selected network")
		}
	}
	var meta struct {
		CollateralToken *int              `json:"collateralToken"`
		MarginTables    []json.RawMessage `json:"marginTables"`
		Universe        []struct {
			Name          string `json:"name"`
			SzDecimals    int    `json:"szDecimals"`
			MaxLeverage   int    `json:"maxLeverage"`
			MarginTableID *int   `json:"marginTableId"`
			Delisted      bool   `json:"isDelisted"`
		} `json:"universe"`
	}
	if err := s.post(ctx, v, "/info", map[string]string{"type": "meta", "dex": dex}, &meta); err != nil {
		return instrument{}, err
	}
	for index, item := range meta.Universe {
		if item.Name == r.Instrument && !item.Delisted && item.SzDecimals >= 0 && item.SzDecimals <= 6 && item.MaxLeverage > 0 && item.MaxLeverage <= 1000 {
			currency, valuationErr := s.collateralCurrency(ctx, v, meta.CollateralToken)
			tiers, tableErr := tableFor(item.MarginTableID, item.MaxLeverage, meta.MarginTables)
			return instrument{asset: offset + index, szDecimals: item.SzDecimals, maxLeverage: item.MaxLeverage, dex: dex, accountCurrency: currency, valuationError: valuationErr, collateralToken: meta.CollateralToken, marginTiers: tiers, marginTableError: tableErr}, nil
		}
	}
	return instrument{}, errors.New("Perpetual instrument is not available on the selected network")
}
func positive(value string) (decimal.Decimal, error) {
	if len(value) > 64 || strings.ContainsAny(value, "eE+") {
		return decimal.Zero, errors.New("Use an ordinary positive decimal value")
	}
	v, err := decimal.NewFromString(value)
	if err != nil || !v.IsPositive() {
		return decimal.Zero, errors.New("Enter a positive decimal value")
	}
	return v, nil
}
func priceDecimals(p decimal.Decimal, sz int, spot bool) int32 {
	maxDecimals := 6 - sz
	if spot {
		maxDecimals = 8 - sz
	}
	magnitude := len(p.Coefficient().Abs(p.Coefficient()).String()) + int(p.Exponent()) - 1
	return int32(min(maxDecimals, max(0, 4-magnitude)))
}
func checkPrice(value string, sz int, spot bool) (decimal.Decimal, error) {
	p, err := positive(value)
	if err != nil {
		return p, err
	}
	if !p.Equal(p.Truncate(priceDecimals(p, sz, spot))) {
		return p, errors.New("Price exceeds Hyperliquid's supported precision (5 significant figures and instrument decimal limit)")
	}
	return p, nil
}
func (s *Store) quote(ctx context.Context, v Settings, r OrderRequest) (decimal.Decimal, error) {
	price, _, err := s.quoteWithTime(ctx, v, r)
	return price, err
}
func (s *Store) quoteWithTime(ctx context.Context, v Settings, r OrderRequest) (decimal.Decimal, int64, error) {
	var book struct {
		Time   int64  `json:"time"`
		Coin   string `json:"coin"`
		Levels [][]struct {
			Px string `json:"px"`
		} `json:"levels"`
	}
	if err := s.post(ctx, v, "/info", map[string]string{"type": "l2Book", "coin": r.Instrument}, &book); err != nil {
		return decimal.Zero, 0, err
	}
	side := 0
	if r.Side == "buy" {
		side = 1
	}
	if book.Coin != r.Instrument || len(book.Levels) != 2 || len(book.Levels[side]) == 0 || book.Time < s.now().Add(-15*time.Second).UnixMilli() || book.Time > s.now().Add(5*time.Second).UnixMilli() {
		return decimal.Zero, 0, errors.New("No fresh executable Hyperliquid quote; try again")
	}
	price, err := positive(book.Levels[side][0].Px)
	return price, book.Time, err
}
func (s *Store) checkInventory(ctx context.Context, v Settings, r OrderRequest, m instrument) error {
	if r.Market != "spot" || r.Side != "sell" {
		return nil
	}
	var state struct {
		Balances []struct {
			Token int    `json:"token"`
			Total string `json:"total"`
			Hold  string `json:"hold"`
		} `json:"balances"`
	}
	if err := s.post(ctx, v, "/info", map[string]string{"type": "spotClearinghouseState", "user": v.AccountAddress}, &state); err != nil {
		return err
	}
	qty, _ := positive(r.Quantity)
	for _, b := range state.Balances {
		if b.Token == m.baseToken {
			total, e := decimal.NewFromString(b.Total)
			hold, e2 := decimal.NewFromString(b.Hold)
			if e == nil && e2 == nil && total.Sub(hold).GreaterThanOrEqual(qty) {
				return nil
			}
		}
	}
	return errors.New("Spot Sell requires enough available base-asset balance; spot shorts are unsupported")
}
func (s *Store) Preview(ctx context.Context, r OrderRequest) (Preview, error) {
	if r.Side != "buy" && r.Side != "sell" {
		return Preview{}, errors.New("Choose Buy or Sell")
	}
	if len(r.Instrument) == 0 || len(r.Instrument) > 128 {
		return Preview{}, errors.New("Choose a valid instrument")
	}
	v, err := s.snapshot()
	if err != nil {
		return Preview{}, err
	}
	if err = s.checkAgent(ctx, v); err != nil {
		return Preview{}, err
	}
	if r.Market == "spot" && v.Network == "testnet" {
		return Preview{}, errors.New("Spot chart IDs belong to mainnet and can identify a different token on testnet; choose mainnet for spot trading")
	}
	m, err := s.market(ctx, v, r)
	if err != nil {
		return Preview{}, err
	}
	if r.Market == "spot" && r.Leverage == 0 {
		r.Leverage = 1
	}
	if r.Leverage < 1 || r.Leverage > m.maxLeverage {
		return Preview{}, fmt.Errorf("Leverage must be between 1 and %d for this instrument", m.maxLeverage)
	}
	qty, err := positive(r.Quantity)
	if err != nil || !qty.Equal(qty.Truncate(int32(m.szDecimals))) {
		return Preview{}, fmt.Errorf("Quantity must be positive with at most %d decimal places (base asset units)", m.szDecimals)
	}
	r.Quantity = qty.String()
	if r.Market == "spot" && (r.StopLoss != "" || r.TakeProfit != "") {
		return Preview{}, errors.New("Hyperliquid spot does not support the perpetual TP/SL bracket; clear SL/TP or choose a perpetual market")
	}
	reference, valuationTime, err := s.quoteWithTime(ctx, v, r)
	if err != nil {
		return Preview{}, err
	}
	if err = validateStops(r, m, reference); err != nil {
		return Preview{}, err
	}
	if err = s.checkInventory(ctx, v, r, m); err != nil {
		return Preview{}, err
	}
	cap := reference.Mul(decimal.NewFromFloat(1.01))
	if r.Side == "sell" {
		cap = reference.Mul(decimal.NewFromFloat(0.99))
	}
	precision := priceDecimals(cap, m.szDecimals, r.Market == "spot")
	if r.Side == "buy" {
		cap = cap.RoundFloor(precision)
	} else {
		cap = cap.RoundCeil(precision)
	}
	if !cap.IsPositive() {
		return Preview{}, errors.New("Order price cannot be represented at this instrument's precision")
	}
	id, err := randomID()
	if err != nil {
		return Preview{}, err
	}
	metrics := s.orderMetrics(ctx, v, r, m, reference, valuationTime)
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	if !s.freshObservation(valuationTime) {
		return Preview{}, errors.New("The entry quote became stale while retrieving account metrics; preview again")
	}
	p := Preview{Metrics: metrics, AccountCurrency: m.accountCurrency, ProfitPerPriceUnit: profitFactor(m), ValuationTime: valuationTime, ValuationError: m.valuationError, ID: id, Request: r, ReferencePrice: reference.String(), LimitPrice: cap.String(), ExpiresAt: s.now().Add(60 * time.Second).UnixMilli(), Network: v.Network, AccountAddress: v.AccountAddress, MaxLeverage: m.maxLeverage, Warnings: []string{"Market entry uses an immediate-or-cancel limit capped at 1% slippage; a partial fill is possible."}}
	if r.Market == "perpetual" {
		p.Warnings = append(p.Warnings, "Leverage uses isolated margin. Existing positions are netted by the exchange. SL/TP trigger on mark price, which can differ from the quoted entry, and reduce-only market execution can slip.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings != v {
		return Preview{}, errors.New("Hyperliquid settings changed; preview again")
	}
	if len(s.previews) >= 32 {
		s.previews = map[string]prepared{}
	}
	s.connected = true
	s.previews[id] = prepared{Preview: p, settings: v, instrument: m}
	return p, nil
}
func validateStops(r OrderRequest, m instrument, reference decimal.Decimal) error {
	for _, stop := range []struct {
		value string
		loss  bool
	}{{r.StopLoss, true}, {r.TakeProfit, false}} {
		if stop.value == "" {
			continue
		}
		p, err := checkPrice(stop.value, m.szDecimals, r.Market == "spot")
		if err != nil {
			return err
		}
		below := (r.Side == "buy") == stop.loss
		if below && !p.LessThan(reference) || !below && !p.GreaterThan(reference) {
			return errors.New("SL and TP must be on their correct sides of the current entry quote")
		}
	}
	return nil
}
func orderWire(m instrument, buy bool, price, qty string, reduce bool, kind wire, cloid string) wire {
	w := wire{{"a", m.asset}, {"b", buy}, {"p", price}, {"s", qty}, {"r", reduce}, {"t", kind}}
	if cloid != "" {
		w = append(w, field{"c", cloid})
	}
	return w
}
func (s *Store) nextNonce() (uint64, error) {
	path := filepath.Join(s.profile, "hyperliquid-nonce.json")
	var last uint64
	raw, err := readBounded(path, 64)
	if err == nil {
		if json.Unmarshal(raw, &last) != nil {
			return 0, errors.New("Invalid saved signing nonce; original preserved")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	next := max(uint64(s.now().UnixMilli()), last+1)
	return next, atomicJSON(path, next)
}
func (s *Store) exchange(ctx context.Context, v Settings, action wire, expires uint64) (json.RawMessage, error) {
	nonce, err := s.nextNonce()
	if err != nil {
		return nil, errors.New("Cannot durably reserve the Hyperliquid signing nonce")
	}
	key, err := keyFromString(v.PrivateKey)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Action       wire      `json:"action"`
		Nonce        uint64    `json:"nonce"`
		Signature    signature `json:"signature"`
		VaultAddress any       `json:"vaultAddress"`
		ExpiresAfter uint64    `json:"expiresAfter"`
	}{Action: action, Nonce: nonce, Signature: signAction(key, action, nonce, expires, v.Network == "mainnet"), ExpiresAfter: expires}
	var response struct {
		Status   string          `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if err = s.post(ctx, v, "/exchange", payload, &response); err != nil {
		return nil, err
	}
	if response.Status != "ok" {
		return nil, errors.New("Hyperliquid rejected the signed action; inspect exchange account before submitting a new order")
	}
	return response.Response, nil
}
func (s *Store) Submit(ctx context.Context, id string) (OrderResult, error) {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return OrderResult{}, ctx.Err()
	}
	s.mu.Lock()
	p, ok := s.previews[id]
	delete(s.previews, id)
	current := s.settings
	s.mu.Unlock()
	if !ok || p.settings != current || s.now().UnixMilli() >= p.ExpiresAt {
		return OrderResult{}, errors.New("Order preview expired or already used; preview again")
	}
	v := p.settings
	r := p.Request
	m := p.instrument
	if err := s.checkAgent(ctx, v); err != nil {
		return OrderResult{}, err
	}
	fresh, err := s.market(ctx, v, r)
	if err != nil {
		return OrderResult{}, err
	}
	if fresh.asset != m.asset || fresh.szDecimals != m.szDecimals {
		return OrderResult{}, errors.New("Hyperliquid instrument metadata changed; preview again")
	}
	if r.Leverage < 1 || r.Leverage > fresh.maxLeverage {
		return OrderResult{}, fmt.Errorf("Hyperliquid leverage limit changed to %d; preview again", fresh.maxLeverage)
	}
	if fresh.accountCurrency != "" && m.accountCurrency != "" && fresh.accountCurrency != m.accountCurrency {
		return OrderResult{}, errors.New("Hyperliquid collateral currency changed; preview again")
	}
	m = fresh
	quote, err := s.quote(ctx, v, r)
	if err != nil {
		return OrderResult{}, err
	}
	cap, _ := positive(p.LimitPrice)
	if r.Side == "buy" && quote.GreaterThan(cap) || r.Side == "sell" && quote.LessThan(cap) {
		return OrderResult{}, errors.New("Market price moved beyond the confirmed slippage cap; preview again")
	}
	if s.now().UnixMilli() >= p.ExpiresAt {
		return OrderResult{}, errors.New("Order preview expired; preview again")
	}
	if err = validateStops(r, m, quote); err != nil {
		return OrderResult{}, err
	}
	if err = s.checkInventory(ctx, v, r, m); err != nil {
		return OrderResult{}, err
	}
	// Consume before any signed action. Restart and uncertain responses cannot
	// replay a confirmation token. The journal deliberately excludes credentials.
	claim := filepath.Join(s.profile, "hyperliquid-orders", id+".json")
	if err = os.MkdirAll(filepath.Dir(claim), 0700); err != nil {
		return OrderResult{}, err
	}
	f, err := os.OpenFile(claim, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return OrderResult{}, errors.New("Order confirmation is already claimed or cannot be saved")
	}
	raw, _ := json.Marshal(p.Preview)
	_, writeErr := f.Write(raw)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return OrderResult{}, errors.New("Order claim could not be saved; no order sent")
	}
	result := OrderResult{Status: "uncertain", Message: "Submission may have reached Hyperliquid. Check the exchange account before placing another order.", ClientOrderID: "0x" + id, OrderIDs: []int64{}}
	finish := func() {
		if err := atomicJSON(claim, struct {
			Preview Preview     `json:"preview"`
			Result  OrderResult `json:"result"`
		}{p.Preview, result}); err != nil {
			result.Message += " The local result journal could not be saved; do not resubmit this confirmation."
		}
	}
	if r.Market == "perpetual" {
		_, err = s.exchange(ctx, v, wire{{"type", "updateLeverage"}, {"asset", m.asset}, {"isCross", false}, {"leverage", r.Leverage}}, uint64(p.ExpiresAt))
		if err != nil {
			result.Status = "rejected"
			result.Message = "Leverage update failed or is uncertain; no entry order was sent. Check leverage on the exchange."
			finish()
			return result, nil
		}
	}
	buy := r.Side == "buy"
	orders := []wire{orderWire(m, buy, p.LimitPrice, r.Quantity, false, wire{{"limit", wire{{"tif", "Ioc"}}}}, result.ClientOrderID)}
	for _, stop := range []struct{ value, kind string }{{r.StopLoss, "sl"}, {r.TakeProfit, "tp"}} {
		if stop.value == "" {
			continue
		}
		trigger, _ := positive(stop.value)
		limit := trigger.Mul(decimal.NewFromFloat(0.99))
		if !buy {
			limit = trigger.Mul(decimal.NewFromFloat(1.01))
		}
		prec := priceDecimals(limit, m.szDecimals, false)
		if buy {
			limit = limit.RoundCeil(prec)
		} else {
			limit = limit.RoundFloor(prec)
		}
		digest := sha256.Sum256([]byte(id + stop.kind))
		orders = append(orders, orderWire(m, !buy, limit.String(), r.Quantity, true, wire{{"trigger", wire{{"isMarket", true}, {"triggerPx", trigger.String()}, {"tpsl", stop.kind}}}}, "0x"+hex.EncodeToString(digest[:16])))
	}
	grouping := "na"
	if len(orders) > 1 {
		grouping = "normalTpsl"
	}
	response, err := s.exchange(ctx, v, wire{{"type", "order"}, {"orders", orders}, {"grouping", grouping}}, uint64(p.ExpiresAt))
	if err != nil {
		finish()
		return result, nil
	}
	var orderResponse struct {
		Type string `json:"type"`
		Data struct {
			Statuses []json.RawMessage `json:"statuses"`
		} `json:"data"`
	}
	if json.Unmarshal(response, &orderResponse) != nil || orderResponse.Type != "order" || len(orderResponse.Data.Statuses) != len(orders) {
		finish()
		return result, nil
	}
	failures := 0
	filled := false
	for i, status := range orderResponse.Data.Statuses {
		var state string
		if json.Unmarshal(status, &state) == nil {
			if i == 0 || (state != "waitingForFill" && state != "waitingForTrigger") {
				failures++
			}
			continue
		}
		var item struct {
			Error  string `json:"error"`
			Filled *struct {
				Oid     int64  `json:"oid"`
				TotalSz string `json:"totalSz"`
				AvgPx   string `json:"avgPx"`
			} `json:"filled"`
			Resting *struct {
				Oid int64 `json:"oid"`
			} `json:"resting"`
			WaitingForFill json.RawMessage `json:"waitingForFill"`
		}
		if json.Unmarshal(status, &item) != nil {
			failures++
			continue
		}
		if item.Error != "" {
			failures++
		}
		if item.Filled != nil {
			size, sizeErr := positive(item.Filled.TotalSz)
			_, priceErr := positive(item.Filled.AvgPx)
			requested, _ := positive(r.Quantity)
			if sizeErr != nil || priceErr != nil || size.GreaterThan(requested) || item.Filled.Oid <= 0 {
				finish()
				return result, nil
			}
			result.OrderIDs = append(result.OrderIDs, item.Filled.Oid)
			if i == 0 {
				filled = true
				result.FilledQuantity = item.Filled.TotalSz
				result.AveragePrice = item.Filled.AvgPx
			}
		} else if item.Resting != nil {
			result.OrderIDs = append(result.OrderIDs, item.Resting.Oid)
		} else if item.Error == "" {
			var text string
			if json.Unmarshal(status, &text) != nil || text != "waitingForFill" {
				failures++
			}
		}
	}
	if !filled {
		result.Status = "not-filled"
		result.Message = "The market entry was not confirmed filled. Inspect Hyperliquid orders and positions before a new attempt."
	} else if failures > 0 {
		result.Status = "partial"
		result.Message = "Entry filled, but one or more protective orders failed or were not confirmed. Manage the position on Hyperliquid immediately."
	} else {
		result.Status = "filled"
		result.Message = "Hyperliquid confirmed the entry fill and accepted all requested protective orders."
		actual, _ := positive(result.FilledQuantity)
		requested, _ := positive(r.Quantity)
		if !actual.Equal(requested) {
			result.Status = "partial"
			result.Message = "Hyperliquid confirmed a partial entry fill; inspect filled quantity and protective orders on the exchange."
		}
	}
	finish()
	return result, nil
}
