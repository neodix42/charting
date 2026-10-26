package exchange

import (
	"context"
	"cryptodesk/internal/domain"
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

func TestHubSequenceCheckedBeforeProductRoutingAndOverflow(t *testing.T) {
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	h := a.hub
	ch := make(chan wireEvent, 512)
	h.subs["BTC-USD"] = map[uint64]chan wireEvent{1: ch}
	h.route([]byte(`{"channel":"market_trades","sequence_num":1,"events":[{"trades":[{"product_id":"BTC-USD"}]}]}`))
	h.route([]byte(`{"channel":"heartbeats","sequence_num":2,"events":[]}`))
	h.route([]byte(`{"channel":"market_trades","sequence_num":4,"events":[{"trades":[{"product_id":"BTC-USD"}]}]}`))
	gap := false
	for len(ch) > 0 {
		e := <-ch
		if strings.Contains(e.reason, "sequence gap") {
			gap = true
		}
	}
	if !gap {
		t.Fatal("connection sequence gap not surfaced")
	}
	for i := 0; i < 514; i++ {
		h.publish("BTC-USD", wireEvent{data: []byte(`{}`)})
	}
	overflow := false
	for len(ch) > 0 {
		e := <-ch
		if strings.Contains(e.reason, "overflow") {
			overflow = true
		}
	}
	if !overflow {
		t.Fatal("bounded buffer overflow not surfaced")
	}
}

type socketTestTransport func(*http.Request) (*http.Response, error)

func (f socketTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSharedProviderSocketAndSubscribeFirstHandover(t *testing.T) {
	var socketCount atomic.Int32
	var initialREST atomic.Bool
	subscriptions := map[string]chan struct{}{"1m": make(chan struct{}), "5m": make(chan struct{})}
	historyStarted, stopping := make(chan struct{}), make(chan struct{})
	var historyOnce sync.Once
	var mu sync.Mutex
	var client *websocket.Conn
	upgrader := websocket.Upgrader{}
	start := time.Now().Unix() / 60 * 60
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			conn, e := upgrader.Upgrade(w, r, nil)
			if e != nil {
				return
			}
			defer conn.Close()
			socketCount.Add(1)
			mu.Lock()
			client = conn
			mu.Unlock()
			// WebSocket and HTTP use independent connections. Deliberately let
			// HTTP arrive first even though the client has already written SUBSCRIBE.
			select {
			case <-historyStarted:
			case <-stopping:
				return
			}
			for {
				var msg struct {
					Method string
					Params []string
				}
				if conn.ReadJSON(&msg) != nil {
					return
				}
				if msg.Method == "SUBSCRIBE" {
					for _, topic := range msg.Params {
						interval := strings.TrimPrefix(topic, "btcusdt@kline_")
						select {
						case <-subscriptions[interval]:
						default:
							close(subscriptions[interval])
						}
						bucket := start / TimeframeSeconds(interval) * TimeframeSeconds(interval)
						_ = conn.WriteJSON(map[string]any{"e": "kline", "E": time.Now().UnixMilli(), "s": "BTCUSDT", "k": map[string]any{"t": bucket * 1000, "i": interval, "o": "100", "h": "102", "l": "99", "c": "101", "v": "6", "x": false}})
					}
				}
			}
		}
		if r.URL.Path == "/api/v3/klines" {
			historyOnce.Do(func() { close(historyStarted) })
			select {
			case <-subscriptions[r.URL.Query().Get("interval")]:
			case <-time.After(3 * time.Second):
				t.Error("upstream subscription was not delivered")
				return
			case <-r.Context().Done():
				return
			}
			initialREST.Store(true)
			bucket := start / TimeframeSeconds(r.URL.Query().Get("interval")) * TimeframeSeconds(r.URL.Query().Get("interval"))
			fmt.Fprintf(w, `[[%d,"100","102","99","101","5",%d]]`, bucket*1000, (bucket+60)*1000-1)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	defer close(stopping)
	defer func() {
		mu.Lock()
		if client != nil {
			client.Close()
		}
		mu.Unlock()
	}()
	generic, _ := New("binance")
	a := generic.(*publicAdapter)
	a.base = server.URL
	a.ws = "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	a.client.Transport = socketTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v3/klines" {
			topic := "btcusdt@kline_" + r.URL.Query().Get("interval")
			a.hub.mu.Lock()
			written := a.hub.active[topic]
			a.hub.mu.Unlock()
			if !written {
				t.Errorf("history requested before writing subscription for %s", topic)
			}
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan domain.Candle, 32)
	done := make(chan error, 2)
	for _, interval := range []string{"1m", "5m"} {
		k := domain.FeedKey{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: interval}
		go func() {
			done <- a.Stream(ctx, k, func(b []domain.Candle, h domain.Health) {
				for _, c := range b {
					results <- c
				}
			})
		}()
	}
	received := 0
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for received < 2 {
		select {
		case c := <-results:
			if c.Volume != 5 && c.Volume != 6 {
				t.Fatalf("cumulative volume summed: %+v", c)
			}
			received++
		case <-timeout.C:
			t.Fatal("shared streams failed to hand over history")
		}
	}
	if !initialREST.Load() || socketCount.Load() != 1 || a.Diagnostics().Sockets != 1 {
		t.Fatalf("expected 1 shared provider socket: sockets%d diag%+v", socketCount.Load(), a.Diagnostics())
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("stream ignored cancellation")
		}
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && a.Diagnostics().Sockets != 0 {
		time.Sleep(time.Millisecond * 5)
	}
	if a.Diagnostics().Sockets != 0 {
		t.Fatal("last unsubscribe retained socket")
	}
}
func TestSharedExistingCoinbaseTopicStartsHistoryForNewInterval(t *testing.T) {
	var sockets atomic.Int32
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			c, e := upgrader.Upgrade(w, r, nil)
			if e != nil {
				return
			}
			sockets.Add(1)
			defer c.Close()
			for {
				if _, _, e = c.ReadMessage(); e != nil {
					return
				}
			}
		}
		fmt.Fprint(w, `{"candles":[]}`)
	}))
	defer server.Close()
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	a.base = server.URL
	a.ws = "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 4)
	done := make(chan error, 2)
	attach := func(interval string) {
		go func() {
			done <- a.Stream(ctx, domain.FeedKey{Exchange: "coinbase", Market: "spot", Instrument: "BTC-USD", Timeframe: interval}, func(b []domain.Candle, h domain.Health) {
				if h.State == "live" {
					select {
					case ready <- interval:
					default:
					}
				}
			})
		}()
	}
	attach("1m")
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("first interval did not reconcile")
	}
	attach("4h")
	select {
	case interval := <-ready:
		if interval != "4h" {
			t.Fatal("unexpected existing interval update")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new interval on existing raw product never fetched history")
	}
	if sockets.Load() != 1 {
		t.Fatal("Coinbase product duplicated upstream")
	}
	cancel()
	for i := 0; i < 2; i++ {
		<-done
	}
}
func TestOverflowConcurrentConsumerCannotDeadlock(t *testing.T) {
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	h := a.hub
	ch := make(chan wireEvent, 2)
	h.subs["BTC-USD"] = map[uint64]chan wireEvent{1: ch}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
			}
		}
	}()
	go func() {
		for i := 0; i < 20000; i++ {
			h.publish("BTC-USD", wireEvent{data: []byte(`{}`)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("overflow drain deadlocked against concurrent consumer")
	}
}
func TestReconnectRepairsTwoMissingBuckets(t *testing.T) {
	var sockets atomic.Int32
	var historyCount atomic.Int32
	upgrader := websocket.Upgrader{}
	start := time.Now().Unix() / 60 * 60
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			conn, e := upgrader.Upgrade(w, r, nil)
			if e != nil {
				return
			}
			defer conn.Close()
			connection := sockets.Add(1)
			var request map[string]any
			if conn.ReadJSON(&request) != nil {
				return
			}
			if connection == 1 {
				time.Sleep(30 * time.Millisecond)
				return
			}
			for {
				if _, _, e = conn.ReadMessage(); e != nil {
					return
				}
			}
		}
		n := historyCount.Add(1)
		if n == 1 {
			fmt.Fprintf(w, `[[%d,"100","101","99","100","1",%d]]`, (start-120)*1000, (start-60)*1000-1)
		} else {
			fmt.Fprintf(w, `[[%d,"100","101","99","100","1",%d],[%d,"100","102","99","101","2",%d],[%d,"101","103","100","102","3",%d]]`, (start-120)*1000, (start-60)*1000-1, (start-60)*1000, start*1000-1, start*1000, (start+60)*1000-1)
		}
	}))
	defer server.Close()
	generic, _ := New("binance")
	a := generic.(*publicAdapter)
	a.base = server.URL
	a.ws = "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan map[int64]domain.Candle, 1)
	done := make(chan error, 1)
	all := map[int64]domain.Candle{}
	gap := false
	go func() {
		done <- a.Stream(ctx, domain.FeedKey{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1m"}, func(b []domain.Candle, h domain.Health) {
			if h.State == "reconciling" && strings.Contains(h.Reason, "interrupted") {
				gap = true
			}
			for _, c := range b {
				all[c.Time] = c
			}
			if len(all) == 3 {
				select {
				case result <- all:
				default:
				}
			}
		})
	}()
	select {
	case bars := <-result:
		if len(bars) != 3 || bars[start-60].Volume != 2 || bars[start].Volume != 3 {
			t.Fatalf("repair %+v", bars)
		}
		cancel()
		<-done
		if !gap {
			t.Fatal("reconnect not announced")
		}
	case <-ctx.Done():
		t.Fatal("missing candles not repaired after reconnect")
	}
}

// Public Binance wire shape observed 2026-10-19; this regression retains all
// case-distinct keys (t/T, l/L, v/V, e/E). Values are public market data.
func TestBinanceHubAcceptsCaseSensitiveEventTimestamp(t *testing.T) {
	generic, _ := New("binance")
	a := generic.(*publicAdapter)
	ch := make(chan wireEvent, 4)
	a.hub.subs["btcusdt@kline_1m"] = map[uint64]chan wireEvent{1: ch}
	wire := []byte(`{"e":"kline","E":1792427564035,"s":"BTCUSDT","k":{"t":1792427520000,"T":1792427579999,"s":"BTCUSDT","i":"1m","f":6775323655,"L":6775325614,"o":"82026.92000000","c":"82018.01000000","h":"82055.88000000","l":"82018.00000000","v":"6.19235000","n":1960,"x":false,"q":"507991.74543730","V":"2.79996000","Q":"229698.40426700","B":"0"}}`)
	a.hub.route(wire)
	parsed, err := ParseNative("binance", wire, domain.FeedKey{Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1m"})
	if err != nil || len(parsed) != 1 || parsed[0].Time != 1792427520 || parsed[0].Volume != 6.19235 || parsed[0].Low != 82018 {
		t.Fatalf("case-distinct Binance fields misparsed: %+v %v", parsed, err)
	}
	select {
	case e := <-ch:
		if string(e.data) != string(wire) || e.state != "" {
			t.Fatalf("real kline not routed: %+v", e)
		}
	default:
		t.Fatal("kline was dropped")
	}
}
