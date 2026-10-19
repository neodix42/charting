package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsStartupCreatesOnlyRequestedProfile(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "data", "WebView2Profile")
	paths := startupPaths{workspacePath: filepath.Join(root, "data", "workspace.sqlite"), webviewProfile: profile}
	options, err := preparePlatformStartup(paths)
	if err != nil {
		t.Fatal(err)
	}
	if options.WebviewUserDataPath != profile || options.WebviewBrowserPath != "" {
		t.Fatalf("wrong Wails browser configuration: %+v", options)
	}
	if info, err := os.Stat(profile); err != nil || !info.IsDir() {
		t.Fatalf("profile was not created: %v", err)
	}
	if _, err := os.Stat(paths.workspacePath); !os.IsNotExist(err) {
		t.Fatalf("profile preparation opened workspace: %v", err)
	}
}

func TestWindowsEmbedsTimezoneDatabase(t *testing.T) {
	t.Setenv("ZONEINFO", filepath.Join(t.TempDir(), "missing-zoneinfo"))
	for _, name := range []string{"Asia/Dubai", "America/New_York", "Europe/Berlin"} {
		if _, err := time.LoadLocation(name); err != nil {
			t.Fatalf("timezone %s unavailable: %v", name, err)
		}
	}
}
