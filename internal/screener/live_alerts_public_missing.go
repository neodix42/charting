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
	return fmt.Errorf("%w; requested %s UTC explicitly but the provider returned no closed candle (no stale or synthetic substitution)", errLiveAlertsPendingBar, time.Unix(end-interval, 0).UTC().Format("2006-01-02 15:04"))
}
