package screener

import (
	"cryptodesk/internal/exchange"
	"fmt"
	"time"
)

// A complete successful history search can legitimately return too few actual
// candles for a new/retention-limited market. It must never create a signal or
// processed-bar claim, but is different from a failed provider/data read.
type publicAlertInsufficientHistory struct {
	Required  int
	Available int
	Period    string
}

func (e *publicAlertInsufficientHistory) Error() string {
	return fmt.Sprintf("indicator/rule warm-up needs %d closed bars; only %d available after older-history backfill (saved for reuse; a new listing or provider history limit can prevent warm-up)", e.Required, e.Available)
}

// This is evidence of one bounded provider read, not proof of a listing date.
// The checksum-bound raw identity owns it; a new earliest bar, larger plan,
// provisional history, or expiry requires another search. A pointer permits
// backwards-compatible omission from old metadata; always replace, never mutate.
type publicAlertBackfillEvidence struct {
	Cutoff    int64 `json:"cutoff"`
	Bars      int   `json:"bars"`
	First     int64 `json:"first"`
	EmptyFrom int64 `json:"emptyFrom,omitempty"`
	EmptyTo   int64 `json:"emptyTo,omitempty"`
}

const publicAlertOlderHistoryRecheck = 7 * 24 * time.Hour

func (e *publicAlertBackfillEvidence) reusable(first, from, end, interval int64, required int, authoritative bool) bool {
	if e == nil || !authoritative || interval <= 0 || required <= 0 || e.Bars < required || e.Bars > 50000 || e.First != first || e.Cutoff <= 0 || e.Cutoff > end || first < 0 || first >= e.Cutoff || exchange.BucketStart(first, interval) != first || exchange.BucketStart(e.Cutoff, interval) != e.Cutoff {
		return false
	}
	if (e.EmptyFrom != 0 || e.EmptyTo != 0) && (e.EmptyFrom < 0 || e.EmptyTo <= e.EmptyFrom || e.EmptyTo-e.EmptyFrom > 1000*interval || e.EmptyTo > first || exchange.BucketStart(e.EmptyFrom, interval) != e.EmptyFrom || exchange.BucketStart(e.EmptyTo, interval) != e.EmptyTo) {
		return false
	}
	// Even reaching the supported search horizon only proves this cutoff's
	// bounded attempt. It must not become a permanent source-inception marker.
	if e.Cutoff == end {
		return true
	}
	return end-e.Cutoff < int64(publicAlertOlderHistoryRecheck/time.Second) && e.EmptyFrom >= 0 && e.EmptyTo > e.EmptyFrom && e.EmptyTo <= first && from >= e.EmptyFrom && exchange.BucketStart(e.EmptyFrom, interval) == e.EmptyFrom && exchange.BucketStart(e.EmptyTo, interval) == e.EmptyTo
}
