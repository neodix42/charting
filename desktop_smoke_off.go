//go:build !desktop_smoke

package main

import (
	"context"
	"cryptodesk/internal/storage"
)

func desktopSmoke(context.Context, *App) {}

func desktopSmokePrepare(context.Context, *storage.Store) error { return nil }
