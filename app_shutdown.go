package main

import (
	"context"
	"fmt"
	"os"
	"sync"
)

// Every service must receive cancellation before waiting for another service's
// workers. For example, market streams unsubscribe through the JForex bridge;
// closing that bridge releases outstanding RPCs instead of waiting for timeout.
func closeAppWorkers(workers ...func()) {
	var pending sync.WaitGroup
	for _, closeWorker := range workers {
		if closeWorker == nil {
			continue
		}
		pending.Add(1)
		go func(closeWorker func()) { defer pending.Done(); closeWorker() }(closeWorker)
	}
	pending.Wait()
}

func (a *App) shutdown(context.Context) {
	a.shutdownOnce.Do(a.closeServices)
}

func (a *App) closeServices() {
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if a.bootstrap != nil {
		a.bootstrap.stop()
	}
	workers := []func(){a.closeTrading}
	if a.liveAlerts != nil {
		workers = append(workers, a.liveAlerts.Close)
	}
	if a.predictions != nil {
		workers = append(workers, a.predictions.Close)
	}
	if a.market != nil {
		workers = append(workers, a.market.Close)
	}
	if a.jforex != nil {
		workers = append(workers, a.jforex.Close)
	}
	if a.screener != nil {
		workers = append(workers, a.screener.Close)
	}
	closeAppWorkers(workers...)
	// Producers have stopped. Finish all queued candle writes before releasing
	// the workspace database and its single-instance lock.
	if a.candles != nil {
		if err := a.candles.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "Close candle storage:", err)
		}
	}
	if a.store != nil {
		_ = a.store.Close()
	}
}
