package screener

import (
	"context"
	"fmt"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
)

// This is a successful read of one still-missing closed candle, not proof that
// it can never trade. It belongs to the checksum-verified raw series identity;
// a new cutoff or an imported authoritative candle bypasses it immediately.
type publicAlertMissingCandleEvidence struct {
	Cutoff    int64 `json:"cutoff"`
	CheckedAt int64 `json:"checkedAt"`
}

type publicAlertClockKey struct{}

func publicAlertSeriesNow(ctx context.Context) time.Time {
	if now, ok := ctx.Value(publicAlertClockKey{}).(func() time.Time); ok && now != nil {
		return now()
	}
	return time.Now()
}

func (e *publicAlertMissingCandleEvidence) valid(end, interval int64, now time.Time) bool {
	return e != nil && interval > 0 && e.Cutoff == end && end > 0 && exchange.BucketStart(end, interval) == end && e.CheckedAt > 0 && e.CheckedAt <= now.UnixMilli()
}

func publicAlertMissingCandleCooldown(source, market string, end, interval int64, bars map[int64]domain.Candle) time.Duration {
	if source == "hyperliquid" && market == "spot" && interval >= 86400 {
		latest := int64(0)
		hasClosed := false
		for ts, bar := range bars {
			if bar.Closed && ts < end {
				if !hasClosed || ts > latest {
					latest = ts
				}
				hasClosed = true
			}
		}
		if !hasClosed || latest <= end-3*interval {
			return 30 * time.Minute
		}
	}
	return 30 * time.Second
}

func publicAlertMissingCandleError(end, interval int64) error {
	return &publicAlertMissingCandleIssue{From: end - interval, To: end}
}

// A successful source read did not publish this requested closing candle. Keep
// this separate from transport failures, undefined warm-up and provisional data
// while preserving the existing pending-bar retry classification.
type publicAlertMissingCandleIssue struct{ From, To int64 }

func (e *publicAlertMissingCandleIssue) Error() string {
	return fmt.Sprintf("%s; requested %s UTC explicitly but the provider returned no closed candle (no stale or synthetic substitution)", errLiveAlertsPendingBar, time.Unix(e.From, 0).UTC().Format("2006-01-02 15:04"))
}

func (e *publicAlertMissingCandleIssue) Unwrap() error { return errLiveAlertsPendingBar }

// Only explicit missing-candle leaves qualify. A joined timeout, malformed file
// or insufficient warm-up must retain its distinct status instead of being
// presented as a successful empty provider response.
func publicAlertMissingClosedCandle(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*publicAlertMissingCandleIssue); ok {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !publicAlertMissingClosedCandle(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return publicAlertMissingClosedCandle(wrapped.Unwrap())
	}
	return false
}
