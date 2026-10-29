package jforex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This opt-in smoke downloads only reviewed public SDK dependencies, then sends
// version through the actual packaged bridge. It never sends a broker login.
func TestPackagedJavaBridgeOfflineProtocol(t *testing.T) {
	if os.Getenv("CRYPTODESK_TEST_JFOREX_BRIDGE") != "1" {
		t.Skip("set CRYPTODESK_TEST_JFOREX_BRIDGE=1 and CRYPTODESK_JFOREX_RUNTIME to verify the actual packaged Java bridge without logging in")
	}
	if os.Getenv("CRYPTODESK_JFOREX_RUNTIME") == "" {
		t.Fatal("the real bridge smoke requires an explicit isolated runtime path")
	}
	runtime := discoverRuntime()
	if runtime.err != nil {
		t.Fatal(runtime.err)
	}
	profile := t.TempDir()
	runtime.privateDirectory = filepath.Join(profile, "private-home")
	runtime.sdkDirectory = filepath.Join(profile, "sdk", "3.6.51")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := runtime.prepare(ctx); err != nil {
		t.Fatal(err)
	}
	process, err := startBridge(ctx, runtime.command, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer process.stop()
	var version struct {
		Protocol    int    `json:"protocol"`
		SDKVersion  string `json:"sdkVersion"`
		JavaVersion int    `json:"javaVersion"`
	}
	rpc, stop := context.WithTimeout(ctx, 30*time.Second)
	err = process.call(rpc, map[string]any{"op": "version"}, &version)
	stop()
	if err != nil {
		t.Fatal(err)
	}
	if version.Protocol != 1 || version.SDKVersion != "3.6.51" || version.JavaVersion < 17 {
		t.Fatalf("unexpected actual bridge version %+v", version)
	}
	// EOF is how abrupt owner loss reaches the child even without a close RPC.
	if err := process.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("actual Java bridge survived its owner input pipe closing")
	}
	if _, err := os.Stat(filepath.Join(profile, "dukascopy-jforex.json")); !os.IsNotExist(err) {
		t.Fatal("offline protocol smoke wrote credential configuration")
	}
}
