package predictions

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRuntimeWorkerHelperProcess(t *testing.T) {
	marker := -1
	for i, arg := range os.Args {
		if arg == "cryptodesk-prediction-helper" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	mode := os.Args[marker+1]
	if mode == "child" {
		time.Sleep(10 * time.Minute)
		os.Exit(0)
	}
	line, err := bufio.NewReader(os.Stdin).ReadBytes('\n')
	if err != nil {
		os.Exit(70)
	}
	var request map[string]any
	if json.Unmarshal(line, &request) != nil {
		os.Exit(70)
	}
	switch mode {
	case "valid":
		fmt.Fprintln(os.Stderr, strings.Repeat("bounded diagnostic ", 5000))
		fmt.Println(`{"type":"progress","phase":"forecasting","progress":50,"message":"CPU inference"}`)
		fmt.Println(`{"type":"result","points":[{"step":1,"lower":-2,"median":0,"upper":2}]}`)
	case "probe":
		fmt.Println(`{"type":"ready"}`)
	case "invalid":
		fmt.Println("non-JSON log on stdout")
	case "unknown":
		fmt.Println(`{"type":"unrecognized"}`)
	case "missing":
	case "duplicate":
		fmt.Println(`{"type":"ready"}`)
		fmt.Println(`{"type":"ready"}`)
	case "runtimeerror":
		fmt.Println(`{"type":"error","phase":"runtime","message":"Pinned dependency versions changed"}`)
		os.Exit(1)
	case "error":
		fmt.Println(`{"type":"error","message":"Model load failed"}`)
		os.Exit(1)
	case "oversized":
		fmt.Println(strings.Repeat("x", 1024*1024+1))
	case "spawn":
		child := exec.Command(os.Args[0], "-test.run=TestRuntimeWorkerHelperProcess", "--", "cryptodesk-prediction-helper", "child")
		if err = child.Start(); err != nil {
			os.Exit(72)
		}
		markerPath, _ := request["marker"].(string)
		if err = os.WriteFile(markerPath, []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
			os.Exit(73)
		}
		defer child.Wait()
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	default:
		os.Exit(74)
	}
	os.Exit(0)
}
func runtimeHelper(t *testing.T, mode string) (string, []string) {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return program, []string{"-test.run=TestRuntimeWorkerHelperProcess", "--", "cryptodesk-prediction-helper", mode}
}
func TestRuntimeWorkerProtocolValidatesBoundedTerminalResponses(t *testing.T) {
	r := newTestLocalRuntime(t)
	if err := os.MkdirAll(r.root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "probe", "invalid", "unknown", "missing", "duplicate", "error", "runtimeerror", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			program, args := runtimeHelper(t, mode)
			request := map[string]any{"action": "predict"}
			if mode == "probe" || mode == "duplicate" {
				request["action"] = "probe"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			progress := 0
			points, err := r.runProtocol(ctx, program, args, request, func(e Event) { progress++ })
			if mode == "valid" {
				if err != nil || len(points) != 1 || points[0].Median != 0 || progress != 1 {
					t.Fatalf("forecast=%+v progress=%d err=%v", points, progress, err)
				}
			} else if mode == "probe" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatalf("invalid protocol %s accepted", mode)
			} else if mode == "runtimeerror" {
				var runtimeErr *runtimeWorkerError
				if !errors.As(err, &runtimeErr) || runtimeErr.phase != "runtime" {
					t.Fatalf("dependency repair classification lost: %v", err)
				}
			}
		})
	}
}
func TestRuntimeWorkerCancellationStopsOwningProcessAndDescendants(t *testing.T) {
	r := newTestLocalRuntime(t)
	if err := os.MkdirAll(r.root, 0700); err != nil {
		t.Fatal(err)
	}
	program, args := runtimeHelper(t, "spawn")
	marker := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := r.runProtocol(ctx, program, args, map[string]any{"action": "predict", "marker": marker}, nil)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(marker)
		if err == nil {
			childPID, _ = strconv.Atoi(string(b))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		cancel()
		<-done
		t.Fatal("worker child did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("worker cancellation leaked a process pipe")
	}
	assertRuntimeProcessGone(t, childPID)
}
func TestRuntimeEmbeddedWorkerExitsWhenOwnerPipeCloses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("system Python command name varies; Windows protocol cancellation is independently covered")
	}
	program, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python not installed on test machine")
	}
	worker, _ := runtimeAssets.ReadFile("runtime_assets/worker.py")
	path := filepath.Join(t.TempDir(), "worker.py")
	if err = os.WriteFile(path, worker, 0600); err != nil {
		t.Fatal(err)
	}
	script := `import runpy,threading,time,sys; m=runpy.run_path(sys.argv[1]); threading.Thread(target=m["owner_watch"],daemon=True).start(); print("owner-watch-ready",flush=True); time.sleep(600)`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "-I", "-u", "-c", script, path)
	configureRuntimeProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Cancel(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "owner-watch-ready" {
		t.Fatalf("owner watcher=%q err=%v", line, err)
	}
	if err = stdin.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 71 {
		t.Fatalf("owner EOF exit=%v", err)
	}
}
