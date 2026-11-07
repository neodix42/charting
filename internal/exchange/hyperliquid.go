package exchange

import (
	"bytes"
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Hyperliquid's public info endpoint is POST-only. This uses the same bounded
// request slots and diagnostics as other providers, with its own weighted budget:
// Reserve the maximum response weight from the requested candle window. The
// 55ms per weight budget leaves headroom below 1200 weight/minute, without
// charging a two-second, 1000-candle delay for a one-candle refresh.
// https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/rate-limits-and-user-limits
func (a *publicAdapter) hyperliquidPost(ctx context.Context, body any, dst any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	spacing := hyperliquidRequestSpacing(payload)
	for attempt := 0; attempt < 3; attempt++ {
		if until := a.blockedUntil.Load(); until > time.Now().UnixMilli() {
			return &Error{Kind: "rateLimited", Message: "Hyperliquid public requests are backing off", RetryAt: until / 1000}
		}
		select {
		case a.requestSlots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		a.requestMu.Lock()
		scheduled := maxTime(time.Now(), a.nextRequest)
		a.nextRequest = scheduled.Add(spacing)
		a.requestMu.Unlock()
		if err := wait(ctx, time.Until(scheduled)); err != nil {
			<-a.requestSlots
			return err
		}
		// A simultaneous request may have established backoff while this one
		// waited for its scheduled turn.
		if until := a.blockedUntil.Load(); until > time.Now().UnixMilli() {
			<-a.requestSlots
			return &Error{Kind: "rateLimited", Message: "Hyperliquid public requests are backing off", RetryAt: until / 1000}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/info", bytes.NewReader(payload))
		if err != nil {
			<-a.requestSlots
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "CryptoDesk/1.0 public-market-data")
		a.requests.Add(1)
		resp, err := a.client.Do(req)
		if err != nil {
			<-a.requestSlots
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt < 2 {
				continue
			}
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
		resp.Body.Close()
		<-a.requestSlots
		if readErr != nil {
			return readErr
		}
		if len(data) > 32<<20 {
			return &Error{Kind: "internal", Message: "Hyperliquid public response exceeded the bounded size limit"}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			retry := time.Now().Add(time.Minute)
			if s := resp.Header.Get("Retry-After"); s != "" {
				if seconds, err := strconv.Atoi(s); err == nil && seconds > 0 {
					retry = time.Now().Add(time.Duration(seconds) * time.Second)
				} else if parsed, err := http.ParseTime(s); err == nil && parsed.After(time.Now()) {
					retry = parsed
				}
			}
			a.blockedUntil.Store(retry.UnixMilli())
			return &Error{Kind: "rateLimited", Message: "Hyperliquid public request rate limited", RetryAt: retry.Unix()}
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnavailableForLegalReasons {
			return &Error{Kind: "restricted", Message: fmt.Sprintf("Hyperliquid public endpoint restricted (HTTP %d)", resp.StatusCode)}
		}
		if resp.StatusCode >= 500 && attempt < 2 {
			continue
		}
		if resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound {
			return &Error{Kind: "unknownInstrument", Message: fmt.Sprintf("Hyperliquid rejected the public market request (HTTP %d)", resp.StatusCode)}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &Error{Kind: "offline", Message: fmt.Sprintf("Hyperliquid public request failed (HTTP %d)", resp.StatusCode)}
		}
		var api struct {
			Error    string `json:"error"`
			Status   string `json:"status"`
			Response string `json:"response"`
		}
		_ = json.Unmarshal(data, &api)
		var message string
		if api.Error != "" {
			message = api.Error
		} else if api.Status == "err" {
			message = api.Response
		} else {
			_ = json.Unmarshal(data, &message)
		}
		if message != "" {
			kind := "offline"
			lower := strings.ToLower(message)
			if strings.Contains(lower, "coin") || strings.Contains(lower, "asset") || strings.Contains(lower, "symbol") {
				kind = "unknownInstrument"
			}
			return &Error{Kind: kind, Message: "Hyperliquid: " + message}
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) || json.Unmarshal(data, dst) != nil {
			return &Error{Kind: "internal", Message: "Hyperliquid returned malformed public data"}
		}
		return nil
	}
	return &Error{Kind: "offline", Message: "Hyperliquid public request failed"}
}

func hyperliquidRequestSpacing(payload []byte) time.Duration {
	var req struct {
		Type string `json:"type"`
		Req  struct {
			Interval string `json:"interval"`
			Start    int64  `json:"startTime"`
			End      int64  `json:"endTime"`
		} `json:"req"`
	}
	weight := int64(20)
	if json.Unmarshal(payload, &req) == nil && req.Type == "candleSnapshot" {
		// Hyperliquid retains at most 5000 native candles. Include both
		// boundary buckets since the API uses inclusive millisecond ends.
		count := int64(5000)
		if d := TimeframeSeconds(req.Req.Interval) * 1000; d > 0 && req.Req.Start >= 0 && req.Req.End >= req.Req.Start {
			count = min(count, req.Req.End/d-req.Req.Start/d+1)
		}
		weight += (count + 59) / 60
	}
	return time.Duration(weight) * 55 * time.Millisecond
}

type hyperliquidToken struct {
	Name       string `json:"name"`
	Index      int    `json:"index"`
	SzDecimals int    `json:"szDecimals"`
	FullName   string `json:"fullName"`
}

// Spot IDs identify pairs, not tokens: HYPE's token index is 150 while its
// USDC pair is @107. PURR/USDC is the sole legacy non-@ API name.
// https://hyperliquid.gitbook.io/hyperliquid-docs/for-developers/api/info-endpoint
func (a *publicAdapter) hyperliquidInstruments(ctx context.Context) ([]domain.Instrument, error) {
	var spot struct {
		Tokens   []hyperliquidToken `json:"tokens"`
		Universe []struct {
			Name   string `json:"name"`
			Index  int    `json:"index"`
			Tokens []int  `json:"tokens"`
		} `json:"universe"`
	}
	var result []domain.Instrument
	var failures []error
	tokens := map[int]hyperliquidToken{}
	if err := a.hyperliquidPost(ctx, map[string]any{"type": "spotMeta"}, &spot); err != nil {
		failures = append(failures, fmt.Errorf("Hyperliquid spot symbols unavailable: %w", err))
	} else {
		if spot.Tokens == nil || spot.Universe == nil {
			failures = append(failures, &Error{Kind: "internal", Message: "Hyperliquid spot metadata has no token/pair universe"})
		}
		for _, token := range spot.Tokens {
			tokens[token.Index] = token
		}
		for _, pair := range spot.Universe {
			if len(pair.Tokens) != 2 || pair.Index < 0 {
				failures = append(failures, &Error{Kind: "internal", Message: "Hyperliquid returned invalid spot pair metadata"})
				continue
			}
			base, baseOK := tokens[pair.Tokens[0]]
			quote, quoteOK := tokens[pair.Tokens[1]]
			if !baseOK || !quoteOK || base.Name == "" || quote.Name == "" || base.SzDecimals < 0 || base.SzDecimals > 8 {
				failures = append(failures, &Error{Kind: "internal", Message: "Hyperliquid spot pair references invalid token metadata"})
				continue
			}
			id := "@" + strconv.Itoa(pair.Index)
			if pair.Index == 0 && pair.Name == "PURR/USDC" {
				id = pair.Name
			}
			result = append(result, instrument("spot", id, base.Name, quote.Name,
				hyperliquidIncrement(8-base.SzDecimals), hyperliquidIncrement(base.SzDecimals), "available",
				map[string]string{"displaySymbol": base.Name + "/" + quote.Name, "baseFullName": base.FullName,
					"quoteFullName": quote.FullName, "pairIndex": strconv.Itoa(pair.Index), "priceSignificantFigures": "5"}))
		}
	}
	var dexs []*struct {
		Name string `json:"name"`
	}
	if err := a.hyperliquidPost(ctx, map[string]any{"type": "perpDexs"}, &dexs); err != nil {
		failures = append(failures, fmt.Errorf("Hyperliquid builder perpetual DEX discovery unavailable: %w", err))
	}
	// Query native perps even if the DEX listing fails. It is a separate public
	// market; a partial listing carries an explicit warning and is never cached.
	dexNames, seenDex := []string{""}, map[string]bool{"": true}
	for _, dex := range dexs {
		if dex != nil && dex.Name != "" && !seenDex[dex.Name] {
			seenDex[dex.Name] = true
			dexNames = append(dexNames, dex.Name)
		}
	}
	if len(dexNames) > 1000 {
		return result, &Error{Kind: "internal", Message: "Hyperliquid DEX discovery exceeded the bounded request budget"}
	}
	seen := map[string]bool{}
	for _, dex := range dexNames {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var meta struct {
			CollateralToken int `json:"collateralToken"`
			Universe        []struct {
				Name        string `json:"name"`
				SzDecimals  int    `json:"szDecimals"`
				MaxLeverage int    `json:"maxLeverage"`
				IsDelisted  bool   `json:"isDelisted"`
			} `json:"universe"`
		}
		if err := a.hyperliquidPost(ctx, map[string]any{"type": "meta", "dex": dex}, &meta); err != nil {
			failures = append(failures, fmt.Errorf("Hyperliquid %s perpetual symbols unavailable: %w", hyperliquidDexLabel(dex), err))
			continue
		}
		if meta.Universe == nil {
			failures = append(failures, &Error{Kind: "internal", Message: "Hyperliquid perpetual metadata has no universe"})
			continue
		}
		quote := tokens[meta.CollateralToken].Name
		if quote == "" {
			quote = "token#" + strconv.Itoa(meta.CollateralToken)
			if meta.CollateralToken == 0 {
				quote = "USDC"
			}
		}
		for _, asset := range meta.Universe {
			if asset.IsDelisted {
				continue
			}
			if asset.Name == "" || asset.SzDecimals < 0 || asset.SzDecimals > 6 || (dex != "" && !strings.HasPrefix(asset.Name, dex+":")) {
				failures = append(failures, &Error{Kind: "internal", Message: "Hyperliquid returned invalid perpetual asset metadata"})
				continue
			}
			if seen[asset.Name] {
				continue
			}
			seen[asset.Name] = true
			base := strings.TrimPrefix(asset.Name, dex+":")
			result = append(result, instrument("perpetual", asset.Name, base, quote,
				hyperliquidIncrement(6-asset.SzDecimals), hyperliquidIncrement(asset.SzDecimals), "available",
				map[string]string{"dex": dex, "settleCoin": quote, "collateralToken": strconv.Itoa(meta.CollateralToken),
					"maxLeverage": strconv.Itoa(asset.MaxLeverage), "priceSignificantFigures": "5"}))
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return result, errors.Join(failures...)
}

func hyperliquidIncrement(decimals int) string {
	if decimals == 0 {
		return "1"
	}
	return "0." + strings.Repeat("0", decimals-1) + "1"
}

func hyperliquidDexLabel(dex string) string {
	if dex == "" {
		return "native"
	}
	return dex
}

// Hyperliquid's native weekly candles begin on Thursday (Unix-epoch weeks),
// observed on mainnet 2026-11-05. The workspace defines Monday UTC weeks, so
// derive those from real daily candles instead of relabelling native week bars.
func (a *publicAdapter) hyperliquidHistory(ctx context.Context, k domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	now := time.Now().Unix()
	if k.Timeframe == "1w" {
		return a.hyperliquidWeeklyHistory(ctx, k, from, to, now)
	}
	rows, err := a.hyperliquidDecimalHistory(ctx, k, from, to, now)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Candle, 0, len(rows))
	for _, row := range rows {
		candle, err := row.Candle()
		if err != nil {
			return nil, err
		}
		result = append(result, candle)
	}
	normalized, err := Normalize(result, TimeframeSeconds(k.Timeframe))
	if err != nil {
		return nil, err
	}
	if len(normalized) > limit {
		normalized = normalized[len(normalized)-limit:]
	}
	return normalized, nil
}

func (a *publicAdapter) hyperliquidDecimalHistory(ctx context.Context, k domain.FeedKey, from, to, now int64) ([]DecimalCandle, error) {
	var rows []struct {
		Start    int64  `json:"t"`
		End      int64  `json:"T"`
		Coin     string `json:"s"`
		Interval string `json:"i"`
		Open     string `json:"o"`
		High     string `json:"h"`
		Low      string `json:"l"`
		Close    string `json:"c"`
		Volume   string `json:"v"`
	}
	if err := a.hyperliquidPost(ctx, map[string]any{"type": "candleSnapshot", "req": map[string]any{
		"coin": k.Instrument, "interval": k.Timeframe, "startTime": from * 1000, "endTime": to*1000 - 1,
	}}, &rows); err != nil {
		return nil, err
	}
	d := TimeframeSeconds(k.Timeframe)
	result := make([]DecimalCandle, 0, len(rows))
	for _, row := range rows {
		if row.Coin != k.Instrument || row.Interval != k.Timeframe || row.Start < 0 || row.Start%1000 != 0 || row.End != row.Start+d*1000-1 || BucketStart(row.Start/1000, d) != row.Start/1000 {
			return nil, &Error{Kind: "internal", Message: "Hyperliquid returned a mismatched or malformed candle"}
		}
		start := row.Start / 1000
		if start < from || start >= to {
			continue
		}
		closed := start+d <= now-2
		quality := "authoritative"
		if !closed {
			quality = "provisional"
		}
		candle, err := ParseCandle(start, row.Open, row.High, row.Low, row.Close, row.Volume, closed, quality, 0)
		if err != nil {
			return nil, &Error{Kind: "internal", Message: "Hyperliquid returned invalid candle OHLCV: " + err.Error()}
		}
		result = append(result, candle)
	}
	return result, nil
}

func (a *publicAdapter) hyperliquidWeeklyHistory(ctx context.Context, k domain.FeedKey, from, to, now int64) ([]domain.Candle, error) {
	day, week := TimeframeSeconds("1d"), TimeframeSeconds("1w")
	first := BucketStart(from, week)
	if first < from {
		first += week
	}
	if first >= to || first > now {
		return []domain.Candle{}, nil
	}
	end := min(BucketStart(to-1, week)+week, now+1)
	k.Timeframe = "1d"
	daily := map[int64]DecimalCandle{}
	// At most 1000 requested weeks means at most eight bounded daily pages.
	// Older daily pages may be empty beyond the provider's 5000-candle window.
	for cursor := first; cursor < end; {
		pageEnd := min(end, cursor+1000*day)
		rows, err := a.hyperliquidDecimalHistory(ctx, k, cursor, pageEnd, now)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			daily[row.Time] = row
		}
		cursor = pageEnd
	}
	ordered := make([]DecimalCandle, 0, len(daily))
	for _, row := range daily {
		ordered = append(ordered, row)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Time < ordered[j].Time })
	var out []domain.Candle
	for i := 0; i < len(ordered); {
		start := BucketStart(ordered[i].Time, week)
		c := ordered[i]
		c.Time, c.Closed = start, start+week <= now-2
		count, complete := 1, ordered[i].Closed
		j := i + 1
		for j < len(ordered) && BucketStart(ordered[j].Time, week) == start {
			v := ordered[j]
			c.High = decimal.Max(c.High, v.High)
			c.Low = decimal.Min(c.Low, v.Low)
			c.Close = v.Close
			c.Volume = c.Volume.Add(v.Volume)
			complete = complete && v.Closed
			count++
			j++
		}
		if !c.Closed || !complete || count != 7 {
			c.Quality = "provisional"
		}
		rendered, err := c.Candle()
		if err != nil {
			return nil, err
		}
		out = append(out, rendered)
		i = j
	}
	return Normalize(out, week)
}
