package domain

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

var Intervals = []string{"1m", "5m", "15m", "1h", "4h", "1d", "1w"}
var hyperliquidSpot = regexp.MustCompile(`^(?:@[0-9]+|[\p{L}\p{N}][\p{L}\p{N}._-]{0,79}/[\p{L}\p{N}][\p{L}\p{N}._-]{0,39})$`)
var hyperliquidPerp = regexp.MustCompile(`^(?:[a-zA-Z0-9_-]{1,30}:)?[\p{L}\p{N}][\p{L}\p{N}._-]{0,79}$`)
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
	if k.Exchange != "binance" && k.Exchange != "bybit" && k.Exchange != "coinbase" && k.Exchange != "dukascopy" && k.Exchange != "hyperliquid" {
		return fmt.Errorf("unsupported exchange")
	}
	if !supportedMarket(k.Exchange, k.Market) {
		return fmt.Errorf("unsupported market for exchange")
	}
	if len(k.Instrument) > 128 || !validNativeInstrument(k.Exchange, k.Market, k.Instrument) {
		return fmt.Errorf("invalid native instrument")
	}
	for _, v := range Intervals {
		if k.Timeframe == v {
			return nil
		}
	}
	return fmt.Errorf("unsupported interval")
}

func supportedMarket(exchange, market string) bool {
	switch exchange {
	case "dukascopy":
		return market == "forex" || market == "metal" || market == "cfd"
	case "binance", "bybit", "hyperliquid":
		return market == "spot" || market == "perpetual"
	case "coinbase":
		return market == "spot"
	}
	return false
}

// Hyperliquid uses spot pair IDs and dex-qualified perpetual names verbatim.
// Other providers retain their existing native symbol rules.
func validNativeInstrument(exchange, market, instrument string) bool {
	if exchange == "hyperliquid" {
		if market == "spot" {
			return hyperliquidSpot.MatchString(instrument)
		}
		return hyperliquidPerp.MatchString(instrument)
	}
	return nativeSymbol.MatchString(instrument)
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
	// Forex history predates crypto. Unix seconds through year 3000 allow that
	// history while still rejecting contemporary millisecond timestamps.
	if c.Time < 0 || c.Time > 32503680000 {
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

// Quote is the current top of book, separate from trade-based candles.
// A nil side means that side of the order book is empty. Time is Unix milliseconds.
type Quote struct {
	Bid  *float64 `json:"bid"`
	Ask  *float64 `json:"ask"`
	Time int64    `json:"time"`
}

type Health struct {
	Quote         *Quote `json:"quote,omitempty"`
	State         string `json:"state"`
	Reason        string `json:"reason"`
	LastMessageAt int64  `json:"lastMessageAt"`
	LastTradeAt   int64  `json:"lastTradeAt"`
}
type Instrument struct {
	NativeID           string            `json:"nativeId"`
	Market             string            `json:"market"`
	BaseAsset          string            `json:"baseAsset"`
	QuoteAsset         string            `json:"quoteAsset"`
	PriceIncrement     string            `json:"priceIncrement"`
	SizeIncrement      string            `json:"sizeIncrement"`
	Availability       string            `json:"availability"`
	SupportedIntervals []string          `json:"supportedIntervals"`
	ProviderMetadata   map[string]string `json:"providerMetadata"`
}
