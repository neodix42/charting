package exchange

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"
)

func (a *publicAdapter) History(ctx context.Context, k domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if err := a.validate(k); err != nil {
		return nil, err
	}
	d := TimeframeSeconds(k.Timeframe)
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	if a.provider == "coinbase" && k.Timeframe != "1w" && limit > 350 {
		limit = 350
	}
	if to <= 0 {
		to = time.Now().Unix() + 1
	}
	if from <= 0 {
		from = BucketStart(to, d) - int64(limit)*d
	}
	if from >= to {
		return []domain.Candle{}, nil
	}
	if to-from > d*int64(limit) {
		from = to - d*int64(limit)
	}
	if a.provider == "coinbase" && k.Timeframe == "1w" {
		return a.coinbaseWeeklyHistory(ctx, k, from, to, time.Now().Unix())
	}
	q := url.Values{}
	var result []domain.Candle
	now := time.Now().Unix()
	switch a.provider {
	case "binance":
		q.Set("symbol", k.Instrument)
		q.Set("interval", k.Timeframe)
		q.Set("startTime", strconv.FormatInt(from*1000, 10))
		q.Set("endTime", strconv.FormatInt(to*1000-1, 10))
		q.Set("limit", strconv.Itoa(limit))
		var rows [][]json.RawMessage
		if err := a.get(ctx, "/api/v3/klines", q, &rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			c, err := parseArrayCandle(row, "binance", d, now)
			if err != nil {
				return nil, err
			}
			result = append(result, c)
		}
	case "bybit":
		q.Set("category", "spot")
		q.Set("symbol", k.Instrument)
		q.Set("interval", bybitIntervals[k.Timeframe])
		q.Set("start", strconv.FormatInt(from*1000, 10))
		q.Set("end", strconv.FormatInt(to*1000-1, 10))
		q.Set("limit", strconv.Itoa(limit))
		var payload struct {
			Result struct {
				List [][]json.RawMessage `json:"list"`
			} `json:"result"`
		}
		if err := a.get(ctx, "/v5/market/kline", q, &payload); err != nil {
			return nil, err
		}
		for _, row := range payload.Result.List {
			c, err := parseArrayCandle(row, "bybit", d, now)
			if err != nil {
				return nil, err
			}
			result = append(result, c)
		}
	case "coinbase":
		q.Set("start", strconv.FormatInt(from, 10))
		q.Set("end", strconv.FormatInt(to, 10))
		q.Set("granularity", coinbaseIntervals[k.Timeframe])
		q.Set("limit", strconv.Itoa(limit))
		var payload struct {
			Candles []struct{ Start, Open, High, Low, Close, Volume string } `json:"candles"`
		}
		if err := a.get(ctx, "/api/v3/brokerage/market/products/"+url.PathEscape(k.Instrument)+"/candles", q, &payload); err != nil {
			return nil, err
		}
		for _, v := range payload.Candles {
			t, err := strconv.ParseInt(v.Start, 10, 64)
			if err != nil {
				return nil, err
			}
			c, err := ParseCandle(t, v.Open, v.High, v.Low, v.Close, v.Volume, t+d <= now-2, "authoritative", 0)
			if err != nil {
				return nil, err
			}
			rendered, err := c.Candle()
			if err != nil {
				return nil, err
			}
			if !rendered.Closed {
				rendered.Quality = "provisional"
			}
			result = append(result, rendered)
		}
	}
	normalized, err := Normalize(result, d)
	if err != nil {
		return nil, err
	}
	out := normalized[:0]
	for _, c := range normalized {
		if c.Time >= from && c.Time < to {
			out = append(out, c)
		}
	}
	return out, nil
}
func rawString(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return string(v)
}
func parseArrayCandle(row []json.RawMessage, provider string, d, now int64) (domain.Candle, error) {
	if len(row) < 6 {
		return domain.Candle{}, fmt.Errorf("short %s kline", provider)
	}
	t, err := strconv.ParseInt(rawString(row[0]), 10, 64)
	if err != nil {
		return domain.Candle{}, err
	}
	if t%1000 != 0 {
		return domain.Candle{}, fmt.Errorf("unaligned millisecond start")
	}
	t /= 1000
	c, err := ParseCandle(t, rawString(row[1]), rawString(row[2]), rawString(row[3]), rawString(row[4]), rawString(row[5]), t+d <= now, "authoritative", 0)
	if err != nil {
		return domain.Candle{}, err
	}
	out, err := c.Candle()
	if !out.Closed {
		out.Quality = "provisional"
	}
	return out, err
}
func (a *publicAdapter) Instruments(ctx context.Context) ([]domain.Instrument, error) {
	a.mu.Lock()
	if len(a.instruments) > 0 && time.Since(a.fetched) < 30*time.Minute {
		out := cloneInstruments(a.instruments)
		a.mu.Unlock()
		return out, nil
	}
	a.mu.Unlock()
	var result []domain.Instrument
	makeInstrument := func(id, base, quote, price, size, status string, metadata map[string]string) {
		result = append(result, domain.Instrument{NativeID: id, BaseAsset: base, QuoteAsset: quote, PriceIncrement: price, SizeIncrement: size, Availability: status, SupportedIntervals: append([]string(nil), domain.Intervals...), ProviderMetadata: metadata})
	}
	switch a.provider {
	case "binance":
		var payload struct {
			Symbols []struct {
				Symbol, Status, BaseAsset, QuoteAsset string
				IsSpotTradingAllowed                  bool `json:"isSpotTradingAllowed"`
				Filters                               []struct{ FilterType, TickSize, StepSize string }
			}
		}
		if err := a.get(ctx, "/api/v3/exchangeInfo", url.Values{"permissions": {"SPOT"}, "showPermissionSets": {"false"}}, &payload); err != nil {
			return nil, err
		}
		for _, v := range payload.Symbols {
			if !v.IsSpotTradingAllowed {
				continue
			}
			price, size := "", ""
			for _, f := range v.Filters {
				if f.FilterType == "PRICE_FILTER" {
					price = f.TickSize
				}
				if f.FilterType == "LOT_SIZE" {
					size = f.StepSize
				}
			}
			makeInstrument(v.Symbol, v.BaseAsset, v.QuoteAsset, price, size, v.Status, nil)
		}
	case "bybit":
		var payload struct {
			Result struct {
				List []struct {
					Symbol, BaseCoin, QuoteCoin, Status string
					PriceFilter                         struct{ TickSize string }
					LotSizeFilter                       struct{ BasePrecision, QtyStep string }
				}
			}
		}
		if err := a.get(ctx, "/v5/market/instruments-info", url.Values{"category": {"spot"}}, &payload); err != nil {
			return nil, err
		}
		for _, v := range payload.Result.List {
			size := v.LotSizeFilter.BasePrecision
			if size == "" {
				size = v.LotSizeFilter.QtyStep
			}
			makeInstrument(v.Symbol, v.BaseCoin, v.QuoteCoin, v.PriceFilter.TickSize, size, v.Status, nil)
		}
	case "coinbase":
		// Prefer documented cursor pagination; offset remains a compatibility path
		// when a public response omits pagination metadata.
		seen := map[string]bool{}
		seenCursors := map[string]bool{}
		cursor := ""
		finished := false
		for page := 0; page < 30; page++ {
			var payload struct {
				Pagination *struct {
					NextCursor string `json:"next_cursor"`
					HasNext    bool   `json:"has_next"`
				} `json:"pagination"`
				Products []struct {
					ProductID       string `json:"product_id"`
					BaseCurrencyID  string `json:"base_currency_id"`
					QuoteCurrencyID string `json:"quote_currency_id"`
					PriceIncrement  string `json:"price_increment"`
					BaseIncrement   string `json:"base_increment"`
					Status          string `json:"status"`
					ProductType     string `json:"product_type"`
					TradingDisabled bool   `json:"trading_disabled"`
				}
			}
			q := url.Values{"product_type": {"SPOT"}, "limit": {"250"}, "offset": {strconv.Itoa(page * 250)}}
			if cursor != "" {
				q.Del("offset")
				q.Set("cursor", cursor)
			}
			if err := a.get(ctx, "/api/v3/brokerage/market/products", q, &payload); err != nil {
				return nil, err
			}
			progress := 0
			for _, v := range payload.Products {
				if seen[v.ProductID] {
					continue
				}
				seen[v.ProductID] = true
				progress++
				if v.ProductType != "" && v.ProductType != "SPOT" {
					continue
				}
				status := v.Status
				metadata := map[string]string{}
				if v.TradingDisabled {
					status = "disabled"
				}
				if coinbaseAlias(v.ProductID) {
					status = "unsupported-public-alias"
					metadata["publicAlias"] = "USD public channel; choose USD explicitly"
				}
				makeInstrument(v.ProductID, v.BaseCurrencyID, v.QuoteCurrencyID, v.PriceIncrement, v.BaseIncrement, status, metadata)
			}
			if payload.Pagination != nil {
				if !payload.Pagination.HasNext {
					finished = true
					break
				}
				next := payload.Pagination.NextCursor
				if next == "" || next == cursor || seenCursors[next] {
					return nil, &Error{Kind: "internal", Message: "Coinbase instrument cursor made no progress"}
				}
				seenCursors[next] = true
				cursor = next
			} else if len(payload.Products) < 250 {
				finished = true
				break
			}
			if progress == 0 {
				return nil, &Error{Kind: "internal", Message: "Coinbase instrument pagination made no progress"}
			}
		}
		if !finished {
			return nil, &Error{Kind: "internal", Message: "Coinbase instrument pagination exceeded bounded request budget"}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].NativeID < result[j].NativeID })
	a.mu.Lock()
	a.instruments = result
	a.fetched = time.Now()
	a.mu.Unlock()
	return cloneInstruments(result), nil
}
func cloneInstruments(items []domain.Instrument) []domain.Instrument {
	out := append([]domain.Instrument(nil), items...)
	for i := range out {
		out[i].SupportedIntervals = append([]string(nil), out[i].SupportedIntervals...)
		if out[i].ProviderMetadata != nil {
			m := map[string]string{}
			for k, v := range out[i].ProviderMetadata {
				m[k] = v
			}
			out[i].ProviderMetadata = m
		}
	}
	return out
}

// reconcileHistory pages by UTC range, so even a no-trade page advances. Repair
// work is bounded independently of outage duration. Older ranges remain available
// through explicit chart history requests and never require a trade replay.
func (a *publicAdapter) reconcileHistory(ctx context.Context, k domain.FeedKey, from, to int64) ([]domain.Candle, bool, error) {
	d := TimeframeSeconds(k.Timeframe)
	pageSize := 1000
	if a.provider == "coinbase" {
		pageSize = 350
	}
	clipped := false
	if to-from > 10000*d {
		from = BucketStart(to, d) - 9999*d
		clipped = true
	}
	var out []domain.Candle
	for cursor := from; cursor < to; {
		end := min(to, cursor+int64(pageSize)*d)
		bars, err := a.History(ctx, k, cursor, end, pageSize)
		if err != nil {
			return nil, clipped, err
		}
		out = append(out, bars...)
		cursor = end
	}
	normalized, err := Normalize(out, d)
	return normalized, clipped, err
}
