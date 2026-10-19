// Package exchange implements fixed, public spot endpoints. It has no Wails dependency.
package exchange

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Adapter interface {
	Instruments(context.Context) ([]domain.Instrument, error)
	History(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error)
	Stream(context.Context, domain.FeedKey, func([]domain.Candle, domain.Health)) error
}
type Error struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	RetryAt int64  `json:"retryAt,omitempty"`
}

func (e *Error) Error() string { return e.Message }
func ErrorKind(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "offline"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return "offline"
}
func RetryAt(err error) int64 {
	var e *Error
	if errors.As(err, &e) {
		return e.RetryAt
	}
	return 0
}
func TimeframeSeconds(v string) int64 {
	return map[string]int64{"1m": 60, "5m": 300, "15m": 900, "1h": 3600, "4h": 14400, "1d": 86400, "1w": 604800}[v]
}

// BucketStart returns the UTC candle boundary containing timestamp. Exchange
// weeks begin on Monday, whereas Unix-epoch division would begin on Thursday.
// A nonpositive duration is invalid and returns timestamp unchanged.
func BucketStart(timestamp, duration int64) int64 {
	if duration <= 0 {
		return timestamp
	}
	offset := int64(0)
	if duration == 604800 {
		offset = 345600 // Monday, 1970-01-05 00:00:00 UTC.
	}
	remainder := (timestamp - offset) % duration
	if remainder < 0 {
		remainder += duration
	}
	return timestamp - remainder
}

var bybitIntervals = map[string]string{"1m": "1", "5m": "5", "15m": "15", "1h": "60", "4h": "240", "1d": "D", "1w": "W"}
var coinbaseIntervals = map[string]string{"1m": "ONE_MINUTE", "5m": "FIVE_MINUTE", "15m": "FIFTEEN_MINUTE", "1h": "ONE_HOUR", "4h": "FOUR_HOUR", "1d": "ONE_DAY"}

type publicAdapter struct {
	provider, base, ws string
	client             *http.Client
	mu                 sync.Mutex
	instruments        []domain.Instrument
	fetched            time.Time
	hub                *socketHub
	requests           atomic.Int64
	requestMu          sync.Mutex
	nextRequest        time.Time
	blockedUntil       atomic.Int64
	requestSlots       chan struct{}
}

func New(provider string) (Adapter, error) {
	a := &publicAdapter{provider: provider, client: &http.Client{Timeout: 15 * time.Second}, requestSlots: make(chan struct{}, 2)}
	switch provider {
	case "binance":
		a.base = "https://api.binance.com"
		a.ws = "wss://stream.binance.com:9443/ws"
	case "bybit":
		a.base = "https://api.bybit.com"
		a.ws = "wss://stream.bybit.com/v5/public/spot"
	case "coinbase":
		a.base = "https://api.coinbase.com"
		a.ws = "wss://advanced-trade-ws.coinbase.com"
	default:
		return nil, &Error{Kind: "unknownInstrument", Message: "Unknown exchange"}
	}
	a.hub = newSocketHub(a)
	return a, nil
}
func (a *publicAdapter) validate(k domain.FeedKey) error {
	if err := k.Validate(); err != nil {
		return &Error{Kind: "unsupportedInterval", Message: err.Error()}
	}
	if k.Exchange != a.provider {
		return &Error{Kind: "unknownInstrument", Message: "Exchange does not match adapter"}
	}
	if a.provider == "coinbase" && coinbaseAlias(k.Instrument) {
		return &Error{Kind: "unknownInstrument", Message: "Coinbase public streams alias this USDC product to USD; choose its USD market explicitly"}
	}
	return nil
}
func coinbaseAlias(id string) bool {
	return strings.HasSuffix(id, "-USDC") && id != "USDT-USDC" && id != "EURC-USDC"
}
func (a *publicAdapter) get(ctx context.Context, path string, q url.Values, dst any) error {
	u := a.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	for attempt := 0; attempt < 3; attempt++ {
		if until := a.blockedUntil.Load(); until > time.Now().UnixMilli() {
			return &Error{Kind: "rateLimited", Message: a.provider + " public requests are backing off", RetryAt: until / 1000}
		}
		select {
		case a.requestSlots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		a.requestMu.Lock()
		scheduled := maxTime(time.Now(), a.nextRequest)
		a.nextRequest = scheduled.Add(150 * time.Millisecond)
		a.requestMu.Unlock()
		if err := wait(ctx, time.Until(scheduled)); err != nil {
			<-a.requestSlots
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			<-a.requestSlots
			return err
		}
		req.Header.Set("User-Agent", "CryptoDesk/1.0 public-spot")
		req.Header.Set("Accept", "application/json")
		a.requests.Add(1)
		resp, err := a.client.Do(req)
		if err != nil {
			<-a.requestSlots
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt < 2 {
				if err = wait(ctx, time.Duration(attempt+1)*500*time.Millisecond); err != nil {
					return err
				}
				continue
			}
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
		resp.Body.Close()
		<-a.requestSlots
		if readErr != nil {
			return readErr
		}
		if len(body) > 32<<20 {
			return &Error{Kind: "internal", Message: a.provider + " public response exceeded the bounded size limit"}
		}
		if resp.StatusCode == 429 {
			retry := time.Now().Add(time.Minute)
			if s := resp.Header.Get("Retry-After"); s != "" {
				if sec, e := strconv.Atoi(s); e == nil {
					retry = time.Now().Add(time.Duration(sec) * time.Second)
				} else if t, e := http.ParseTime(s); e == nil {
					retry = t
				}
			}
			a.blockedUntil.Store(retry.UnixMilli())
			return &Error{Kind: "rateLimited", Message: a.provider + " public request rate limited", RetryAt: retry.Unix()}
		}
		if resp.StatusCode == 403 || resp.StatusCode == 451 {
			return &Error{Kind: "restricted", Message: fmt.Sprintf("%s public endpoint restricted (HTTP %d)", a.provider, resp.StatusCode)}
		}
		if resp.StatusCode == 404 {
			return &Error{Kind: "unknownInstrument", Message: a.provider + " instrument or endpoint unavailable"}
		}
		if resp.StatusCode >= 500 && attempt < 2 {
			if err = wait(ctx, time.Duration(attempt+1)*time.Second); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &Error{Kind: "offline", Message: fmt.Sprintf("%s public request failed (HTTP %d)", a.provider, resp.StatusCode)}
		}
		var api struct {
			Code    int    `json:"code"`
			Msg     string `json:"msg"`
			RetCode int    `json:"retCode"`
			RetMsg  string `json:"retMsg"`
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &api)
		if api.Code < 0 || api.RetCode != 0 || api.Error != "" {
			msg := api.Msg + api.RetMsg + api.Message + api.Error
			kind := "offline"
			if api.Code == -1121 || api.RetCode == 10029 || strings.Contains(strings.ToLower(msg), "invalid symbol") {
				kind = "unknownInstrument"
			}
			if api.RetCode == 10006 || api.Code == -1003 {
				kind = "rateLimited"
				a.blockedUntil.Store(time.Now().Add(time.Minute).UnixMilli())
			}
			return &Error{Kind: kind, Message: a.provider + ": " + msg, RetryAt: a.blockedUntil.Load() / 1000}
		}
		if err = json.Unmarshal(body, dst); err != nil {
			return &Error{Kind: "internal", Message: a.provider + " returned malformed public data"}
		}
		return nil
	}
	return &Error{Kind: "offline", Message: "Public request failed"}
}
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Diagnostics reports actual provider sockets, including shared Coinbase products.
type Diagnostics struct {
	Sockets      int   `json:"sockets"`
	Reconnects   int64 `json:"reconnects"`
	QueueDepth   int   `json:"queueDepth"`
	RESTRequests int64 `json:"restRequests"`
}

func (a *publicAdapter) Diagnostics() Diagnostics {
	h := a.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	d := Diagnostics{Sockets: h.sockets, Reconnects: h.reconnects, RESTRequests: a.requests.Load()}
	for _, channels := range h.subs {
		for _, ch := range channels {
			d.QueueDepth += len(ch)
		}
	}
	return d
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
