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

// Each interval subscribes through the existing provider/category hub. The hub
// owns one upstream top-of-book subscription per native instrument.
func publicQuoteTopic(provider, instrument string) string {
	switch provider {
	case "binance":
		return strings.ToLower(instrument) + "@bookTicker"
	case "bybit":
		return "orderbook.1." + instrument
	case "coinbase":
		return "ticker." + instrument
	}
	return ""
}

type quoteSnapshot struct {
	quote    *domain.Quote
	updateID int64
}

func (h *socketHub) publishQuote(topic string, quote *domain.Quote, updateID int64, err error) {
	h.mu.Lock()
	if old, ok := h.latestQuotes[topic]; err == nil && ok && (old.quote.Time > quote.Time || updateID > 0 && old.updateID > updateID) {
		h.mu.Unlock()
		return
	}
	if err != nil {
		delete(h.latestQuotes, topic)
	} else if len(h.subs[topic]) > 0 {
		h.latestQuotes[topic] = quoteSnapshot{quote, updateID}
	}
	h.mu.Unlock()
	event := wireEvent{state: "quote", quote: quote}
	if err != nil {
		event.quote = nil
		event.reason = "Invalid public top of book; waiting for a valid quote"
	}
	h.publish(topic, event)
}

func quoteTime(at, observed int64) error {
	if at <= 0 || at > 32503680000000 || at > observed+5000 {
		return fmt.Errorf("invalid top-of-book timestamp")
	}
	return nil
}

func quoteSide(price, size string, requireSize bool) (*float64, error) {
	if price == "" {
		return nil, nil
	}
	if len(price) > 100 || len(size) > 100 {
		return nil, fmt.Errorf("top-of-book decimal too long")
	}
	p, err := decimal.NewFromString(price)
	if err != nil {
		return nil, err
	}
	if p.Exponent() < -100 || p.Exponent() > 100 || p.IsNegative() {
		return nil, fmt.Errorf("invalid top-of-book price")
	}
	if size != "" || requireSize {
		amount, err := decimal.NewFromString(size)
		if err != nil {
			return nil, err
		}
		if amount.Exponent() < -100 || amount.Exponent() > 100 || amount.IsNegative() {
			return nil, fmt.Errorf("invalid top-of-book size")
		}
		if amount.IsZero() {
			return nil, nil
		}
	}
	if p.IsZero() {
		return nil, nil
	}
	value, _ := p.Float64()
	if !finitePositive(value) {
		return nil, fmt.Errorf("top-of-book price outside chart range")
	}
	return &value, nil
}
func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func validateQuote(quote *domain.Quote) error {
	if quote.Bid != nil && quote.Ask != nil && *quote.Bid > *quote.Ask {
		return fmt.Errorf("crossed top of book")
	}
	return nil
}

// Binance Spot bookTicker has no exchange timestamp. Its original receipt time
// is retained on replay; derivative bookTicker supplies transaction/event time.
func parseBinanceBook(data []byte, symbol string, observed int64) (*domain.Quote, int64, error) {
	var e struct {
		Event           string `json:"e"`
		Symbol          string `json:"s"`
		UpdateID        int64  `json:"u"`
		EventTime       int64  `json:"E"`
		TransactionTime int64  `json:"T"`
		Bid             string `json:"b"`
		BidSize         string `json:"B"`
		Ask             string `json:"a"`
		AskSize         string `json:"A"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, 0, err
	}
	if e.Symbol != symbol || e.UpdateID < 0 || e.Event != "" && e.Event != "bookTicker" {
		return nil, 0, fmt.Errorf("invalid Binance book selection")
	}
	at := e.TransactionTime
	if at == 0 {
		at = e.EventTime
	}
	if at == 0 && e.Event == "" {
		at = observed
	}
	if err := quoteTime(at, observed); err != nil {
		return nil, 0, err
	}
	quote := &domain.Quote{Time: at}
	var err error
	quote.Bid, err = quoteSide(e.Bid, e.BidSize, true)
	if err != nil {
		return nil, 0, err
	}
	quote.Ask, err = quoteSide(e.Ask, e.AskSize, true)
	if err != nil {
		return nil, 0, err
	}
	return quote, e.UpdateID, validateQuote(quote)
}
func (h *socketHub) routeBinanceBook(symbol string, data []byte) {
	quote, revision, err := parseBinanceBook(data, symbol, time.Now().UnixMilli())
	h.publishQuote(publicQuoteTopic("binance", symbol), quote, revision, err)
}

// Bybit documents depth 1 for Spot, Linear and Inverse as snapshots only,
// including a repeated unchanged snapshot every three quiet seconds.
func parseBybitBook(data []byte, symbol string, observed int64) (*domain.Quote, error) {
	var e struct {
		Topic string `json:"topic"`
		Type  string `json:"type"`
		Time  int64  `json:"ts"`
		Data  struct {
			Symbol string     `json:"s"`
			Bids   [][]string `json:"b"`
			Asks   [][]string `json:"a"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	if e.Topic != publicQuoteTopic("bybit", symbol) || e.Data.Symbol != symbol || e.Type != "snapshot" {
		return nil, fmt.Errorf("invalid Bybit depth-one snapshot")
	}
	if err := quoteTime(e.Time, observed); err != nil {
		return nil, err
	}
	quote := &domain.Quote{Time: e.Time}
	for index, side := range [][][]string{e.Data.Bids, e.Data.Asks} {
		if len(side) == 0 {
			continue
		}
		if len(side) != 1 || len(side[0]) != 2 {
			return nil, fmt.Errorf("invalid Bybit depth-one side")
		}
		value, err := quoteSide(side[0][0], side[0][1], true)
		if err != nil {
			return nil, err
		}
		if index == 0 {
			quote.Bid = value
		} else {
			quote.Ask = value
		}
	}
	return quote, validateQuote(quote)
}
func (h *socketHub) routeBybitBook(topic string, data []byte) {
	quote, err := parseBybitBook(data, strings.TrimPrefix(topic, "orderbook.1."), time.Now().UnixMilli())
	h.publishQuote(topic, quote, 0, err)
}

type coinbaseTicker struct {
	Product string `json:"product_id"`
	Bid     string `json:"best_bid"`
	Ask     string `json:"best_ask"`
	BidSize string `json:"best_bid_quantity"`
	AskSize string `json:"best_ask_quantity"`
}

func parseCoinbaseTicker(ticker coinbaseTicker, at, observed int64) (*domain.Quote, error) {
	if err := quoteTime(at, observed); err != nil {
		return nil, err
	}
	quote := &domain.Quote{Time: at}
	var err error
	quote.Bid, err = quoteSide(ticker.Bid, ticker.BidSize, false)
	if err != nil {
		return nil, err
	}
	quote.Ask, err = quoteSide(ticker.Ask, ticker.AskSize, false)
	if err != nil {
		return nil, err
	}
	return quote, validateQuote(quote)
}
func (h *socketHub) routeCoinbaseTicker(data []byte) {
	var e struct {
		Time   string `json:"timestamp"`
		Events []struct {
			Tickers []coinbaseTicker `json:"tickers"`
		} `json:"events"`
	}
	if json.Unmarshal(data, &e) != nil {
		return
	}
	at, timeErr := time.Parse(time.RFC3339Nano, e.Time)
	observed := time.Now().UnixMilli()
	for _, event := range e.Events {
		for _, ticker := range event.Tickers {
			if ticker.Product == "" {
				continue
			}
			quote, err := parseCoinbaseTicker(ticker, at.UnixMilli(), observed)
			if timeErr != nil {
				err = timeErr
			}
			h.publishQuote(publicQuoteTopic("coinbase", ticker.Product), quote, 0, err)
		}
	}
}
