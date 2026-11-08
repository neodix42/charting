package exchange

import (
	"context"
	"os"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func TestHyperliquidLiveFullDailyHistory(t *testing.T) {
	if os.Getenv("CRYPTODESK_LIVE_HYPERLIQUID_DOWNLOAD") != "1" {
		t.Skip("explicit opt-in only: one real public BTC D1 snapshot; no profile writes")
	}
	a, err := New("hyperliquid")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1d"}
	bars, err := a.History(ctx, key, 0, time.Now().Unix()+1, 5000)
	if err != nil || len(bars) == 0 || len(bars) > 5000 {
		t.Fatalf("full real BTC daily snapshot: count=%d error=%v", len(bars), err)
	}
	for i, bar := range bars {
		if err := bar.Validate(); err != nil || bar.Time%86400 != 0 || (i > 0 && bars[i-1].Time >= bar.Time) {
			t.Fatalf("invalid real daily candle %d: %+v error=%v", i, bar, err)
		}
	}
	t.Logf("real BTC perpetual full daily snapshot: %d bars, earliest=%s latest=%s", len(bars), time.Unix(bars[0].Time, 0).UTC().Format(time.RFC3339), time.Unix(bars[len(bars)-1].Time, 0).UTC().Format(time.RFC3339))
}
