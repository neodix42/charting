package main

import (
	"context"
	"errors"
	"sync"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/storage"
)

// Each waiting caller owns one attempt's result. A later explicit Retry cannot
// turn the first failure into an unnoticed retry or replace its error in flight.
type bootstrapAttempt struct {
	done        chan struct{}
	started     bool
	finished    bool
	failureRead bool
	err         error
}

type appBootstrap struct {
	mu         sync.Mutex
	enabled    bool
	closed     bool
	attempt    *bootstrapAttempt
	cancel     context.CancelFunc
	initialize func(context.Context, *App) error
}

// Native startup binds this lightweight receiver before touching user databases
// or service recovery. No placeholder workspace is created for the loading UI.
func newBootstrapApp(path string) *App {
	return newBootstrapAppWithInitializer(func(ctx context.Context, a *App) error {
		return a.initializeWorkspace(ctx, path)
	})
}

func newBootstrapAppWithInitializer(initialize func(context.Context, *App) error) *App {
	return &App{window: domain.Window{Width: 1600, Height: 1000}, bootstrap: &appBootstrap{
		attempt: &bootstrapAttempt{done: make(chan struct{})}, initialize: initialize,
	}}
}

// beginBootstrap is deliberately nonblocking and is called only after DOM ready.
// LoadWorkspace can already be waiting, without making the native callback wait.
func (a *App) beginBootstrap() {
	if a.bootstrap == nil {
		return
	}
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	b := a.bootstrap
	b.mu.Lock()
	b.enabled = true
	b.startLocked(ctx, a)
	b.mu.Unlock()
}

func (b *appBootstrap) startLocked(parent context.Context, a *App) {
	attempt := b.attempt
	if b.closed || !b.enabled || attempt.started || attempt.finished {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	b.cancel = cancel
	attempt.started = true
	go func() {
		err := ctx.Err()
		if err == nil {
			err = b.initialize(ctx, a)
		}
		if err == nil {
			err = ctx.Err()
		}
		b.mu.Lock()
		attempt.err, attempt.finished = err, true
		close(attempt.done)
		b.mu.Unlock()
	}()
}

// Only LoadWorkspace starts a new attempt after failure. Recovery/status reads
// must report that failure instead of repeatedly reopening a broken/locked file.
func (a *App) awaitBootstrap(retry bool) error {
	b := a.bootstrap
	if b == nil {
		return nil
	}
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return context.Canceled
	}
	if retry && b.attempt.finished && b.attempt.err != nil && b.attempt.failureRead && ctx.Err() == nil {
		if b.cancel != nil {
			b.cancel()
		}
		b.attempt = &bootstrapAttempt{done: make(chan struct{})}
	}
	attempt := b.attempt
	b.startLocked(ctx, a)
	b.mu.Unlock()
	select {
	case <-attempt.done:
		b.mu.Lock()
		closed := b.closed
		if retry && attempt.err != nil {
			attempt.failureRead = true
		}
		b.mu.Unlock()
		if closed {
			return context.Canceled
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return attempt.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *appBootstrap) stop() {
	b.mu.Lock()
	b.closed = true
	if b.cancel != nil {
		b.cancel()
	}
	attempt := b.attempt
	if !attempt.started && !attempt.finished {
		attempt.err, attempt.finished = context.Canceled, true
		close(attempt.done)
	}
	b.mu.Unlock()
	// Constructors cannot publish resources after shutdown has drained them.
	<-attempt.done
}

func (a *App) initializeWorkspace(ctx context.Context, path string) (err error) {
	store, err := storage.Open(path)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.store = store
	a.mu.Unlock()
	servicesStarted := false
	defer func() {
		if err != nil && !servicesStarted {
			_ = store.Close()
			a.mu.Lock()
			if a.store == store {
				a.store = nil
			}
			a.mu.Unlock()
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = desktopSmokePrepare(ctx, store); err != nil {
		return err
	}
	loaded, loadErr := store.Load()
	var recovery *storage.RecoveryError
	if loadErr != nil && !errors.As(loadErr, &recovery) {
		return loadErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	servicesStarted = true
	if err = a.initializeServices(ctx); err != nil {
		return err
	}
	window := loaded.Workspace.Window
	if window.Width < 400 {
		window = domain.Window{Width: 1600, Height: 1000}
	}
	a.mu.Lock()
	if err = ctx.Err(); err == nil {
		a.revision = loaded.Revision
		// The close path shares this mutex and cancels ctx before the native
		// loop ends, fencing all saved-window queries during background startup.
		a.applyWindowLocked(window)
	}
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if a.liveAlerts != nil && a.liveAlertsError == "" {
		a.liveAlerts.Start(ctx)
	}
	return nil
}
