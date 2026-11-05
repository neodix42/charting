//go:build bindings

package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestCloseAppWorkersStartsAllCancellationBeforeWaiting(t *testing.T) {
	const services = 5
	entered, allStarted := make(chan struct{}, services), make(chan struct{})
	done := make(chan struct{})
	var drained atomic.Int32
	workers := make([]func(), services)
	for i := range workers {
		workers[i] = func() { entered <- struct{}{}; <-allStarted; drained.Add(1) }
	}
	go func() { closeAppWorkers(workers...); close(done) }()
	for i := 0; i < services; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(allStarted)
			t.Fatal("a service wait prevented another service receiving cancellation")
		}
	}
	select {
	case <-done:
		t.Fatal("shutdown returned while a worker was still draining")
	default:
	}
	close(allStarted)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not drain cancelled workers")
	}
	if drained.Load() != services {
		t.Fatal("shutdown dropped an unfinished worker")
	}
}

func TestCloseAppWorkersHandlesAbsentServices(t *testing.T) { closeAppWorkers(nil); closeAppWorkers() }
