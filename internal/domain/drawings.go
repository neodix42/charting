package domain

import (
	"fmt"
	"strings"
)

const MaxDrawings = 100

// Drawing stores price/time anchors independently of chart dimensions and
// timeframe. Its instrument scope prevents marks from appearing on another feed
// after a selection change. The whole chart's drawings survive that change.
type Drawing struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Exchange   string         `json:"exchange"`
	Market     string         `json:"market"`
	Instrument string         `json:"instrument"`
	Points     []DrawingPoint `json:"points"`
}

type DrawingPoint struct {
	Time  int64   `json:"time"`
	Price float64 `json:"price"`
}

func validateDrawing(d Drawing) error {
	count := 1
	switch d.Type {
	case "arrowUp", "arrowDown":
	case "horizontal":
		// Legacy levels span the chart with one anchor. Resizing a level stores
		// two endpoints without changing its type or the workspace version.
		if len(d.Points) != 1 && len(d.Points) != 2 {
			return fmt.Errorf("horizontal drawing requires one point or two endpoints")
		}
		count = len(d.Points)
	case "trendline":
		count = 2
	default:
		return fmt.Errorf("unsupported drawing type %q", d.Type)
	}
	if !member(d.Exchange, "binance", "bybit", "coinbase") || !supportedMarket(d.Exchange, d.Market) {
		return fmt.Errorf("unsupported drawing exchange or market")
	}
	if !text(d.Instrument, 128) || strings.Contains(d.Instrument, "://") {
		return fmt.Errorf("drawing instrument must be nonblank, safe and at most 128 characters")
	}
	if len(d.Points) != count {
		return fmt.Errorf("%s drawing requires exactly %d points", d.Type, count)
	}
	for _, point := range d.Points {
		if point.Time < 1 || point.Time > 253402300799 {
			return fmt.Errorf("drawing time must be UTC Unix seconds in the supported range")
		}
		if !finite(point.Price) || point.Price <= 0 {
			return fmt.Errorf("drawing price must be finite and positive")
		}
	}
	if d.Type == "horizontal" && count == 2 &&
		(d.Points[0].Time == d.Points[1].Time || d.Points[0].Price != d.Points[1].Price) {
		return fmt.Errorf("horizontal drawing requires distinct endpoint times at the same price")
	}
	if d.Type == "trendline" && d.Points[0] == d.Points[1] {
		return fmt.Errorf("trendline drawing requires two distinct points")
	}
	return nil
}
