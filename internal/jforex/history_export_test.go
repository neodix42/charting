package jforex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

const exportTestFrom = int64(1704067200000)
const exportTestTo = exportTestFrom + tickHistoryHour
const exportTestHeader = "timestamp,askPrice,bidPrice,askVolume,bidVolume\n"

func tickExportManager(t *testing.T, scenario string) (*Manager, *atomic.Int32) {
	t.Helper()
	starts := &atomic.Int32{}
	m := newManager(t.TempDir(), bridgeRuntime{directory: "offline-export-bridge", java: "offline-java"}, func(ctx context.Context) (*exec.Cmd, error) {
		starts.Add(1)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTickExportBridgeProcess$")
		cmd.Env = append(os.Environ(), "CRYPTODESK_JFOREX_TICK_TEST=1", "CRYPTODESK_JFOREX_TICK_SCENARIO="+scenario)
		configureProcess(cmd)
		return cmd, nil
	})
	t.Cleanup(m.Close)
	if _, err := m.Configure(Settings{Username: "offline-export-user", Password: "offline-export-password", Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	return m, starts
}

func emptyTickOutput(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ticks.csv")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHistoricalSourceIsOpaqueAccountAndEnvironmentScopedWithoutLogin(t *testing.T) {
	m, starts := tickExportManager(t, "normal")
	key, configured := m.HistoricalSource()
	sum := sha256.Sum256([]byte("demo\x00offline-export-user"))
	if !configured || key != hex.EncodeToString(sum[:]) || starts.Load() != 0 {
		t.Fatalf("source identity leaked, changed, or logged in: %q %v starts=%d", key, configured, starts.Load())
	}
	if _, err := m.Configure(Settings{Username: "offline-export-user", Password: "changed-offline-password", Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	if same, ok := m.HistoricalSource(); !ok || same != key {
		t.Fatal("password rotation changed source identity")
	}
	for _, config := range []Settings{
		{Username: "other-offline-user", Password: "other-offline-password", Environment: "demo"},
		{Username: "offline-export-user", Password: "other-offline-password", Environment: "live"},
	} {
		if _, err := m.Configure(config); err != nil {
			t.Fatal(err)
		}
		if next, ok := m.HistoricalSource(); !ok || next == key {
			t.Fatal("source identity crossed account/environment")
		}
	}
	if _, err := m.Configure(Settings{Username: "offline-export-user", ClearPassword: true, Environment: "demo"}); err != nil {
		t.Fatal(err)
	}
	if key, ok := m.HistoricalSource(); ok || key != "" || starts.Load() != 0 {
		t.Fatal("unconfigured source available or started a bridge")
	}
}

func TestExportTicksUsesNativeIdentityAndPreservesMillisecondDuplicates(t *testing.T) {
	for _, scenario := range []string{"normal", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			m, starts := tickExportManager(t, scenario)
			key, _ := m.HistoricalSource()
			output := emptyTickOutput(t)
			rows, err := m.ExportTicks(context.Background(), key, "btcusd", exportTestFrom, exportTestTo, output)
			expected := int64(3)
			if scenario == "empty" {
				expected = 0
			}
			if err != nil || rows != expected || starts.Load() != 1 {
				t.Fatalf("native tick export: rows=%d starts=%d err=%v", rows, starts.Load(), err)
			}
			data, err := os.ReadFile(output)
			if err != nil || string(data) != tickExportFixture(scenario, exportTestFrom) {
				t.Fatalf("tick values/ordering changed: %q %v", data, err)
			}
			m.mu.Lock()
			active := m.operations
			m.mu.Unlock()
			if active != 0 {
				t.Fatal("completed export retained account reservation")
			}
		})
	}
}

func TestExportTicksRejectsInvalidRequestBeforeLoginAndPreservesFiles(t *testing.T) {
	for _, kind := range []string{"source", "symbol", "negative", "unaligned", "two-hours", "forming", "overflow", "nonempty", "symlink", "directory", "missing", "relative", "permissions", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			m, starts := tickExportManager(t, "normal")
			key, _ := m.HistoricalSource()
			output, symbol, from, to := emptyTickOutput(t), "BTCUSD", exportTestFrom, exportTestTo
			ctx := context.Background()
			switch kind {
			case "source":
				key = strings.Repeat("0", 64)
			case "symbol":
				symbol = "BTC/USD"
			case "negative":
				from, to = -tickHistoryHour, 0
			case "unaligned":
				from, to = from+1, to+1
			case "two-hours":
				from -= tickHistoryHour
			case "forming":
				from = time.Now().UnixMilli() / tickHistoryHour * tickHistoryHour
				to = from + tickHistoryHour
			case "overflow":
				from, to = math.MaxInt64/tickHistoryHour*tickHistoryHour, math.MinInt64+tickHistoryHour
			case "nonempty":
				if err := os.WriteFile(output, []byte("saved original"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := output + ".retained"
				if err := os.Rename(output, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, output); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			case "directory":
				output = filepath.Dir(output)
			case "missing":
				output += ".missing"
			case "relative":
				output = "ticks.csv"
			case "permissions":
				if runtime.GOOS == "windows" {
					t.Skip("Windows private files use access control rather than POSIX bits")
				}
				if err := os.Chmod(output, 0644); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			if rows, err := m.ExportTicks(ctx, key, symbol, from, to, output); err == nil || rows != 0 || starts.Load() != 0 {
				t.Fatalf("invalid export reached login: rows=%d starts=%d err=%v", rows, starts.Load(), err)
			}
			if kind == "nonempty" {
				data, _ := os.ReadFile(output)
				if string(data) != "saved original" {
					t.Fatal("invalid output was modified")
				}
			}
		})
	}
}

func TestExportTicksChecksCapabilityAndCompletedFileIntegrity(t *testing.T) {
	for _, scenario := range []string{"legacy", "invalid-value", "invalid-count", "replaced", "failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := tickExportManager(t, scenario)
			key, _ := m.HistoricalSource()
			if rows, err := m.ExportTicks(context.Background(), key, "BTCUSD", exportTestFrom, exportTestTo, emptyTickOutput(t)); err == nil || rows != 0 {
				t.Fatalf("incompatible/corrupt/failed export published: rows=%d err=%v", rows, err)
			}
		})
	}
}

func TestExportTicksCancellationStopsWriterAndKeepsSharedSession(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("CRYPTODESK_JFOREX_TICK_TRACE", trace)
	m, starts := tickExportManager(t, "blocked")
	key, _ := m.HistoricalSource()
	output := emptyTickOutput(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := m.ExportTicks(ctx, key, "BTCUSD", exportTestFrom, exportTestTo, output)
		finished <- err
	}()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		data, _ := os.ReadFile(trace)
		if strings.Contains(string(data), "historyTicks\n") {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("export ended before entering SDK: %v", err)
		case <-deadline.C:
			t.Fatal("export did not reach its private bridge")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if _, err := m.Configure(Settings{Username: "different-offline-user", Password: "different-password", Environment: "demo"}); err == nil {
		t.Fatal("account switched under a reserved SDK export")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled export did not preserve context error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("tick cancellation did not acknowledge stopping its writer")
	}
	data, _ := os.ReadFile(trace)
	if !strings.Contains(string(data), "historyTicksCancel\n") {
		t.Fatalf("canceled export lacked stop handshake: %s", data)
	}
	if info, err := os.Stat(output); err != nil || info.Size() != 0 {
		t.Fatal("canceled SDK read published data", err)
	}
	if _, err := m.TestConnection(context.Background()); err != nil || starts.Load() != 1 {
		t.Fatal("cancellation disconnected unrelated chart session", err, starts.Load())
	}
	m.mu.Lock()
	active := m.operations
	m.mu.Unlock()
	if active != 0 {
		t.Fatal("canceled export retained its account reservation")
	}
}

func TestExportTicksMissingCancellationAcknowledgementStopsBridgeBeforeReturning(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("CRYPTODESK_JFOREX_TICK_TRACE", trace)
	m, starts := tickExportManager(t, "blocked-badcancel")
	key, _ := m.HistoricalSource()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := emptyTickOutput(t)
	finished := make(chan error, 1)
	go func() {
		_, err := m.ExportTicks(ctx, key, "BTCUSD", exportTestFrom, exportTestTo, output)
		finished <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(trace)
		if strings.Contains(string(data), "historyTicks\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("export did not reach its private bridge")
		}
		select {
		case err := <-finished:
			t.Fatalf("export unexpectedly completed: %v", err)
		case <-time.After(5 * time.Millisecond):
		}
	}
	m.mu.Lock()
	process := m.process
	m.mu.Unlock()
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("lost original cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("unacknowledged writer was not stopped")
	}
	select {
	case <-process.exited:
	default:
		t.Fatal("export returned before its unsafe writer process exited")
	}
	if starts.Load() != 1 || m.Info().Connected {
		t.Fatal("failed cancellation retained/restarted the bridge")
	}
}

func tickExportFixture(scenario string, from int64) string {
	if scenario == "empty" {
		return exportTestHeader
	}
	if scenario == "invalid-value" {
		return exportTestHeader + fmt.Sprintf("%d,NaN,1,1,1\n", from)
	}
	return exportTestHeader + fmt.Sprintf("%d,2.5,2,0,3\n%d,3,2.5,1,4\n%d,4,3,2,5\n", from, from, from+tickHistoryHour-1)
}

func TestTickExportBridgeProcess(t *testing.T) {
	if os.Getenv("CRYPTODESK_JFOREX_TICK_TEST") != "1" {
		return
	}
	scenario := os.Getenv("CRYPTODESK_JFOREX_TICK_SCENARIO")
	encoder := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	respond := func(id string, result any) {
		_ = encoder.Encode(map[string]any{"id": id, "ok": true, "result": result})
	}
	fail := func(id, message string) {
		_ = encoder.Encode(map[string]any{"id": id, "ok": false, "error": map[string]any{"kind": "offline", "message": message}})
	}
	for scanner.Scan() {
		var request struct {
			ID, Op, ExportID, Instrument, Output string
			From, To                             int64
			FeedKey                              domain.FeedKey
			Download                             bool
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		if trace := os.Getenv("CRYPTODESK_JFOREX_TICK_TRACE"); trace != "" {
			file, err := os.OpenFile(trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(3)
			}
			_, _ = fmt.Fprintln(file, request.Op)
			_ = file.Close()
		}
		switch request.Op {
		case "version":
			version := map[string]any{"protocol": 1, "sdkVersion": "3.6.51", "javaVersion": 17, "instrumentCatalog": bridgeInstrumentCatalog}
			if scenario != "legacy" {
				version["tickHistory"] = tickHistoryCapability
			}
			respond(request.ID, version)
		case "connect":
			respond(request.ID, map[string]any{"connected": true, "instrumentCount": 1})
		case "history":
			if trace := os.Getenv("CRYPTODESK_JFOREX_DOWNLOAD_TRACE"); trace != "" {
				file, err := os.OpenFile(trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					os.Exit(6)
				}
				_, _ = file.Write(append(append([]byte(nil), scanner.Bytes()...), '\n'))
				_ = file.Close()
			}
			if scenario == "daily-failure" {
				fail(request.ID, "JForex native daily download timed out. Completed history is retained; retry to resume.")
				continue
			}
			key := request.FeedKey
			if !strings.HasPrefix(scenario, "daily") || key.Timeframe != "1d" || !(key.Instrument == "BTCUSD" && key.Market == "cfd" || key.Instrument == "XAUXAG" && key.Market == "metal") || request.Download && (request.From%86400 != 0 || request.To%86400 != 0 || request.To-request.From > 366*86400) {
				fail(request.ID, "Unexpected native daily history request")
				continue
			}
			bars := []domain.Candle{}
			for at := request.From / 86400 * 86400; at < request.To; at += 86400 {
				bars = append(bars, cacheBar(at))
			}
			respond(request.ID, bars)
		case "historyTicks":
			if request.Instrument != "BTCUSD" || request.From != exportTestFrom || request.To != exportTestTo {
				fail(request.ID, "Wrong native instrument or exact UTC hour")
				continue
			}
			if strings.HasPrefix(scenario, "blocked") {
				continue
			}
			if scenario == "failure" {
				fail(request.ID, "Explicit offline SDK failure")
				continue
			}
			if scenario == "replaced" {
				if os.Rename(request.Output, request.Output+".original") != nil {
					os.Exit(4)
				}
			}
			if os.WriteFile(request.Output, []byte(tickExportFixture(scenario, request.From)), 0600) != nil {
				os.Exit(5)
			}
			rows := 3
			if scenario == "empty" || scenario == "invalid-count" {
				rows = 0
			}
			respond(request.ID, map[string]any{"rows": rows})
		case "historyTicksCancel":
			respond(request.ID, map[string]any{"cancelled": scenario != "blocked-badcancel"})
		case "disconnect", "close":
			respond(request.ID, map[string]any{})
			return
		default:
			fail(request.ID, "Unsupported offline test operation")
		}
	}
}
