package screener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const providerRequestInterval = 2 * time.Second
const providerDeniedCooldown = 15 * time.Minute

type providerRateState struct {
	Version     int   `json:"version"`
	NextAt      int64 `json:"nextAt"`
	LimitedTill int64 `json:"limitedTill"`
	DeniedTill  int64 `json:"deniedTill"`
	RateCount   int   `json:"rateCount"`
}

type providerLimiter struct {
	mu       sync.Mutex
	state    providerRateState
	path     string
	interval time.Duration
	wake     chan struct{}
}

func newProviderLimiter(path string) (*providerLimiter, error) {
	l := &providerLimiter{path: path, interval: providerRequestInterval, wake: make(chan struct{}), state: providerRateState{Version: 1}}
	if err := readState(path, 4096, &l.state); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read provider cooldown: %w", err)
	}
	if l.state.Version != 1 || l.state.NextAt < 0 || l.state.LimitedTill < 0 || l.state.DeniedTill < 0 || l.state.RateCount < 0 || l.state.RateCount > 10 {
		return nil, errors.New("invalid saved Dukascopy rate-limit state")
	}
	return l, nil
}

func untilUTC(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05 UTC") }

func (l *providerLimiter) permit(ctx context.Context, waiting func(string)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.mu.Lock()
		now := time.Now().UnixMilli()
		if l.state.DeniedTill > now {
			until := l.state.DeniedTill
			l.mu.Unlock()
			return fmt.Errorf("Dukascopy HTTP 403 access-denial cooldown until %s; no request sent. Retry manually after that time. Saved data is retained", untilUTC(until))
		}
		until := max(l.state.NextAt, l.state.LimitedTill)
		if l.state.LimitedTill-now > int64((5*time.Minute)/time.Millisecond) {
			l.mu.Unlock()
			return fmt.Errorf("Dukascopy rate-limited until %s (Retry-After); no request sent. Retry after that time", untilUTC(until))
		}
		if until <= now {
			l.state.NextAt = now + l.interval.Milliseconds()
			err := writeState(l.path, l.state)
			l.mu.Unlock()
			return err
		}
		wake := l.wake
		message := "Waiting for shared Dukascopy request pacing"
		if l.state.LimitedTill > now {
			message = "Dukascopy rate limit; all workers wait until " + untilUTC(l.state.LimitedTill)
		}
		l.mu.Unlock()
		if waiting != nil {
			waiting(message)
		}
		timer := time.NewTimer(time.Duration(min(until-now, int64(1000))) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func providerRetryAfter(value string, now time.Time) int64 {
	value = strings.TrimSpace(value)
	// Saturate very large valid delta-seconds instead of overflowing or retrying
	// early. Unparseable headers use the bounded exponential fallback below.
	if value != "" && strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		seconds, err := strconv.ParseUint(value, 10, 64)
		const maximum = int64(253402300799000) // Last whole second of year 9999.
		if err != nil || seconds > uint64((maximum-now.UnixMilli())/1000) {
			return maximum
		}
		return now.UnixMilli() + int64(seconds)*1000
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return max(now.UnixMilli(), parsed.UnixMilli())
	}
	return 0
}

func (l *providerLimiter) observe(status int, retryAfter string) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	until := providerRetryAfter(retryAfter, now)
	switch status {
	case 403:
		// A manual retry during a saved cooldown must not extend it indefinitely.
		if l.state.DeniedTill <= now.UnixMilli() {
			l.state.DeniedTill = now.Add(providerDeniedCooldown).UnixMilli()
		}
		l.state.DeniedTill = max(l.state.DeniedTill, until)
	case 429:
		l.state.RateCount = min(10, l.state.RateCount+1)
		backoff := min(time.Minute, 5*time.Second*time.Duration(1<<min(4, l.state.RateCount-1)))
		l.state.LimitedTill = max(l.state.LimitedTill, until, now.Add(backoff).UnixMilli())
	default:
		return max(l.state.DeniedTill, l.state.LimitedTill), nil
	}
	err := writeState(l.path, l.state)
	close(l.wake)
	l.wake = make(chan struct{})
	return max(l.state.DeniedTill, l.state.LimitedTill), err
}

type providerEvent struct {
	RequestID  uint64 `json:"requestId,omitempty"`
	Operation  string `json:"operation,omitempty"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	RetryAfter string `json:"retryAfter,omitempty"`
}

// Replies share the existing private owner pipe. Permits run asynchronously so
// a response carrying Retry-After can pause peers whose permits are waiting.
type providerReplies struct {
	ctx     context.Context
	cancel  context.CancelFunc
	state   *providerSession
	owner   io.Writer
	mu      sync.Mutex
	wg      sync.WaitGroup
	slots   chan struct{}
	waiting func(string)
}

func newProviderReplies(ctx context.Context, owner io.Writer, waiting func(string)) *providerReplies {
	child, cancel := context.WithCancel(ctx)
	return &providerReplies{ctx: child, cancel: cancel, state: sessionFromContext(ctx), owner: owner, slots: make(chan struct{}, 16), waiting: waiting}
}

func (r *providerReplies) handle(event providerEvent) error {
	if r.state == nil || r.state.limiter == nil || event.RequestID == 0 || len(event.RetryAfter) > 128 || (event.Operation != "permit" && event.Operation != "response") || (event.Operation == "response" && (event.HTTPStatus < 100 || event.HTTPStatus > 599)) {
		return errors.New("invalid Dukascopy pacing request")
	}
	select {
	case r.slots <- struct{}{}:
	default:
		return errors.New("too many pending Dukascopy pacing requests")
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() { <-r.slots }()
		var err error
		var until int64
		if event.Operation == "permit" {
			err = r.state.limiter.permit(r.ctx, r.waiting)
		} else {
			until, err = r.state.limiter.observe(event.HTTPStatus, event.RetryAfter)
		}
		reply := struct {
			Type          string `json:"type"`
			RequestID     uint64 `json:"requestId"`
			Error         string `json:"error,omitempty"`
			CooldownUntil int64  `json:"cooldownUntil,omitempty"`
		}{Type: "provider-reply", RequestID: event.RequestID, CooldownUntil: until}
		if err != nil {
			reply.Error = cleanMessage(err.Error())
		}
		r.mu.Lock()
		_ = json.NewEncoder(r.owner).Encode(reply)
		r.mu.Unlock()
	}()
	return nil
}

func (r *providerReplies) close() { r.cancel(); r.wg.Wait() }
