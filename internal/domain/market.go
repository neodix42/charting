package domain

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

var Intervals = []string{"1m", "5m", "15m", "1h", "4h", "1d"}
var nativeSymbol = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._-]{0,79}$`)

type FeedKey struct {
	Exchange   string `json:"exchange"`
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Timeframe  string `json:"timeframe"`
}

func (k FeedKey) String() string {
	return strings.Join([]string{k.Exchange, k.Market, k.Instrument, k.Timeframe}, ":")
}
func (k FeedKey) Validate() error {
	if k.Exchange != "binance" && k.Exchange != "bybit" && k.Exchange != "coinbase" {
		return fmt.Errorf("unsupported exchange")
	}
	if k.Market != "spot" {
		return fmt.Errorf("only public spot markets are supported")
	}
	if len(k.Instrument) > 128 || !nativeSymbol.MatchString(k.Instrument) {
		return fmt.Errorf("invalid native instrument")
	}
	for _, v := range Intervals {
		if k.Timeframe == v {
			return nil
		}
	}
	return fmt.Errorf("unsupported interval")
}

type Candle struct {
	Time           int64   `json:"time"`
	Open           float64 `json:"open"`
	High           float64 `json:"high"`
	Low            float64 `json:"low"`
	Close          float64 `json:"close"`
	Volume         float64 `json:"volume"`
	Closed         bool    `json:"closed"`
	Quality        string  `json:"quality"`
	SourceRevision int64   `json:"sourceRevision"`
}

func (c Candle) Validate() error {
	// Reject millisecond timestamps before the rendering boundary. These spot markets
	// did not exist before 2009; limiting to year 3000 catches accidental unit errors.
	if c.Time < 1230768000 || c.Time > 32503680000 {
		return fmt.Errorf("candle timestamp must be UTC Unix seconds")
	}
	for _, v := range []float64{c.Open, c.High, c.Low, c.Close, c.Volume} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("non-finite candle")
		}
	}
	if c.Open <= 0 || c.High <= 0 || c.Low <= 0 || c.Close <= 0 || c.Volume < 0 || c.Low > math.Min(c.Open, c.Close) || c.High < math.Max(c.Open, c.Close) || c.Low > c.High {
		return fmt.Errorf("invalid candle OHLCV")
	}
	return nil
}

type Health struct {
	State         string `json:"state"`
	Reason        string `json:"reason"`
	LastMessageAt int64  `json:"lastMessageAt"`
	LastTradeAt   int64  `json:"lastTradeAt"`
}
type Instrument struct {
	NativeID           string            `json:"nativeId"`
	BaseAsset          string            `json:"baseAsset"`
	QuoteAsset         string            `json:"quoteAsset"`
	PriceIncrement     string            `json:"priceIncrement"`
	SizeIncrement      string            `json:"sizeIncrement"`
	Availability       string            `json:"availability"`
	SupportedIntervals []string          `json:"supportedIntervals"`
	ProviderMetadata   map[string]string `json:"providerMetadata"`
}
