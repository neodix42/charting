//go:build bindings

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/market"
	"cryptodesk/internal/predictions"
	"cryptodesk/internal/screener"
	"cryptodesk/internal/storage"
)

func TestWorkspaceAndStatusRemainResponsiveWhileOptionalServicesRestore(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, "workspace.sqlite")
	owner, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := owner.Load()
	if err != nil {
		t.Fatal(err)
	}
	workspace := initial.Workspace
	workspace.Tabs = append(workspace.Tabs, domain.NewTab("Saved tab"))
	ack, err := owner.Save(initial.Revision, "held-restore", 1, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.Close(); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	a := newBootstrapAppWithInitializer(func(ctx context.Context, app *App) error {
		store, e := storage.Open(path)
		if e != nil {
			return e
		}
		loaded, e := store.Load()
		if e != nil {
			_ = store.Close()
			return e
		}
		services := newServiceStartup()
		app.mu.Lock()
		app.store, app.market, app.services = store, market.New(market.Options{Factory: func(string) (exchange.Adapter, error) {
			return nil, errors.New("provider unavailable in isolated readiness test")
		}}), services
		app.revision, app.initialWorkspace = loaded.Revision, &loaded
		app.mu.Unlock()
		services.run("screener", "Restoring historical data tools…", func() error {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			scanner := screener.New(profile)
			app.mu.Lock()
			app.screener = scanner
			app.mu.Unlock()
			return nil
		})
		services.run("predictions", "Restoring saved forecasts…", func() error { return nil })
		services.run("alerts", "Restoring Live Alerts…", func() error { return services.wait(ctx, "screener") })
		return nil
	})
	defer a.shutdown(context.Background())
	a.beginBootstrap()
	bootSignal(t, entered)
	loaded, err := a.LoadWorkspace()
	if err != nil || loaded.Revision != ack.Revision || !reflect.DeepEqual(loaded.Workspace, workspace) {
		t.Fatal("optional work held or changed the actual workspace", loaded, err)
	}
	status := a.GetStartupStatus()
	if !status.WorkspaceReady || status.Ready || len(status.Tasks) != 3 {
		t.Fatal("incorrect independent readiness", status)
	}
	for _, task := range status.Tasks {
		if task.ElapsedMS < 0 || task.ElapsedMS > 3000 {
			t.Fatal("invalid task time", task)
		}
	}
	viewDone := make(chan error, 1)
	go func() { _, e := a.GetScreenerAnalysisView(); viewDone <- e }()
	select {
	case err := <-viewDone:
		t.Fatal("unrestored Screener facade escaped its gate", err)
	default:
	}
	// A waiting Hyperliquid chart cannot own the global market lock.
	chartDone := make(chan error, 1)
	go func() {
		_, e := a.AttachChart("held", 1, domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1d"}, 0, 0)
		chartDone <- e
	}()
	responsive := make(chan struct{})
	go func() { a.GetDiagnostics(); close(responsive) }()
	bootSignal(t, responsive)
	// Core persistence works before optional restoration is released.
	workspace.Theme = "dark"
	if _, err = a.SaveWorkspace(loaded.Revision, "held-core-save", 1, workspace); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = bootLoad(t, viewDone); err != nil {
		t.Fatal(err)
	}
	// The isolated market factory cannot contact a real exchange.
	a.shutdown(context.Background())
	select {
	case <-chartDone:
	case <-time.After(3 * time.Second):
		t.Fatal("chart facade survived shutdown")
	}
}

func TestOptionalRestoreShutdownCancelsWaitersAndDrainsLateResources(t *testing.T) {
	profile := t.TempDir()
	entered, published, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a := newBootstrapAppWithInitializer(func(ctx context.Context, app *App) error {
		store, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
		if err != nil {
			return err
		}
		services := newServiceStartup()
		app.mu.Lock()
		app.store, app.services = store, services
		app.mu.Unlock()
		services.run("predictions", "Restoring saved forecasts…", func() error {
			close(entered)
			<-ctx.Done()
			manager := predictions.New(profile, nil, nil)
			app.mu.Lock()
			app.predictions = manager
			app.mu.Unlock()
			close(published)
			<-release
			return ctx.Err()
		})
		services.run("screener", "Restoring historical data tools…", func() error { <-ctx.Done(); return ctx.Err() })
		services.run("alerts", "Restoring Live Alerts…", func() error { return services.wait(ctx, "screener") })
		return nil
	})
	a.beginBootstrap()
	bootSignal(t, entered)
	if _, err := a.LoadWorkspace(); err != nil {
		t.Fatal(err)
	}
	waiter := make(chan error, 1)
	go func() { _, err := a.PreparePredictionModel(); waiter <- err }()
	closed := make(chan struct{})
	go func() { a.shutdown(context.Background()); close(closed) }()
	bootSignal(t, published)
	select {
	case <-closed:
		t.Fatal("shutdown failed to drain a late constructor")
	default:
	}
	close(release)
	bootSignal(t, closed)
	if err := bootLoad(t, waiter); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled readiness waiter escaped", err)
	}
	if _, err := a.predictions.Prepare(); err == nil {
		t.Fatal("late manager remained open")
	}
	reopened, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
	if err != nil {
		t.Fatal("profile lock leaked", err)
	}
	_ = reopened.Close()
}

func TestUnstartedServiceProgressHasFiniteZeroDuration(t *testing.T) {
	a := &App{services: newServiceStartup()}
	status := a.GetStartupStatus()
	for _, task := range status.Tasks {
		if task.ElapsedMS != 0 || task.State != "loading" {
			t.Fatal(task)
		}
	}
}

func TestOptionalAlertRestoreFailurePreservesWorkspaceAndSafeResponse(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, "screener-live-alerts.json")
	original := []byte(`{"version":999}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	a := newBootstrapApp(filepath.Join(profile, "workspace.sqlite"))
	defer a.shutdown(context.Background())
	a.beginBootstrap()
	loaded, err := a.LoadWorkspace()
	if err != nil || len(loaded.Workspace.Tabs) != 1 {
		t.Fatal("optional error blocked workspace", loaded, err)
	}
	info := a.GetScreenerLiveAlerts()
	if info.Error == "" || info.Assets == nil || info.ConditionScopes == nil || info.Recent == nil || info.Checks == nil || info.Config.ConditionIDs == nil {
		t.Fatal("unsafe unavailable alert response", info)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(original) {
		t.Fatal("failed optional restore rewrote original", string(data), err)
	}
	found := false
	for _, task := range a.GetStartupStatus().Tasks {
		if task.ID == "alerts" {
			found = task.State == "error" && task.Error != ""
		}
	}
	if !found {
		t.Fatal("optional restore error was hidden from progress")
	}
}
