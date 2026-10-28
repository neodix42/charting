package telegram

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestSettingsSaveRestartReplaceAndClear(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	store := NewStore(directory)
	if settings, err := store.Read(); err != nil || settings.APIToken != "" {
		t.Fatal("new profile must have no Telegram token", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("reading settings should not create files")
	}
	for _, token := range []string{"  123456:explicit-test-token\n", "987654:replacement", ""} {
		saved, err := store.Save(token)
		if err != nil || saved.APIToken != strings.TrimSpace(token) {
			t.Fatal("settings did not save correctly", err)
		}
		restored, err := NewStore(directory).Read()
		if err != nil || restored != saved {
			t.Fatal("settings changed after restart", err)
		}
		stat, err := os.Stat(store.path)
		if err != nil || runtime.GOOS != "windows" && stat.Mode().Perm() != 0600 {
			t.Fatal("Telegram configuration must use private file permissions", err)
		}
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 1 || entries[0].Name() != "telegram-bot.json" {
			t.Fatal("temporary token files were not removed", err)
		}
	}
}

func TestInvalidTokenAndCorruptSettingsNeverExposeOrReplaceSecrets(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Save("123:original-secret"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(store.path)
	for _, token := range []string{"private secret", "private\x00secret", strings.Repeat("x", 4097), "private\xffsecret"} {
		if _, err := store.Save(token); err == nil || strings.Contains(err.Error(), token) {
			t.Fatal("invalid token accepted or exposed")
		}
		after, _ := os.ReadFile(store.path)
		if string(after) != string(before) {
			t.Fatal("invalid input changed saved token")
		}
	}
	// Worst-case JSON escaping must still fit the bounded reader.
	if _, err := store.Save(strings.Repeat("<", 4096)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); err != nil {
		t.Fatal("saved a token that cannot be read", err)
	}
	for _, document := range []string{"", "null", `{"apiToken":"private-secret`, `{"apiToken":"private-secret","future":true}`, `{"apiToken":"private-secret"} {}`, strings.Repeat(" ", 32*1024+1)} {
		if err := os.WriteFile(store.path, []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Read(); err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatal("unreadable configuration accepted or exposed")
		}
		if _, err := store.Save("new-token"); err == nil {
			t.Fatal("unreadable configuration overwritten")
		}
		after, _ := os.ReadFile(store.path)
		if string(after) != document {
			t.Fatal("unreadable configuration changed")
		}
	}
}

func TestConcurrentSettingsAndFailedPublication(t *testing.T) {
	store := NewStore(t.TempDir())
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := store.Save("123:concurrent"); err != nil {
				t.Error("concurrent save failed", err)
			}
			if settings, err := store.Read(); err != nil || settings.APIToken != "123:concurrent" {
				t.Error("concurrent read saw partial settings", err)
			}
		}()
	}
	group.Wait()
	blocked := NewStore(t.TempDir())
	if err := os.Mkdir(blocked.path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := blocked.writeLocked([]byte(`{"apiToken":"private-test"}`)); err == nil {
		t.Fatal("replaced an existing directory")
	}
	entries, err := os.ReadDir(filepath.Dir(blocked.path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "telegram-bot.json" {
		t.Fatal("failed publication retained a temporary secret file", err)
	}
}

func TestRecipientSettingsCompatibilityAndCacheInvalidation(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := os.WriteFile(store.path, []byte(`{"apiToken":"123:legacy"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if settings, err := store.Read(); err != nil || settings.Recipient != "" || settings.APIToken != "123:legacy" {
		t.Fatal("legacy token settings changed", err)
	}
	if _, err := store.Configure("123:legacy", " @neodix "); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	config, _ := store.readLocked()
	config.ChatID, config.BotID = 42, 10
	data, _ := json.Marshal(config)
	_ = store.writeLocked(data)
	store.mu.Unlock()
	if _, err := store.Configure("123:legacy", "@NeoDix"); err != nil {
		t.Fatal(err)
	}
	config, _ = store.readLocked()
	if config.ChatID != 42 {
		t.Fatal("case-only edit lost verified identity")
	}
	if _, err := store.Save("456:replacement"); err != nil {
		t.Fatal(err)
	}
	config, _ = store.readLocked()
	if config.ChatID != 0 || config.BotID != 0 || config.Recipient != "@NeoDix" {
		t.Fatal("token-only save lost recipient or retained old bot binding")
	}
	for _, invalid := range []string{"@", "name", "@bad name", "@../user", "0", "-42", "4503599627370496"} {
		if _, err := store.Configure("123:legacy", invalid); err == nil {
			t.Fatal("invalid recipient accepted")
		}
	}
}
