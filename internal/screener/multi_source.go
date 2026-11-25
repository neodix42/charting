package screener

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// All data sources is a Downloader selection, never a market-data identity.
// Resolve it into concrete, immutable requests in a stable provider order.
func allSourceRequests(request Request, now time.Time) ([]Request, error) {
	if request.DataSource != "all" || len(request.Symbols) != 0 || len(request.SourceSelections) < 1 || len(request.SourceSelections) > 2 {
		return nil, errors.New("All data sources requires separate source selections")
	}
	if request.Timeframe != "h1" && request.Timeframe != "d1" && request.Timeframe != "w1" {
		return nil, errors.New("All data sources supports H1, D1 or W1")
	}
	selected := map[string][]string{}
	for _, selection := range request.SourceSelections {
		if selection.DataSource != "dukascopy" && selection.DataSource != "hyperliquid" {
			return nil, errors.New("unsupported source in All data sources")
		}
		if _, exists := selected[selection.DataSource]; exists {
			return nil, errors.New("duplicate source in All data sources")
		}
		selected[selection.DataSource] = append([]string{}, selection.Symbols...)
	}
	requests := []Request{}
	jobs := 0
	for _, source := range []string{"dukascopy", "hyperliquid"} {
		if len(selected[source]) == 0 {
			continue
		}
		child := Request{SyntheticSource: request.SyntheticSource, DataSource: source, Symbols: selected[source], Timeframe: request.Timeframe, From: request.From, To: request.To, Concurrency: request.Concurrency}
		if err := validateRequest(child, now); err != nil {
			return nil, err
		}
		requests = append(requests, child)
		jobs += len(child.Symbols)
	}
	if len(requests) == 0 {
		return nil, errors.New("select at least one instrument across the data sources")
	}
	if request.Timeframe == "w1" {
		jobs *= 2
	}
	if jobs > 40000 {
		return nil, errors.New("combined download queue exceeds 40000 jobs")
	}
	return requests, nil
}

func (m *Manager) startAllSources(request Request) (Info, error) {
	requests, err := allSourceRequests(request, time.Now())
	if err != nil {
		return m.Info(), err
	}
	assets := map[string]Instrument{}
	order := make([]string, 0, len(requests))
	for _, child := range requests {
		order = append(order, child.DataSource)
		if child.DataSource != "hyperliquid" {
			continue
		}
		catalog, err := m.CatalogForSource(context.Background(), child.DataSource)
		if err != nil {
			return m.Info(), err
		}
		for _, group := range catalog.Groups {
			for _, asset := range group.Instruments {
				assets[asset.ID] = asset
			}
		}
		for _, symbol := range child.Symbols {
			if _, ok := assets[symbol]; !ok {
				return m.Info(), fmt.Errorf("unsupported Hyperliquid instrument %q", symbol)
			}
		}
	}
	return m.commitDownloadBatch(requests, assets, order, false)
}

// Missing Dukascopy runtime cannot prevent a later Hyperliquid phase from
// resuming when every Dukascopy job has already completed or been cancelled.
func (m *Manager) downloadRuntimeNeededLocked(retryFailed bool) bool {
	if m.runtimeError == "" {
		return false
	}
	for _, job := range m.jobs {
		if normalizeDataSource(job.DataSource) == "dukascopy" && (job.Status == "queued" || job.Status == "running" || retryFailed && job.Status == "failed") {
			return true
		}
	}
	return false
}

func (m *Manager) nextDownloadSourceLocked() string {
	for _, job := range m.jobs {
		if job.Status == "queued" {
			return normalizeDataSource(job.DataSource)
		}
	}
	return ""
}

// A versioned marker is necessary: legacy queues remain strictly one source,
// and a corrupt interleaved queue must never be accepted as a source plan.
func validateSequentialQueue(jobs []Job, order []string, concurrency int) error {
	if len(jobs) == 0 || jobs[0].AsOf <= 0 {
		return errors.New("sequential source plan has no shared cutoff")
	}
	for _, job := range jobs {
		if job.AsOf != jobs[0].AsOf {
			return errors.New("sequential jobs have different cutoffs")
		}
	}
	if len(order) < 1 || len(order) > 2 {
		return errors.New("invalid sequential source order")
	}
	if order[0] != "dukascopy" && order[0] != "hyperliquid" || len(order) == 2 && (order[0] != "dukascopy" || order[1] != "hyperliquid") {
		return errors.New("invalid sequential source order")
	}
	next := 0
	var common *Request
	for _, source := range order {
		start := next
		for next < len(jobs) && jobs[next].DataSource == source {
			next++
		}
		if start == next {
			return errors.New("sequential source has no jobs")
		}
		request, err := downloadBatchRequest(jobs[start:next], concurrency)
		if err != nil {
			return err
		}
		if request.Timeframe != "h1" && request.Timeframe != "d1" && request.Timeframe != "w1" {
			return errors.New("unsupported sequential timeframe")
		}
		if request.Timeframe != "w1" {
			for _, job := range jobs[start:next] {
				if job.Timeframe != request.Timeframe {
					return errors.New("only weekly sequential downloads have daily prerequisites")
				}
			}
		}
		if err = validateRequest(request, time.Now()); err != nil {
			return err
		}
		if common != nil && (request.Timeframe != common.Timeframe || request.From != common.From || request.To != common.To || jobs[start].AsOf != jobs[0].AsOf) {
			return errors.New("sequential sources do not share a request")
		}
		if common == nil {
			common = &request
		}
	}
	if next != len(jobs) {
		return errors.New("jobs do not follow sequential source order")
	}
	return nil
}
