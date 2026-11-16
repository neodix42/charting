package screener

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"cryptodesk/internal/domain"
)

type publicAlertReuseClosedHistoryKey struct{}

// This records successful history inspection, including genuinely unavailable
// or incomplete bars. It never establishes candle completeness or a signal claim.
// The raw checksum also invalidates it after a Downloader import or a weekly
// re-derivation from changed retained daily bars.
type publicAlertHistoryReadEvidence struct {
	Cutoff    int64  `json:"cutoff"`
	Bars      int    `json:"bars"`
	CheckedAt int64  `json:"checkedAt"`
	Checksum  string `json:"checksum"`
}

func (e *publicAlertHistoryReadEvidence) reusable(end int64, required int, checksum string, now time.Time) bool {
	if e == nil || e.Cutoff != end || end <= 0 || e.Bars < required || e.Bars > 50000 || required <= 0 || e.Checksum != checksum || checksum == "" || e.CheckedAt < end*1000 || e.CheckedAt > now.UnixMilli() {
		return false
	}
	// Near the close, leave publication-race repair available even for manual
	// checks. A later successful inspection is reusable for this closed period.
	return e.CheckedAt-end*1000 >= liveAlertPublicationWindow.Milliseconds() || now.Sub(time.UnixMilli(e.CheckedAt)) < liveAlertRetryDelay
}

func publicAlertBarsChecksum(bars map[int64]domain.Candle) (string, error) {
	ordered := make([]domain.Candle, 0, len(bars))
	for _, bar := range bars {
		ordered = append(ordered, bar)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Time < ordered[j].Time })
	encoded, err := publicAlertRawCSV(ordered, true)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
