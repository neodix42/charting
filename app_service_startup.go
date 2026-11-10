package main

import (
	"context"
	"sync"
	"time"

	"cryptodesk/internal/candlestorage"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/hyperliquid"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/market"
	"cryptodesk/internal/predictions"
	"cryptodesk/internal/screener"
	"cryptodesk/internal/telegram"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Background restoration has its own readiness gates. Workspace persistence and
// ordinary charts do not need to wait for historical tools or saved forecasts.
type serviceStartup struct {
	mu    sync.Mutex
	tasks map[string]*serviceRestoreTask
	order []string
	wg    sync.WaitGroup
}

type serviceRestoreTask struct {
	done     chan struct{}
	message  string
	started  time.Time
	finished bool
	err      error
	duration time.Duration
}

type StartupTask struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	State     string `json:"state"`
	ElapsedMS int64  `json:"elapsedMs"`
	Error     string `json:"error,omitempty"`
}

type StartupStatus struct {
	WorkspaceReady bool          `json:"workspaceReady"`
	Ready          bool          `json:"ready"`
	Phase          string        `json:"phase"`
	ElapsedMS      int64         `json:"elapsedMs"`
	Tasks          []StartupTask `json:"tasks"`
	Error          string        `json:"error,omitempty"`
}

func newServiceStartup() *serviceStartup {
	s := &serviceStartup{tasks: map[string]*serviceRestoreTask{}, order: []string{"screener", "predictions", "alerts"}}
	for _, id := range s.order {
		s.tasks[id] = &serviceRestoreTask{done: make(chan struct{})}
	}
	return s
}

func (s *serviceStartup) run(id, message string, work func() error) {
	s.mu.Lock()
	task := s.tasks[id]
	task.message, task.started = message, time.Now()
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		err := work()
		s.mu.Lock()
		task.err, task.finished, task.duration = err, true, time.Since(task.started)
		close(task.done)
		s.mu.Unlock()
	}()
}

func (s *serviceStartup) wait(ctx context.Context, id string) error {
	s.mu.Lock()
	task := s.tasks[id]
	s.mu.Unlock()
	if task == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-task.done:
		s.mu.Lock()
		err := task.err
		s.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
}

func (a *App) awaitService(id string) error {
	if err := a.awaitBootstrap(false); err != nil {
		return err
	}
	a.mu.Lock()
	services, ctx := a.services, a.ctx
	a.mu.Unlock()
	if services == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return services.wait(ctx, id)
}

func (a *App) startupPhase(message string) {
	if b := a.bootstrap; b != nil {
		b.mu.Lock()
		b.phase = message
		b.mu.Unlock()
	}
}

// This endpoint only inspects memory. It never waits for a constructor, scans
// CSVs or measures a storage folder, so the loading UI stays responsive.
func (a *App) GetStartupStatus() StartupStatus {
	status := StartupStatus{WorkspaceReady: true, Ready: true, Tasks: []StartupTask{}}
	if b := a.bootstrap; b != nil {
		b.mu.Lock()
		status.WorkspaceReady = b.attempt.finished && b.attempt.err == nil && !b.closed
		elapsed := b.duration
		if !b.attempt.finished {
			elapsed = time.Since(b.started)
		}
		status.Phase, status.ElapsedMS = b.phase, elapsed.Milliseconds()
		if b.attempt.err != nil {
			status.Error = b.attempt.err.Error()
		}
		b.mu.Unlock()
	}
	a.mu.Lock()
	services, candles := a.services, a.candles
	a.mu.Unlock()
	status.Ready = status.WorkspaceReady
	if services != nil {
		services.mu.Lock()
		for _, id := range services.order {
			task := services.tasks[id]
			timing := task.duration
			state := "ready"
			if !task.finished {
				state = "loading"
				if !task.started.IsZero() {
					timing = time.Since(task.started)
				}
				status.Ready = false
			}
			item := StartupTask{ID: id, Message: task.message, State: state, ElapsedMS: timing.Milliseconds()}
			if task.err != nil {
				item.State, item.Error = "error", task.err.Error()
			}
			status.Tasks = append(status.Tasks, item)
		}
		services.mu.Unlock()
	}
	if candles != nil {
		pending, errorText := candles.StartupState()
		state := "ready"
		if pending {
			state = "loading"
			status.Ready = false
		}
		if errorText != "" {
			state = "error"
		}
		status.Tasks = append(status.Tasks, StartupTask{ID: "candles", Message: "Checking saved candle cache…", State: state, Error: errorText})
	}
	return status
}

func (a *App) initializeChartServices(ctx context.Context) error {
	profile := a.store.Directory()
	a.startupPhase("Restoring exchange settings…")
	forex, trader, messenger := jforex.New(profile), hyperliquid.NewStore(profile), telegram.NewStore(profile)
	a.mu.Lock()
	a.jforex, a.hyperliquid, a.telegram = forex, trader, messenger
	a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	a.startupPhase("Starting chart services…")
	candles := candlestorage.NewDeferred(profile, func(message string) {
		a.mu.Lock()
		appCtx := a.ctx
		a.mu.Unlock()
		if appCtx != nil && appCtx.Err() == nil {
			runtime.EventsEmit(appCtx, "candle-storage-error", message)
		}
	})
	services := newServiceStartup()
	feeds := market.New(market.Options{Cache: chartCandleCache{manager: candles, forex: forex, loadScreener: func() (*screener.Manager, error) {
		if err := a.awaitService("screener"); err != nil {
			return nil, err
		}
		a.mu.Lock()
		scanner := a.screener
		a.mu.Unlock()
		return scanner, nil
	}}, Factory: func(provider string) (exchange.Adapter, error) {
		if provider == "dukascopy" {
			return forex, nil
		}
		return exchange.New(provider)
	}, Emit: func(event market.Event) {
		a.mu.Lock()
		appCtx := a.ctx
		a.mu.Unlock()
		if appCtx != nil && appCtx.Err() == nil {
			runtime.EventsEmit(appCtx, "market:update", event)
		}
	}})
	a.mu.Lock()
	a.candles, a.market, a.services = candles, feeds, services
	a.mu.Unlock()
	services.run("screener", "Restoring historical data tools…", func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		scanner := screener.New(profile)
		a.mu.Lock()
		a.screener = scanner
		a.mu.Unlock()
		return ctx.Err()
	})
	services.run("predictions", "Restoring saved forecasts…", func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		forecasts := predictions.New(profile, a.predictionDatasets, predictions.NewRuntime(profile))
		a.mu.Lock()
		a.predictions = forecasts
		a.mu.Unlock()
		return ctx.Err()
	})
	services.run("alerts", "Restoring Live Alerts…", func() error {
		if err := services.wait(ctx, "screener"); err != nil {
			return err
		}
		a.mu.Lock()
		scanner := a.screener
		a.mu.Unlock()
		alerts, err := screener.NewLiveAlerts(scanner, a.sendScreenerLiveAlert)
		a.mu.Lock()
		a.liveAlerts = alerts
		if err != nil {
			a.liveAlertsError = "Could not restore Live Alerts settings; saved data was preserved: " + err.Error()
		}
		a.mu.Unlock()
		if err == nil && ctx.Err() == nil {
			alerts.Start(ctx)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	})
	return ctx.Err()
}
