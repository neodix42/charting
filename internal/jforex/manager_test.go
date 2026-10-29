package jforex

import (
	"bufio"
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testUsername = "private-demo-user"
const testPassword = "private-password-83"
const pngImage = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aF9kAAAAASUVORK5CYII="

func testManager(t *testing.T, scenario string) (*Manager, *atomic.Int32) {
	t.Helper()
	count := &atomic.Int32{}
	manager := newManager(t.TempDir(), bridgeRuntime{directory: "fake-bridge", java: "fake-java"}, func(ctx context.Context) (*exec.Cmd, error) {
		count.Add(1)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBridgeHelperProcess$")
		cmd.Env = append(os.Environ(), "CRYPTODESK_JFOREX_TEST_PROCESS=1", "CRYPTODESK_JFOREX_TEST_SCENARIO="+scenario)
		configureProcess(cmd)
		return cmd, nil
	})
	t.Cleanup(manager.Close)
	if _, err := manager.Configure(Settings{Username: testUsername, Password: testPassword, Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	return manager, count
}

func forexKey() domain.FeedKey {
	return domain.FeedKey{Exchange: "dukascopy", Market: "forex", Instrument: "EURUSD", Timeframe: "1h"}
}

func TestSettingsPersistWithoutExposingPasswordAndIdentityChangesRequireNewSecret(t *testing.T) {
	m, count := testManager(t, "")
	if count.Load() != 0 {
		t.Fatal("configuration started Java")
	}
	info := m.Info()
	if info.Username != testUsername || !info.PasswordConfigured || info.Environment != "demo" {
		t.Fatal(info)
	}
	encoded, _ := json.Marshal(info)
	if strings.Contains(string(encoded), testPassword) || strings.Contains(string(encoded), `"password"`) {
		t.Fatal("secret exposed", string(encoded))
	}
	stat, err := os.Stat(m.path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && stat.Mode().Perm()&0077 != 0 {
		t.Fatal("credentials accessible to other users", stat.Mode())
	}
	stored, err := readConfiguration(m.path)
	if err != nil || stored.Password != testPassword {
		t.Fatal("password was not persisted", err)
	}
	if _, err = m.Configure(Settings{Username: testUsername, Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Configure(Settings{Username: "other-user", Environment: "demo"}); err == nil {
		t.Fatal("identity changed while retaining a password")
	}
	if _, err = m.Configure(Settings{Username: testUsername, Environment: "live"}); err == nil {
		t.Fatal("environment changed while retaining a password")
	}
	if _, err = m.Configure(Settings{Username: testUsername, Environment: "demo", Password: "never-print-this\n"}); err == nil || strings.Contains(err.Error(), "never-print-this") {
		t.Fatal("invalid password was echoed", err)
	}
	if _, err = m.Configure(Settings{Username: testUsername, Environment: "demo", ClearPassword: true}); err != nil {
		t.Fatal(err)
	}
	if m.Info().PasswordConfigured {
		t.Fatal("password not cleared")
	}
	if count.Load() != 0 {
		t.Fatal("settings read/write contacted provider")
	}
}

func TestCorruptAndNonregularSettingsPreserved(t *testing.T) {
	for _, fixture := range []string{`{"version":2,"password":"never-print-this"}`, `{"version":1,"password":"never-print-this",`, `null`} {
		t.Run(fixture[:min(15, len(fixture))], func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "dukascopy-jforex.json")
			if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
				t.Fatal(err)
			}
			m := newManager(directory, bridgeRuntime{}, nil)
			defer m.Close()
			if !strings.Contains(m.Info().Error, "preserved") {
				t.Fatal(m.Info())
			}
			if _, err := m.Configure(Settings{Username: testUsername, Password: testPassword}); err == nil || strings.Contains(err.Error(), "never-print-this") {
				t.Fatal(err)
			}
			actual, _ := os.ReadFile(path)
			if string(actual) != fixture {
				t.Fatal("corrupt credentials replaced")
			}
		})
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "precious")
	path := filepath.Join(directory, "dukascopy-jforex.json")
	if err := os.WriteFile(target, []byte("precious"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	m := newManager(directory, bridgeRuntime{}, nil)
	defer m.Close()
	if _, err := m.Configure(Settings{Username: testUsername, Password: testPassword}); err == nil {
		t.Fatal("followed credential symlink")
	}
	actual, _ := os.ReadFile(target)
	if string(actual) != "precious" {
		t.Fatal("symlink target overwritten")
	}
}

func TestConcurrentRequestsShareOneAuthenticatedProcessAndNormalizeRealBars(t *testing.T) {
	m, count := testManager(t, "")
	var wg sync.WaitGroup
	errors := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bars, err := m.History(context.Background(), forexKey(), 1792368000, 1792378800, 3)
			if err == nil && (len(bars) != 3 || bars[0].Time != 1792368000 || math.Abs(bars[1].Close-1.2) > 1e-10 || bars[2].Time != 1792375200) {
				err = fmt.Errorf("bad normalized bars %+v", bars)
			}
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count.Load() != 1 {
		t.Fatalf("started %d SDK processes", count.Load())
	}
	items, err := m.Instruments(context.Background())
	if err != nil || len(items) != 2 || items[0].NativeID != "EURUSD" {
		t.Fatal(items, err)
	}
	if !m.Info().Connected || m.Info().InstrumentCount != 2 {
		t.Fatal(m.Info())
	}
	if count.Load() != 1 {
		t.Fatal("catalog opened a separate SDK connection")
	}
}

func TestStreamsDetachIndependentlyAndCredentialChangesWaitForActiveCharts(t *testing.T) {
	m, count := testManager(t, "")
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	updates1 := make(chan []domain.Candle, 10)
	updates2 := make(chan []domain.Candle, 10)
	result1 := make(chan error, 1)
	result2 := make(chan error, 1)
	go func() {
		result1 <- m.Stream(ctx1, forexKey(), func(b []domain.Candle, _ domain.Health) {
			if len(b) > 0 {
				updates1 <- b
			}
		})
	}()
	go func() {
		result2 <- m.Stream(ctx2, forexKey(), func(b []domain.Candle, _ domain.Health) {
			if len(b) > 0 {
				updates2 <- b
			}
		})
	}()
	waitBars(t, updates1)
	waitBars(t, updates2)
	if count.Load() != 1 {
		t.Fatal("streams did not share process")
	}
	if _, err := m.Configure(Settings{Username: testUsername, Password: "replacement", Environment: "demo"}); err == nil {
		t.Fatal("changed credentials under active charts")
	}
	cancel1()
	select {
	case err := <-result1:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled subscription did not detach")
	}
	if _, err := m.History(context.Background(), forexKey(), 1792368000, 1792378800, 3); err != nil {
		t.Fatal(err)
	}
	waitBars(t, updates2)
	select {
	case err := <-result2:
		t.Fatal("second feed disconnected", err)
	default:
	}
	if !m.Info().Connected {
		t.Fatal("canceling one chart disconnected the other")
	}
	cancel2()
	select {
	case <-result2:
	case <-time.After(5 * time.Second):
		t.Fatal("second subscription did not detach")
	}
	m.Close()
	if count.Load() != 1 {
		t.Fatal("unexpected extra connection")
	}
}

func TestBarsOnlyEventsPreservePreviousConnectionAndQuoteHealth(t *testing.T) {
	m, _ := testManager(t, "separate-health")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type update struct {
		bars   []domain.Candle
		health domain.Health
	}
	updates := make(chan update, 10)
	done := make(chan error, 1)
	go func() {
		done <- m.Stream(ctx, forexKey(), func(bars []domain.Candle, health domain.Health) { updates <- update{bars, health} })
	}()
	retained := domain.Health{}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case item := <-updates:
			if item.health.State != "" {
				retained = item.health
			}
			if len(item.bars) == 0 {
				continue
			}
			if item.health != (domain.Health{}) {
				t.Fatalf("bars synthesized connection health: %+v", item.health)
			}
			if retained.State != "offline" || retained.LastTradeAt != 1792375100 || retained.LastMessageAt != 1792375200 {
				t.Fatalf("bars reset the preceding SDK status: %+v", retained)
			}
			if m.Info().Connected {
				t.Fatal("a history/bar event falsely marked the disconnected SDK live")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("stream did not detach")
			}
			return
		case err := <-done:
			t.Fatal("stream ended before bars arrived", err)
		case <-deadline:
			t.Fatal("missing SDK bars")
		}
	}
}

func TestInitialConfigurationCanUnblockAnUnconfiguredFeedCheck(t *testing.T) {
	m, count := testManager(t, "")
	if _, err := m.Configure(Settings{Environment: "demo", ClearPassword: true}); err != nil {
		t.Fatal(err)
	}
	// Freeze the pending chart before its credentials check. Saving its first
	// configuration is safe: there is no process, login or source to replace.
	m.gate <- struct{}{}
	done := make(chan error, 1)
	go func() { _, err := m.Instruments(context.Background()); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		m.mu.Lock()
		active := m.operations
		m.mu.Unlock()
		if active > 0 {
			break
		}
		if time.Now().After(deadline) {
			<-m.gate
			t.Fatal("chart check did not begin")
		}
		time.Sleep(time.Millisecond)
	}
	_, err := m.Configure(Settings{Username: testUsername, Password: testPassword, Environment: "demo"})
	<-m.gate
	if err != nil {
		t.Fatal("first configuration rejected a harmless pending check", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("configured chart check did not resume")
	}
	if count.Load() != 1 {
		t.Fatal("unconfigured check spawned an unexpected process")
	}
}

func waitBars(t *testing.T, updates <-chan []domain.Candle) []domain.Candle {
	t.Helper()
	select {
	case bars := <-updates:
		return bars
	case <-time.After(5 * time.Second):
		t.Fatal("no bridge bars arrived")
		return nil
	}
}

func TestPINChallengeUsesSameProcessAndIsNeverStoredOrExposed(t *testing.T) {
	m, count := testManager(t, "pin")
	challenge, err := m.Challenge(context.Background())
	if err != nil || challenge.ImageDataURL != pngImage {
		t.Fatal(challenge, err)
	}
	if _, err = m.Configure(Settings{Username: testUsername, Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.TestConnectionWithPIN(context.Background(), "image-code"); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 || !m.Info().Connected {
		t.Fatal("challenge session was replaced")
	}
	for iteration := range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		updates := make(chan []domain.Candle, 5)
		done := make(chan error, 1)
		go func() {
			done <- m.Stream(ctx, forexKey(), func(bars []domain.Candle, _ domain.Health) {
				if len(bars) > 0 {
					updates <- bars
				}
			})
		}()
		waitBars(t, updates)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("PIN chart subscription did not detach")
		}
		if iteration == 0 {
			// Switching to Screener releases all quote subscriptions. Its visit
			// must retain the PIN-authorized client for subsequent chart reuse.
			time.Sleep(2100 * time.Millisecond)
		}
		if !m.Info().Connected || count.Load() != 1 {
			t.Fatal("PIN-authenticated IClient expired between chart panels")
		}
	}
	m.mu.Lock()
	p := m.process
	m.mu.Unlock()
	p.mu.Lock()
	cachedPIN := len(p.secrets)
	p.mu.Unlock()
	if cachedPIN != 0 {
		t.Fatal("transient PIN retained after authentication")
	}
	data, _ := os.ReadFile(m.path)
	if strings.Contains(string(data), "image-code") {
		t.Fatal("PIN persisted")
	}
	info, _ := json.Marshal(m.Info())
	if strings.Contains(string(info), "image-code") {
		t.Fatal("PIN exposed")
	}
}

func TestSDKFailureRedactsCredentialsAndPIN(t *testing.T) {
	m, _ := testManager(t, "error")
	_, err := m.TestConnectionWithPIN(context.Background(), "secret-pin")
	if err == nil {
		t.Fatal("expected refused login")
	}
	for _, secret := range []string{testUsername, testPassword, "secret-pin"} {
		if strings.Contains(err.Error(), secret) || strings.Contains(m.Info().Error, secret) {
			t.Fatal("provider error exposed a credential", err)
		}
	}
}

func TestExplicitConnectionTestChecksActualSDKConnectionRatherThanOnlyProcess(t *testing.T) {
	m, count := testManager(t, "disconnected-on-retest")
	if _, err := m.TestConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.TestConnection(context.Background()); err == nil {
		t.Fatal("an alive process masked the disconnected SDK session")
	}
	if m.Info().Connected {
		t.Fatal("disconnected SDK reported connected")
	}
	if count.Load() != 1 {
		t.Fatal("connection retest spawned a second client")
	}
}

func TestInvalidBarsAndMalformedProtocolAreVisibleFailures(t *testing.T) {
	for _, scenario := range []string{"invalid-candle", "invalid-protocol"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := testManager(t, scenario)
			if _, err := m.History(context.Background(), forexKey(), 1792368000, 1792378800, 3); err == nil {
				t.Fatal("invalid provider data accepted")
			}
		})
	}
}

func TestCloseCancelsBlockedRequestsAndDoesNotRetainWorker(t *testing.T) {
	m, _ := testManager(t, "hang-history")
	if _, err := m.TestConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := m.History(context.Background(), forexKey(), 1792368000, 1792378800, 3); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		m.mu.Lock()
		active := m.operations
		m.mu.Unlock()
		if active > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("history did not start")
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked history succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("history remained blocked on app close")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("worker was not reaped")
	}
}

func TestCachePartitionedByAccountAndEnvironment(t *testing.T) {
	m, _ := testManager(t, "")
	a := m.cachePath(configuration{Username: "user-a", Environment: "demo"})
	b := m.cachePath(configuration{Username: "user-a", Environment: "live"})
	c := m.cachePath(configuration{Username: "user-b", Environment: "demo"})
	if a == b || a == c || b == c || strings.Contains(a, "user-a") {
		t.Fatal("cache source identity collided or exposed account", a, b, c)
	}
}

func TestBridgeHelperProcess(t *testing.T) {
	if os.Getenv("CRYPTODESK_JFOREX_TEST_PROCESS") != "1" {
		return
	}
	scenario := os.Getenv("CRYPTODESK_JFOREX_TEST_SCENARIO")
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	subs := map[string]bool{}
	username, password := "", ""
	challenge := false
	connections := 0
	respond := func(id string, result any) {
		_ = encoder.Encode(map[string]any{"id": id, "ok": true, "result": result})
	}
	bar := func(at int64, revision int) map[string]any {
		return map[string]any{"time": at, "open": 1.1, "high": 1.3, "low": 1.0, "close": 1.1 + float64(revision)/100, "volume": 2.0, "closed": true, "quality": "authoritative", "sourceRevision": revision}
	}
	event := func(id string) {
		if scenario == "separate-health" {
			_ = encoder.Encode(map[string]any{"event": "health", "subscriptionId": id, "health": map[string]any{"state": "offline", "reason": "fixture connection interruption", "lastMessageAt": 1792375200, "lastTradeAt": 1792375100}})
			_ = encoder.Encode(map[string]any{"event": "bars", "subscriptionId": id, "bars": []any{bar(1792375200, 2)}})
			return
		}
		_ = encoder.Encode(map[string]any{"event": "bars", "subscriptionId": id, "bars": []any{bar(1792375200, 2)}, "health": map[string]any{"state": "live", "reason": "Dukascopy Bid quotes", "lastMessageAt": 1792375200}})
	}
	for scanner.Scan() {
		var request map[string]json.RawMessage
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		var id, op string
		_ = json.Unmarshal(request["id"], &id)
		_ = json.Unmarshal(request["op"], &op)
		switch op {
		case "connect":
			connections++
			_ = json.Unmarshal(request["username"], &username)
			_ = json.Unmarshal(request["password"], &password)
			var pin string
			_ = json.Unmarshal(request["pin"], &pin)
			if scenario == "error" || scenario == "pin" && (!challenge || pin != "image-code") || scenario == "disconnected-on-retest" && connections > 1 {
				_ = encoder.Encode(map[string]any{"id": id, "ok": false, "error": map[string]any{"kind": "authentication", "message": "Rejected " + username + " " + password + " " + pin}})
			} else {
				respond(id, map[string]any{"connected": true, "instrumentCount": 2})
			}
		case "captcha":
			challenge = true
			respond(id, LoginChallenge{ImageDataURL: pngImage})
		case "instruments":
			respond(id, []domain.Instrument{{NativeID: "USDJPY", Market: "forex", BaseAsset: "USD", QuoteAsset: "JPY", SupportedIntervals: domain.Intervals}, {NativeID: "EURUSD", Market: "forex", BaseAsset: "EUR", QuoteAsset: "USD", SupportedIntervals: domain.Intervals}})
		case "history":
			if scenario == "hang-history" {
				select {}
			}
			if scenario == "invalid-protocol" {
				fmt.Fprintln(os.Stdout, "secret invalid protocol")
				continue
			}
			bars := []any{bar(1792375200, 2), bar(1792371600, 1), bar(1792368000, 1), bar(1792371600, 10)}
			if scenario == "invalid-candle" {
				invalid := bar(1792368000, 1)
				invalid["close"] = -1
				bars = []any{invalid}
			}
			respond(id, bars)
			for sub := range subs {
				event(sub)
			}
		case "subscribe":
			var sub string
			_ = json.Unmarshal(request["subscriptionId"], &sub)
			subs[sub] = true
			respond(id, map[string]any{"subscribed": true})
			event(sub)
		case "unsubscribe":
			var sub string
			_ = json.Unmarshal(request["subscriptionId"], &sub)
			delete(subs, sub)
			respond(id, map[string]any{})
		default:
			respond(id, map[string]any{})
		}
	}
	// Prevent the Go test runner's PASS line entering the JSON protocol.
	os.Exit(0)
}

func TestHistoryAndFeedInvalidRequestsDoNotLaunchWorker(t *testing.T) {
	m, count := testManager(t, "")
	key := forexKey()
	key.Exchange = "binance"
	if _, err := m.History(context.Background(), key, 1, 2, 1); err == nil {
		t.Fatal("wrong provider accepted")
	}
	if _, err := m.History(context.Background(), forexKey(), 20, 10, 1); err == nil {
		t.Fatal("reversed range accepted")
	}
	if count.Load() != 0 {
		t.Fatal("invalid request contacted broker")
	}
}

func TestRuntimeCommandContainsNoCredentialsAndIsolatesJavaHome(t *testing.T) {
	dir := t.TempDir()
	runtime := bridgeRuntime{directory: dir, java: "java", privateDirectory: filepath.Join(dir, "private"), sdkDirectory: filepath.Join(dir, "sdk")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := runtime.preparedCommand(ctx)
	if cmd.Dir != runtime.privateDirectory || !reflect.DeepEqual(cmd.Args, []string{"java", "-Djava.awt.headless=true", "-Duser.home=" + runtime.privateDirectory, "-Djava.util.prefs.userRoot=" + filepath.Join(runtime.privateDirectory, "preferences"), "-cp", filepath.Join(dir, "cryptodesk-jforex.jar") + string(os.PathListSeparator) + filepath.Join(runtime.sdkDirectory, "lib", "*"), "cryptodesk.jforex.Bridge"}) {
		t.Fatal(cmd.Args, cmd.Dir)
	}
}
