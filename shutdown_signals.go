//go:build !bindings

package main

import "os"

// Wails' signal listener stops after its first request. Keep our listener alive
// so a close paused for saving can be retried without terminating the process.
func watchTermination(signals <-chan os.Signal, requestClose func()) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			case _, open := <-signals:
				if !open {
					return
				}
				requestClose()
			}
		}
	}()
	return func() { close(done); <-stopped }
}
