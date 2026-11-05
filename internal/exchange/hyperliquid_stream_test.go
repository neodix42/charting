package exchange

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Synthetic envelopes follow the official Hyperliquid websocket/subscriptions
// schemas, reviewed 2026-11-05. These are deliberately not live recordings.
func TestHyperliquidNativeCandles(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 3, 0, 0, time.UTC)
	for _, interval := range domain.Intervals {
		if interval == "1w" {
			continue
		} // Thursday-native weeks are explicitly rejected below.
		for _, coin := range []string{"BTC", "@107", "PURR/USDC", "xyz:TSLA"} {
			t.Run(interval+"/"+coin, func(t *testing.T) {
				d := TimeframeSeconds(interval)
				start := BucketStart(now.Unix(), d)
				k := domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: coin, Timeframe: interval}
				wire := fmt.Sprintf(`{"channel":"candle","data":{"t":%d,"T":%d,"s":%q,"i":%q,"o":"100.123456789","h":"104","l":"99","c":102,"v":"5.25","n":7}}`, start*1000, (start+d)*1000-1, coin, interval)
				bars, err := parseHyperliquidCandle([]byte(wire), k, now)
				if err != nil || len(bars) != 1 {
					t.Fatalf("parse: %v %+v", err, bars)
				}
				if bars[0].Closed || bars[0].Quality != "provisional" || bars[0].Time != start || bars[0].Volume != 5.25 || bars[0].Open != 100.123456789 || bars[0].SourceRevision != 0 {
					t.Fatalf("wrong current candle: %+v", bars[0])
				}
				closed, err := parseHyperliquidCandle([]byte(wire), k, time.Unix(start+d+2, 0))
				if err != nil || !closed[0].Closed || closed[0].Quality == "authoritative" {
					t.Fatalf("wrong settled snapshot: %v %+v", err, closed)
				}
				k.Instrument = "OTHER"
				if _, err := parseHyperliquidCandle([]byte(wire), k, now); err == nil {
					t.Fatal("accepted different coin")
				}
			})
		}
	}
	k := domain.FeedKey{Instrument: "BTC", Timeframe: "1m"}
	for _, wire := range []string{
		`{"channel":"candle","data":{"t":1792368000001,"T":1792368059999,"s":"BTC","i":"1m"}}`,
		`{"channel":"candle","data":{"t":1792368000000,"T":1792368099999,"s":"BTC","i":"1m"}}`,
		`{"channel":"candle","data":{"t":1792368000000,"T":1792368059999,"s":"BTC","i":"1m","o":"NaN","h":"104","l":"99","c":"102","v":"5"}}`,
	} {
		if _, err := parseHyperliquidCandle([]byte(wire), k, now); err == nil {
			t.Fatalf("accepted invalid candle %s", wire)
		}
	}
	k.Timeframe = "1w"
	if _, err := ParseNative("hyperliquid", []byte(`{}`), k); err == nil {
		t.Fatal("native Thursday week accepted for a Monday-week chart")
	}
}

func TestHyperliquidWeeklyLivePreservesRESTVolume(t *testing.T) {
	week := domain.Candle{Time: 1793577600, Open: 100, High: 104, Low: 98, Close: 101, Volume: 12, Quality: "authoritative"}
	day := domain.Candle{Time: week.Time + 3*86400, Open: 102, High: 105, Low: 99, Close: 103, Volume: 5, Quality: "live"}
	updated := mergeHyperliquidWeek(week, day)
	if updated.Open != 100 || updated.High != 105 || updated.Low != 98 || updated.Close != 103 || updated.Volume != 12 || updated.Quality != "provisional" || updated.Closed {
		t.Fatalf("weekly snapshot lost baseline or double-counted volume: %+v", updated)
	}
}

func TestHyperliquidBestBidAskSnapshots(t *testing.T) {
	book := func(levels string) []byte {
		return []byte(`{"channel":"l2Book","data":{"coin":"xyz:TSLA","time":1792368000123,"levels":` + levels + `}}`)
	}
	quote, err := parseHyperliquidBook(book(`[[{"px":"100.123456789","sz":"2","n":1}],[{"px":"100.2","sz":"3","n":1}]]`), "xyz:TSLA")
	if err != nil || quote.Bid == nil || quote.Ask == nil || *quote.Bid != 100.123456789 || *quote.Ask != 100.2 || quote.Time != 1792368000123 {
		t.Fatalf("quote: %v %+v", err, quote)
	}
	for _, levels := range []string{`[[],[]]`, `[[{"px":"100","sz":"2"}],[]]`, `[[],[{"px":"101","sz":"1"}]]`} {
		if _, err := parseHyperliquidBook(book(levels), "xyz:TSLA"); err != nil {
			t.Fatalf("empty book side rejected: %v", err)
		}
	}
	for _, levels := range []string{`[[{"px":"102","sz":"2"}],[{"px":"101","sz":"1"}]]`, `[[{"px":"NaN","sz":"2"}],[]]`, `[[{"px":"0","sz":"2"}],[]]`, `[[{"px":"100","sz":"0"}],[]]`, `[[]]`} {
		if _, err := parseHyperliquidBook(book(levels), "xyz:TSLA"); err == nil {
			t.Fatalf("accepted invalid book %s", levels)
		}
	}
	if _, err := parseHyperliquidBook(book(`[[],[]]`), "BTC"); err == nil {
		t.Fatal("accepted wrong market")
	}
}

func TestHyperliquidHubRoutesAndAcknowledgesSubscriptions(t *testing.T) {
	a := &publicAdapter{provider: "hyperliquid"}
	h := newSocketHub(a)
	candleTopic := hyperliquidCandleTopic("xyz:BRK.B", "1m")
	bookTopic := hyperliquidBookTopic("xyz:BRK.B")
	candles, books := make(chan wireEvent, 16), make(chan wireEvent, 16)
	h.subs[candleTopic] = map[uint64]chan wireEvent{1: candles}
	h.subs[bookTopic] = map[uint64]chan wireEvent{2: books}
	h.route([]byte(`{"channel":"subscriptionResponse","data":{"method":"subscribe","subscription":{"type":"candle","coin":"xyz:BRK.B","interval":"1m"}}}`))
	if e := <-candles; e.state != "subscribed" || !h.active[candleTopic] {
		t.Fatalf("ack not applied: %+v", e)
	}
	if len(books) != 0 {
		t.Fatal("candle ack routed to book")
	}
	h.route([]byte(`{"channel":"candle","data":{"s":"xyz:BRK.B","i":"1m"}}`))
	if e := <-candles; len(e.data) == 0 {
		t.Fatal("candle lost")
	}
	h.route([]byte(`{"channel":"l2Book","data":{"coin":"xyz:BRK.B"}}`))
	if e := <-books; len(e.data) == 0 {
		t.Fatal("book lost")
	}
	h.route([]byte(`{"channel":"error","data":"Invalid subscription"}`))
	if e := <-candles; e.err == nil {
		t.Fatal("subscription error ignored")
	}
	if e := <-books; e.err == nil {
		t.Fatal("book subscription error ignored")
	}
	for _, topic := range []string{candleTopic, bookTopic} {
		sub, err := hyperliquidSubscription(topic)
		if err != nil || sub["coin"] != "xyz:BRK.B" {
			t.Fatalf("native coin changed: %v %v", sub, err)
		}
	}
}

func TestHyperliquidSharedBookReplaysForNewInterval(t *testing.T) {
	h := newSocketHub(&publicAdapter{provider: "hyperliquid"})
	h.running = true // Exercise the in-memory attachment without opening a socket.
	topic := hyperliquidBookTopic("@107")
	h.active[topic] = true
	h.latestBooks[topic] = []byte(`{"channel":"l2Book","data":{"coin":"@107","time":1792368000000,"levels":[[],[]]}}`)
	messages, unsubscribe := h.subscribe(topic)
	if message := <-messages; message.state != "subscribed" {
		t.Fatalf("missing shared acknowledgment %+v", message)
	}
	if message := <-messages; len(message.data) == 0 {
		t.Fatal("new interval must receive existing book snapshot")
	}
	unsubscribe()
	if len(h.latestBooks) != 0 {
		t.Fatal("last book subscriber retained snapshot")
	}
}

func TestHyperliquidBookReplayRejectsOlderAndMalformedSnapshots(t *testing.T) {
	h := newSocketHub(&publicAdapter{provider: "hyperliquid"})
	h.running = true
	topic := hyperliquidBookTopic("BTC")
	h.active[topic] = true
	first, unsubscribeFirst := h.subscribe(topic)
	defer unsubscribeFirst()
	<-first
	for _, timestamp := range []int64{1792368000123, 1792368000000} {
		h.route([]byte(fmt.Sprintf(`{"channel":"l2Book","data":{"coin":"BTC","time":%d,"levels":[[{"px":"100","sz":"2"}],[{"px":"101","sz":"3"}]]}}`, timestamp)))
	}
	second, unsubscribeSecond := h.subscribe(topic)
	defer unsubscribeSecond()
	<-second
	quote, err := parseHyperliquidBook((<-second).data, "BTC")
	if err != nil || quote.Time != 1792368000123 {
		t.Fatalf("new interval got stale snapshot: %+v %v", quote, err)
	}
	h.route([]byte(`{"channel":"l2Book","data":{"coin":"BTC","time":1792368000200,"levels":[[{"px":"NaN","sz":"2"}],[]]}}`))
	if len(h.latestBooks) != 0 {
		t.Fatal("malformed quote remained available for replay")
	}
}

func TestHyperliquidSharedSocketQuotesHistoryAndCleanup(t *testing.T) {
	var sockets, bookSubscriptions, candleSubscriptions atomic.Int32
	var connectionMu sync.Mutex
	var connection *websocket.Conn
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			connectionMu.Lock()
			connection = conn
			connectionMu.Unlock()
			sockets.Add(1)
			for {
				var msg struct {
					Method       string            `json:"method"`
					Subscription map[string]string `json:"subscription"`
				}
				if conn.ReadJSON(&msg) != nil {
					return
				}
				if msg.Method == "ping" {
					_ = conn.WriteJSON(map[string]string{"channel": "pong"})
					continue
				}
				if msg.Method != "subscribe" {
					continue
				}
				if msg.Subscription["coin"] != "@107" {
					t.Errorf("wrong subscribed coin %v", msg.Subscription)
				}
				_ = conn.WriteJSON(map[string]any{"channel": "subscriptionResponse", "data": map[string]any{"method": msg.Method, "subscription": msg.Subscription}})
				if msg.Subscription["type"] == "l2Book" {
					bookSubscriptions.Add(1)
					_ = conn.WriteJSON(map[string]any{"channel": "l2Book", "data": map[string]any{"coin": "@107", "time": time.Now().UnixMilli(), "levels": [][]map[string]string{{{"px": "100", "sz": "2"}}, {{"px": "101", "sz": "3"}}}}})
				} else {
					candleSubscriptions.Add(1)
					interval := msg.Subscription["interval"]
					start := BucketStart(time.Now().Unix(), TimeframeSeconds(interval)) * 1000
					for _, volume := range []string{"3", "5"} {
						_ = conn.WriteJSON(map[string]any{"channel": "candle", "data": map[string]any{"t": start, "T": start + TimeframeSeconds(interval)*1000 - 1, "s": "@107", "i": interval, "o": "100", "h": "103", "l": "99", "c": "102", "v": volume, "n": 3}})
					}
				}
			}
		}
		var req struct {
			Type string `json:"type"`
			Req  struct {
				Coin, Interval     string
				StartTime, EndTime int64
			} `json:"req"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Type != "candleSnapshot" || req.Req.Coin != "@107" {
			t.Errorf("wrong history request %+v", req)
		}
		d := TimeframeSeconds(req.Req.Interval)
		start := BucketStart(time.Now().Unix(), d) * 1000
		fmt.Fprintf(w, `[{"t":%d,"T":%d,"s":"@107","i":%q,"o":"100","h":"103","l":"99","c":"102","v":"5","n":3}]`, start, start+d*1000-1, req.Req.Interval)
	}))
	defer server.Close()
	defer func() {
		connectionMu.Lock()
		if connection != nil {
			connection.Close()
		}
		connectionMu.Unlock()
	}()
	generic, err := New("hyperliquid")
	if err != nil {
		t.Fatal(err)
	}
	a := generic.(*publicAdapter)
	a.base, a.ws = server.URL, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws"
	a.client.Transport = socketTestTransport(func(r *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(r)
		a.requestMu.Lock()
		a.nextRequest = time.Time{} // Payload/lifecycle coverage does not need production rate-limit waits.
		a.requestMu.Unlock()
		return response, err
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan struct {
		interval string
		bars     []domain.Candle
		health   domain.Health
	}, 128)
	done := make(chan error, 2)
	for _, interval := range []string{"1m", "5m"} {
		go func(interval string) {
			done <- a.Stream(ctx, domain.FeedKey{Exchange: "hyperliquid", Market: "spot", Instrument: "@107", Timeframe: interval}, func(b []domain.Candle, h domain.Health) {
				select {
				case results <- struct {
					interval string
					bars     []domain.Candle
					health   domain.Health
				}{interval, b, h}:
				case <-ctx.Done():
				}
			})
		}(interval)
	}
	seenBars, seenQuote := map[string]bool{}, map[string]bool{}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for len(seenBars) < 2 || len(seenQuote) < 2 {
		select {
		case result := <-results:
			for _, bar := range result.bars {
				if bar.Volume != 3 && bar.Volume != 5 {
					t.Fatalf("summed candle volume %+v", bar)
				}
				seenBars[result.interval] = true
			}
			if q := result.health.Quote; q != nil && q.Bid != nil && q.Ask != nil && *q.Bid == 100 && *q.Ask == 101 {
				seenQuote[result.interval] = true
			}
		case <-deadline.C:
			t.Fatalf("missing history or quote: bars%v quote%v", seenBars, seenQuote)
		}
	}
	if sockets.Load() != 1 || bookSubscriptions.Load() != 1 || candleSubscriptions.Load() != 2 {
		t.Fatalf("subscriptions sockets%d book%d candles%d", sockets.Load(), bookSubscriptions.Load(), candleSubscriptions.Load())
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("stream did not cancel")
		}
	}
	end := time.Now().Add(2 * time.Second)
	for a.Diagnostics().Sockets != 0 && time.Now().Before(end) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.Diagnostics().Sockets != 0 {
		t.Fatal("socket survived last release")
	}
}

func TestHyperliquidWeeklyStreamUsesDailyPricesWithoutAddingOverlappingVolume(t *testing.T) {
	for _, delayedHistory := range []bool{false, true} {
		t.Run(fmt.Sprintf("delayedHistory=%v", delayedHistory), func(t *testing.T) { testHyperliquidWeeklyStream(t, delayedHistory) })
	}
}

func testHyperliquidWeeklyStream(t *testing.T, delayedHistory bool) {
	start := BucketStart(time.Now().Unix(), TimeframeSeconds("1w"))
	dayStart := BucketStart(time.Now().Unix(), 86400)
	expectedVolume := float64((dayStart-start)/86400+1) * 5
	update := make(chan struct{})
	historyStarted, dailyConsumed := make(chan struct{}), make(chan struct{})
	var historyOnce, dailyOnce sync.Once
	var connectionMu sync.Mutex
	var connection *websocket.Conn
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			connectionMu.Lock()
			connection = conn
			connectionMu.Unlock()
			for {
				var msg struct {
					Method       string            `json:"method"`
					Subscription map[string]string `json:"subscription"`
				}
				if conn.ReadJSON(&msg) != nil {
					return
				}
				if msg.Method != "subscribe" {
					continue
				}
				_ = conn.WriteJSON(map[string]any{"channel": "subscriptionResponse", "data": map[string]any{"method": "subscribe", "subscription": msg.Subscription}})
				if msg.Subscription["type"] == "candle" {
					if msg.Subscription["interval"] != "1d" {
						t.Errorf("weekly stream subscribed wrong upstream interval %v", msg.Subscription)
					}
					gate := update
					if delayedHistory {
						gate = historyStarted
					}
					select {
					case <-gate:
					case <-r.Context().Done():
						return
					}
					_ = conn.WriteJSON(map[string]any{"channel": "candle", "data": map[string]any{"t": dayStart * 1000, "T": (dayStart+86400)*1000 - 1, "s": "BTC", "i": "1d", "o": "100", "h": "106", "l": "98", "c": "105", "v": "9"}})
					if delayedHistory {
						_ = conn.WriteJSON(map[string]string{"channel": "pong"})
					}
				}
			}
		}
		var request struct {
			Type string                          `json:"type"`
			Req  struct{ Coin, Interval string } `json:"req"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Type != "candleSnapshot" || request.Req.Interval != "1d" {
			t.Errorf("weekly history uses wrong upstream interval %+v", request)
		}
		if delayedHistory {
			historyOnce.Do(func() { close(historyStarted) })
			select {
			case <-dailyConsumed:
			case <-r.Context().Done():
				return
			}
		}
		var days []map[string]any
		for timestamp := start; timestamp <= dayStart; timestamp += 86400 {
			days = append(days, map[string]any{"t": timestamp * 1000, "T": (timestamp+86400)*1000 - 1, "s": "BTC", "i": "1d", "o": "100", "h": "104", "l": "99", "c": "101", "v": "5"})
		}
		_ = json.NewEncoder(w).Encode(days)
	}))
	defer server.Close()
	defer func() {
		connectionMu.Lock()
		if connection != nil {
			connection.Close()
		}
		connectionMu.Unlock()
	}()
	generic, err := New("hyperliquid")
	if err != nil {
		t.Fatal(err)
	}
	a := generic.(*publicAdapter)
	a.base, a.ws = server.URL, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws"
	a.client.Transport = socketTestTransport(func(r *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(r)
		a.requestMu.Lock()
		a.nextRequest = time.Time{}
		a.requestMu.Unlock()
		return response, err
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bars := make(chan domain.Candle, 16)
	done := make(chan error, 1)
	go func() {
		done <- a.Stream(ctx, domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1w"}, func(updates []domain.Candle, h domain.Health) {
			if delayedHistory && h.LastTradeAt > 0 {
				dailyOnce.Do(func() { close(dailyConsumed) })
			}
			for _, c := range updates {
				select {
				case bars <- c:
				case <-ctx.Done():
					return
				}
			}
		})
	}()
	select {
	case baseline := <-bars:
		if baseline.Time != start || baseline.Volume != expectedVolume {
			t.Fatalf("invalid Monday weekly baseline %+v", baseline)
		}
		if delayedHistory && (baseline.Close != 105 || baseline.High != 106 || baseline.Low != 98 || baseline.Quality != "provisional") {
			t.Fatalf("older initial REST replaced buffered daily prices %+v", baseline)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("weekly history was not established")
	}
	close(update)
	if !delayedHistory {
		select {
		case live := <-bars:
			if live.Time != start || live.Close != 105 || live.High != 106 || live.Low != 98 || live.Volume != expectedVolume || live.Quality != "provisional" {
				t.Fatalf("weekly live price or volume mismatch %+v", live)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("daily update did not reach weekly chart")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("weekly stream ignored cancellation")
	}
}
