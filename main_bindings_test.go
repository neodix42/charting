//go:build bindings

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"cryptodesk/internal/storage"
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
			var originals map[string][]byte
			if locked {
				store, err := storage.Open(workspacePath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				if _, err := store.Load(); err != nil {
					t.Fatal(err)
				}
				originals = readWorkspaceFiles(t, workspaceDir)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, "-test.run=^TestBindingsMainProcess$")
			cmd.Dir = projectDir
			cmd.Env = append(os.Environ(), "CRYPTODESK_TEST_BINDINGS_PROCESS=1", "XDG_DATA_HOME="+dataDir, "LOCALAPPDATA="+dataDir, "APPDATA="+dataDir)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("bindings entry point failed: %v\n%s", err, output)
			}
			if locked {
				actual := readWorkspaceFiles(t, workspaceDir)
				if len(actual) != len(originals) {
					t.Fatalf("workspace directory changed: %v", actual)
				}
				for name, before := range originals {
					if after, ok := actual[name]; !ok || !bytes.Equal(before, after) {
						t.Fatalf("workspace file changed: %s", name)
					}
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

func readWorkspaceFiles(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = data
	}
	return files
}
