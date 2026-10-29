package screener

import "sync"

// File batches use independent child processes and scratch directories,
// keeping at most the configured number of files active. Queued files are still
// visited after cancellation so their owners can mark every job terminal
// without starting a child process. The batch waits for all workers to drain.

func runFilePoolWithConcurrency(count, concurrency int, run func(int)) {
	concurrency = min(MaxConcurrency, max(1, concurrency))
	indexes := make(chan int)
	var workers sync.WaitGroup
	for range min(concurrency, count) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range indexes {
				run(index)
			}
		}()
	}
	for index := range count {
		indexes <- index
	}
	close(indexes)
	workers.Wait()
}
