package exchange

import (
	"context"
	"cryptodesk/internal/domain"
	"fmt"
	"net/url"
	"sort"
	"strconv"

	"github.com/shopspring/decimal"
)

// Coinbase's public candle API ends at ONE_DAY. Fetch actual daily spot candles
// in bounded pages, then derive Monday-based weeks without inventing a provider
// granularity or manufacturing candles for missing days.
func (a *publicAdapter) coinbaseWeeklyHistory(ctx context.Context, k domain.FeedKey, from, to, now int64) ([]domain.Candle, error) {
	day, week := TimeframeSeconds("1d"), TimeframeSeconds("1w")
	first := BucketStart(from, week)
	if first < from {
		first += week // History returns candle opening times in [from, to).
	}
	if first >= to || first > now {
		return []domain.Candle{}, nil
	}
	// A historical viewport ending midweek still receives that complete week's
	// candle, just as a native weekly endpoint does. The current week stays open.
	end := min(BucketStart(to-1, week)+week, now+1)
	daily := map[int64]DecimalCandle{}
	for cursor := first; cursor < end; {
		pageEnd := min(end, cursor+350*day)
		q := url.Values{
			"start":       {strconv.FormatInt(cursor, 10)},
			"end":         {strconv.FormatInt(pageEnd-1, 10)},
			"granularity": {"ONE_DAY"},
			"limit":       {"350"},
		}
		var payload struct {
			Candles []struct{ Start, Open, High, Low, Close, Volume string } `json:"candles"`
		}
		if err := a.get(ctx, "/api/v3/brokerage/market/products/"+url.PathEscape(k.Instrument)+"/candles", q, &payload); err != nil {
			return nil, err
		}
		for _, row := range payload.Candles {
			timestamp, err := strconv.ParseInt(row.Start, 10, 64)
			if err != nil {
				return nil, err
			}
			if BucketStart(timestamp, day) != timestamp {
				return nil, fmt.Errorf("Coinbase daily candle is not on a UTC boundary")
			}
			if timestamp < cursor || timestamp >= pageEnd {
				continue // Some provider versions include the end boundary.
			}
			c, err := ParseCandle(timestamp, row.Open, row.High, row.Low, row.Close, row.Volume, timestamp+day <= now-2, "authoritative", 0)
			if err != nil {
				return nil, err
			}
			daily[timestamp] = c // Duplicate or overlapping days never add volume twice.
		}
		cursor = pageEnd // Empty/no-trade pages still advance the bounded request.
	}
	ordered := make([]DecimalCandle, 0, len(daily))
	for _, c := range daily {
		ordered = append(ordered, c)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Time < ordered[j].Time })
	var out []domain.Candle
	for i := 0; i < len(ordered); {
		start := BucketStart(ordered[i].Time, week)
		c := ordered[i]
		c.Time, c.Closed = start, start+week <= now-2
		count, complete := 1, ordered[i].Closed
		j := i + 1
		for j < len(ordered) && BucketStart(ordered[j].Time, week) == start {
			v := ordered[j]
			c.High = decimal.Max(c.High, v.High)
			c.Low = decimal.Min(c.Low, v.Low)
			c.Close = v.Close
			c.Volume = c.Volume.Add(v.Volume)
			complete = complete && v.Closed
			count++
			j++
		}
		if !c.Closed || !complete || count != 7 {
			c.Quality = "provisional"
		}
		rendered, err := c.Candle()
		if err != nil {
			return nil, err
		}
		out = append(out, rendered)
		i = j
	}
	return Normalize(out, week)
}
