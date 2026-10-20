//go:build linux && cgo && production && !bindings

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestWebKitSignalSelection(t *testing.T) {
	// Keep the production entry point reachable so native Wails/JSC libraries
	// are linked into this test binary, just as they are into the desktop app.
	t.Logf("native entry point: %p", main)
	if os.Getenv("CRYPTODESK_SIGNAL_TEST_PROCESS") == "1" {
		selected := configureWebKitSignal()
		if expected, explicit := os.LookupEnv("CRYPTODESK_SIGNAL_TEST_EXPECT"); explicit {
			if value := os.Getenv("JSC_SIGNAL_FOR_GC"); selected != 0 || value != expected {
				t.Fatalf("explicit GC signal changed: selection %d, got %q, want %q", selected, value, expected)
			}
		} else {
			if selected < 34 || selected > 64 {
				t.Fatalf("expected an unused public realtime signal, got %d", selected)
			}
			if _, set := os.LookupEnv("JSC_SIGNAL_FOR_GC"); set {
				t.Fatal("GC signal selection must not add JSC environment options")
			}
		}
		return
	}
	for _, tc := range []struct {
		name     string
		explicit bool
		value    string
	}{
		{name: "unused realtime signal"},
		{name: "explicit signal", explicit: true, value: "40"},
		{name: "explicit empty value", explicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestWebKitSignalSelection$")
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "JSC_SIGNAL_FOR_GC=") && !strings.HasPrefix(entry, "CRYPTODESK_SIGNAL_TEST_") {
					command.Env = append(command.Env, entry)
				}
			}
			command.Env = append(command.Env, "CRYPTODESK_SIGNAL_TEST_PROCESS=1")
			if tc.explicit {
				command.Env = append(command.Env, "JSC_SIGNAL_FOR_GC="+tc.value, "CRYPTODESK_SIGNAL_TEST_EXPECT="+tc.value)
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("signal setup subprocess: %v\n%s", err, output)
			}
		})
	}
}
