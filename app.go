package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cryptodesk/internal/candlestorage"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/market"
	"cryptodesk/internal/predictions"
	"cryptodesk/internal/screener"
	"cryptodesk/internal/storage"
	"cryptodesk/internal/telegram"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the intentionally small privileged webview facade. Feed URLs, filesystem
// paths and execution capabilities are never accepted from imported workspaces.
type App struct {
	ctx             context.Context
	cancel          context.CancelFunc
	store           *storage.Store
	market          *market.Service
	candles         *candlestorage.Manager
	screener        *screener.Manager
	predictions     *predictions.Manager
	jforex          *jforex.Manager
	jforexMu        sync.Mutex
	telegram        *telegram.Store
	liveAlerts      *screener.LiveAlerts
	liveAlertsError string
	mu              sync.Mutex
	revision        int64
	closeToken      string
	closeAllowed    bool
	closeTimedOut   bool
	closeTimer      *time.Timer
	domReady        bool
	frontendReady   bool
	closeRequested  bool
	window          domain.Window
}

func newApp(store *storage.Store, loaded storage.Loaded) *App {
	a := &App{store: store, revision: loaded.Revision, window: loaded.Workspace.Window}
	a.screener = screener.New(store.Directory())
	a.predictions = predictions.New(store.Directory(), a.predictionDatasets, predictions.NewRuntime(store.Directory()))
	a.jforex = jforex.New(store.Directory())
	a.telegram = telegram.NewStore(store.Directory())
	alerts, err := screener.NewLiveAlerts(a.screener, a.sendScreenerLiveAlert)
	a.liveAlerts = alerts
	if err != nil {
		a.liveAlertsError = "Could not restore Live Alerts settings; saved data was preserved: " + err.Error()
	}
	if a.window.Width < 400 {
		a.window = domain.Window{Width: 1600, Height: 1000}
	}
	a.candles = candlestorage.New(store.Directory(), func(message string) {
		a.mu.Lock()
		ctx := a.ctx
		a.mu.Unlock()
		if ctx != nil && ctx.Err() == nil {
			runtime.EventsEmit(ctx, "candle-storage-error", message)
		}
	})
	a.market = market.New(market.Options{Cache: chartCandleCache{manager: a.candles, forex: a.jforex}, Factory: func(provider string) (exchange.Adapter, error) {
		if provider == "dukascopy" {
			return a.jforex, nil
		}
		return exchange.New(provider)
	}, Emit: func(e market.Event) {
		a.mu.Lock()
		ctx := a.ctx
		a.mu.Unlock()
		if ctx != nil {
			runtime.EventsEmit(ctx, "market:update", e)
		}
	}})
	return a
}

func (a *App) GetCandleStorage() (candlestorage.Info, error) { return a.candles.Info() }
func (a *App) ConfigureCandleStorage(enabled bool, directory string) (candlestorage.Info, error) {
	if strings.TrimSpace(directory) == "" {
		directory = filepath.Join(a.store.Directory(), "candles")
	}
	info, err := a.candles.Configure(enabled, directory)
	if err == nil && enabled {
		a.market.StoreCurrentCandles()
	}
	return info, err
}
func (a *App) ChooseCandleStorageDirectory() (string, error) {
	info, err := a.candles.Info()
	if err != nil {
		return "", err
	}
	initial := info.Directory
	if st, e := os.Stat(initial); e != nil || !st.IsDir() {
		initial = a.store.Directory()
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose candle data folder", DefaultDirectory: initial, CanCreateDirectories: true})
}
func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()
	if a.liveAlerts != nil && a.liveAlertsError == "" {
		a.liveAlerts.Start(a.ctx)
	}
}
func (a *App) domready(ctx context.Context) {
	a.mu.Lock()
	a.domReady = true
	w := a.window
	a.applyWindowLocked(w)
	pending := a.closeRequested
	a.mu.Unlock()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				a.mu.Lock()
				if a.ctx.Err() != nil {
					a.mu.Unlock()
					return
				}
				before := a.window
				a.captureWindowLocked()
				after := a.window
				a.mu.Unlock()
				if before != after {
					runtime.EventsEmit(ctx, "window:state", after)
				}
			}
		}
	}()
	desktopSmoke(ctx, a)
	if pending {
		a.requestClose()
	}
}

// FrontendReady is called after the workspace's save/close listeners are
// installed. A signal received during startup must not emit a lost save request.
func (a *App) FrontendReady() {
	a.mu.Lock()
	a.frontendReady = true
	pending := a.closeRequested
	token := a.closeToken
	ctx := a.ctx
	a.mu.Unlock()
	if token != "" {
		runtime.EventsEmit(ctx, "prepare-close", map[string]string{"token": token})
	} else if pending {
		a.requestClose()
	}
}

func (a *App) requestClose() {
	a.mu.Lock()
	a.closeRequested = true
	ready, ctx := a.domReady, a.ctx
	a.mu.Unlock()
	if ready {
		runtime.Quit(ctx)
	}
}

func (a *App) applyWindowLocked(w domain.Window) {
	ctx := a.ctx
	if ctx == nil {
		a.window = w
		return
	}
	if runtime.WindowIsMaximised(ctx) {
		runtime.WindowUnmaximise(ctx)
	}
	width, height := w.Width, w.Height
	if screens, e := runtime.ScreenGetAll(ctx); e == nil {
		for _, screen := range screens {
			if screen.IsCurrent || screen.IsPrimary {
				if screen.Size.Width > 0 && width > screen.Size.Width {
					width = screen.Size.Width
				}
				if screen.Size.Height > 0 && height > screen.Size.Height {
					height = screen.Size.Height
				}
				break
			}
		}
	}
	runtime.WindowSetSize(ctx, width, height)
	if w.Maximized {
		runtime.WindowMaximise(ctx)
	}
	a.window = w
}
func (a *App) captureWindowLocked() {
	if a.ctx == nil {
		return
	}
	max := runtime.WindowIsMaximised(a.ctx)
	if runtime.WindowIsNormal(a.ctx) {
		w, h := runtime.WindowGetSize(a.ctx)
		if w >= 400 && h >= 300 && w <= 16384 && h <= 16384 {
			a.window.Width = w
			a.window.Height = h
		}
	}
	a.window.Maximized = max
}
func (a *App) LoadWorkspace() (storage.Loaded, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	loaded, err := a.store.Load()
	a.revision = loaded.Revision
	var recovery *storage.RecoveryError
	if errors.As(err, &recovery) {
		loaded.Recovery = recovery.Error()
		return loaded, nil
	}
	return loaded, err
}
func (a *App) SaveWorkspace(expectedRevision int64, session string, sequence int64, w domain.Workspace) (storage.Ack, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := domain.Validate(w); err != nil {
		return storage.Ack{}, err
	}
	a.captureWindowLocked()
	w.Window = a.window
	ack, err := a.store.Save(expectedRevision, session, sequence, w)
	if err == nil {
		a.revision = ack.Revision
	}
	return ack, err
}
func (a *App) ListInstruments(provider string) ([]domain.Instrument, error) {
	return a.market.Instruments(a.ctx, provider)
}
func (a *App) AttachChart(chartID string, generation int64, key domain.FeedKey, fromSec, toSec int64) (market.Snapshot, error) {
	if key.Exchange == "dukascopy" {
		a.jforexMu.Lock()
		defer a.jforexMu.Unlock()
	}
	return a.market.Attach(chartID, generation, key, fromSec, toSec)
}
func (a *App) DetachChart(leaseID string) { a.market.Detach(leaseID) }
func (a *App) GetFeedSnapshot(leaseID string, afterRevision int64) (market.Snapshot, error) {
	return a.market.Snapshot(leaseID)
}
func (a *App) GetHistory(requestID string, key domain.FeedKey, fromSec, toSec int64, maxBars int) (market.History, error) {
	return a.market.History(a.ctx, requestID, key, fromSec, toSec, maxBars)
}
func (a *App) GetDiagnostics() map[string]any {
	a.mu.Lock()
	revision := a.revision
	a.mu.Unlock()
	return map[string]any{"market": a.market.Diagnostics(), "persistedRevision": revision, "mode": "read-only market data", "version": applicationVersion}
}
func (a *App) ExportWorkspace() (string, error) {
	data, err := a.store.Export()
	if err != nil {
		return "", err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{Title: "Export CryptoDesk workspace", DefaultFilename: "cryptodesk-workspace.json", Filters: []runtime.FileFilter{{DisplayName: "Workspace JSON", Pattern: "*.json"}}})
	if err != nil || path == "" {
		return "", err
	}
	// Write in the destination directory and rename atomically; caches never enter
	// the exported document. Native dialog supplies the user-selected destination.
	f, err := os.CreateTemp(filepath.Dir(path), ".cryptodesk-export-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err = os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}
func (a *App) ImportWorkspace() (storage.Loaded, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "Import CryptoDesk workspace", Filters: []runtime.FileFilter{{DisplayName: "Workspace JSON", Pattern: "*.json"}}})
	if err != nil {
		return storage.Loaded{}, err
	}
	if path == "" {
		return a.LoadWorkspace()
	}
	f, err := os.Open(path)
	if err != nil {
		return storage.Loaded{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, domain.MaxImportBytes+1))
	if err != nil {
		return storage.Loaded{}, err
	}
	w, err := domain.DecodeWorkspace(data)
	if err != nil {
		return storage.Loaded{}, err
	}
	charts := 0
	for _, tab := range w.Tabs {
		charts += len(tab.Charts)
	}
	answer, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{Type: runtime.WarningDialog, Title: "Replace workspace?", Message: fmt.Sprintf("Import %d tabs with %d charts? The current committed workspace will be backed up before replacement.", len(w.Tabs), charts), Buttons: []string{"Cancel", "Replace"}, DefaultButton: "Cancel", CancelButton: "Cancel"})
	if err != nil {
		return storage.Loaded{}, err
	}
	if answer != "Replace" {
		return a.LoadWorkspace()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	loaded, err := a.store.Import(a.revision, w)
	if err == nil {
		a.revision = loaded.Revision
		a.applyWindowLocked(loaded.Workspace.Window)
	}
	return loaded, err
}
func (a *App) ResetWorkspace() (storage.Loaded, error) {
	answer, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{Type: runtime.WarningDialog, Title: "Reset workspace?", Message: "Return to exactly one tab and one chart? Your current committed workspace will be backed up.", Buttons: []string{"Cancel", "Reset"}, DefaultButton: "Cancel", CancelButton: "Cancel"})
	if err != nil {
		return storage.Loaded{}, err
	}
	if answer != "Reset" {
		return a.LoadWorkspace()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	loaded, err := a.store.Reset(a.revision)
	if err == nil {
		a.revision = loaded.Revision
		a.applyWindowLocked(loaded.Workspace.Window)
	}
	return loaded, err
}
func (a *App) GetBackups() ([]storage.Backup, error) { return a.store.Backups() }
func (a *App) RecoverWorkspace(backupID int64) (storage.Loaded, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	loaded, err := a.store.Recover(a.revision, backupID)
	if err == nil {
		a.revision = loaded.Revision
		a.applyWindowLocked(loaded.Workspace.Window)
	}
	return loaded, err
}
func (a *App) OpenTradingView() { runtime.BrowserOpenURL(a.ctx, "https://www.tradingview.com/") }

func (a *App) beforeClose(ctx context.Context) bool {
	a.mu.Lock()
	if a.closeAllowed {
		a.mu.Unlock()
		return false
	}
	a.closeRequested = true
	if !a.domReady {
		a.mu.Unlock()
		return true
	}
	// A preserved invalid workspace has no interactive draft to flush.
	if loaded, err := a.store.Load(); err != nil && loaded.Recovery != "" {
		a.allowCloseLocked()
		a.mu.Unlock()
		return false
	}
	if !a.frontendReady {
		a.mu.Unlock()
		return true
	}
	if a.closeTimedOut {
		a.closeTimedOut = false
		a.mu.Unlock()
		answer, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{Type: runtime.WarningDialog, Title: "Final save was not acknowledged", Message: "Retry keeps CryptoDesk open so you can save. Discard quits and restores only the last committed workspace next launch.", Buttons: []string{"Retry", "Discard and quit"}, DefaultButton: "Retry", CancelButton: "Retry"})
		if err == nil && answer == "Discard and quit" {
			a.mu.Lock()
			a.allowCloseLocked()
			a.mu.Unlock()
			return false
		}
		return true
	}
	if a.closeToken != "" {
		a.mu.Unlock()
		return true
	}
	token := domain.ID()
	a.closeToken = token
	runtime.EventsEmit(ctx, "prepare-close", map[string]string{"token": token})
	// Never close on a timeout: it is not an acknowledgement of durable storage.
	a.closeTimer = time.AfterFunc(10*time.Second, func() {
		a.mu.Lock()
		if a.closeToken == token {
			a.closeToken = ""
			a.closeTimedOut = true
			runtime.EventsEmit(ctx, "close-timeout", map[string]string{"reason": "The final save was not acknowledged. Retry closing, or explicitly discard pending edits."})
		}
		a.mu.Unlock()
	})
	a.mu.Unlock()
	return true
}
func (a *App) ConfirmClose(token string, discard bool) error {
	a.mu.Lock()
	if token == "" || token != a.closeToken {
		a.mu.Unlock()
		return errors.New("close token expired; retry closing the window")
	}
	a.allowCloseLocked()
	a.mu.Unlock()
	runtime.Quit(a.ctx)
	return nil
}

func (a *App) allowCloseLocked() {
	if a.closeTimer != nil {
		a.closeTimer.Stop()
	}
	a.closeAllowed = true
	// Stop window queries before the GTK main loop ends. Otherwise a ticker
	// could wait for that stopped loop while holding the app mutex during exit.
	if a.cancel != nil {
		a.cancel()
	}
}
func (a *App) shutdown(ctx context.Context) {
	if a.cancel != nil {
		a.cancel()
	}
	if a.liveAlerts != nil {
		a.liveAlerts.Close()
	}
	if a.predictions != nil {
		a.predictions.Close()
	}
	a.market.Close()
	if a.jforex != nil {
		a.jforex.Close()
	}
	if a.screener != nil {
		a.screener.Close()
	}
	if a.candles != nil {
		if err := a.candles.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "Close candle storage:", err)
		}
	}
	_ = a.store.Close()
}
