package screener

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderLimiterDoesNotSpendRequestSpacingInsideSlowDurableCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate.json")
	l, err := newProviderLimiter(path)
	if err != nil {
		t.Fatal(err)
	}
	l.interval = 15 * time.Millisecond
	var clock atomic.Int64
	clock.Store(time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC).UnixMilli())
	l.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	first := true
	l.persist = func(state providerRateState) error {
		if err := writeState(path, state); err != nil {
			return err
		}
		if first {
			first = false
			clock.Add(4 * l.interval.Milliseconds())
		}
		return nil
	}
	if err := l.permit(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	next := l.state.NextAt
	l.mu.Unlock()
	if next < clock.Load()+l.interval.Milliseconds() {
		t.Fatal("slow commit exhausted the next request's interval", next, clock.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- l.permit(ctx, func(message string) {
			if message != "" {
				select {
				case waiting <- struct{}{}:
				default:
				}
			}
		})
	}()
	select {
	case <-waiting:
	case err := <-done:
		t.Fatal("next permit bypassed the post-commit interval", err)
	case <-time.After(2 * time.Second):
		t.Fatal("next permit did not enter the queue")
	}
	clock.Store(next)
	l.mu.Lock()
	l.notifyLocked()
	l.mu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("next permit did not wake at its deadline")
	}
	// A restart retains a conservative interval even when the stored pre-fsync
	// reservation has elapsed. Other durable cooldown fields are unchanged.
	before := time.Now()
	restored, err := newProviderLimiter(path)
	if err != nil || restored.state.NextAt < before.Add(providerRequestInterval).UnixMilli() {
		t.Fatal("restart bypassed committed permit spacing", restored, err)
	}
}
