//go:build !bindings

package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func receiveCloseRequest(t *testing.T, requests <-chan struct{}) {
	t.Helper()
	select {
	case <-requests:
	case <-time.After(3 * time.Second):
		t.Fatal("termination did not request a close")
	}
}

func TestWatchTerminationKeepsHandlingRequests(t *testing.T) {
	signals := make(chan os.Signal, 2)
	requests := make(chan struct{}, 4)
	stop := watchTermination(signals, func() { requests <- struct{}{} })
	t.Cleanup(stop)
	for _, received := range []os.Signal{os.Interrupt, syscall.SIGTERM, os.Interrupt} {
		signals <- received
		receiveCloseRequest(t, requests)
	}
}

func TestWatchTerminationHandlesCoalescedBurstAndLaterRetry(t *testing.T) {
	signals := make(chan os.Signal, 1)
	requests := make(chan struct{}, 4)
	resume := make(chan struct{})
	count := 0
	stop := watchTermination(signals, func() {
		count++
		requests <- struct{}{}
		if count == 1 {
			<-resume
		}
	})
	t.Cleanup(stop)
	signals <- os.Interrupt
	receiveCloseRequest(t, requests)
	// os/signal uses nonblocking sends: a full notification channel coalesces
	// a burst while the first close request is still being processed.
	for range 100 {
		select {
		case signals <- os.Interrupt:
		default:
		}
	}
	close(resume)
	receiveCloseRequest(t, requests)
	signals <- syscall.SIGTERM
	receiveCloseRequest(t, requests)
}

func TestWatchTerminationStopWaitsForActiveCallback(t *testing.T) {
	signals := make(chan os.Signal, 1)
	entered, resume, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stop := watchTermination(signals, func() { close(entered); <-resume })
	signals <- os.Interrupt
	receiveCloseRequest(t, entered)
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned while the callback was still active")
	case <-time.After(25 * time.Millisecond):
	}
	close(resume)
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not join the completed callback")
	}
}

func TestWatchTerminationStopsAndAcceptsClosedInput(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "closed input"}[closed], func(t *testing.T) {
			signals := make(chan os.Signal, 1)
			requests := make(chan struct{}, 1)
			stop := watchTermination(signals, func() { requests <- struct{}{} })
			if closed {
				close(signals)
			}
			stop()
			if !closed {
				signals <- os.Interrupt
			}
			select {
			case <-requests:
				t.Fatal("received a close callback after stopping")
			case <-time.After(25 * time.Millisecond):
			}
		})
	}
}
