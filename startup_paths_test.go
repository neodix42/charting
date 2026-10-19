package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func startupPackage(t *testing.T, marker, browser bool) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "portable app with spaces")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if marker {
		if err := os.WriteFile(filepath.Join(root, "portable.marker"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if browser {
		if err := os.Mkdir(filepath.Join(root, "WebView2"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "WebView2", "msedgewebview2.exe"), []byte("test runtime"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "cryptodesk.exe")
}

func TestStartupPortablePackageUsesExecutableDirectoryWithoutWriting(t *testing.T) {
	executable := startupPackage(t, true, true)
	t.Chdir(t.TempDir())
	paths, err := resolveStartupPaths("windows", executable, "", func() (string, error) { t.Fatal("portable mode asked for a user profile"); return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(executable)
	if !paths.portable || paths.workspacePath != filepath.Join(root, "data", "workspace.sqlite") || paths.webviewRuntime != filepath.Join(root, "WebView2") || paths.webviewProfile != filepath.Join(root, "data", "WebView2Profile") {
		t.Fatalf("unexpected paths: %+v", paths)
	}
	if _, err := os.Stat(filepath.Join(root, "data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path resolution created user data: %v", err)
	}
}

func TestStartupDataOverrideBypassesUnavailableDefault(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			executable := startupPackage(t, true, true)
			override := filepath.Join(t.TempDir(), "custom workspace")
			paths, err := resolveStartupPaths(goos, executable, override, func() (string, error) {
				t.Fatal("override called DefaultPath")
				return "", errors.New("home is unavailable")
			})
			if err != nil {
				t.Fatal(err)
			}
			if paths.workspacePath != filepath.Join(override, "workspace.sqlite") {
				t.Fatalf("override ignored: %+v", paths)
			}
			if goos == "windows" && paths.webviewProfile != filepath.Join(override, "WebView2Profile") {
				t.Fatalf("profile ignored override: %+v", paths)
			}
			if goos == "linux" && (paths.portable || paths.webviewRuntime != "" || paths.webviewProfile != "") {
				t.Fatalf("Linux behavior changed: %+v", paths)
			}
		})
	}
}

func TestStartupStandaloneUsesDefaultAndOptionalLocalRuntime(t *testing.T) {
	for _, browser := range []bool{false, true} {
		executable := startupPackage(t, false, browser)
		workspace := filepath.Join(t.TempDir(), "application data", "workspace.sqlite")
		paths, err := resolveStartupPaths("windows", executable, "", func() (string, error) { return workspace, nil })
		if err != nil {
			t.Fatal(err)
		}
		if paths.portable || paths.workspacePath != workspace || paths.webviewProfile != filepath.Join(filepath.Dir(workspace), "WebView2Profile") {
			t.Fatalf("wrong standalone paths: %+v", paths)
		}
		if browser && paths.webviewRuntime != filepath.Join(filepath.Dir(executable), "WebView2") || !browser && paths.webviewRuntime != "" {
			t.Fatalf("wrong runtime: %+v", paths)
		}
	}
	want := errors.New("home unavailable")
	_, err := resolveStartupPaths("linux", "", "", func() (string, error) { return "", want })
	if !errors.Is(err, want) {
		t.Fatalf("default error lost: %v", err)
	}
}

func TestStartupIncompletePortablePackageNeverFallsBack(t *testing.T) {
	for _, mode := range []string{"missing-runtime", "missing-browser", "runtime-is-file", "browser-is-directory", "marker-is-directory"} {
		t.Run(mode, func(t *testing.T) {
			executable := startupPackage(t, mode != "marker-is-directory", false)
			root, runtimeDir := filepath.Dir(executable), filepath.Join(filepath.Dir(executable), "WebView2")
			switch mode {
			case "missing-browser", "browser-is-directory":
				if err := os.Mkdir(runtimeDir, 0700); err != nil {
					t.Fatal(err)
				}
				if mode == "browser-is-directory" {
					if err := os.Mkdir(filepath.Join(runtimeDir, "msedgewebview2.exe"), 0700); err != nil {
						t.Fatal(err)
					}
				}
			case "runtime-is-file":
				if err := os.WriteFile(runtimeDir, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "marker-is-directory":
				if err := os.Mkdir(filepath.Join(root, "portable.marker"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			_, err := resolveStartupPaths("windows", executable, filepath.Join(t.TempDir(), "override"), func() (string, error) { t.Fatal("incomplete package fell back"); return "", nil })
			if err == nil || !strings.Contains(err.Error(), "portable") && !strings.Contains(err.Error(), "packaged WebView2") {
				t.Fatalf("expected clear package error, got %v", err)
			}
		})
	}
}

func TestStartupRejectsNetworkRuntimeLocations(t *testing.T) {
	for _, path := range []string{`\\server\share\cryptodesk.exe`, `//server/share/cryptodesk.exe`, `\\?\UNC\server\share\cryptodesk.exe`} {
		if !isNetworkPath(path) {
			t.Errorf("did not recognize UNC path %q", path)
		}
		_, err := resolveStartupPaths("windows", path, "", func() (string, error) { t.Fatal("network package fell back"); return "", nil })
		if err == nil || !strings.Contains(err.Error(), "local folder") {
			t.Errorf("expected actionable UNC error: %v", err)
		}
	}
	for _, path := range []string{`C:\app\cryptodesk.exe`, `\\?\C:\app\cryptodesk.exe`, "/local/app/cryptodesk.exe"} {
		if isNetworkPath(path) {
			t.Errorf("local path rejected: %q", path)
		}
	}
}

func TestStartupRuntimeACLsAreScopedToWindows10Package(t *testing.T) {
	runtime := `C:\Users\Someone\Crypto Desk\WebView2`
	want := [][]string{{runtime, "/grant", "*S-1-15-2-2:(OI)(CI)(RX)"}, {runtime, "/grant", "*S-1-15-2-1:(OI)(CI)(RX)"}}
	if got := webviewACLArguments(10, 19045, runtime); !reflect.DeepEqual(got, want) {
		t.Fatalf("incorrect scoped ACL arguments: %#v", got)
	}
	for _, version := range [][2]uint32{{10, 22000}, {10, 26100}, {11, 30000}, {6, 9600}} {
		if got := webviewACLArguments(version[0], version[1], runtime); len(got) != 0 {
			t.Errorf("unnecessary ACL changes for %v: %v", version, got)
		}
	}
	if got := webviewACLArguments(10, 19045, ""); len(got) != 0 {
		t.Fatalf("standalone runtime permissions changed: %v", got)
	}
}
