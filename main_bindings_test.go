//go:build bindings

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"syscall"
	"testing"
)

// Run the real bindings entry point in a subprocess so its command-line parsing
// and failure handling cannot be hidden by the test process.
func TestBindingsMainProcess(t *testing.T) {
	if os.Getenv("CRYPTODESK_TEST_BINDINGS_PROCESS") != "1" {
		return
	}
	os.Args = []string{"cryptodesk-bindings"}
	main()
}

func TestBindingsGenerationLeavesWorkspaceUntouched(t *testing.T) {
	for _, locked := range []bool{false, true} {
		name := "fresh"
		if locked {
			name = "running workspace"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			projectDir := filepath.Join(root, "project")
			if err := os.Mkdir(projectDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(projectDir, "wails.json"), []byte(`{"name":"CryptoDesk"}`), 0600); err != nil {
				t.Fatal(err)
			}
			dataDir := filepath.Join(root, "data")
			workspaceDir := filepath.Join(dataDir, "cryptodesk")
			workspacePath := filepath.Join(workspaceDir, "workspace.sqlite")
			sentinel := []byte("existing workspace must remain untouched")
			if locked {
				if err := os.MkdirAll(workspaceDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(workspacePath, sentinel, 0600); err != nil {
					t.Fatal(err)
				}
				lock, err := os.OpenFile(workspacePath+".lock", os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = lock.Close() })
				if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, "-test.run=^TestBindingsMainProcess$")
			cmd.Dir = projectDir
			cmd.Env = append(os.Environ(), "CRYPTODESK_TEST_BINDINGS_PROCESS=1", "XDG_DATA_HOME="+dataDir)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("bindings entry point failed: %v\n%s", err, output)
			}
			if locked {
				actual, err := os.ReadFile(workspacePath)
				if err != nil || !bytes.Equal(actual, sentinel) {
					t.Fatalf("workspace changed: %q, %v", actual, err)
				}
				entries, err := os.ReadDir(workspaceDir)
				if err != nil || len(entries) != 2 {
					t.Fatalf("workspace directory changed: %v, %v", entries, err)
				}
			} else if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
				t.Fatalf("bindings created a data directory: %v", err)
			}
			// Ensure the harmless zero-value facade still exposes the complete
			// production API through both generated JavaScript and TypeScript.
			for _, filename := range []string{"App.js", "App.d.ts"} {
				generated, err := os.ReadFile(filepath.Join(projectDir, "frontend", "wailsjs", "go", "main", filename))
				if err != nil {
					t.Fatal(err)
				}
				methods := regexp.MustCompile(`export function (\w+)\(`).FindAllSubmatch(generated, -1)
				exports := make(map[string]bool, len(methods))
				for _, method := range methods {
					exports[string(method[1])] = true
				}
				api := reflect.TypeOf(&App{})
				if len(exports) != api.NumMethod() {
					t.Fatalf("%s exports %d methods; App has %d", filename, len(exports), api.NumMethod())
				}
				for i := range api.NumMethod() {
					if !exports[api.Method(i).Name] {
						t.Errorf("%s is missing %s", filename, api.Method(i).Name)
					}
				}
			}
		})
	}
}
