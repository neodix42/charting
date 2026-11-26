package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"cryptodesk/internal/domain"
)

// Coinbase includes its end boundary and returns the latest `limit` buckets.
// The synthetic endpoint deliberately models both behaviours, including the
// extra end candle that previously displaced a Comparison baseline.
func coinbaseBoundsServer(t *testing.T, step int64, visit func(int64, int64, int)) (*publicAdapter, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
		if err != nil {
			t.Error(err)
		}
		last, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
		if err != nil {
			t.Error(err)
		}
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil {
			t.Error(err)
		}
		if limit < 1 || limit > 350 {
			t.Errorf("Coinbase page exceeds its limit: %d", limit)
		}
		if visit != nil {
			visit(first, last, limit)
		}
		rows := []map[string]string{}
		for at := BucketStart(last, step); at >= first && len(rows) < limit; at -= step {
			rows = append(rows, map[string]string{"start": fmt.Sprint(at), "open": "100", "high": "101", "low": "99", "close": "100", "volume": "1"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"candles": rows})
	}))
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	a.base = server.URL
	return a, server.Close
}

func coinbaseBoundsKey() domain.FeedKey {
	return domain.FeedKey{Exchange: "coinbase", Market: "spot", Instrument: "TON-USD", Timeframe: "1d"}
}

func TestCoinbaseHalfOpenHistoryPreserves26WeekBaseline(t *testing.T) {
	const start, step = int64(1704067200), int64(86400)
	end := start + 183*step
	requests := 0
	a, closeServer := coinbaseBoundsServer(t, step, func(first, last int64, limit int) {
		requests++
		if first != start || last != end-1 || limit != 183 {
			t.Errorf("half-open request changed: %d %d limit%d", first, last, limit)
		}
	})
	defer closeServer()
	bars, err := a.History(context.Background(), coinbaseBoundsKey(), start, end, 183)
	if err != nil || len(bars) != 183 || bars[0].Time != start || bars[len(bars)-1].Time != end-step || requests != 1 {
		t.Fatalf("common baseline displaced by inclusive end candle: count%d requests%d err%v", len(bars), requests, err)
	}
	for i, bar := range bars {
		if bar.Time != start+int64(i)*step || !bar.Closed || bar.Quality != "authoritative" {
			t.Fatalf("invalid closed daily candle: %+v", bar)
		}
	}
}

func TestCoinbaseNativeHistoryPages729And1000Candles(t *testing.T) {
	for _, count := range []int{729, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			const start, step = int64(1577836800), int64(86400)
			end := start + int64(count)*step
			requests, cursor := 0, start
			a, closeServer := coinbaseBoundsServer(t, step, func(first, last int64, limit int) {
				requests++
				if first != cursor || last >= end || last-first >= 350*step {
					t.Errorf("unbounded or discontinuous page: first%d last%d cursor%d", first, last, cursor)
				}
				cursor = last + 1
			})
			defer closeServer()
			bars, err := a.History(context.Background(), coinbaseBoundsKey(), start, end, count)
			if err != nil || len(bars) != count || requests != (count+349)/350 || cursor != end {
				t.Fatalf("native history truncated or unbounded: count%d want%d requests%d cursor%d end%d err%v", len(bars), count, requests, cursor, end, err)
			}
			for i, bar := range bars {
				if bar.Time != start+int64(i)*step || bar.Volume != 1 || !bar.Closed {
					t.Fatalf("wrong native page ordering/data: %d %+v", i, bar)
				}
			}
		})
	}
}

func TestCoinbaseHistoryFromZeroBoundsWorkToRequestedLatestCandles(t *testing.T) {
	const step = int64(86400)
	const end = int64(1704067200)
	const count = 729
	first, requests := int64(-1), 0
	a, closeServer := coinbaseBoundsServer(t, step, func(from, to int64, limit int) {
		if requests == 0 {
			first = from
		}
		requests++
		if from < end-count*step || to >= end {
			t.Error("latest history escaped requested bound")
		}
	})
	defer closeServer()
	bars, err := a.History(context.Background(), coinbaseBoundsKey(), 0, end, count)
	if err != nil || len(bars) != count || first != end-count*step || requests != 3 {
		t.Fatalf("from=0 expanded or truncated requested work: count%d first%d requests%d err%v", len(bars), first, requests, err)
	}
}

func TestCoinbaseHistoryPagesPreserveEmptyRangesAndDeduplicateOverlap(t *testing.T) {
	const start, step = int64(1577836800), int64(86400)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		first, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
		last, _ := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
		rows := []map[string]string{}
		row := func(at int64) map[string]string {
			return map[string]string{"start": fmt.Sprint(at), "open": "100", "high": "101", "low": "99", "close": "100", "volume": "1"}
		}
		if requests > 1 {
			rows = append(rows, row(first), row(first), row(last+1))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"candles": rows})
	}))
	defer server.Close()
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	a.base = server.URL
	bars, err := a.History(context.Background(), coinbaseBoundsKey(), start, start+729*step, 729)
	if err != nil || requests != 3 || len(bars) != 2 {
		t.Fatalf("empty pages stalled or gaps were filled: bars%d requests%d err%v", len(bars), requests, err)
	}
	for i, at := range []int64{start + 350*step, start + 700*step} {
		if bars[i].Time != at || bars[i].Volume != 1 {
			t.Fatalf("overlap changed native candle or volume: %+v", bars)
		}
	}
}

func TestCoinbasePagedHistoryHonorsCancellationBeforeAnotherPage(t *testing.T) {
	const start, step = int64(1577836800), int64(86400)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		first, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
		_ = json.NewEncoder(w).Encode(map[string]any{"candles": []map[string]string{{"start": fmt.Sprint(first), "open": "100", "high": "101", "low": "99", "close": "100", "volume": "1"}}})
		cancel()
	}))
	defer server.Close()
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	a.base = server.URL
	bars, err := a.History(ctx, coinbaseBoundsKey(), start, start+729*step, 729)
	if !errors.Is(err, context.Canceled) || len(bars) != 0 || requests.Load() != 1 {
		t.Fatalf("cancelled request retained work or published partial data: bars%d requests%d err%v", len(bars), requests.Load(), err)
	}
}
