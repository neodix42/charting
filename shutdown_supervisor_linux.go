//go:build linux && !bindings

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const guiWorkerParent = "CRYPTODESK_INTERNAL_GUI_PARENT"

// The terminal sends Ctrl+C to its foreground process group, not just the app.
// WebKit processes must survive until React acknowledges the final save. Keep
// the CLI in that group and run the GUI/browser tree in a separate group; relay
// termination only to the GUI PID, then return its exit status to the shell.
func superviseGUI(signals <-chan os.Signal) (handled bool, exitCode int, err error) {
	if os.Getenv(guiWorkerParent) == strconv.Itoa(os.Getppid()) {
		ready := os.NewFile(3, "cryptodesk-signal-ready")
		if ready == nil {
			return true, 1, errors.New("missing GUI signal readiness pipe")
		}
		_, err = ready.Write([]byte{1})
		_ = ready.Close()
		return false, 0, err
	}
	executable, err := os.Executable()
	if err != nil {
		return true, 1, err
	}
	command := exec.Command(executable, os.Args[1:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, guiWorkerParent+"=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, guiWorkerParent+"="+strconv.Itoa(os.Getpid()))
	exitCode, err = superviseCommand(command, signals)
	return true, exitCode, err
}

func superviseCommand(command *exec.Cmd, signals <-chan os.Signal) (int, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return 1, err
	}
	defer reader.Close()
	defer writer.Close()
	command.ExtraFiles = []*os.File{writer}
	// Pdeathsig follows the creating OS thread on Linux. Keep that thread alive
	// while supervising so only the actual broker's death requests GUI shutdown.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if err := command.Start(); err != nil {
		return 1, err
	}
	_ = writer.Close()
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	ready := make(chan bool, 1)
	go func() {
		var value [1]byte
		_, err := io.ReadFull(reader, value[:])
		ready <- err == nil && value[0] == 1
	}()
	armed, pending := false, os.Signal(nil)
	for {
		select {
		case err := <-exited:
			var status *exec.ExitError
			if errors.As(err, &status) {
				if result, ok := status.Sys().(syscall.WaitStatus); ok && result.Signaled() {
					return 128 + int(result.Signal()), nil
				}
				return status.ExitCode(), nil
			}
			return 0, err
		case armed = <-ready:
			ready = nil
			if armed && pending != nil {
				if err := command.Process.Signal(pending); err != nil && !errors.Is(err, os.ErrProcessDone) {
					return 1, fmt.Errorf("relay pending close signal: %w", err)
				}
				pending = nil
			}
		case received, open := <-signals:
			if !open {
				signals = nil
				continue
			}
			if !armed {
				pending = received
				continue
			}
			if err := command.Process.Signal(received); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return 1, fmt.Errorf("relay close signal: %w", err)
			}
		}
	}
}
