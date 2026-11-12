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
	mu         sync.Mutex
	state      providerRateState
	path       string
	interval   time.Duration
	wake       chan struct{}
	queue      []uint64
	nextTicket uint64
	now        func() time.Time
	persist    func(providerRateState) error
}

func newProviderLimiter(path string) (*providerLimiter, error) {
	l := &providerLimiter{path: path, interval: providerRequestInterval, wake: make(chan struct{}), state: providerRateState{Version: 1}}
	err := readState(path, 4096, &l.state)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read provider cooldown: %w", err)
	}
	if l.state.Version != 1 || l.state.NextAt < 0 || l.state.LimitedTill < 0 || l.state.DeniedTill < 0 || l.state.RateCount < 0 || l.state.RateCount > 10 {
		return nil, errors.New("invalid saved Dukascopy rate-limit state")
	}
	if err == nil {
		// The saved reservation can predate a slow final fsync. Conservatively
		// retain a full request interval when reconstructing the limiter.
		l.state.NextAt = max(l.state.NextAt, l.timeNow().Add(l.interval).UnixMilli())
	}
	return l, nil
}

func (l *providerLimiter) timeNow() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

func (l *providerLimiter) saveLocked() error {
	if l.persist != nil {
		return l.persist(l.state)
	}
	return writeState(l.path, l.state)
}

func untilUTC(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05 UTC") }

func (l *providerLimiter) permit(ctx context.Context, waiting func(string)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	l.nextTicket++
	ticket := l.nextTicket
	l.queue = append(l.queue, ticket)
	l.mu.Unlock()
	reportedWait := false
	defer func() {
		l.mu.Lock()
		for i, queued := range l.queue {
			if queued == ticket {
				l.queue = append(l.queue[:i], l.queue[i+1:]...)
				break
			}
		}
		l.notifyLocked()
		l.mu.Unlock()
		if reportedWait && waiting != nil {
			waiting("")
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.mu.Lock()
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return err
		}
		now := l.timeNow().UnixMilli()
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
		ahead := 0
		for l.queue[ahead] != ticket {
			ahead++
		}
		if ahead == 0 && until <= now {
			l.state.NextAt = now + l.interval.Milliseconds()
			err := l.saveLocked()
			if err == nil {
				// Do not let time spent durably saving a permit consume the next
				// request's spacing, or slow storage can release queued workers in
				// a catch-up burst as soon as their old reservations expire.
				l.state.NextAt = max(l.state.NextAt, l.timeNow().Add(l.interval).UnixMilli())
				err = ctx.Err()
			}
			l.mu.Unlock()
			return err
		}
		wake := l.wake
		seconds := max(int64(1), (max(until, now)+int64(ahead)*l.interval.Milliseconds()-now+999)/1000)
		message := fmt.Sprintf("Waiting for shared Dukascopy request pacing: %d requests ahead; next turn in about %ds (one request start every %s)", ahead, seconds, l.interval)
		if l.state.LimitedTill > now {
			message = fmt.Sprintf("Dukascopy HTTP 429 rate limit; all workers wait until %s; %d requests ahead", untilUTC(l.state.LimitedTill), ahead)
		}
		l.mu.Unlock()
		if waiting != nil {
			reportedWait = true
			waiting(message)
		}
		waitMS := int64(1000)
		if until > now {
			waitMS = min(until-now, waitMS)
		}
		timer := time.NewTimer(time.Duration(waitMS) * time.Millisecond)
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

func (l *providerLimiter) notifyLocked() {
	close(l.wake)
	l.wake = make(chan struct{})
}

// Keep completed-file/row progress visible while the next request is queued.
// An empty wait restores the actual worker stage as soon as a permit is given.
func providerProgressMessage(progress, waiting string) string {
	if progress == "" {
		return waiting
	}
	if waiting == "" {
		return progress
	}
	return progress + " · " + waiting
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
	now := l.timeNow()
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
	err := l.saveLocked()
	l.notifyLocked()
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
