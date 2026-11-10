//go:build bindings

package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/candlestorage"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/storage"
)

func bootSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("startup synchronization did not complete")
	}
}

func bootLoad(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("workspace startup did not return")
		return nil
	}
}

func TestBootstrapWaitsForDOMWithoutBlockingWindowOrExposingPartialServices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	a := newBootstrapAppWithInitializer(func(ctx context.Context, app *App) error {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		store, err := storage.Open(path)
		if err == nil {
			app.mu.Lock()
			app.store = store
			app.mu.Unlock()
		}
		return err
	})
	defer a.shutdown(context.Background())
	a.startup(context.Background())
	loaded := make(chan error, 1)
	go func() { _, err := a.LoadWorkspace(); loaded <- err }()
	if _, err := os.Stat(path); !os.IsNotExist(err) || calls.Load() != 0 {
		t.Fatal("constructor/startup touched user storage before the window was ready", err)
	}
	a.beginBootstrap()
	bootSignal(t, entered)
	// Native callbacks can keep acquiring the facade mutex during slow work.
	responsive := make(chan struct{})
	go func() { a.mu.Lock(); a.mu.Unlock(); close(responsive) }()
	bootSignal(t, responsive)
	select {
	case err := <-loaded:
		t.Fatal("LoadWorkspace exposed a partially initialized app", err)
	default:
	}
	if calls.Load() != 1 {
		t.Fatal("parallel workspace loading duplicated initialization")
	}
	close(release)
	if err := bootLoad(t, loaded); err != nil {
		t.Fatal(err)
	}
	first, err := a.LoadWorkspace()
	if err != nil || len(first.Workspace.Tabs) != 1 || len(first.Workspace.Tabs[0].Charts) != 1 || calls.Load() != 1 {
		t.Fatal("ready app did not retain its one actual persisted workspace", first, err)
	}
}

func TestBootstrapReportsFastFailureBeforeExplicitSingleflightRetry(t *testing.T) {
	firstError := errors.New("workspace could not be opened")
	secondEntered, secondRelease := make(chan struct{}), make(chan struct{})
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	var calls atomic.Int32
	a := newBootstrapAppWithInitializer(func(ctx context.Context, app *App) error {
		if calls.Add(1) == 1 {
			return firstError
		}
		close(secondEntered)
		select {
		case <-secondRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
		store, err := storage.Open(path)
		if err == nil {
			app.mu.Lock()
			app.store = store
			app.mu.Unlock()
		}
		return err
	})
	defer a.shutdown(context.Background())
	a.beginBootstrap()
	bootSignal(t, a.bootstrap.attempt.done)
	// A failure preceding the first frontend request must be displayed once,
	// rather than immediately starting another invisible initialization.
	if _, err := a.LoadWorkspace(); !errors.Is(err, firstError) || calls.Load() != 1 {
		t.Fatal("first startup failure was hidden by an implicit retry", err)
	}
	if _, err := a.GetBackups(); !errors.Is(err, firstError) || calls.Load() != 1 {
		t.Fatal("automatic recovery read retried failed startup", err)
	}
	if _, err := a.ResetWorkspace(); !errors.Is(err, firstError) {
		t.Fatal("failed startup entered a native recovery dialog", err)
	}
	if _, err := a.ImportWorkspace(); !errors.Is(err, firstError) {
		t.Fatal("failed startup entered a native import dialog", err)
	}
	if _, err := a.ExportWorkspace(); !errors.Is(err, firstError) {
		t.Fatal("failed startup entered a native export dialog", err)
	}
	if _, err := a.RecoverWorkspace(1); !errors.Is(err, firstError) {
		t.Fatal("failed startup dereferenced absent recovery storage", err)
	}
	if _, err := a.SaveWorkspace(1, "startup-test", 1, domain.NewWorkspace()); !errors.Is(err, firstError) {
		t.Fatal("failed startup accepted a placeholder workspace save", err)
	}
	const waiters = 12
	results := make(chan error, waiters)
	go func() { _, err := a.LoadWorkspace(); results <- err }()
	bootSignal(t, secondEntered)
	for range waiters - 1 {
		go func() { _, err := a.LoadWorkspace(); results <- err }()
	}
	close(secondRelease)
	for range waiters {
		if err := bootLoad(t, results); err != nil {
			t.Fatal("parallel retry failed", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("parallel frontend requests repeated service initialization", calls.Load())
	}
}

func TestBootstrapCloseBeforeDOMDoesNotCreateAWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	a := newBootstrapApp(path)
	a.startup(context.Background())
	a.requestClose()
	a.mu.Lock()
	if !a.closeRequested || a.domReady {
		t.Fatal("early termination was not retained")
	}
	a.domReady = true
	a.mu.Unlock()
	if a.beforeClose(context.Background()) {
		t.Fatal("loading window required a nonexistent frontend save acknowledgement")
	}
	a.shutdown(context.Background())
	a.shutdown(context.Background()) // Native callback and Run defer both close.
	if _, err := a.LoadWorkspace(); !errors.Is(err, context.Canceled) {
		t.Fatal("closed startup accepted a new load", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("closing before initialization created a placeholder workspace", err)
	}
}

func TestBootstrapShutdownCancelsWaitersAndDrainsLateConstructedResources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	entered, constructed, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a := newBootstrapAppWithInitializer(func(ctx context.Context, app *App) error {
		store, err := storage.Open(path)
		if err != nil {
			return err
		}
		app.mu.Lock()
		app.store = store
		app.mu.Unlock()
		close(entered)
		<-ctx.Done()
		// Simulate a non-context constructor returning after cancellation. Its
		// new worker still belongs to this startup attempt and must be closed.
		candles := candlestorage.New(filepath.Dir(path), nil)
		app.mu.Lock()
		app.candles = candles
		app.mu.Unlock()
		close(constructed)
		<-release
		return ctx.Err()
	})
	a.startup(context.Background())
	a.beginBootstrap()
	bootSignal(t, entered)
	loads := make(chan error, 1)
	go func() { _, err := a.LoadWorkspace(); loads <- err }()
	a.mu.Lock()
	a.domReady = true
	a.mu.Unlock()
	if a.beforeClose(context.Background()) {
		t.Fatal("loading window could not close")
	}
	bootSignal(t, constructed)
	if err := bootLoad(t, loads); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation did not unblock the waiting workspace request", err)
	}
	shutdown := make(chan struct{})
	go func() { a.shutdown(context.Background()); close(shutdown) }()
	select {
	case <-shutdown:
		t.Fatal("shutdown returned before the constructor finished publishing")
	default:
	}
	close(release)
	bootSignal(t, shutdown)
	if _, err := a.candles.Info(); err == nil {
		t.Fatal("startup's late candle worker was not drained")
	}
	reopened, err := storage.Open(path)
	if err != nil {
		t.Fatal("startup shutdown leaked the workspace lock", err)
	}
	_ = reopened.Close()
}

func TestBootstrapUsesActualSavedWorkspaceAndKeepsRecoveryAvailable(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved geometry", true: "preserved recovery"}[invalid], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "workspace.sqlite")
			store, err := storage.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			workspace := loaded.Workspace
			workspace.Window = domain.Window{Width: 1300, Height: 850, Maximized: true}
			workspace.Tabs = append(workspace.Tabs, domain.NewTab("Actual saved tab"))
			ack, err := store.Save(loaded.Revision, "startup-test", 1, workspace)
			if err != nil {
				t.Fatal(err)
			}
			if invalid {
				// Explicit replacement creates the existing recoverable backup;
				// ordinary autosave deliberately does not create one each time.
				if _, err = store.Import(ack.Revision, workspace); err != nil {
					t.Fatal(err)
				}
			}
			_ = store.Close()
			const badDocument = `{"existing":"unrecognized, preserve this document"}`
			if invalid {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec("UPDATE workspace_state SET json_document=? WHERE id=1", badDocument)
				_ = db.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			a := newBootstrapApp(path)
			defer a.shutdown(context.Background())
			// No OnStartup/context is needed for this offline storage/service
			// test; saved native geometry is retained without invoking a window.
			a.beginBootstrap()
			actual, err := a.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if a.market == nil || a.candles == nil || a.jforex == nil {
				t.Fatal("LoadWorkspace exposed incomplete chart services")
			}
			for _, service := range []string{"screener", "predictions", "alerts"} {
				if err := a.awaitService(service); err != nil {
					t.Fatal(err)
				}
			}
			if !invalid {
				if actual.Recovery != "" || !reflect.DeepEqual(actual.Workspace, workspace) || a.window != workspace.Window {
					t.Fatal("startup substituted a default workspace or lost actual geometry", actual)
				}
				return
			}
			if actual.Recovery == "" || len(actual.Workspace.Tabs) != 0 {
				t.Fatal("invalid saved state was replaced during startup", actual)
			}
			backups, err := a.GetBackups()
			if err != nil || len(backups) == 0 {
				t.Fatal("startup recovery backups became unavailable", backups, err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			var document string
			err = db.QueryRow("SELECT json_document FROM workspace_state WHERE id=1").Scan(&document)
			_ = db.Close()
			if err != nil || document != badDocument {
				t.Fatal("startup rewrote preserved invalid data", document, err)
			}
			if recovered, err := a.RecoverWorkspace(backups[0].ID); err != nil || recovered.Workspace.ID != workspace.ID || recovered.Recovery != "" {
				t.Fatal("explicit saved backup recovery failed after startup", recovered, err)
			}
		})
	}
}

func TestBootstrapLockedWorkspacePreservesOwnerAndRetriesAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	owner, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	saved, err := owner.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := newBootstrapApp(path)
	defer a.shutdown(context.Background())
	a.beginBootstrap()
	bootSignal(t, a.bootstrap.attempt.done)
	if _, err := a.LoadWorkspace(); !errors.Is(err, storage.ErrLocked) {
		t.Fatal("startup ignored the existing owner or replaced its workspace", err)
	}
	if a.store != nil || a.market != nil {
		t.Fatal("a locked profile exposed partially initialized services")
	}
	if unchanged, err := owner.Load(); err != nil || !reflect.DeepEqual(unchanged, saved) {
		t.Fatal("failed startup changed the running owner's workspace", unchanged, err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if actual, err := a.LoadWorkspace(); err != nil || !reflect.DeepEqual(actual, saved) {
		t.Fatal("explicit Retry did not restore the released profile", actual, err)
	}
}
