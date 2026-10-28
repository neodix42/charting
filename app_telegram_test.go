//go:build bindings

package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"cryptodesk/internal/market"
	"cryptodesk/internal/storage"
	"cryptodesk/internal/telegram"
)

func TestTelegramSettingsStayOutsidePortableWorkspace(t *testing.T) {
	profile := t.TempDir()
	store, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Export()
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: store, telegram: telegram.NewStore(profile), market: market.New(market.Options{})}
	defer app.market.Close()
	if _, err = app.ConfigureTelegramBot("123:explicit-local-token"); err != nil {
		t.Fatal(err)
	}
	after, err := store.Export()
	if err != nil || !bytes.Equal(before, after) || bytes.Contains(after, []byte("explicit-local-token")) {
		t.Fatal("Telegram settings changed or leaked into workspace export", err)
	}
	diagnostics, err := json.Marshal(app.GetDiagnostics())
	if err != nil || bytes.Contains(diagnostics, []byte("explicit-local-token")) || bytes.Contains(diagnostics, []byte("apiToken")) {
		t.Fatal("Telegram token leaked into application diagnostics", err)
	}
	if _, err = store.Reset(loaded.Revision); err != nil {
		t.Fatal(err)
	}
	reopened := &App{telegram: telegram.NewStore(profile)}
	settings, err := reopened.GetTelegramBotSettings()
	if err != nil || settings.APIToken != "123:explicit-local-token" {
		t.Fatal("workspace reset changed the separate bot configuration", err)
	}
	if _, err = reopened.ConfigureTelegramBot(""); err != nil {
		t.Fatal(err)
	}
	if settings, err = app.GetTelegramBotSettings(); err != nil || settings.APIToken != "" {
		t.Fatal("token did not clear", err)
	}
}
