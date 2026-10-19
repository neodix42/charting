package exchange

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// ParseNative decodes documented cumulative kline envelopes, including Binance's
// combined wrapper. Synthetic documentation-shaped fixtures test this exact parser.
func ParseNative(provider string, data []byte, k domain.FeedKey) ([]domain.Candle, error) {
	var out []domain.Candle
	switch provider {
	case "binance":
		var e struct {
			Data      json.RawMessage `json:"data"`
			Event     string          `json:"e"`
			EventTime int64           `json:"E"`
			Symbol    string          `json:"s"`
			K         struct {
				Start       int64           `json:"t"`
				End         int64           `json:"T"`
				LastTrade   json.RawMessage `json:"L"`
				TakerVolume json.RawMessage `json:"V"`
				Interval    string          `json:"i"`
				Open        string          `json:"o"`
				High        string          `json:"h"`
				Low         string          `json:"l"`
				Close       string          `json:"c"`
				Volume      string          `json:"v"`
				Closed      bool            `json:"x"`
			} `json:"k"`
		}
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		if len(e.Data) > 0 {
			return ParseNative(provider, e.Data, k)
		}
		if e.Event != "kline" {
			return nil, nil
		}
		if e.Symbol != k.Instrument || e.K.Interval != k.Timeframe {
			return nil, fmt.Errorf("kline selection mismatch")
		}
		if e.K.Start%1000 != 0 {
			return nil, fmt.Errorf("invalid millisecond kline start")
		}
		c, err := ParseCandle(e.K.Start/1000, e.K.Open, e.K.High, e.K.Low, e.K.Close, e.K.Volume, e.K.Closed, "live", e.EventTime)
		if err != nil {
			return nil, err
		}
		v, err := c.Candle()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	case "bybit":
		var e struct {
			Topic     string `json:"topic"`
			Timestamp int64  `json:"ts"`
			Data      []struct {
				Start                          int64  `json:"start"`
				Interval                       string `json:"interval"`
				Open, High, Low, Close, Volume string
				Confirm                        bool  `json:"confirm"`
				Timestamp                      int64 `json:"timestamp"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		if e.Topic != "kline."+bybitIntervals[k.Timeframe]+"."+k.Instrument {
			return nil, fmt.Errorf("kline selection mismatch")
		}
		for _, v := range e.Data {
			if v.Interval != bybitIntervals[k.Timeframe] || v.Start%1000 != 0 {
				return nil, fmt.Errorf("kline interval or timestamp mismatch")
			}
			revision := e.Timestamp
			if revision == 0 {
				revision = v.Timestamp
			}
			c, err := ParseCandle(v.Start/1000, v.Open, v.High, v.Low, v.Close, v.Volume, v.Confirm, "live", revision)
			if err != nil {
				return nil, err
			}
			rendered, err := c.Candle()
			if err != nil {
				return nil, err
			}
			out = append(out, rendered)
		}
	default:
		return nil, fmt.Errorf("provider does not expose arbitrary native intervals")
	}
	return Normalize(out, TimeframeSeconds(k.Timeframe))
}
func ParseTrades(data []byte, product string) ([]Trade, error) {
	var e struct {
		Channel string `json:"channel"`
		Events  []struct {
			Type   string `json:"type"`
			Trades []struct {
				ID                string `json:"trade_id"`
				Product           string `json:"product_id"`
				Price, Size, Time string
			} `json:"trades"`
		} `json:"events"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	if e.Channel != "market_trades" {
		return nil, nil
	}
	var out []Trade
	for _, event := range e.Events {
		for _, v := range event.Trades {
			if v.Product != product {
				continue
			}
			if len(v.Price) > 100 || len(v.Size) > 100 {
				return nil, fmt.Errorf("trade decimal field too long")
			}
			p, err := decimal.NewFromString(v.Price)
			if err != nil {
				return nil, err
			}
			size, err := decimal.NewFromString(v.Size)
			if err != nil {
				return nil, err
			}
			if p.Exponent() < -100 || p.Exponent() > 100 || size.Exponent() < -100 || size.Exponent() > 100 {
				return nil, fmt.Errorf("trade decimal exponent outside supported range")
			}
			t, err := time.Parse(time.RFC3339Nano, v.Time)
			if err != nil {
				return nil, err
			}
			out = append(out, Trade{ID: v.ID, Time: t, Price: p, Size: size})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return tradeBefore(out[i], out[j]) })
	return out, nil
}

func (a *publicAdapter) Stream(ctx context.Context, k domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
	if err := a.validate(k); err != nil {
		return err
	}
	topic := k.Instrument
	if a.provider == "binance" {
		topic = lower(k.Instrument) + "@kline_" + k.Timeframe
	} else if a.provider == "bybit" {
		topic = "kline." + bybitIntervals[k.Timeframe] + "." + k.Instrument
	}
	messages, unsubscribe := a.hub.subscribe(topic)
	defer unsubscribe()
	duration := TimeframeSeconds(k.Timeframe)
	localRevision := time.Now().UnixMilli()
	send := func(bars []domain.Candle, h domain.Health) {
		if a.provider == "coinbase" {
			localRevision = max(localRevision+1, time.Now().UnixMilli())
			for i := range bars {
				bars[i].SourceRevision = localRevision
			}
		}
		emit(bars, h)
	}
	native := NewNativeSeries(512)
	trades := NewTradeAggregator(duration, time.Now().Unix())
	health := domain.Health{State: "connecting", Reason: "Opening public spot stream"}
	send(nil, health)
	type historyResult struct {
		bars    []domain.Candle
		err     error
		started int64
		initial bool
		clipped bool
	}
	history := make(chan historyResult, 1)
	fetching := false
	initial := true
	repairFrom := int64(0)
	lastFetch := time.Time{}
	fetch := func() {
		if fetching {
			return
		}
		fetching = true
		lastFetch = time.Now()
		start := lastFetch.UnixMilli()
		first := initial
		initial = false
		from := repairFrom
		if first {
			from = (lastFetch.Unix()/duration - 499) * duration
		}
		if from == 0 {
			from = (lastFetch.Unix()/duration - 3) * duration
		}
		go func() {
			bars, clipped, err := a.reconcileHistory(ctx, k, from, time.Now().Unix()+1)
			select {
			case history <- historyResult{bars: bars, err: err, started: start, initial: first, clipped: clipped}:
			case <-ctx.Done():
			}
		}()
	}
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	var buffered []domain.Candle
	ready := false
	connected := false
	lastHealthEmit := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if connected {
				fetch()
			}
		case result := <-history:
			fetching = false
			if result.err != nil {
				health.State = ErrorKind(result.err)
				health.Reason = result.err.Error()
				send(nil, health)
				if health.State == "restricted" || health.State == "unknownInstrument" {
					return result.err
				}
				continue
			}
			bars := make([]domain.Candle, 0, len(result.bars)+len(buffered))
			for _, c := range result.bars {
				if a.provider == "coinbase" {
					c.SourceRevision = result.started
					c = trades.Reconcile(c)
				} else {
					// REST has no exchange event watermark. Never let an old buffered open-bar
					// snapshot overwrite it. Later received native snapshots replace cumulatively.
					c.SourceRevision = result.started
					if old, ok := native.values[c.Time]; ok && old.SourceRevision >= result.started {
						continue
					}
					native.Apply(c)
				}
				bars = append(bars, c)
			}
			if a.provider != "coinbase" {
				for _, c := range buffered {
					if c.SourceRevision >= result.started && native.Apply(c) {
						bars = append(bars, c)
					}
				}
			}
			buffered = nil
			ready = true
			repairFrom = 0
			health.State = "live"
			health.Reason = "Public spot data; current bucket may be provisional"
			if a.provider == "coinbase" {
				health.Reason = "Live trades; partial bucket volume is the last REST snapshot or observed-only"
			}
			if result.clipped {
				health.Reason = "Recent outage repaired; older data outside 10,000-bar repair window must be loaded with chart history"
			}
			if !connected {
				health.State = "offline"
				health.Reason = "Public history repaired; waiting for stream reconnection"
			}
			normalized, err := Normalize(bars, duration)
			if err != nil {
				return err
			}
			send(normalized, health)
		case message := <-messages:
			now := time.Now()
			if message.err != nil {
				health.State = ErrorKind(message.err)
				health.Reason = message.err.Error()
				send(nil, health)
				return message.err
			}
			if message.state != "" {
				if message.state == "heartbeat" {
					health.LastMessageAt = now.Unix()
					if now.Sub(lastHealthEmit) > 5*time.Second {
						send(nil, health)
						lastHealthEmit = now
					}
					continue
				}
				if message.state == "subscribed" {
					connected = true
					health.State = "reconciling"
					health.Reason = "Subscribed; loading overlapping public history"
					fetch()
				} else {
					health.State = message.state
					if message.state == "connecting" || message.state == "offline" || message.state == "disconnected" {
						connected = false
					}
					if message.state == "disconnected" {
						health.State = "reconciling"
					}
					health.Reason = message.reason
				}
				if message.state == "reconciling" || message.state == "offline" || message.state == "disconnected" {
					trades.Discontinuity(now.Unix())
					if repairFrom == 0 {
						repairFrom = (now.Unix()/duration - 2) * duration
					}
					if message.state == "reconciling" && now.Sub(lastFetch) > 2*time.Second {
						fetch()
					}
				}
				send(nil, health)
				continue
			}
			health.LastMessageAt = now.Unix()
			var updates []domain.Candle
			if a.provider == "coinbase" {
				events, err := ParseTrades(message.data, k.Instrument)
				if err != nil {
					health.State = "reconciling"
					health.Reason = err.Error()
					trades.Discontinuity(now.Unix())
					send(nil, health)
					fetch()
					continue
				}
				for _, event := range events {
					if event.Time.Unix() > health.LastTradeAt {
						health.LastTradeAt = event.Time.Unix()
					}
					c, changed, err := trades.Apply(event)
					if err != nil {
						health.State = "reconciling"
						health.Reason = err.Error()
						send(nil, health)
						fetch()
						continue
					}
					if changed {
						updates = append(updates, c)
					}
				}
			} else {
				candles, err := ParseNative(a.provider, message.data, k)
				if err != nil {
					health.State = "reconciling"
					health.Reason = "Invalid stream candle; requesting overlap"
					send(nil, health)
					fetch()
					continue
				}
				for _, c := range candles {
					previous, known := native.values[c.Time]
					if c.Volume > 0 && (!known || previous.Volume != c.Volume) {
						health.LastTradeAt = now.Unix()
					}
					if !ready {
						if len(buffered) >= 512 {
							buffered = nil
							health.State = "reconciling"
							health.Reason = "Initial candle buffer overflow; repairing overlap"
							send(nil, health)
							continue
						}
						buffered = append(buffered, c)
					} else if native.Apply(c) {
						updates = append(updates, c)
					}
				}
			}
			if len(updates) > 0 {
				if ready && health.State != "reconciling" {
					health.State = "live"
				}
				updates, err := Normalize(updates, duration)
				if err != nil {
					return err
				}
				send(updates, health)
			}
		}
	}
}
func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
