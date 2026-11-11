package jforex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
	var version bridgeVersion
	rpc, stop := context.WithTimeout(ctx, 30*time.Second)
	err = process.call(rpc, map[string]any{"op": "version"}, &version)
	stop()
	if err != nil {
		t.Fatal(err)
	}
	if version.Protocol != 1 || version.SDKVersion != "3.6.51" || version.JavaVersion < 17 || version.InstrumentCatalog != bridgeInstrumentCatalog {
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

// The opt-in bundle probe verifies the actual executable-relative bridge and
// seeds only checksum-verified local SDK files, without downloads or login.
func TestBundledJavaBridgeOfflineProtocol(t *testing.T) {
	executable, cache := os.Getenv("CRYPTODESK_TEST_JFOREX_EXECUTABLE"), os.Getenv("CRYPTODESK_TEST_SDK_CACHE")
	if executable == "" || cache == "" {
		t.Skip("set CRYPTODESK_TEST_JFOREX_EXECUTABLE and CRYPTODESK_TEST_SDK_CACHE for an offline bundle probe")
	}
	t.Setenv("CRYPTODESK_JFOREX_RUNTIME", "")
	bundle := discoverRuntimeAt(executable, nil, runtime.GOOS)
	if bundle.err != nil {
		t.Fatal(bundle.err)
	}
	expected := filepath.Join(filepath.Dir(executable), "jforex-runtime")
	if bundle.directory != expected {
		t.Fatalf("selected %s, expected matching bundle %s", bundle.directory, expected)
	}
	manifest, err := readSDKManifest(filepath.Join(bundle.directory, "SDK-ARTIFACTS.json"))
	if err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	bundle.privateDirectory = filepath.Join(profile, "home")
	bundle.sdkDirectory = filepath.Join(profile, "sdk")
	lib := filepath.Join(bundle.sdkDirectory, "lib")
	if err = os.MkdirAll(lib, 0700); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range manifest.Artifacts {
		source := filepath.Join(cache, artifact.Name)
		if !validArtifact(source, artifact) {
			t.Fatal("verified local SDK artifact required", artifact.Name)
		}
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(lib, artifact.Name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = bundle.prepare(ctx); err != nil {
		t.Fatal(err)
	}
	process, err := startBridge(ctx, bundle.command, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer process.stop()
	var version bridgeVersion
	if err = process.call(ctx, map[string]any{"op": "version"}, &version); err != nil {
		t.Fatal(err)
	}
	if version.SDKVersion != "3.6.51" || version.TradeHistory != "platform-orders-v1" || version.TradePnL != "account-pnl-v1" {
		t.Fatal("bundle/bridge capabilities do not match", version)
	}
	t.Log("Actual matching bundled bridge initialized with verified cached SDK, no credentials and no network downloads")
}
