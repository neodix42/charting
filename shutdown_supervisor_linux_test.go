//go:build linux && !bindings

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const shutdownHelperEnvironment = "CRYPTODESK_SHUTDOWN_TEST_HELPER"

type shutdownHelperOptions struct {
	Mode       string
	Metadata   string
	Result     string
	Gate       string
	GuardReady string
	GuardHit   string
	ExitCode   int
}

type shutdownHelperMetadata struct {
	PID  int
	PGID int
	PPID int
}

type shutdownHelperResult struct {
	Signal      string
	BeforeReady bool
	GuardPID    int
	GuardPGID   int
	GuardAlive  bool
	GuardSignal bool
}

func writeShutdownFixture(path string, value any) {
	content, err := json.Marshal(value)
	if err != nil || os.WriteFile(path, content, 0o600) != nil {
		os.Exit(90)
	}
}

func shutdownFixtureCommand(options shutdownHelperOptions) *exec.Cmd {
	encoded, _ := json.Marshal(options)
	command := exec.Command(os.Args[0], "-test.run=^TestShutdownSupervisorProcess$")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, shutdownHelperEnvironment+"=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, shutdownHelperEnvironment+"="+string(encoded))
	return command
}

// TestShutdownSupervisorProcess runs only in a subprocess created by these tests.
// All real signals target owned positive PIDs; no test signals a process group.
func TestShutdownSupervisorProcess(t *testing.T) {
	encoded := os.Getenv(shutdownHelperEnvironment)
	if encoded == "" {
		return
	}
	var options shutdownHelperOptions
	if json.Unmarshal([]byte(encoded), &options) != nil {
		os.Exit(90)
	}
	go func() { time.Sleep(8 * time.Second); os.Exit(91) }()
	if options.Mode == "signal-exit" {
		signal.Reset(syscall.SIGTERM)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		select {}
	}
	if options.Mode == "exit" || options.Mode == "no-ready" {
		ready := os.NewFile(3, "supervisor-test-ready")
		if options.Mode == "exit" {
			_, _ = ready.Write([]byte{1})
		}
		_ = ready.Close()
		os.Exit(options.ExitCode)
	}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	pgid, _ := syscall.Getpgid(0)
	writeShutdownFixture(options.Metadata, shutdownHelperMetadata{PID: os.Getpid(), PGID: pgid, PPID: os.Getppid()})
	if options.Mode == "guard" {
		<-signals
		writeShutdownFixture(options.Result, true)
		os.Exit(0)
	}
	var guard *exec.Cmd
	var guardMetadata shutdownHelperMetadata
	if options.Mode == "tree" {
		guard = shutdownFixtureCommand(shutdownHelperOptions{Mode: "guard", Metadata: options.GuardReady, Result: options.GuardHit})
		if guard.Start() != nil {
			os.Exit(90)
		}
		defer func() { _ = guard.Process.Kill(); _ = guard.Wait() }()
		for {
			data, err := os.ReadFile(options.GuardReady)
			if err == nil && json.Unmarshal(data, &guardMetadata) == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	for options.Gate != "" {
		if _, err := os.Stat(options.Gate); err == nil {
			break
		}
		select {
		case received := <-signals:
			writeShutdownFixture(options.Result, shutdownHelperResult{Signal: received.String(), BeforeReady: true})
			os.Exit(92)
		case <-time.After(5 * time.Millisecond):
		}
	}
	ready := os.NewFile(3, "supervisor-test-ready")
	if _, err := ready.Write([]byte{1}); err != nil {
		os.Exit(90)
	}
	_ = ready.Close()
	received := <-signals
	result := shutdownHelperResult{Signal: received.String()}
	if guard != nil {
		// A mistakenly relayed process-group signal would also reach this
		// browser-like descendant. Give its handler time to record that error.
		time.Sleep(75 * time.Millisecond)
		result.GuardPID, result.GuardPGID = guardMetadata.PID, guardMetadata.PGID
		result.GuardAlive = syscall.Kill(guardMetadata.PID, 0) == nil
		_, err := os.Stat(options.GuardHit)
		result.GuardSignal = err == nil
		_ = guard.Process.Kill()
		_ = guard.Wait()
		guard = nil
	}
	writeShutdownFixture(options.Result, result)
	os.Exit(0)
}

func waitShutdownFixture(t *testing.T, path string, destination any) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(content, destination) == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("helper did not write %s", filepath.Base(path))
}

type shutdownSupervisorResult struct {
	code int
	err  error
}

func startShutdownFixture(t *testing.T, options shutdownHelperOptions, signals chan os.Signal) <-chan shutdownSupervisorResult {
	t.Helper()
	completed := make(chan shutdownSupervisorResult, 1)
	command := shutdownFixtureCommand(options)
	go func() {
		code, err := superviseCommand(command, signals)
		completed <- shutdownSupervisorResult{code, err}
	}()
	return completed
}

func finishShutdownFixture(t *testing.T, completed <-chan shutdownSupervisorResult) shutdownSupervisorResult {
	t.Helper()
	select {
	case result := <-completed:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("supervisor did not return after its owned helper exited")
		return shutdownSupervisorResult{}
	}
}

func TestSupervisorQueuesEarlySignalsUntilFD3Ready(t *testing.T) {
	root := t.TempDir()
	options := shutdownHelperOptions{Mode: "wait", Metadata: filepath.Join(root, "child.json"), Result: filepath.Join(root, "result.json"), Gate: filepath.Join(root, "ready-gate")}
	signals := make(chan os.Signal, 2)
	signals <- os.Interrupt
	signals <- syscall.SIGTERM
	completed := startShutdownFixture(t, options, signals)
	var metadata shutdownHelperMetadata
	waitShutdownFixture(t, options.Metadata, &metadata)
	if metadata.PID <= 0 || metadata.PGID != metadata.PID || metadata.PGID == syscall.Getpgrp() || metadata.PPID != os.Getpid() {
		t.Fatalf("worker must have its own process group: %+v", metadata)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(signals) != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(signals) != 0 {
		t.Fatal("supervisor did not queue the early termination requests")
	}
	if err := os.WriteFile(options.Gate, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := finishShutdownFixture(t, completed)
	if result.code != 0 || result.err != nil {
		t.Fatalf("queued close failed: code=%d error=%v", result.code, result.err)
	}
	var received shutdownHelperResult
	waitShutdownFixture(t, options.Result, &received)
	if received.BeforeReady || received.Signal != syscall.SIGTERM.String() {
		t.Fatalf("expected the latest queued signal after readiness: %+v", received)
	}
}

func TestSupervisorSignalsOnlyWorkerPIDAndKeepsBrowserDescendantAlive(t *testing.T) {
	root := t.TempDir()
	options := shutdownHelperOptions{Mode: "tree", Metadata: filepath.Join(root, "worker.json"), Result: filepath.Join(root, "result.json"), GuardReady: filepath.Join(root, "guard.json"), GuardHit: filepath.Join(root, "guard-signalled.json")}
	signals := make(chan os.Signal, 1)
	completed := startShutdownFixture(t, options, signals)
	var worker, guard shutdownHelperMetadata
	waitShutdownFixture(t, options.Metadata, &worker)
	waitShutdownFixture(t, options.GuardReady, &guard)
	if guard.PPID != worker.PID || guard.PGID != worker.PID || worker.PGID == syscall.Getpgrp() {
		t.Fatalf("unexpected isolated process tree: worker=%+v guard=%+v", worker, guard)
	}
	signals <- os.Interrupt
	result := finishShutdownFixture(t, completed)
	if result.code != 0 || result.err != nil {
		t.Fatalf("worker close failed: code=%d error=%v", result.code, result.err)
	}
	var received shutdownHelperResult
	waitShutdownFixture(t, options.Result, &received)
	if received.Signal != os.Interrupt.String() || !received.GuardAlive || received.GuardSignal || received.GuardPID != guard.PID {
		t.Fatalf("close signal must reach the worker, preserving its browser: %+v", received)
	}
}

func TestSupervisorPreservesExitStatusAndClosedInput(t *testing.T) {
	for _, fixture := range []struct {
		mode string
		code int
		want int
	}{{"exit", 0, 0}, {"exit", 7, 7}, {"no-ready", 4, 4}, {"signal-exit", 0, 128 + int(syscall.SIGTERM)}} {
		t.Run(fmt.Sprintf("%s-%d", fixture.mode, fixture.want), func(t *testing.T) {
			signals := make(chan os.Signal)
			close(signals)
			completed := startShutdownFixture(t, shutdownHelperOptions{Mode: fixture.mode, ExitCode: fixture.code}, signals)
			result := finishShutdownFixture(t, completed)
			if result.code != fixture.want || result.err != nil {
				t.Fatalf("exit status changed: got=%d want=%d error=%v", result.code, fixture.want, result.err)
			}
		})
	}
}

func TestSupervisorReportsStartFailure(t *testing.T) {
	command := exec.Command(filepath.Join(t.TempDir(), "missing-cryptodesk"))
	code, err := superviseCommand(command, nil)
	if code != 1 || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("start failure was hidden: code=%d error=%v", code, err)
	}
}
