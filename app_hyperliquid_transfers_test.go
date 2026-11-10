//go:build bindings

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cryptodesk/internal/hyperliquid"
)

func transferFacadeErrors(app *App) []error {
	_, balances := app.GetHyperliquidBalances()
	_, preview := app.PreviewHyperliquidTransfer(hyperliquid.TransferRequest{})
	_, submit := app.SubmitHyperliquidTransfer("not-a-confirmation")
	return []error{balances, preview, submit}
}

func TestHyperliquidTransferFacadeRejectsUnavailableAndClosingServices(t *testing.T) {
	for _, err := range transferFacadeErrors(&App{}) {
		if err == nil || !strings.Contains(err.Error(), "unavailable") {
			t.Fatal("missing service was accepted", err)
		}
	}
	app := &App{hyperliquid: hyperliquid.NewStore(t.TempDir())}
	app.closeTrading()
	for _, err := range transferFacadeErrors(app) {
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "clos") {
			t.Fatal("closed facade reached provider or accepted work", err)
		}
	}
}

func TestHyperliquidTransferFacadeDoesNotBypassFailedStartup(t *testing.T) {
	problem := errors.New("workspace startup failed")
	app := newBootstrapAppWithInitializer(func(context.Context, *App) error { return problem })
	app.beginBootstrap()
	defer app.shutdown(context.Background())
	for _, err := range transferFacadeErrors(app) {
		if !errors.Is(err, problem) {
			t.Fatal("transfer facade bypassed initialization failure", err)
		}
	}
}
