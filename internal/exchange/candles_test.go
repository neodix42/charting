package exchange

import (
	"cryptodesk/internal/domain"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

type fixtureCandle struct {
	Start                  int64 `json:"startUnixSec"`
	Open, High, Low, Close string
	Volume                 string `json:"baseVolume"`
	Closed                 bool
	Revision               int64 `json:"sourceRevision"`
}

func (f fixtureCandle) candle(t *testing.T) domain.Candle {
	t.Helper()
	d, e := ParseCandle(f.Start, f.Open, f.High, f.Low, f.Close, f.Volume, f.Closed, "live", f.Revision)
	if e != nil {
		t.Fatal(e)
	}
	v, e := d.Candle()
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func loadFixture(t *testing.T, path string, dst any) {
	t.Helper()
	data, e := os.ReadFile("../../fixtures/" + path)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(data, dst); e != nil {
		t.Fatal(e)
	}
}
func TestNativeFixtureCumulativeOrdering(t *testing.T) {
	var f struct{ Events, Expected []fixtureCandle }
	loadFixture(t, "native-candles.json", &f)
	series := NewNativeSeries(512)
	for _, c := range f.Events {
		series.Apply(c.candle(t))
	}
	var expected []domain.Candle
	for _, c := range f.Expected {
		expected = append(expected, c.candle(t))
	}
	if !reflect.DeepEqual(series.Snapshot(), expected) {
		t.Fatalf("got %#v want %#v", series.Snapshot(), expected)
	}
	// Documented later authoritative correction may reduce volume.
	correction := expected[0]
	correction.Volume = 4
	correction.SourceRevision = 5
	if !series.Apply(correction) || series.Snapshot()[0].Volume != 4 {
		t.Fatal("correction was maxed or ignored")
	}
}
func TestHistoryFixtureReversedOverlap(t *testing.T) {
	var f struct {
		Pages    [][]fixtureCandle
		Expected []fixtureCandle
	}
	loadFixture(t, "history-pages.json", &f)
	var all []domain.Candle
	for _, page := range f.Pages {
		for _, c := range page {
			all = append(all, c.candle(t))
		}
	}
	got, e := Normalize(all, 60)
	if e != nil {
		t.Fatal(e)
	}
	var want []domain.Candle
	for _, c := range f.Expected {
		want = append(want, c.candle(t))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

type fixtureTrade struct {
	ID          string
	TimeSec     int64
	Price, Size string
}

func (f fixtureTrade) trade() Trade {
	return Trade{ID: f.ID, Time: time.Unix(f.TimeSec, 0), Price: decimal.RequireFromString(f.Price), Size: decimal.RequireFromString(f.Size)}
}
func TestTradeFixtureExactDedupOutOfOrderLate(t *testing.T) {
	var f struct {
		Events                                []fixtureTrade
		LateEvent                             fixtureTrade
		ExpectedBeforeLate, ExpectedAfterLate []fixtureCandle
		ExpectedFiveMinuteAfterLate           fixtureCandle
	}
	loadFixture(t, "trades.json", &f)
	for _, duration := range []int64{60, 300} {
		a := NewTradeAggregator(duration, 1792367940)
		for _, e := range f.Events {
			if _, _, err := a.Apply(e.trade()); err != nil {
				t.Fatal(err)
			}
		}
		if duration == 60 {
			assertTradeBuckets(t, a, f.ExpectedBeforeLate)
		}
		if _, _, e := a.Apply(f.LateEvent.trade()); e != nil {
			t.Fatal(e)
		}
		if duration == 60 {
			assertTradeBuckets(t, a, f.ExpectedAfterLate)
		} else {
			assertTradeBuckets(t, a, []fixtureCandle{f.ExpectedFiveMinuteAfterLate})
		}
	}
	// Decimal additions stay exact inside the production aggregator.
	a := NewTradeAggregator(60, 1792367940)
	for i, size := range []string{"0.1", "0.2"} {
		_, _, e := a.Apply(Trade{ID: string(rune('a' + i)), Time: time.Unix(1792368000+int64(i), 0), Price: decimal.NewFromInt(100), Size: decimal.RequireFromString(size)})
		if e != nil {
			t.Fatal(e)
		}
	}
	if !a.buckets[1792368000].volume.Equal(decimal.RequireFromString("0.3")) {
		t.Fatal("inexact decimal aggregation")
	}
}
func assertTradeBuckets(t *testing.T, a *TradeAggregator, want []fixtureCandle) {
	t.Helper()
	if len(a.buckets) != len(want) {
		t.Fatalf("bucket count %d want %d", len(a.buckets), len(want))
	}
	for _, expected := range want {
		v, e := a.candle(expected.Start)
		if e != nil {
			t.Fatal(e)
		}
		c := expected.candle(t)
		if v.Time != c.Time || v.Open != c.Open || v.High != c.High || v.Low != c.Low || v.Close != c.Close || v.Volume != c.Volume {
			t.Fatalf("got %+v want %+v", v, c)
		}
	}
}
func TestAllIntervalUTCBoundaries(t *testing.T) {
	for _, interval := range domain.Intervals {
		t.Run(interval, func(t *testing.T) {
			d := TimeframeSeconds(interval)
			a := NewTradeAggregator(d, 1792368000-d)
			for i, sec := range []int64{1792368000 + d - 1, 1792368000 + d} {
				_, _, e := a.Apply(Trade{ID: string(rune('a' + i)), Time: time.Unix(sec, 999999999), Price: decimal.NewFromInt(100), Size: decimal.NewFromInt(1)})
				if e != nil {
					t.Fatal(e)
				}
			}
			if len(a.buckets) != 2 {
				t.Fatalf("boundary did not create 2 buckets: %#v", a.buckets)
			}
			for b := range a.buckets {
				if b%d != 0 {
					t.Fatal("not UTC")
				}
			}
		})
	}
}
func TestOverlapAndGapNeverSumRESTVolume(t *testing.T) {
	a := NewTradeAggregator(60, 1792368010)
	baseline := domain.Candle{Time: 1792368000, Open: 100, High: 102, Low: 99, Close: 101, Volume: 10, Quality: "provisional", SourceRevision: 1792368020000}
	a.Reconcile(baseline)
	for i, size := range []int64{2, 3} {
		c, _, e := a.Apply(Trade{ID: string(rune('a' + i)), Time: time.Unix(1792368021+int64(i), 0), Price: decimal.NewFromInt(103 + int64(i)), Size: decimal.NewFromInt(size)})
		if e != nil {
			t.Fatal(e)
		}
		if c.Volume != 10 || c.Quality != "provisional" {
			t.Fatalf("REST overlap incorrectly summed: %+v", c)
		}
	}
	a.Discontinuity(1792368060)
	c, _, _ := a.Apply(Trade{ID: "next", Time: time.Unix(1792368061, 0), Price: decimal.NewFromInt(104), Size: decimal.NewFromInt(1)})
	if c.Quality != "provisional" {
		t.Fatal("gap claimed complete")
	}
	a.MaxTrades = len(a.seen)
	if _, _, e := a.Apply(Trade{ID: "overflow", Time: time.Unix(1792368062, 0), Price: decimal.NewFromInt(104), Size: decimal.NewFromInt(1)}); e == nil || !a.Incomplete {
		t.Fatal("overflow not surfaced")
	}
}
func TestInvalidCandlesRejected(t *testing.T) {
	for _, args := range [][5]string{{"NaN", "1", "1", "1", "1"}, {"1", "1", "2", "1", "0"}, {"1", "1", "1", "1", "-1"}, {"1e1000000000", "1", "1", "1", "1"}} {
		if _, e := ParseCandle(1792368000, args[0], args[1], args[2], args[3], args[4], false, "live", 0); e == nil {
			t.Fatalf("accepted invalid %#v", args)
		}
	}
	if _, e := ParseCandle(1792368000000, "1", "1", "1", "1", "0", false, "live", 0); e == nil {
		t.Fatal("accepted milliseconds")
	}
}
