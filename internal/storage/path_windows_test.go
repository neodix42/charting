//go:build windows

package storage

import (
	"path/filepath"
	"testing"
)

func TestWindowsDefaultPathUsesLocalProfileAndIgnoresXDG(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	got, err := DefaultPath()
	if err != nil || got != filepath.Join(local, "cryptodesk", "workspace.sqlite") {
		t.Fatal(got, err)
	}
}

func TestWindowsDefaultPathFallsBackToUserConfigDirectory(t *testing.T) {
	config := t.TempDir()
	t.Setenv("LOCALAPPDATA", "relative-path-is-not-a-profile")
	t.Setenv("APPDATA", config)
	got, err := DefaultPath()
	if err != nil || got != filepath.Join(config, "cryptodesk", "workspace.sqlite") {
		t.Fatal(got, err)
	}
	t.Setenv("APPDATA", "")
	if got, err := DefaultPath(); err == nil || got != "" {
		t.Fatal("missing profile silently became a relative path", got, err)
	}
}

func TestWindowsDriveSQLiteURI(t *testing.T) {
	path := `C:\Users\Crypto Desk\AppData\Local\cryptodesk\workspace #25%.sqlite`
	want := "file:///C:/Users/Crypto%20Desk/AppData/Local/cryptodesk/workspace%20%2325%25.sqlite"
	if got := sqliteURI(path); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
