package main

import (
	"context"
	"sync"
	"time"

	"cryptodesk/internal/domain"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The native window is created hidden. Once the branded HTML has painted, only
// saved window metadata must be ready before showing it: provider connections,
// chart history and optional service restoration never hold this gate.
type startupPresentation struct {
	metadataReady chan struct{}
	metadataOnce  sync.Once
	showOnce      sync.Once
	shown         bool // App.mu protects native-loop lifetime and this state.
	err           error
	window        startupWindowActions
	settleLimit   time.Duration
}

type startupWindowActions struct {
	resize    func(context.Context, domain.Window)
	maximise  func(context.Context)
	show      func(context.Context)
	maximised func(context.Context) bool
	size      func(context.Context) (int, int)
}

func newStartupPresentation() *startupPresentation {
	return &startupPresentation{metadataReady: make(chan struct{}), settleLimit: 2 * time.Second,
		window: startupWindowActions{resize: resizeStartupWindow, maximise: runtime.WindowMaximise,
			show: runtime.WindowShow, maximised: runtime.WindowIsMaximised, size: runtime.WindowGetSize}}
}

func resizeStartupWindow(ctx context.Context, window domain.Window) {
	width, height := window.Width, window.Height
	if screens, err := runtime.ScreenGetAll(ctx); err == nil {
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
}

func (a *App) prepareStartupWindowMetadata(window domain.Window) {
	if window.Width < 400 || window.Height < 300 {
		window = domain.Window{Width: 1600, Height: 1000}
	}
	a.mu.Lock()
	if a.ctx != nil {
		window.Maximized = true
	}
	a.window = window
	a.mu.Unlock()
	a.finishStartupWindowMetadata()
}

func (a *App) finishStartupWindowMetadata() {
	if presentation := a.presentation; presentation != nil {
		presentation.metadataOnce.Do(func() { close(presentation.metadataReady) })
	}
}

// StartupPainted is a one-shot presentation acknowledgement, independent of the
// later FrontendReady save/close handshake. A retry never hides/re-sizes a shown
// window. Context cancellation fences native calls against closing the GTK loop.
func (a *App) StartupPainted() error {
	a.mu.Lock()
	ctx, presentation := a.ctx, a.presentation
	a.mu.Unlock()
	if presentation == nil || ctx == nil {
		return nil
	}
	presentation.showOnce.Do(func() {
		select {
		case <-ctx.Done():
			presentation.err = ctx.Err()
			return
		case <-presentation.metadataReady:
		}
		a.mu.Lock()
		if ctx.Err() != nil || a.closeRequested || a.closeAllowed {
			presentation.err = context.Canceled
			a.mu.Unlock()
			return
		}
		// Resize only while hidden, before the one maximized presentation. This
		// retains the saved normal size without a visible restore/maximize cycle.
		presentation.window.resize(ctx, a.window)
		presentation.window.maximise(ctx)
		presentation.window.show(ctx)
		a.window.Maximized = true
		a.mu.Unlock()

		deadline := time.NewTimer(presentation.settleLimit)
		defer deadline.Stop()
		poll := time.NewTicker(10 * time.Millisecond)
		defer poll.Stop()
		lastWidth, lastHeight, stableSamples := 0, 0, 0
		for {
			a.mu.Lock()
			if ctx.Err() != nil || a.closeRequested || a.closeAllowed {
				presentation.err = context.Canceled
				a.mu.Unlock()
				return
			}
			maximised := presentation.window.maximised(ctx)
			if maximised {
				width, height := presentation.window.size(ctx)
				if width >= 400 && height >= 300 {
					if width == lastWidth && height == lastHeight {
						stableSamples++
					} else {
						lastWidth, lastHeight, stableSamples = width, height, 1
					}
				} else {
					stableSamples = 0
				}
			} else {
				stableSamples = 0
			}
			// A window manager can publish its MAXIMIZED flag a configure event
			// before the client bounds change. Wait for stable valid geometry as
			// well, without guessing work-area sizes from full monitor geometry.
			settled := maximised && stableSamples >= 2
			if settled {
				presentation.shown = true
			}
			a.mu.Unlock()
			if settled {
				return
			}
			select {
			case <-ctx.Done():
				presentation.err = ctx.Err()
				return
			case <-poll.C:
			case <-deadline.C:
				// A compositor may decline/report no maximize state. The splash
				// is already visible; do not strand an otherwise usable recovery
				// or workspace behind an indefinitely pending acknowledgement.
				a.mu.Lock()
				presentation.shown = true
				a.mu.Unlock()
				return
			}
		}
	})
	return presentation.err
}
