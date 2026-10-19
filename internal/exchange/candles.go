package exchange

import (
	"cryptodesk/internal/domain"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

// DecimalCandle preserves provider decimal strings until the checked chart boundary.
type DecimalCandle struct {
	Time                           int64
	Open, High, Low, Close, Volume decimal.Decimal
	Closed                         bool
	Quality                        string
	SourceRevision                 int64
}

func ParseCandle(t int64, o, h, l, c, v string, closed bool, quality string, revision int64) (DecimalCandle, error) {
	out := DecimalCandle{Time: t, Closed: closed, Quality: quality, SourceRevision: revision}
	fields := []*decimal.Decimal{&out.Open, &out.High, &out.Low, &out.Close, &out.Volume}
	for i, s := range []string{o, h, l, c, v} {
		if len(s) > 100 {
			return out, fmt.Errorf("decimal field too long")
		}
		d, err := decimal.NewFromString(s)
		if err != nil {
			return out, fmt.Errorf("invalid provider decimal: %w", err)
		}
		if d.Exponent() < -100 || d.Exponent() > 100 {
			return out, fmt.Errorf("decimal exponent outside supported range")
		}
		*fields[i] = d
	}
	_, err := out.Candle()
	return out, err
}
func (c DecimalCandle) Candle() (domain.Candle, error) {
	out := domain.Candle{Time: c.Time, Closed: c.Closed, Quality: c.Quality, SourceRevision: c.SourceRevision}
	p := []*float64{&out.Open, &out.High, &out.Low, &out.Close, &out.Volume}
	for i, d := range []decimal.Decimal{c.Open, c.High, c.Low, c.Close, c.Volume} {
		f, _ := d.Float64()
		if math.IsNaN(f) || math.IsInf(f, 0) || (!d.IsZero() && f == 0) {
			return out, fmt.Errorf("decimal outside chart numeric range")
		}
		*p[i] = f
	}
	return out, out.Validate()
}
func Normalize(candles []domain.Candle, duration int64) ([]domain.Candle, error) {
	if duration <= 0 {
		return nil, fmt.Errorf("invalid interval")
	}
	m := map[int64]domain.Candle{}
	for _, c := range candles {
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if c.Time%duration != 0 {
			return nil, fmt.Errorf("candle is not on UTC interval boundary")
		}
		prev, ok := m[c.Time]
		if !ok || c.SourceRevision >= prev.SourceRevision {
			if ok && prev.Closed && c.SourceRevision <= prev.SourceRevision {
				c.Closed = true
			}
			m[c.Time] = c
		}
	}
	out := make([]domain.Candle, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out, nil
}

// NativeSeries replaces cumulative snapshots; volume is never added or maxed.
type NativeSeries struct {
	values map[int64]domain.Candle
	limit  int
}

func NewNativeSeries(limit int) *NativeSeries {
	if limit < 2 {
		limit = 512
	}
	return &NativeSeries{values: map[int64]domain.Candle{}, limit: limit}
}
func (s *NativeSeries) Apply(c domain.Candle) bool {
	if c.Validate() != nil {
		return false
	}
	old, ok := s.values[c.Time]
	if ok {
		if c.SourceRevision < old.SourceRevision {
			return false
		}
		if c.SourceRevision == old.SourceRevision && old.Closed && !c.Closed {
			return false
		}
		if old == c {
			return false
		}
		if old.Closed && !c.Closed {
			c.Closed = true
		}
	}
	s.values[c.Time] = c
	if len(s.values) > s.limit {
		var earliest int64 = math.MaxInt64
		for t := range s.values {
			if t < earliest {
				earliest = t
			}
		}
		delete(s.values, earliest)
	}
	return true
}
func (s *NativeSeries) Snapshot() []domain.Candle {
	out := make([]domain.Candle, 0, len(s.values))
	for _, v := range s.values {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

type Trade struct {
	ID          string
	Time        time.Time
	Price, Size decimal.Decimal
}
type tradeBucket struct {
	first, last       Trade
	high, low, volume decimal.Decimal
}

// TradeAggregator's retained identities cover its complete retained bucket horizon.
// Equal timestamps are ordered by numeric trade ID when both IDs are numeric,
// otherwise bytewise ID ordering. Coinbase trade IDs are unique per product.
type TradeAggregator struct {
	Duration      int64
	completeAfter int64
	buckets       map[int64]*tradeBucket
	seen          map[string]int64
	baselines     map[int64]domain.Candle
	MaxTrades     int
	MaxBuckets    int
	Incomplete    bool
}

func NewTradeAggregator(duration, attachedAt int64) *TradeAggregator {
	return &TradeAggregator{Duration: duration, completeAfter: (attachedAt/duration + 1) * duration, buckets: map[int64]*tradeBucket{}, seen: map[string]int64{}, baselines: map[int64]domain.Candle{}, MaxTrades: 100000, MaxBuckets: 512}
}
func tradeBefore(a, b Trade) bool {
	if !a.Time.Equal(b.Time) {
		return a.Time.Before(b.Time)
	}
	ai, ae := strconv.ParseUint(a.ID, 10, 64)
	bi, be := strconv.ParseUint(b.ID, 10, 64)
	if ae == nil && be == nil {
		return ai < bi
	}
	return a.ID < b.ID
}
func (a *TradeAggregator) Discontinuity(at int64) {
	a.completeAfter = (at/a.Duration + 1) * a.Duration
	a.Incomplete = true
	for _, c := range a.baselines {
		c.Quality = "provisional"
		a.baselines[c.Time] = c
	}
}
func (a *TradeAggregator) Apply(t Trade) (domain.Candle, bool, error) {
	if t.ID == "" || len(t.ID) > 128 || t.Time.Unix() < 1230768000 || !t.Price.IsPositive() || t.Size.IsNegative() {
		return domain.Candle{}, false, fmt.Errorf("invalid trade")
	}
	bucket := t.Time.Unix() / a.Duration * a.Duration
	if _, exists := a.seen[t.ID]; exists {
		return domain.Candle{}, false, nil
	}
	if len(a.seen) >= a.MaxTrades {
		a.Discontinuity(t.Time.Unix())
		// Drop a bounded replay cache only after explicitly losing completeness.
		// REST reconciliation remains authoritative until a future full bucket.
		a.seen = map[string]int64{}
		return domain.Candle{}, false, fmt.Errorf("trade deduplication buffer overflow; reconciliation required")
	}
	a.seen[t.ID] = bucket
	b := a.buckets[bucket]
	if b == nil {
		b = &tradeBucket{first: t, last: t, high: t.Price, low: t.Price, volume: t.Size}
		a.buckets[bucket] = b
	} else {
		if tradeBefore(t, b.first) {
			b.first = t
		}
		if tradeBefore(b.last, t) {
			b.last = t
		}
		b.high = decimal.Max(b.high, t.Price)
		b.low = decimal.Min(b.low, t.Price)
		b.volume = b.volume.Add(t.Size)
	}
	c, err := a.candle(bucket)
	a.evict()
	return c, true, err
}
func (a *TradeAggregator) evict() {
	for len(a.buckets) > a.MaxBuckets {
		var earliest int64 = math.MaxInt64
		for t := range a.buckets {
			if t < earliest {
				earliest = t
			}
		}
		delete(a.buckets, earliest)
		delete(a.baselines, earliest)
		for id, t := range a.seen {
			if t == earliest {
				delete(a.seen, id)
			}
		}
	}
}
func (a *TradeAggregator) candle(bucket int64) (domain.Candle, error) {
	b := a.buckets[bucket]
	d := DecimalCandle{Time: bucket, Open: b.first.Price, High: b.high, Low: b.low, Close: b.last.Price, Volume: b.volume, Quality: "live", SourceRevision: b.last.Time.UnixMilli()}
	if bucket < a.completeAfter {
		d.Quality = "provisional"
	}
	out, err := d.Candle()
	if err != nil {
		return out, err
	}
	if baseline, ok := a.baselines[bucket]; ok {
		// REST and trades have no common transaction watermark: volume stays exactly
		// the verified snapshot. Only a trade later than the snapshot may freshen prices.
		out.Open = baseline.Open
		out.High = math.Max(baseline.High, out.High)
		out.Low = math.Min(baseline.Low, out.Low)
		if out.SourceRevision <= baseline.SourceRevision {
			out.Close = baseline.Close
		}
		out.Volume = baseline.Volume
		out.Closed = baseline.Closed
		out.Quality = "provisional"
	}
	return out, out.Validate()
}
func (a *TradeAggregator) Reconcile(c domain.Candle) domain.Candle {
	// Reconciliation replaces an uncertain snapshot wholesale, including a lower
	// corrected volume. It never adds buffered trades to that volume.
	a.baselines[c.Time] = c
	if c.Closed {
		delete(a.buckets, c.Time)
	}
	if len(a.baselines) > a.MaxBuckets {
		var earliest int64 = math.MaxInt64
		for t := range a.baselines {
			if t < earliest {
				earliest = t
			}
		}
		delete(a.baselines, earliest)
	}
	return c
}
