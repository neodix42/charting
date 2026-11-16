package screener

import (
	"context"
	"fmt"
	"path/filepath"
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
					want := due.UnixMilli()
					if got != want {
						t.Fatalf("refresh changed the original closed-bar cutoff: %d want %d", got, want)
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
			} else if sends.Load() != 0 || info.Error == "" || info.Status != "Check completed with errors; waiting for next closed bar" {
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

func TestLiveAlertsManualBusyRetryRunsWithoutAnyEligibleCalendarBoundary(t *testing.T) {
	start := time.Date(2024, 1, 2, 12, 30, 0, 0, time.UTC)
	m, l, config, clock := scheduledAlertsFixture(t, start)
	saved, err := m.SavedAnalysisConditions()
	if err != nil {
		t.Fatal(err)
	}
	// Retained historical evidence can coexist with a legacy or externally
	// repaired impossible rule. It has no automatic close, but manual checks
	// remain available and their busy retries must still have a timer.
	saved[0].Request.Conditions[0].Operator = "equal"
	saved[0].Request.Conditions[0].Left.Field = "weekday"
	saved[0].Request.Conditions[0].Right.Value = 9
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), savedAnalysisConditions{Version: 1, Conditions: saved}); err != nil {
		t.Fatal(err)
	}
	config.ConditionIDs = config.ConditionIDs[:1]
	base := start.Truncate(time.Hour).Add(-2 * time.Hour).UnixMilli()
	writeDataFile(t, m, "eurusd-h1-2024-01-01-2024-01-04.csv", fmt.Sprintf("timestamp,open,high,low,close,weekday\n%d,10,11,9,10,2\n%d,11,12,10,11,2\n", base, base+time.Hour.Milliseconds()))
	info, err := l.Configure(config)
	if err != nil || info.NextRun != 0 {
		t.Fatal("impossible calendar unexpectedly acquired an automatic boundary", info, err)
	}
	var refreshes atomic.Int64
	refresh := l.refresh
	l.refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
		refreshes.Add(1)
		return refresh(ctx, directory, groups, asOf)
	}
	m.mu.Lock()
	m.active = true
	m.mu.Unlock()
	l.Start(context.Background())
	if _, err = l.CheckNow(); err != nil {
		t.Fatal(err)
	}
	info = waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool {
		return !info.Running && info.Status == "Waiting to retry closed-bar check"
	})
	retryAt := start.Add(liveAlertRetryDelay).UnixMilli()
	if info.NextRun != retryAt || refreshes.Load() != 0 {
		t.Fatal("zero automatic boundary suppressed explicit manual retry", info, refreshes.Load())
	}
	m.mu.Lock()
	m.active = false
	m.mu.Unlock()
	clock.Store(retryAt)
	l.signal()
	info = waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return !info.Running && info.LastRun == retryAt })
	if info.Error != "" || refreshes.Load() != 1 || info.NextRun != 0 || len(info.Checks) != 1 || info.Checks[0].Status != "completed" || !info.Checks[0].Manual || info.Checks[0].AssetsScanned != 1 {
		t.Fatal("manual retry did not scan once and return to unscheduled state", info, refreshes.Load())
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

func TestLiveAlertsStopAfterThreeRetriesPerClosedCandle(t *testing.T) {
	for _, period := range []string{"h1", "d1", "w1"} {
		for _, busy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/busy=%t", period, busy), func(t *testing.T) {
				start := time.Date(2024, 1, 7, 23, 30, 0, 0, time.UTC)
				m, l, config, clock := scheduledAlertsFixture(t, start)
				id := config.ConditionIDs[0]
				if period == "h1" {
					writeDataFile(t, m, "eurusd-h1-2024-01-01-2024-01-04.csv", reviewHourlyCSV(start.Truncate(time.Hour).Add(-2*time.Hour).UnixMilli(), []float64{10, 11}))
				} else if period == "d1" {
					id = config.ConditionIDs[1]
				} else {
					name := "eurusd-w1-2023-12-25-2024-01-08.csv"
					writeDataFile(t, m, name, "timestamp,open,high,low,close\n1703462400000,10,12,9,11\n")
					saved := saveWeeklyLiveCondition(t, m, weeklyLiveRequest(), name)
					id = saved.ID
				}
				config.ConditionIDs = []string{id}
				if _, err := l.Configure(config); err != nil {
					t.Fatal(err)
				}
				var refreshes atomic.Int64
				refresh := l.refresh
				l.refresh = func(ctx context.Context, directory string, groups []historyGroup, asOf int64) (map[string]bool, error) {
					refreshes.Add(1)
					return refresh(ctx, directory, groups, asOf)
				}
				if busy {
					m.mu.Lock()
					m.active = true
					m.mu.Unlock()
				}
				l.Start(context.Background())
				due := start.Truncate(time.Hour).Add(time.Hour)
				for attempt := 0; attempt <= liveAlertMaxRetries; attempt++ {
					at := due.Add(time.Duration(attempt) * liveAlertRetryDelay)
					clock.Store(at.UnixMilli())
					l.signal()
					info := waitLiveSchedule(t, l, func(info LiveAlertsInfo) bool { return !info.Running && info.LastRun == at.UnixMilli() })
					if attempt < liveAlertMaxRetries {
						if info.Status != "Waiting to retry closed-bar check" || info.NextRun != at.Add(liveAlertRetryDelay).UnixMilli() {
							t.Fatal("retry missing", attempt, info)
						}
					} else {
						if info.Status != "Retry limit reached; waiting for next eligible bar close" || info.NextRun != nextLiveAlertCheck(due, []string{period}).UnixMilli() {
							t.Fatal("retry budget renewed", info)
						}
						if len(info.Checks) != 1 || info.Checks[0].ProgressState == "queued" {
							t.Fatal("exhausted row still queued", info.Checks)
						}
					}
				}
				expected := int64(1 + liveAlertMaxRetries)
				if busy {
					expected = 0
				}
				if refreshes.Load() != expected {
					t.Fatalf("refreshes=%d want %d", refreshes.Load(), expected)
				}
				// Repeated wakes (or the old morning retry) must not restart this candle.
				later := due.Add(8 * time.Hour)
				if period == "h1" {
					later = due.Add(10 * time.Minute)
				}
				clock.Store(later.UnixMilli())
				l.signal()
				time.Sleep(15 * time.Millisecond)
				if l.Info().LastRun != due.Add(3*liveAlertRetryDelay).UnixMilli() || refreshes.Load() != expected {
					t.Fatal("exhausted candle ran again", l.Info())
				}
				if busy {
					m.mu.Lock()
					m.active = false
					m.mu.Unlock()
				}
			})
		}
	}
}

func TestLiveAlertRetryBudgetSurvivesNewlyDuePeers(t *testing.T) {
	id, peer := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	monday := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	weekly := LiveAlertsConfig{ConditionIDs: []string{id}, scope: []LiveAlertConditionPeriod{{ConditionID: id, Period: "w1"}}}
	attempts := liveAlertAttempts{}
	attempts.record(weekly, monday.UnixMilli(), false, true)
	for i := 1; i <= liveAlertMaxRetries; i++ {
		joined := copyLiveAlertsConfig(weekly)
		joined.ConditionIDs = append(joined.ConditionIDs, peer)
		joined.scope = append(joined.scope, LiveAlertConditionPeriod{ConditionID: peer, Period: "h1"})
		attempts.record(joined, monday.Add(time.Duration(i)*time.Hour).UnixMilli(), false, false)
		remaining := attempts.remaining(joined)
		if containsLiveValue(remaining.ConditionIDs, id) != (i < liveAlertMaxRetries) || !containsLiveValue(remaining.ConditionIDs, peer) {
			t.Fatal("peer renewed weekly budget or lost new hourly budget", i, remaining)
		}
	}
	attempts.record(weekly, monday.AddDate(0, 0, 7).UnixMilli(), false, false)
	if len(attempts.remaining(weekly).ConditionIDs) != 1 {
		t.Fatal("next week did not get a fresh budget")
	}
}
