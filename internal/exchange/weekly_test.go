package exchange

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func utc(t *testing.T, value string) int64 {
	t.Helper()
	v, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return v.Unix()
}

func TestWeeklyBoundariesMondayAndYearRollover(t *testing.T) {
	week := TimeframeSeconds("1w")
	for _, tc := range []struct{ at, want string }{
		{"2024-12-29T23:59:59Z", "2024-12-23T00:00:00Z"},
		{"2024-12-30T00:00:00Z", "2024-12-30T00:00:00Z"},
		{"2025-01-01T12:00:00Z", "2024-12-30T00:00:00Z"},
		{"2025-01-05T23:59:59Z", "2024-12-30T00:00:00Z"},
		{"2025-01-06T00:00:00Z", "2025-01-06T00:00:00Z"},
		{"1970-01-01T00:00:00Z", "1969-12-29T00:00:00Z"},
	} {
		if got := BucketStart(utc(t, tc.at), week); got != utc(t, tc.want) {
			t.Errorf("%s: got %s want %s", tc.at, time.Unix(got, 0).UTC(), tc.want)
		}
	}
	monday := utc(t, "2024-12-30T00:00:00Z")
	c := domain.Candle{Time: monday, Open: 100, High: 100, Low: 100, Close: 100, Volume: 1}
	if _, err := Normalize([]domain.Candle{c}, week); err != nil {
		t.Fatal(err)
	}
	c.Time = monday + 3*86400 // A Unix-epoch-aligned Thursday is not a weekly opening.
	if _, err := Normalize([]domain.Candle{c}, week); err == nil {
		t.Fatal("accepted a Thursday weekly candle")
	}
}

func TestCoinbaseWeeklyDerivedDailyOHLCVAndPartialCoverage(t *testing.T) {
	start := utc(t, "2024-12-23T00:00:00Z")
	now := utc(t, "2025-01-10T12:00:00Z")
	day := TimeframeSeconds("1d")
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		q := r.URL.Query()
		if q.Get("granularity") != "ONE_DAY" || q.Get("limit") != "350" {
			t.Errorf("not bounded documented daily history: %v", q)
		}
		from, _ := strconv.ParseInt(q.Get("start"), 10, 64)
		to, _ := strconv.ParseInt(q.Get("end"), 10, 64)
		var rows []map[string]string
		for at := from; at <= to; at += day {
			if at == start+2*day {
				continue // A missing day must remain explicitly provisional.
			}
			i := (at - start) / day
			rows = append(rows, map[string]string{"start": strconv.FormatInt(at, 10), "open": fmt.Sprint(100 + i), "high": fmt.Sprint(102 + i), "low": fmt.Sprint(99 + i), "close": fmt.Sprint(101 + i), "volume": "0.1"})
		}
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
		rows = append(rows, rows[0]) // Duplicate rows cannot double the base volume.
		json.NewEncoder(w).Encode(map[string]any{"candles": rows})
	}))
	defer server.Close()
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	a.base = server.URL
	k := domain.FeedKey{Exchange: "coinbase", Market: "spot", Instrument: "BTC-USD", Timeframe: "1w"}
	got, err := a.coinbaseWeeklyHistory(context.Background(), k, start, now+1, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || hits != 1 {
		t.Fatalf("weekly count %d requests %d", len(got), hits)
	}
	if c := got[0]; c.Time != start || c.Open != 100 || c.High != 108 || c.Low != 99 || c.Close != 107 || c.Volume != 0.6 || !c.Closed || c.Quality != "provisional" {
		t.Fatalf("missing day lost or OHLCV incorrect: %+v", c)
	}
	if c := got[1]; c.Time != start+7*day || c.Open != 107 || c.Close != 114 || c.Volume != 0.7 || !c.Closed || c.Quality != "authoritative" {
		t.Fatalf("complete week incorrect: %+v", c)
	}
	if c := got[2]; c.Time != start+14*day || c.Open != 114 || c.Close != 119 || c.Volume != 0.5 || c.Closed || c.Quality != "provisional" {
		t.Fatalf("current week must be partial: %+v", c)
	}
}

func TestCoinbaseWeeklyDailyPaginationAndHistoricalMidweekEnd(t *testing.T) {
	start := utc(t, "2023-01-02T00:00:00Z")
	day, week := TimeframeSeconds("1d"), TimeframeSeconds("1w")
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		q := r.URL.Query()
		from, _ := strconv.ParseInt(q.Get("start"), 10, 64)
		to, _ := strconv.ParseInt(q.Get("end"), 10, 64)
		if q.Get("granularity") != "ONE_DAY" || to-from >= 350*day {
			t.Errorf("provider received unsupported weekly or oversized request: %v", q)
		}
		var rows []map[string]string
		for at := from; at <= to; at += day {
			rows = append(rows, map[string]string{"start": fmt.Sprint(at), "open": "100", "high": "102", "low": "99", "close": "101", "volume": "0.1"})
		}
		json.NewEncoder(w).Encode(map[string]any{"candles": rows})
	}))
	defer server.Close()
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	a.base = server.URL
	k := domain.FeedKey{Exchange: "coinbase", Market: "spot", Instrument: "BTC-USD", Timeframe: "1w"}
	got, err := a.History(context.Background(), k, start, start+51*week+2*day, 1000)
	if err != nil || len(got) != 52 || hits != 2 {
		t.Fatalf("bounded daily pagination: bars%d hits%d err%v", len(got), hits, err)
	}
	for i, c := range got {
		if c.Time != start+int64(i)*week || c.Volume != 0.7 || c.Quality != "authoritative" || !c.Closed {
			t.Fatalf("week %d incomplete or misaligned: %+v", i, c)
		}
	}
}

func TestWeeklyTradesBoundaryDedupAndRESTReconciliation(t *testing.T) {
	monday := utc(t, "2024-12-30T00:00:00Z")
	week := TimeframeSeconds("1w")
	a := NewTradeAggregator(week, monday-30)
	baseline := domain.Candle{Time: monday, Open: 100, High: 103, Low: 99, Close: 102, Volume: 7, Quality: "provisional", SourceRevision: (monday + 10) * 1000}
	a.Reconcile(baseline)
	for _, trade := range []Trade{
		{ID: "last-old", Time: time.Unix(monday-1, 0), Price: decimal.NewFromInt(98), Size: decimal.RequireFromString("0.1")},
		{ID: "new", Time: time.Unix(monday+20, 0), Price: decimal.NewFromInt(104), Size: decimal.RequireFromString("0.2")},
		{ID: "new", Time: time.Unix(monday+20, 0), Price: decimal.NewFromInt(104), Size: decimal.RequireFromString("0.2")},
	} {
		if _, _, err := a.Apply(trade); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.buckets) != 2 || len(a.seen) != 2 {
		t.Fatal("week rollover or deduplication failed")
	}
	c, err := a.candle(monday)
	if err != nil || c.Open != 100 || c.Close != 104 || c.Volume != 7 || c.Quality != "provisional" {
		t.Fatalf("REST overlap volume was double counted: %+v %v", c, err)
	}
	a.Discontinuity(monday + 2*86400)
	if a.completeAfter != monday+week {
		t.Fatal("gap completeness did not advance to next Monday")
	}
	baseline.Volume = 6 // A corrected daily-derived snapshot may lower weekly volume.
	if got := a.Reconcile(baseline); got.Volume != 6 {
		t.Fatal("weekly authoritative correction ignored")
	}
	if c, _ := a.candle(monday); c.Volume != 6 {
		t.Fatal("weekly trades retained obsolete baseline")
	}
}
