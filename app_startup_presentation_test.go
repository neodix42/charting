package main

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func startupPresentationSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("startup presentation did not reach the expected gate")
	}
}

func startupPresentationResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("startup presentation did not return")
		return nil
	}
}

func fakeStartupWindow(a *App, maximised bool) (*[]string, *domain.Window) {
	operations := []string{}
	restore := domain.Window{}
	a.presentation.window = startupWindowActions{
		resize: func(_ context.Context, window domain.Window) {
			restore = window
			operations = append(operations, "resize hidden")
		},
		maximise:  func(context.Context) { operations = append(operations, "maximize") },
		show:      func(context.Context) { operations = append(operations, "show") },
		maximised: func(context.Context) bool { return maximised },
		size:      func(context.Context) (int, int) { return restore.Width, restore.Height },
	}
	return &operations, &restore
}

func TestStartupPresentationShowsSavedNormalBoundsMaximizedBeforeServicesFinish(t *testing.T) {
	metadata, release, serviceDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a := newBootstrapAppWithInitializer(func(ctx context.Context, app *App) error {
		app.prepareStartupWindowMetadata(domain.Window{Width: 1300, Height: 850, Maximized: false})
		close(metadata)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			close(serviceDone)
			return nil
		}
	})
	defer a.shutdown(context.Background())
	a.startup(context.Background())
	operations, restore := fakeStartupWindow(a, true)
	a.beginBootstrap()
	startupPresentationSignal(t, metadata)
	if err := a.StartupPainted(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-serviceDone:
		t.Fatal("presentation waited for unrelated service restoration")
	default:
	}
	if !reflect.DeepEqual(*operations, []string{"resize hidden", "maximize", "show"}) || restore.Width != 1300 || restore.Height != 850 || !restore.Maximized {
		t.Fatal("saved normal rectangle was discarded or exposed before maximize", operations, restore)
	}
	// Repeated HTML/React acknowledgements and recovery retries must not create
	// another resize/maximize/show cycle after the splash is visible.
	var callers sync.WaitGroup
	for range 5 {
		callers.Add(1)
		go func() { defer callers.Done(); _ = a.StartupPainted() }()
	}
	callers.Wait()
	if len(*operations) != 3 {
		t.Fatal("duplicate splash paint remapped the window", operations)
	}
	close(release)
}

func TestStartupPresentationWaitsForMetadataButNotItsFailure(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a := newBootstrapAppWithInitializer(func(context.Context, *App) error {
		close(entered)
		<-release
		return errors.New("workspace database is locked")
	})
	defer a.shutdown(context.Background())
	a.startup(context.Background())
	operations, restore := fakeStartupWindow(a, true)
	a.beginBootstrap()
	startupPresentationSignal(t, entered)
	finished := make(chan error, 1)
	go func() { finished <- a.StartupPainted() }()
	select {
	case err := <-finished:
		t.Fatal("splash was shown before metadata settled", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := startupPresentationResult(t, finished); err != nil {
		t.Fatal("failed workspace opening stranded the error/retry screen", err)
	}
	if len(*operations) != 3 || restore.Width != 1600 || restore.Height != 1000 {
		t.Fatal("storage failure could not present the fallback splash/error UI", operations, restore)
	}
}

func TestStartupPresentationCancellationNeverMapsTheWindow(t *testing.T) {
	entered := make(chan struct{})
	a := newBootstrapAppWithInitializer(func(ctx context.Context, _ *App) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	defer a.shutdown(context.Background())
	a.startup(context.Background())
	operations, _ := fakeStartupWindow(a, true)
	a.beginBootstrap()
	startupPresentationSignal(t, entered)
	finished := make(chan error, 1)
	go func() { finished <- a.StartupPainted() }()
	a.mu.Lock()
	a.domReady = true
	a.mu.Unlock()
	if a.beforeClose(context.Background()) {
		t.Fatal("startup close required an unmounted frontend")
	}
	if err := startupPresentationResult(t, finished); !errors.Is(err, context.Canceled) {
		t.Fatal("closing did not release presentation", err)
	}
	if len(*operations) != 0 {
		t.Fatal("late splash appeared after closing started", operations)
	}
}

func TestStartupPresentationBoundsCompositorWaitAndPreservesHiddenGeometry(t *testing.T) {
	a := newBootstrapAppWithInitializer(func(context.Context, *App) error { return nil })
	defer a.shutdown(context.Background())
	a.startup(context.Background())
	operations, _ := fakeStartupWindow(a, false)
	a.presentation.settleLimit = 20 * time.Millisecond
	a.prepareStartupWindowMetadata(domain.Window{Width: 1250, Height: 820})
	// No runtime frontend is attached to this context. A hidden geometry sample
	// must not call into Wails or replace the saved restore rectangle.
	a.mu.Lock()
	a.captureWindowLocked()
	hidden := a.window
	a.mu.Unlock()
	if hidden.Width != 1250 || hidden.Height != 820 || !hidden.Maximized {
		t.Fatal("hidden startup sampling replaced the restore rectangle", hidden)
	}
	finished := make(chan error, 1)
	go func() { finished <- a.StartupPainted() }()
	if err := startupPresentationResult(t, finished); err != nil || len(*operations) != 3 {
		t.Fatal("compositor refusal stranded a visible splash", err, operations)
	}
	a.mu.Lock()
	shown := a.presentation.shown
	a.mu.Unlock()
	if !shown {
		t.Fatal("bounded presentation did not allow an otherwise usable window")
	}
}

func TestFrontendReadyPresentsBeforeEnablingInteractiveCloseHandshake(t *testing.T) {
	a := newBootstrapAppWithInitializer(func(context.Context, *App) error { return nil })
	defer a.shutdown(context.Background())
	a.startup(context.Background())
	operations, _ := fakeStartupWindow(a, true)
	a.prepareStartupWindowMetadata(domain.Window{Width: 1250, Height: 820})
	a.presentation.window.show = func(context.Context) {
		if a.frontendReady {
			t.Fatal("hidden window accepted an interactive save/Keep-open handshake")
		}
		*operations = append(*operations, "show")
	}
	a.FrontendReady()
	a.mu.Lock()
	ready, shown := a.frontendReady, a.presentation.shown
	a.mu.Unlock()
	if !ready || !shown || len(*operations) != 3 {
		t.Fatal("frontend readiness bypassed first presentation", ready, shown, operations)
	}
}

func TestStartupPresentationWaitsForStableBoundsAfterMaximizedFlag(t *testing.T) {
	a := newBootstrapAppWithInitializer(func(context.Context, *App) error { return nil })
	defer a.shutdown(context.Background())
	a.startup(context.Background())
	fakeStartupWindow(a, true)
	a.prepareStartupWindowMetadata(domain.Window{Width: 1300, Height: 850})
	samples := [][2]int{{0, 0}, {1300, 850}, {3840, 2120}, {3840, 2120}}
	observed := 0
	a.presentation.window.size = func(context.Context) (int, int) {
		index := min(observed, len(samples)-1)
		observed++
		return samples[index][0], samples[index][1]
	}
	if err := a.StartupPainted(); err != nil {
		t.Fatal(err)
	}
	if observed < len(samples) {
		t.Fatal("maximized flag exposed the workspace before its final client bounds", observed)
	}
	a.mu.Lock()
	shown := a.presentation.shown
	a.mu.Unlock()
	if !shown {
		t.Fatal("stable maximized geometry did not complete presentation")
	}
}
