package screener

import "sync"

// Local enrichment uses independent child processes and scratch directories,
// keeping at most twenty files active. Network refresh explicitly uses one
// worker. run still visits queued files after cancellation so their owners can
// mark every job terminal without starting a child process. The batch owner
// does not finish until all workers have drained.
const fileBatchConcurrency = 20

func runFilePool(count int, run func(int)) {
	runFilePoolWithConcurrency(count, fileBatchConcurrency, run)
}

func runFilePoolWithConcurrency(count, concurrency int, run func(int)) {
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
