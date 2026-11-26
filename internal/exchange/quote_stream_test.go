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

func TestNativeTopOfBookParsersPreserveRealSidesAndTimes(t *testing.T) {
	const observed = int64(1795680000123)
	spot := `{"u":7,"s":"BTCUSDT","b":"100.123456789","B":"2","a":"101.5","A":"3"}`
	quote, revision, err := parseBinanceBook([]byte(spot), "BTCUSDT", observed)
	if err != nil || revision != 7 || quote.Time != observed || *quote.Bid != 100.123456789 || *quote.Ask != 101.5 {
		t.Fatal(quote, revision, err)
	}
	future := `{"e":"bookTicker","u":8,"s":"BTCUSD_PERP","b":"100","B":"2","a":"101","A":"3","E":1795680000003,"T":1795680000001}`
	quote, _, err = parseBinanceBook([]byte(future), "BTCUSD_PERP", observed)
	if err != nil || quote.Time != 1795680000001 || *quote.Bid != 100 || *quote.Ask != 101 {
		t.Fatal(quote, err)
	}
	bybit := `{"topic":"orderbook.1.BTCUSDT","type":"snapshot","ts":1795680000001,"data":{"s":"BTCUSDT","b":[["100","2"]],"a":[["101","3"]],"u":7}}`
	quote, err = parseBybitBook([]byte(bybit), "BTCUSDT", observed)
	if err != nil || quote.Time != 1795680000001 || *quote.Bid != 100 || *quote.Ask != 101 {
		t.Fatal(quote, err)
	}
	quote, err = parseCoinbaseTicker(coinbaseTicker{Product: "BTC-USD", Bid: "100", Ask: "101"}, 1795680000001, observed)
	if err != nil || quote.Time != 1795680000001 || *quote.Bid != 100 || *quote.Ask != 101 {
		t.Fatal(quote, err)
	}
	quote, _, err = parseBinanceBook([]byte(strings.Replace(spot, `"B":"2"`, `"B":"0"`, 1)), "BTCUSDT", observed)
	if err != nil || quote.Bid != nil || quote.Ask == nil {
		t.Fatal("empty side carried or invented", quote, err)
	}
	quote, err = parseBybitBook([]byte(strings.Replace(bybit, `[["100","2"]]`, `[]`, 1)), "BTCUSDT", observed)
	if err != nil || quote.Bid != nil || quote.Ask == nil {
		t.Fatal("empty side carried or invented", quote, err)
	}
	for _, mutate := range []func(string) string{
		func(s string) string { return strings.Replace(s, `"b":"100.123456789"`, `"b":"102"`, 1) },
		func(s string) string { return strings.Replace(s, `"b":"100.123456789"`, `"b":"-1"`, 1) },
		func(s string) string { return strings.Replace(s, `"b":"100.123456789"`, `"b":"1e999"`, 1) },
		func(s string) string { return strings.Replace(s, `"B":"2"`, `"B":"-1"`, 1) },
		func(s string) string { return strings.Replace(s, `"s":"BTCUSDT"`, `"s":"ETHUSDT"`, 1) },
	} {
		if _, _, err := parseBinanceBook([]byte(mutate(spot)), "BTCUSDT", observed); err == nil {
			t.Fatal("invalid book accepted", mutate(spot))
		}
	}
	if _, err := parseBybitBook([]byte(strings.Replace(bybit, `"snapshot"`, `"delta"`, 1)), "BTCUSDT", observed); err == nil {
		t.Fatal("partial delta was treated as complete level1 book")
	}
	if _, err := parseCoinbaseTicker(coinbaseTicker{Bid: "100", Ask: "101"}, observed+5001, observed); err == nil {
		t.Fatal("future quote accepted")
	}
}

func TestHubNativeQuoteReplayRetainsTimeAndClearsUntrustedBooks(t *testing.T) {
	generic, _ := New("binance")
	h := generic.(*publicAdapter).hub
	topic := publicQuoteTopic("binance", "BTCUSDT")
	h.running = true // Explicit routing fixture; do not dial a real provider.
	h.active[topic] = true
	first, removeFirst := h.subscribe(topic)
	defer removeFirst()
	<-first
	h.route([]byte(`{"u":7,"s":"BTCUSDT","b":"100","B":"2","a":"101","A":"3"}`))
	initial := <-first
	if initial.quote == nil || initial.quote.Time <= 0 {
		t.Fatal("book was not routed", initial)
	}
	second, removeSecond := h.subscribe(topic)
	defer removeSecond()
	<-second
	replay := <-second
	if replay.quote == nil || replay.quote.Time != initial.quote.Time || *replay.quote.Bid != 100 {
		t.Fatal("shared attach refreshed or replaced source observation", replay)
	}
	h.route([]byte(`{"u":6,"s":"BTCUSDT","b":"90","B":"2","a":"91","A":"3"}`))
	if len(first) != 0 || len(second) != 0 {
		t.Fatal("out-of-order Binance update replaced current book")
	}
	h.route([]byte(`{"u":8,"s":"BTCUSDT","b":"102","B":"2","a":"101","A":"3"}`))
	if event := <-first; event.state != "quote" || event.quote != nil {
		t.Fatal("crossed book retained", event)
	}
	<-second
	if len(h.latestQuotes) != 0 {
		t.Fatal("invalid book retained for replay")
	}
	h.route([]byte(`{"u":9,"s":"BTCUSDT","b":"100","B":"2","a":"101","A":"3"}`))
	<-first
	<-second
	h.publish("", wireEvent{state: "disconnected"})
	<-first
	<-second
	if len(h.latestQuotes) != 0 {
		t.Fatal("disconnected book retained for replay")
	}
}

func TestNativeQuoteTopicsShareSocketAndNeverCreateTradesOrCandles(t *testing.T) {
	for _, test := range []struct{ provider, market, instrument string }{
		{"binance", "spot", "BTCUSDT"}, {"binance", "perpetual", "BTCUSDT"}, {"binance", "perpetual", "BTCUSD_PERP"},
		{"bybit", "spot", "BTCUSDT"}, {"bybit", "perpetual", "BTCUSDT"}, {"bybit", "perpetual", "BTCUSD"},
		{"coinbase", "spot", "BTC-USD"},
	} {
		t.Run(test.provider+"-"+test.market+"-"+test.instrument, func(t *testing.T) {
			var sockets, bookSubscriptions atomic.Int32
			separateBookEndpoint := test.provider == "binance" && test.market == "perpetual"
			candleHandshake := make(chan struct{})
			var releaseHandshake sync.Once
			releaseCandle := func() { releaseHandshake.Do(func() { close(candleHandshake) }) }
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if websocket.IsWebSocketUpgrade(r) {
					if separateBookEndpoint && !strings.Contains(r.URL.Path, "/public/ws") {
						// Public books are independent of the candle transport. Force
						// them to arrive before its handshake instead of relying on
						// favorable scheduling between the two provider hubs.
						select {
						case <-candleHandshake:
						case <-r.Context().Done():
							return
						}
					}
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()
					sockets.Add(1)
					for {
						var message struct {
							Method   string   `json:"method"`
							Op       string   `json:"op"`
							Type     string   `json:"type"`
							Params   []string `json:"params"`
							Args     []string `json:"args"`
							Channel  string   `json:"channel"`
							Products []string `json:"product_ids"`
						}
						if conn.ReadJSON(&message) != nil {
							return
						}
						book := false
						switch test.provider {
						case "binance":
							if message.Method == "SUBSCRIBE" {
								for _, topic := range message.Params {
									if topic == publicQuoteTopic(test.provider, test.instrument) {
										book = true
									}
								}
							}
						case "bybit":
							if message.Op == "subscribe" {
								for _, topic := range message.Args {
									if topic == publicQuoteTopic(test.provider, test.instrument) {
										book = true
									}
								}
							}
						case "coinbase":
							if message.Type == "subscribe" && message.Channel == "ticker" {
								book = true
								if len(message.Products) != 1 || message.Products[0] != test.instrument {
									t.Error("ticker subscription used routing key instead of native product", message.Products)
								}
							}
						}
						if !book {
							if separateBookEndpoint && strings.Contains(r.URL.Path, "/public/ws") {
								t.Error("candle subscription reached Binance public book endpoint")
							}
							continue
						}
						if separateBookEndpoint && !strings.Contains(r.URL.Path, "/public/ws") {
							t.Error("book subscription reached Binance market candle endpoint")
						}
						bookSubscriptions.Add(1)
						now := time.Now()
						switch test.provider {
						case "binance":
							_ = conn.WriteJSON(map[string]any{"u": 1, "s": test.instrument, "b": "100", "B": "2", "a": "101", "A": "3", "E": now.UnixMilli(), "T": now.UnixMilli()})
						case "bybit":
							_ = conn.WriteJSON(map[string]any{"topic": publicQuoteTopic(test.provider, test.instrument), "type": "snapshot", "ts": now.UnixMilli(), "data": map[string]any{"s": test.instrument, "b": [][]string{{"100", "2"}}, "a": [][]string{{"101", "3"}}}})
						case "coinbase":
							_ = conn.WriteJSON(map[string]any{"channel": "ticker", "sequence_num": 1, "timestamp": now.UTC().Format(time.RFC3339Nano), "events": []any{map[string]any{"type": "snapshot", "tickers": []any{map[string]string{"product_id": test.instrument, "best_bid": "100", "best_ask": "101"}}}}})
						}
					}
				}
				switch test.provider {
				case "binance":
					fmt.Fprint(w, `[]`)
				case "bybit":
					fmt.Fprint(w, `{"retCode":0,"result":{"list":[]}}`)
				case "coinbase":
					fmt.Fprint(w, `{"candles":[]}`)
				}
			}))
			defer server.Close()
			defer releaseCandle()
			generic, _ := New(test.provider)
			a := generic.(*publicAdapter)
			a.base, a.futuresBase, a.inverseBase = server.URL, server.URL, server.URL
			a.ws = "ws" + strings.TrimPrefix(server.URL, "http")
			a.futuresWS, a.inverseWS = a.ws, a.ws
			if separateBookEndpoint {
				a.futuresWS, a.inverseWS = a.ws+"/linear/market/ws", a.ws+"/inverse/ws"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type observation struct {
				interval string
				bars     int
				health   domain.Health
			}
			observations := make(chan observation, 256)
			done := make(chan error, 2)
			for _, interval := range []string{"1m", "5m"} {
				go func() {
					done <- a.Stream(ctx, domain.FeedKey{Exchange: test.provider, Market: test.market, Instrument: test.instrument, Timeframe: interval}, func(b []domain.Candle, h domain.Health) {
						select {
						case observations <- observation{interval, len(b), h}:
						case <-ctx.Done():
						}
					})
				}()
			}
			seen := map[string]int64{}
			ready := map[string]bool{}
			record := func(value observation) {
				if value.bars != 0 || value.health.LastTradeAt != 0 {
					t.Fatal("book fabricated candle or trade", value)
				}
				if quote := value.health.Quote; quote != nil && quote.Bid != nil && quote.Ask != nil && *quote.Bid == 100 && *quote.Ask == 101 {
					seen[value.interval] = quote.Time
				}
				if value.health.State == "live" {
					ready[value.interval] = true
				}
			}
			deadline := time.NewTimer(4 * time.Second)
			defer deadline.Stop()
			for len(seen) < 2 {
				select {
				case value := <-observations:
					record(value)
				case err := <-done:
					t.Fatal("stream ended before both book observations", err)
				case <-deadline.C:
					t.Fatal("shared top-of-book did not reach both intervals", seen)
				}
			}
			if separateBookEndpoint && (len(ready) != 0 || sockets.Load() != 1 || a.Diagnostics().Sockets != 1) {
				t.Fatal("books must arrive independently while the candle handshake is blocked", ready, sockets.Load(), a.Diagnostics())
			}
			releaseCandle()
			// Live health follows the interval's candle subscription and initial
			// history response. Quote delivery alone cannot establish readiness of
			// Binance derivatives' separate market socket.
			for len(ready) < 2 {
				select {
				case value := <-observations:
					record(value)
				case err := <-done:
					t.Fatal("stream ended before both candle subscriptions became ready", err)
				case <-deadline.C:
					t.Fatal("both candle subscriptions did not become ready", ready, a.Diagnostics())
				}
			}
			wantSockets := int32(1)
			if separateBookEndpoint {
				wantSockets = 2
			}
			if sockets.Load() != wantSockets || bookSubscriptions.Load() != 1 || a.Diagnostics().Sockets != int(wantSockets) || seen["1m"] != seen["5m"] {
				t.Fatal("book subscription or observation duplicated", sockets.Load(), bookSubscriptions.Load(), seen, a.Diagnostics())
			}
			cancel()
			for i := 0; i < 2; i++ {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("quote stream ignored cancellation")
				}
			}
			until := time.Now().Add(2 * time.Second)
			for a.Diagnostics().Sockets != 0 && time.Now().Before(until) {
				time.Sleep(5 * time.Millisecond)
			}
			if a.Diagnostics().Sockets != 0 {
				t.Fatal("last quote unsubscribe retained shared socket")
			}
		})
	}
}

func TestCoinbaseTickerRoutingIsProductScopedAndTimestamped(t *testing.T) {
	generic, _ := New("coinbase")
	h := generic.(*publicAdapter).hub
	h.running = true
	btcTopic, ethTopic := publicQuoteTopic("coinbase", "BTC-USD"), publicQuoteTopic("coinbase", "ETH-USD")
	h.active[btcTopic], h.active[ethTopic] = true, true
	btc, removeBTC := h.subscribe(btcTopic)
	defer removeBTC()
	<-btc
	eth, removeETH := h.subscribe(ethTopic)
	defer removeETH()
	<-eth
	at := time.Now().Add(-time.Second).UTC().Truncate(time.Millisecond)
	wire := map[string]any{"channel": "ticker", "sequence_num": 1, "timestamp": at.Format(time.RFC3339Nano), "events": []any{map[string]any{"tickers": []any{map[string]string{"product_id": "BTC-USD", "best_bid": "100", "best_ask": "101"}}}}}
	data, _ := json.Marshal(wire)
	h.route(data)
	q := (<-btc).quote
	if q == nil || q.Time != at.UnixMilli() || len(eth) != 0 {
		t.Fatal("ticker quote leaked products or changed provider time", q)
	}
}

func TestQuoteEpochRejectsPreDisconnectBooksAndReplay(t *testing.T) {
	for _, provider := range []string{"binance", "hyperliquid"} {
		t.Run(provider, func(t *testing.T) {
			generic, _ := New(provider)
			h := generic.(*publicAdapter).hub
			h.running = true
			topic := publicQuoteTopic(provider, "BTCUSDT")
			if provider == "hyperliquid" {
				topic = hyperliquidBookTopic("BTC")
			}
			h.active[topic] = true
			messages, remove := h.subscribe(topic)
			defer remove()
			<-messages
			route := func(price int) {
				if provider == "hyperliquid" {
					h.route([]byte(fmt.Sprintf(`{"channel":"l2Book","data":{"coin":"BTC","time":%d,"levels":[[{"px":"%d","sz":"2","n":1}],[{"px":"%d","sz":"3","n":1}]]}}`, time.Now().UnixMilli(), price, price+1)))
				} else {
					h.route([]byte(fmt.Sprintf(`{"u":%d,"s":"BTCUSDT","b":"%d","B":"2","a":"%d","A":"3"}`, price, price, price+1)))
				}
			}
			route(100)
			old := <-messages
			replay, removeReplay := h.subscribe(topic)
			defer removeReplay()
			<-replay
			cached := <-replay
			if !h.currentQuoteEpoch(old.quoteEpoch) || cached.quoteEpoch != old.quoteEpoch {
				t.Fatal("current observation or shared replay was fenced out")
			}
			h.publish("", wireEvent{state: "disconnected"})
			if h.currentQuoteEpoch(old.quoteEpoch) || h.currentQuoteEpoch(cached.quoteEpoch) {
				t.Fatal("queued pre-disconnect book remained trusted")
			}
			<-messages
			<-replay
			h.publish("", wireEvent{state: "connecting"})
			<-messages
			<-replay
			route(200)
			current := <-messages
			<-replay
			if !h.currentQuoteEpoch(current.quoteEpoch) || h.currentQuoteEpoch(old.quoteEpoch) {
				t.Fatal("reconnect revived old books or rejected its new snapshot")
			}
			h.publish("candle-only-topic", wireEvent{state: "reconciling"})
			if !h.currentQuoteEpoch(current.quoteEpoch) {
				t.Fatal("candle history repair invalidated a connected quote transport")
			}
			h.publish("", wireEvent{state: "reconciling"})
			if h.currentQuoteEpoch(current.quoteEpoch) {
				t.Fatal("transport discontinuity retained queued books")
			}
		})
	}
}

func TestStreamFencesQueuedQuotesAcrossDisconnectAndReconnect(t *testing.T) {
	for _, provider := range []string{"binance", "hyperliquid"} {
		t.Run(provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `[]`)
			}))
			defer server.Close()
			generic, _ := New(provider)
			a := generic.(*publicAdapter)
			a.base = server.URL
			h := a.hub
			h.running = true // Queue ordering fixture; no remote socket is opened.
			instrument, candleTopic, market := "BTCUSDT", "btcusdt@kline_1m", "spot"
			if provider == "hyperliquid" {
				instrument, candleTopic = "BTC", hyperliquidCandleTopic("BTC", "1m")
				market = "perpetual"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			observations := make(chan domain.Health, 128)
			done := make(chan error, 1)
			go func() {
				first := true
				done <- a.Stream(ctx, domain.FeedKey{Exchange: provider, Market: market, Instrument: instrument, Timeframe: "1m"}, func(_ []domain.Candle, health domain.Health) {
					if first {
						first = false
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
							return
						}
					}
					select {
					case observations <- health:
					case <-ctx.Done():
					}
				})
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatal("stream failed before initial emission", err)
			case <-time.After(time.Second):
				t.Fatal("stream did not reach initial emission")
			}
			route := func(price int) {
				if provider == "hyperliquid" {
					h.route([]byte(fmt.Sprintf(`{"channel":"l2Book","data":{"coin":"BTC","time":%d,"levels":[[{"px":"%d","sz":"2","n":1}],[{"px":"%d","sz":"3","n":1}]]}}`, time.Now().UnixMilli(), price, price+1)))
				} else {
					h.route([]byte(fmt.Sprintf(`{"u":%d,"s":"BTCUSDT","b":"%d","B":"2","a":"%d","A":"3"}`, price, price, price+1)))
				}
			}
			// Both queues contain transport markers, with an older book still at
			// the front of the quote queue. Selection between queues is arbitrary.
			route(100)
			h.publish("", wireEvent{state: "disconnected"})
			h.publish("", wireEvent{state: "connecting"})
			route(200)
			close(release)
			deadline := time.After(2 * time.Second)
			seenCurrent := false
			for !seenCurrent {
				select {
				case health := <-observations:
					if health.Quote != nil && health.Quote.Bid != nil {
						if *health.Quote.Bid == 100 {
							t.Fatal("pre-disconnect quote was emitted again as current", health)
						}
						seenCurrent = *health.Quote.Bid == 200
					}
				case <-deadline:
					t.Fatal("new connection quote was never emitted")
				}
			}
			// Drain both control queues before proving that a candle-only repair
			// keeps a valid book from the still-connected quote transport.
			until := time.Now().Add(time.Second)
			for a.Diagnostics().QueueDepth != 0 && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
			if a.Diagnostics().QueueDepth != 0 {
				t.Fatal("transport control queues did not drain")
			}
			route(300)
			seenLatest := false
			for !seenLatest {
				select {
				case health := <-observations:
					seenLatest = health.Quote != nil && health.Quote.Bid != nil && *health.Quote.Bid == 300
				case <-deadline:
					t.Fatal("latest connected book was never emitted")
				}
			}
			h.publish(candleTopic, wireEvent{state: "reconciling", reason: "Repairing candle history"})
			seenRepairQuote := false
			for !seenRepairQuote {
				select {
				case health := <-observations:
					seenRepairQuote = health.State == "reconciling" && health.Quote != nil && health.Quote.Bid != nil && *health.Quote.Bid == 300
				case <-deadline:
					t.Fatal("candle history repair hid a valid connected book")
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("stream ignored cancellation")
			}
		})
	}
}
