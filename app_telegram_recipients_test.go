//go:build bindings

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cryptodesk/internal/market"
	"cryptodesk/internal/storage"
	"cryptodesk/internal/telegram"
)

func TestTelegramMultipleRecipientsRemainPrivateAndSurviveWorkspaceReset(t *testing.T) {
	profile := t.TempDir()
	workspace, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	loaded, err := workspace.Load()
	if err != nil {
		t.Fatal(err)
	}
	before, err := workspace.Export()
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: workspace, telegram: telegram.NewStore(profile), market: market.New(market.Options{})}
	defer app.market.Close()
	saved, err := app.ConfigureTelegramBotSettings("123:explicit-multi-recipient-token", " @Alice , 42, @alice, 42, @Bob ")
	if err != nil {
		t.Fatal(err)
	}
	var recipients []string
	for _, recipient := range strings.Split(saved.Recipient, ",") {
		recipients = append(recipients, strings.TrimSpace(recipient))
	}
	if !reflect.DeepEqual(recipients, []string{"@Alice", "42", "@Bob"}) {
		t.Fatalf("settings did not normalize independent recipients: %+v", recipients)
	}
	after, err := workspace.Export()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("recipient configuration changed the portable workspace", err)
	}
	diagnostics, err := json.Marshal(app.GetDiagnostics())
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"explicit-multi-recipient-token", "@Alice", "@Bob", "resolvedChat"} {
		if bytes.Contains(after, []byte(private)) || bytes.Contains(diagnostics, []byte(private)) {
			t.Fatalf("private Telegram configuration leaked: %q", private)
		}
	}
	if _, err = workspace.Reset(loaded.Revision); err != nil {
		t.Fatal(err)
	}
	reopened := &App{telegram: telegram.NewStore(profile)}
	if restored, err := reopened.GetTelegramBotSettings(); err != nil || restored != saved {
		t.Fatalf("restart/reset changed recipients: %+v %v", restored, err)
	}
	settingsPath := filepath.Join(profile, "telegram-bot.json")
	original, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.ConfigureTelegramBotSettings("456:replacement", "@Alice,,42"); err == nil {
		t.Fatal("invalid recipient list replaced the saved configuration")
	}
	preserved, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(original, preserved) {
		t.Fatal("failed multi-recipient save altered previous settings", err)
	}
	updated, err := reopened.ConfigureTelegramBot("456:replacement")
	if err != nil || updated.Recipient != saved.Recipient {
		t.Fatalf("token-only compatibility path discarded recipients: %+v %v", updated, err)
	}
}
