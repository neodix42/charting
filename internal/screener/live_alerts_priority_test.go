package screener

import (
	"context"
	"cryptodesk/internal/domain"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAlertPriorityFinishesEveryHigherTimeframeBeforeLowerAndAllowsOtherSources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	work := []liveAlertWork{
		{0, "hyperliquid", "h1", 3600}, {1, "hyperliquid", "d1", 86400},
		{2, "hyperliquid", "w1", 604800}, {3, "hyperliquid", "w1", 604800},
		{4, "bybit", "h1", 3600}, {5, "dukascopy", "w1", 604800},
		{6, "dukascopy", "w1", 604800}, {7, "dukascopy", "d1", 86400},
	}
	gates := make([]chan struct{}, len(work))
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	started := make(chan int, len(work))
	done := make(chan []error, 1)
	var mu sync.Mutex
	finished := map[string]map[int64]int{}
	active, dukascopyActive := 0, 0
	go func() {
		done <- runPriorityAlertWork(ctx, work, func(ctx context.Context, task liveAlertWork) error {
			mu.Lock()
			active++
			if active > liveAlertCheckConcurrency {
				t.Error("global alert worker limit exceeded")
			}
			if task.source == "dukascopy" {
				dukascopyActive++
				if dukascopyActive != 1 {
					t.Error("Dukascopy refreshes overlapped")
				}
			}
			if task.source == "hyperliquid" && task.duration < 604800 && finished[task.source][604800] != 2 || task.source == "hyperliquid" && task.duration < 86400 && finished[task.source][86400] != 1 || task.source == "dukascopy" && task.duration < 604800 && finished[task.source][604800] != 2 {
				t.Errorf("lower timeframe started before all higher alerts finished: %+v", task)
			}
			mu.Unlock()
			started <- task.condition
			select {
			case <-gates[task.condition]:
			case <-ctx.Done():
			}
			mu.Lock()
			active--
			if task.source == "dukascopy" {
				dukascopyActive--
			}
			if finished[task.source] == nil {
				finished[task.source] = map[int64]int{}
			}
			finished[task.source][task.duration]++
			mu.Unlock()
			return ctx.Err()
		}, nil, nil, nil)
	}()
	seen := map[int]bool{}
	wait := func(want int) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !seen[want] {
			select {
			case id := <-started:
				seen[id] = true
			case <-deadline:
				t.Fatalf("alert %d did not start; observed %v", want, seen)
			}
		}
	}
	// The independent hourly source starts while both Hyperliquid weekly
	// checks and the first Dukascopy weekly check are still blocked.
	for _, id := range []int{2, 3, 4, 5} {
		wait(id)
	}
	close(gates[4])
	close(gates[2])
	close(gates[5])
	wait(6)
	close(gates[3])
	wait(1)
	close(gates[1])
	wait(0)
	close(gates[0])
	close(gates[6])
	wait(7)
	close(gates[7])
	select {
	case failures := <-done:
		for _, err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("alert workers did not drain")
	}
}

func TestAlertPriorityCancellationDoesNotStartQueuedLowerTimeframes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var attempts atomic.Int32
	done := make(chan []error, 1)
	go func() {
		done <- runPriorityAlertWork(ctx, []liveAlertWork{{0, "hyperliquid", "h1", 3600}, {1, "hyperliquid", "w1", 604800}}, func(ctx context.Context, task liveAlertWork) error {
			attempts.Add(1)
			if task.period != "w1" {
				t.Error("queued hourly work started after cancellation")
			}
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}, nil, nil, nil)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("weekly alert did not start")
	}
	cancel()
	select {
	case failures := <-done:
		if attempts.Load() != 1 || !errors.Is(errors.Join(failures...), context.Canceled) {
			t.Fatal(attempts.Load(), failures)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled alert did not drain")
	}
}

func TestAlertPriorityWorkSplitsLegacyPeriodsWithoutWideningScheduledScope(t *testing.T) {
	saved := SavedAnalysisCondition{ID: strings.Repeat("a", 32), Request: AnalysisRequest{Period: "all"}, Analysis: &SavedConditionAnalysis{Assets: []SavedConditionProfitability{{Symbol: "btcusd", Timeframe: "h1", Matches: 1}, {Symbol: "btcusd", Timeframe: "d1", Matches: 1}, {Symbol: "btcusd", Timeframe: "w1", Matches: 1}}}}
	for _, source := range []string{"dukascopy", "hyperliquid"} {
		config := LiveAlertsConfig{ConditionIDs: []string{saved.ID}, ConditionSources: []LiveAlertConditionSource{{ConditionID: saved.ID, Source: source}}}
		work := liveAlertPriorityWork([]SavedAnalysisCondition{saved}, config, time.Now().UnixMilli(), false)
		if len(work) != 3 {
			t.Fatal("legacy all-period condition was not split", source, work)
		}
		config.scope = []LiveAlertConditionPeriod{{ConditionID: saved.ID, Period: "d1"}}
		work = liveAlertPriorityWork([]SavedAnalysisCondition{saved}, config, time.Now().UnixMilli(), true)
		if len(work) != 1 || work[0].period != "d1" || work[0].duration != 86400 {
			t.Fatal("priority planning widened a due-period scope", source, work)
		}
	}
}

func TestLiveAlertCycleDeliversAllWeeklyBeforeDailyBeforeHourly(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "scheduled"}[scheduled], func(t *testing.T) {
			_, l, saved, fake := namedPublicLiveFixture(t)
			fake.items = []domain.Instrument{{NativeID: "BTC", Market: "perpetual", Availability: "available"}}
			l.publicData.adapters["hyperliquid"] = fake
			now := time.Date(2026, 11, 9, 0, 0, 5, 0, time.UTC)
			l.now = func() time.Time { return now }
			config := LiveAlertsConfig{Enabled: true}
			for i, period := range []string{"h1", "d1", "w1", "w1"} {
				config.Alerts = append(config.Alerts, LiveAlertDefinition{ID: strings.Repeat(string(rune('a'+i)), 32), Name: period + string(rune('a'+i)), ConditionID: saved.ID, Period: period, DataSource: "hyperliquid", Enabled: true})
			}
			if _, err := l.Configure(config); err != nil {
				t.Fatal(err)
			}
			if scheduled {
				resolved, err := l.resolveConfig(config)
				if err != nil {
					t.Fatal(err)
				}
				config = dueLiveAlertsConfig(resolved, now.Add(-time.Minute), now)
			}
			var mu sync.Mutex
			var sent []string
			l.send = func(_ context.Context, alert LiveAlert) error {
				mu.Lock()
				defer mu.Unlock()
				sent = append(sent, alert.Timeframe)
				return nil
			}
			if err := l.checkCycle(context.Background(), config, now.UnixMilli(), scheduled); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sent, []string{"w1", "w1", "d1", "h1"}) {
				t.Fatal("actual refresh/evaluation/delivery did not honor timeframe priority", sent)
			}
			for _, entry := range config.Alerts {
				row := sequentialCheckRow(t, l, entry.ID)
				if row.Status != "completed" || row.AssetsScanned != 1 || row.SentAlerts != 1 {
					t.Fatal("priority dispatch lost independent check history", row)
				}
			}
		})
	}
}
