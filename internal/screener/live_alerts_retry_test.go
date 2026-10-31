package screener

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveAlertsPublicationRetryKeepsOriginalCutoffAndExpires(t *testing.T) {
	for _, published := range []bool{true, false} {
		name := "expires-with-visible-error"
		if published {
			name = "new-bar-sent-once"
		}
		t.Run(name, func(t *testing.T) {
			start := time.Date(2024, 1, 2, 12, 30, 0, 0, time.UTC)
			m, l, config, clock := scheduledAlertsFixture(t, start)
			config.ConditionIDs = config.ConditionIDs[:1]
			if _, err := l.Configure(config); err != nil {
				t.Fatal(err)
			}
			name := "eurusd-h1-2024-01-01-2024-01-04.csv"
			base := start.Truncate(time.Hour).Add(-2 * time.Hour)
			// Provider initially has 10:00 and 11:00, but not the bar due at 13:00.
			writeDataFile(t, m, name, reviewHourlyCSV(base.UnixMilli(), []float64{10, 11}))
			var sends atomic.Int64
			l.send = func(context.Context, LiveAlert) error { sends.Add(1); return nil }
			cutoff := make(chan int64, 4)
			refresh := l.refresh
			l.refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
				cutoff <- asOf
				return refresh(ctx, directory, groups, asOf)
			}
			due := start.Truncate(time.Hour).Add(time.Hour)
			l.Start(context.Background())
			clock.Store(due.UnixMilli())
			l.signal()
			info := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
				return !info.Running && info.LastRun == due.UnixMilli() && info.Status == "Waiting to retry closed-bar check"
			})
			if sends.Load() != 0 || info.NextRun != due.Add(liveAlertRetryDelay).UnixMilli() {
				t.Fatalf("missing bar was sent or first retry not scheduled: %+v sends=%d", info, sends.Load())
			}
			retryAt := due.Add(liveAlertPublicationWindow)
			if published {
				writeDataFile(t, m, name, reviewHourlyCSV(base.UnixMilli(), []float64{10, 11, 12}))
				retryAt = due.Add(liveAlertRetryDelay)
			}
			clock.Store(retryAt.UnixMilli())
			l.signal()
			info = waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return !info.Running && info.LastRun == retryAt.UnixMilli() })
			for range 2 {
				select {
				case got := <-cutoff:
					if got != due.UnixMilli() {
						t.Fatalf("retry changed closed-bar cutoff: %d want %d", got, due.UnixMilli())
					}
				case <-time.After(time.Second):
					t.Fatal("refresh cutoff missing")
				}
			}
			if info.NextRun != due.Add(time.Hour).UnixMilli() {
				t.Fatalf("retry moved the next UTC boundary or exceeded publication window: %+v", info)
			}
			if published {
				if sends.Load() != 1 || info.Sent != 1 || info.Error != "" {
					t.Fatalf("published closed bar not delivered once: %+v sends=%d", info, sends.Load())
				}
			} else if sends.Load() != 0 || info.Error == "" || info.Status != "Check stopped" {
				t.Fatalf("unavailable bar did not stop with visible status: %+v sends=%d", info, sends.Load())
			}
			// Same time cannot trigger another automatic cycle.
			l.signal()
			time.Sleep(10 * time.Millisecond)
			if len(cutoff) != 0 {
				t.Fatal("automatic retry continued after success or publication timeout")
			}
		})
	}
}

func TestLiveAlertsBusyManualRetryRetainsLatestActualClosedSemantics(t *testing.T) {
	start := time.Date(2024, 1, 2, 12, 30, 0, 0, time.UTC)
	m, l, config, clock := scheduledAlertsFixture(t, start)
	config.ConditionIDs = config.ConditionIDs[:1]
	if _, err := l.Configure(config); err != nil {
		t.Fatal(err)
	}
	// No 11:00 bar, so only the 10:00 bar can be checked explicitly at 12:30.
	writeDataFile(t, m, "eurusd-h1-2024-01-01-2024-01-04.csv", reviewHourlyCSV(start.Truncate(time.Hour).Add(-2*time.Hour).UnixMilli(), []float64{10}))
	var sends atomic.Int64
	l.send = func(context.Context, LiveAlert) error { sends.Add(1); return nil }
	m.mu.Lock()
	m.active = true
	m.mu.Unlock()
	l.Start(context.Background())
	if _, err := l.CheckNow(); err != nil {
		t.Fatal(err)
	}
	waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && info.Status == "Waiting to retry closed-bar check"
	})
	m.mu.Lock()
	m.active = false
	m.mu.Unlock()
	retryAt := start.Add(liveAlertRetryDelay)
	clock.Store(retryAt.UnixMilli())
	l.signal()
	info := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return !info.Running && info.LastRun == retryAt.UnixMilli() })
	if sends.Load() != 1 || info.Error != "" || info.NextRun != start.Truncate(time.Hour).Add(time.Hour).UnixMilli() {
		t.Fatalf("busy manual retry changed to strict scheduled semantics or moved boundary: %+v sends=%d", info, sends.Load())
	}
}
