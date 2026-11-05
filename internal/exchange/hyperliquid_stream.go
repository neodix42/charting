package exchange

import (
	"cryptodesk/internal/domain"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

func hyperliquidCandleTopic(coin, interval string) string { return "candle." + interval + "." + coin }
func hyperliquidBookTopic(coin string) string             { return "l2Book." + coin }

func hyperliquidSubscription(topic string) (map[string]string, error) {
	if strings.HasPrefix(topic, "l2Book.") {
		return map[string]string{"type": "l2Book", "coin": strings.TrimPrefix(topic, "l2Book.")}, nil
	}
	parts := strings.SplitN(topic, ".", 3)
	if len(parts) != 3 || parts[0] != "candle" || TimeframeSeconds(parts[1]) == 0 || parts[2] == "" {
		return nil, fmt.Errorf("invalid Hyperliquid subscription topic")
	}
	return map[string]string{"type": "candle", "coin": parts[2], "interval": parts[1]}, nil
}

func (h *socketHub) routeHyperliquid(channel string, data json.RawMessage, envelope []byte) {
	switch channel {
	case "subscriptionResponse":
		var ack struct {
			Method       string                                `json:"method"`
			Subscription struct{ Type, Coin, Interval string } `json:"subscription"`
		}
		if err := json.Unmarshal(data, &ack); err != nil {
			h.publish("", wireEvent{state: "reconciling", reason: "Malformed Hyperliquid subscription response"})
			return
		}
		if ack.Method != "subscribe" {
			return
		}
		topic := ""
		if ack.Subscription.Type == "candle" {
			topic = hyperliquidCandleTopic(ack.Subscription.Coin, ack.Subscription.Interval)
		}
		if ack.Subscription.Type == "l2Book" {
			topic = hyperliquidBookTopic(ack.Subscription.Coin)
		}
		if topic == "" {
			return
		}
		h.mu.Lock()
		wanted := len(h.subs[topic]) > 0
		if wanted {
			h.active[topic] = true
		}
		h.mu.Unlock()
		if wanted {
			h.publish(topic, wireEvent{state: "subscribed", reason: "Hyperliquid public subscription acknowledged"})
		}
	case "candle":
		var candle struct {
			Coin     string `json:"s"`
			Interval string `json:"i"`
		}
		if err := json.Unmarshal(data, &candle); err != nil || candle.Coin == "" || TimeframeSeconds(candle.Interval) == 0 {
			h.publish("", wireEvent{state: "reconciling", reason: "Malformed Hyperliquid candle routing payload"})
			return
		}
		h.publish(hyperliquidCandleTopic(candle.Coin, candle.Interval), wireEvent{data: envelope})
	case "l2Book":
		var book struct {
			Coin string `json:"coin"`
		}
		if err := json.Unmarshal(data, &book); err != nil || book.Coin == "" {
			h.publish("", wireEvent{state: "reconciling", reason: "Malformed Hyperliquid order book routing payload"})
			return
		}
		topic := hyperliquidBookTopic(book.Coin)
		quote, err := parseHyperliquidBook(envelope, book.Coin)
		h.mu.Lock()
		if err != nil {
			delete(h.latestBooks, topic)
		} else if previous := h.latestBooks[topic]; len(previous) > 0 {
			var cached struct {
				Data struct {
					Time int64 `json:"time"`
				} `json:"data"`
			}
			if json.Unmarshal(previous, &cached) == nil && cached.Data.Time > quote.Time {
				h.mu.Unlock()
				return // A new interval must receive the same newest quote as existing ones.
			}
		}
		if err == nil && len(h.subs[topic]) > 0 {
			h.latestBooks[topic] = envelope
		}
		h.mu.Unlock()
		h.publish(topic, wireEvent{data: envelope})
	case "error":
		h.publish("", wireEvent{err: &Error{Kind: "unknownInstrument", Message: "Hyperliquid rejected a public subscription"}})
	case "pong":
		h.publish("", wireEvent{state: "heartbeat"})
	}
}

// Candle values are cumulative snapshots, with no close flag or event sequence.
// The current bucket remains provisional and periodic REST reconciliation verifies
// settled buckets. The close millis field is a boundary, never an event watermark.
func parseHyperliquidCandle(data []byte, k domain.FeedKey, now time.Time) ([]domain.Candle, error) {
	if k.Timeframe == "1w" {
		return nil, fmt.Errorf("Hyperliquid native weeks use Thursday boundaries; use daily-derived Monday weeks")
	}
	var e struct {
		Channel string `json:"channel"`
		Data    struct {
			Start    int64           `json:"t"`
			End      int64           `json:"T"`
			Coin     string          `json:"s"`
			Interval string          `json:"i"`
			Open     json.RawMessage `json:"o"`
			High     json.RawMessage `json:"h"`
			Low      json.RawMessage `json:"l"`
			Close    json.RawMessage `json:"c"`
			Volume   json.RawMessage `json:"v"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	if e.Channel != "candle" {
		return nil, nil
	}
	v := e.Data
	d := TimeframeSeconds(k.Timeframe)
	if v.Coin != k.Instrument || v.Interval != k.Timeframe || d == 0 || v.Start%1000 != 0 || v.End != v.Start+d*1000-1 {
		return nil, fmt.Errorf("Hyperliquid candle selection or boundary mismatch")
	}
	closed := v.Start/1000+d <= now.Unix()-2
	quality := "provisional"
	if closed {
		quality = "live"
	}
	c, err := ParseCandle(v.Start/1000, rawString(v.Open), rawString(v.High), rawString(v.Low), rawString(v.Close), rawString(v.Volume), closed, quality, 0)
	if err != nil {
		return nil, err
	}
	bar, err := c.Candle()
	if err != nil {
		return nil, err
	}
	return Normalize([]domain.Candle{bar}, d)
}

// The REST baseline establishes the real Monday open and cumulative base volume.
// A daily snapshot cannot establish exactly how much of its volume the overlapping
// weekly REST response already included. Keep that volume until reconciliation.
func mergeHyperliquidWeek(week, day domain.Candle) domain.Candle {
	week.High = max(week.High, day.High)
	week.Low = min(week.Low, day.Low)
	week.Close = day.Close
	week.Closed = false
	week.Quality = "provisional"
	return week
}

// l2Book is a snapshot feed. Selecting its first level gives an initial best bid
// and ask even for quiet markets; empty sides remain explicitly unavailable.
func parseHyperliquidBook(data []byte, coin string) (*domain.Quote, error) {
	var e struct {
		Channel string `json:"channel"`
		Data    struct {
			Coin   string `json:"coin"`
			Time   int64  `json:"time"`
			Levels [][]struct {
				Price string `json:"px"`
				Size  string `json:"sz"`
			} `json:"levels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	if e.Channel != "l2Book" || e.Data.Coin != coin || e.Data.Time <= 0 || e.Data.Time > 32503680000000 || len(e.Data.Levels) != 2 {
		return nil, fmt.Errorf("invalid Hyperliquid book selection or timestamp")
	}
	quote := &domain.Quote{Time: e.Data.Time}
	for i, side := range e.Data.Levels {
		if len(side) == 0 {
			continue
		}
		if len(side[0].Price) > 100 || len(side[0].Size) > 100 {
			return nil, fmt.Errorf("Hyperliquid book decimal too long")
		}
		price, err := decimal.NewFromString(side[0].Price)
		if err != nil {
			return nil, err
		}
		size, err := decimal.NewFromString(side[0].Size)
		if err != nil {
			return nil, err
		}
		if price.Exponent() < -100 || price.Exponent() > 100 || size.Exponent() < -100 || size.Exponent() > 100 || !price.IsPositive() || !size.IsPositive() {
			return nil, fmt.Errorf("invalid Hyperliquid book price or size")
		}
		n, _ := price.Float64()
		if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
			return nil, fmt.Errorf("Hyperliquid book price outside chart range")
		}
		if i == 0 {
			quote.Bid = &n
		} else {
			quote.Ask = &n
		}
	}
	if quote.Bid != nil && quote.Ask != nil && *quote.Bid > *quote.Ask {
		return nil, fmt.Errorf("crossed Hyperliquid book")
	}
	return quote, nil
}
