package jforex

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeRuntimeFixture(t *testing.T, directory string) {
	t.Helper()
	manifest, err := os.ReadFile("../../jforex-bridge/SDK-ARTIFACTS.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"cryptodesk-jforex.jar": []byte("explicit discovery fixture, never executed"),
		"SDK-ARTIFACTS.json":    manifest,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func discoveryJavaFixture(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	name := "java"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.Mkdir(filepath.Join(home, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "bin", name), []byte("discovery only; never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JAVA_HOME", home)
}

func TestRuntimeUsesInstalledHomeFolderFromAnyWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows retains the portable sibling runtime")
	}
	discoveryJavaFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("CRYPTODESK_JFOREX_RUNTIME", "")
	installed := filepath.Join(home, ".local", "share", "cryptodesk-app", "jforex-runtime")
	writeRuntimeFixture(t, installed)
	checkout := t.TempDir()
	writeRuntimeFixture(t, filepath.Join(checkout, "jforex-runtime"))
	if err := os.WriteFile(filepath.Join(checkout, "go.mod"), []byte("module discovery-fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{checkout, t.TempDir()} {
		t.Chdir(cwd)
		got := discoverRuntime()
		if got.directory != installed || got.err != nil {
			t.Fatalf("working directory %q selected %+v", cwd, got)
		}
	}
	// A missing or damaged installation must not silently select a different
	// bridge from the checkout. Discovery never rewrites the installation.
	t.Chdir(checkout)
	if err := os.Remove(filepath.Join(installed, "cryptodesk-jforex.jar")); err != nil {
		t.Fatal(err)
	}
	got := discoverRuntime()
	if got.directory != installed || got.err == nil || !strings.Contains(got.err.Error(), "install-user.sh") {
		t.Fatalf("missing installation fell back to checkout: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(installed, "cryptodesk-jforex.jar")); !os.IsNotExist(err) {
		t.Fatal("discovery changed the installed bridge", err)
	}
}

func TestInstalledRuntimeHonorsAbsoluteXDGAndIgnoresRelativeXDG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix user-data convention")
	}
	home, data := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	for _, value := range []string{data, "relative-data", ""} {
		t.Setenv("XDG_DATA_HOME", value)
		got, err := installedRuntimeDirectory()
		base := filepath.Join(home, ".local", "share")
		if value == data {
			base = data
		}
		if err != nil || got != filepath.Join(base, "cryptodesk-app", "jforex-runtime") {
			t.Fatal(value, got, err)
		}
	}
}

func TestExplicitBridgeOverrideRemainsIsolatedAndValidatesPinnedManifest(t *testing.T) {
	discoveryJavaFixture(t)
	directory := t.TempDir()
	writeRuntimeFixture(t, directory)
	t.Setenv("CRYPTODESK_JFOREX_RUNTIME", directory)
	got := discoverRuntime()
	if got.directory != directory || got.err != nil {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(directory, "SDK-ARTIFACTS.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	got = discoverRuntime()
	if got.directory != directory || got.err == nil {
		t.Fatal("override bypassed pinned manifest validation", got)
	}
}

func TestJavaSelectionPrefersSystemThenPathThenJavaHome(t *testing.T) {
	name := "java"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	root := t.TempDir()
	system := filepath.Join(root, "system", name)
	pathDirectory := filepath.Join(root, "path")
	javaHome := filepath.Join(root, "ide-jdk")
	pathJava := filepath.Join(pathDirectory, name)
	homeJava := filepath.Join(javaHome, "bin", name)
	for _, path := range []string{system, pathJava, homeJava} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("discovery only; never executed"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", pathDirectory)
	for _, expected := range []string{system, pathJava, homeJava} {
		if got := findJava(name, system, javaHome); got != expected {
			t.Fatalf("got %q, want %q", got, expected)
		}
		if err := os.Remove(expected); err != nil {
			t.Fatal(err)
		}
	}
	if got := findJava(name, system, javaHome); got != "" {
		t.Fatal("missing Java reported available", got)
	}
}
