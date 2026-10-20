package exchange

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Synthetic public wire fixtures, reviewed 2026-10-20 against:
// https://developers.binance.com/docs/derivatives/usds-margined-futures/market-data/rest-api/Kline-Candlestick-Data
// https://developers.binance.com/docs/derivatives/coin-margined-futures/market-data/rest-api/Kline-Candlestick-Data
// https://bybit-exchange.github.io/docs/v5/market/kline
// https://bybit-exchange.github.io/docs/v5/websocket/public/kline
// Inverse volume is deliberately different from contract/quote turnover.
func TestPerpetualHistoryAndNativeVolumeAllIntervals(t *testing.T) {
	for _, provider := range []string{"binance", "bybit"} {
		for _, category := range []string{"linear", "inverse"} {
			for _, interval := range domain.Intervals {
				t.Run(provider+"/"+category+"/"+interval, func(t *testing.T) {
					start := BucketStart(1792368000, TimeframeSeconds(interval))
					d := TimeframeSeconds(interval)
					symbol := "XAGUSDT"
					if category == "inverse" {
						symbol = "BTCUSD"
						if provider == "binance" {
							symbol += "_PERP"
						}
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						q := r.URL.Query()
						if q.Get("symbol") != symbol {
							t.Errorf("wrong symbol: %v", q)
						}
						if provider == "binance" {
							path := "/fapi/v1/klines"
							volume, other := "3.25", "99999"
							if category == "inverse" {
								path, volume, other = "/dapi/v1/klines", "99999", "3.25"
							}
							if r.URL.Path != path || q.Get("interval") != interval {
								t.Errorf("wrong Binance endpoint/interval: %s %v", r.URL.Path, q)
							}
							fmt.Fprintf(w, `[[%d,"100","103","99","101","%s",%d,"%s"]]`, start*1000, volume, (start+d)*1000-1, other)
						} else {
							if r.URL.Path != "/v5/market/kline" || q.Get("category") != category || q.Get("interval") != bybitIntervals[interval] {
								t.Errorf("wrong Bybit endpoint/category: %s %v", r.URL.Path, q)
							}
							volume, turnover := "3.25", "99999"
							if category == "inverse" {
								volume, turnover = "99999", "3.25"
							}
							fmt.Fprintf(w, `{"retCode":0,"result":{"list":[["%d","100","103","99","101","%s","%s"]]}}`, start*1000, volume, turnover)
						}
					}))
					defer server.Close()
					generic, _ := New(provider)
					a := generic.(*publicAdapter)
					a.base, a.futuresBase, a.inverseBase = server.URL, server.URL, server.URL
					key := domain.FeedKey{Exchange: provider, Market: "perpetual", Instrument: symbol, Timeframe: interval}
					bars, err := a.History(context.Background(), key, start, start+d, 300)
					if err != nil || len(bars) != 1 || bars[0].Volume != 3.25 {
						t.Fatalf("wrong historical base volume: %+v %v", bars, err)
					}
					var wire string
					if provider == "binance" {
						volume, other := "3.25", "99999"
						if category == "inverse" {
							volume, other = "99999", "3.25"
						}
						wire = fmt.Sprintf(`{"e":"kline","E":1792368001000,"s":%q,"k":{"t":%d,"T":%d,"i":%q,"o":"100","h":"103","l":"99","c":"101","v":%q,"V":"123","q":%q,"Q":"987","x":true}}`, symbol, start*1000, (start+d)*1000-1, interval, volume, other)
					} else {
						volume, turnover := "3.25", "99999"
						if category == "inverse" {
							volume, turnover = "99999", "3.25"
						}
						wire = fmt.Sprintf(`{"topic":"kline.%s.%s","ts":1792368001000,"data":[{"start":%d,"interval":%q,"open":"100","high":"103","low":"99","close":"101","volume":%q,"turnover":%q,"confirm":true}]}`, bybitIntervals[interval], symbol, start*1000, bybitIntervals[interval], volume, turnover)
					}
					bars, err = ParseNative(provider, []byte(wire), key)
					if err != nil || len(bars) != 1 || bars[0].Volume != 3.25 || !bars[0].Closed {
						t.Fatalf("wrong live base volume: %+v %v", bars, err)
					}
				})
			}
		}
	}
}

func TestBinanceAllMarketsDiscovery(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/api/v3/exchangeInfo":
			if r.URL.Query().Get("permissions") != "SPOT" {
				t.Error("spot query omitted permissions")
			}
			fmt.Fprint(w, `{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","isSpotTradingAllowed":true,"filters":[{"filterType":"PRICE_FILTER","tickSize":"0.01"}]}]}`)
		case "/fapi/v1/exchangeInfo":
			fmt.Fprint(w, `{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","contractType":"PERPETUAL","baseAsset":"BTC","quoteAsset":"USDT","filters":[{"filterType":"PRICE_FILTER","tickSize":"0.1"}]},{"symbol":"XAGUSDT","status":"TRADING","contractType":"TRADIFI_PERPETUAL","baseAsset":"XAG","quoteAsset":"USDT"},{"symbol":"BTCUSDT_261225","status":"TRADING","contractType":"CURRENT_QUARTER"},{"symbol":"OLDUSDT","status":"SETTLING","contractType":"PERPETUAL"}]}`)
		case "/dapi/v1/exchangeInfo":
			fmt.Fprint(w, `{"symbols":[{"symbol":"BTCUSD_PERP","contractStatus":"TRADING","contractType":"PERPETUAL","baseAsset":"BTC","quoteAsset":"USD"},{"symbol":"BTCUSD_261225","contractStatus":"TRADING","contractType":"NEXT_QUARTER"}]}`)
		default:
			t.Errorf("unexpected catalog endpoint: %s", r.URL)
		}
	}))
	defer server.Close()
	generic, _ := New("binance")
	a := generic.(*publicAdapter)
	a.base, a.futuresBase, a.inverseBase = server.URL, server.URL, server.URL
	items, err := a.Instruments(context.Background())
	if err != nil || len(items) != 4 || hits.Load() != 3 {
		t.Fatalf("combined catalog: %+v %v hits=%d", items, err, hits.Load())
	}
	var bitcoin []domain.Instrument
	for _, item := range items {
		if item.NativeID == "BTCUSDT" {
			bitcoin = append(bitcoin, item)
		}
		if item.NativeID == "XAGUSDT" && item.Market != "perpetual" {
			t.Fatal("silver perpetual mislabeled")
		}
	}
	if len(bitcoin) != 2 || bitcoin[0].Market != "spot" || bitcoin[1].Market != "perpetual" || bitcoin[0].PriceIncrement == bitcoin[1].PriceIncrement {
		t.Fatalf("same symbol metadata mixed across markets: %+v", bitcoin)
	}
}

func TestBybitDerivativeDiscoveryPaginationAndExclusions(t *testing.T) {
	var pages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("category") == "spot" {
			if q.Has("cursor") || q.Has("limit") {
				t.Error("spot was paginated")
			}
			fmt.Fprint(w, `{"retCode":0,"result":{"list":[{"symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"Trading"}]}}`)
			return
		}
		if q.Get("status") != "Trading" || q.Get("limit") != "1000" {
			t.Errorf("derivatives query: %v", q)
		}
		if q.Get("category") == "inverse" {
			fmt.Fprint(w, `{"retCode":0,"result":{"list":[{"symbol":"BTCUSD","baseCoin":"BTC","quoteCoin":"USD","contractType":"InversePerpetual","status":"Trading"},{"symbol":"BTCUSDZ26","contractType":"InverseFutures","status":"Trading"}]}}`)
			return
		}
		pages.Add(1)
		list := []map[string]any{}
		next := ""
		if q.Get("cursor") == "" {
			// More than the default 500 entries must remain searchable.
			for i := 0; i < 1000; i++ {
				list = append(list, map[string]any{"symbol": fmt.Sprintf("COIN%dUSDT", i), "baseCoin": "COIN", "quoteCoin": "USDT", "status": "Trading", "contractType": "LinearPerpetual"})
			}
			next = "cursor%encoded&next"
		} else if q.Get("cursor") == "cursor%encoded&next" {
			list = append(list, map[string]any{"symbol": "BTCUSDT", "baseCoin": "BTC", "quoteCoin": "USDT", "status": "Trading", "contractType": "LinearPerpetual"}, map[string]any{"symbol": "XAGUSDT", "baseCoin": "XAG", "quoteCoin": "USDT", "status": "Trading", "contractType": "LinearPerpetual"}, map[string]any{"symbol": "BTCUSDT_261225", "status": "Trading", "contractType": "LinearFutures"}, map[string]any{"symbol": "OLDUSDT", "status": "Settled", "contractType": "LinearPerpetual"})
		} else {
			t.Errorf("cursor changed: %q", q.Get("cursor"))
		}
		json.NewEncoder(w).Encode(map[string]any{"retCode": 0, "result": map[string]any{"list": list, "nextPageCursor": next}})
	}))
	defer server.Close()
	generic, _ := New("bybit")
	a := generic.(*publicAdapter)
	a.base = server.URL
	items, err := a.Instruments(context.Background())
	if err != nil || len(items) != 1004 || pages.Load() != 2 {
		t.Fatalf("paginated catalog: items=%d pages=%d err=%v", len(items), pages.Load(), err)
	}
	markets := map[string]int{}
	for _, item := range items {
		markets[item.Market]++
		if strings.Contains(item.NativeID, "261225") || item.NativeID == "OLDUSDT" || item.NativeID == "BTCUSDZ26" {
			t.Fatalf("dated/settled contract exposed: %+v", item)
		}
	}
	if markets["spot"] != 1 || markets["perpetual"] != 1003 {
		t.Fatalf("wrong market labels: %v", markets)
	}
}

func TestPartialCatalogWarningsRetryAndFullFailure(t *testing.T) {
	for _, provider := range []string{"binance", "bybit"} {
		t.Run(provider, func(t *testing.T) {
			var fixed atomic.Bool
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				isSpot := r.URL.Path == "/api/v3/exchangeInfo" || r.URL.Query().Get("category") == "spot"
				if !isSpot && !fixed.Load() {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if provider == "binance" {
					if isSpot {
						fmt.Fprint(w, `{"symbols":[{"symbol":"BTCUSDT","isSpotTradingAllowed":true}]}`)
					} else {
						fmt.Fprint(w, `{"symbols":[]}`)
					}
				} else if isSpot {
					fmt.Fprint(w, `{"retCode":0,"result":{"list":[{"symbol":"BTCUSDT","status":"Trading"}]}}`)
				} else {
					fmt.Fprint(w, `{"retCode":0,"result":{"list":[]}}`)
				}
			}))
			defer server.Close()
			generic, _ := New(provider)
			a := generic.(*publicAdapter)
			a.base, a.futuresBase, a.inverseBase = server.URL, server.URL, server.URL
			items, err := a.Instruments(context.Background())
			if err != nil || len(items) != 1 || !strings.Contains(items[0].ProviderMetadata["catalogWarning"], "restricted") {
				t.Fatalf("working spot missing or incomplete catalog hidden: %+v %v", items, err)
			}
			fixed.Store(true)
			items, err = a.Instruments(context.Background())
			if err != nil || len(items) != 1 || items[0].ProviderMetadata["catalogWarning"] != "" || hits.Load() != 6 {
				t.Fatalf("partial failure was cached: %+v %v hits=%d", items, err, hits.Load())
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(451) }))
	defer server.Close()
	generic, _ := New("binance")
	a := generic.(*publicAdapter)
	a.base, a.futuresBase, a.inverseBase = server.URL, server.URL, server.URL
	items, err := a.Instruments(context.Background())
	if len(items) != 0 || ErrorKind(err) != "restricted" {
		t.Fatalf("complete restriction lost error kind: %+v %v", items, err)
	}
}

func TestBybitNonprogressingDerivativeCursor(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"retCode":0,"result":{"list":[{"symbol":"BTCUSDT","status":"Trading","contractType":"LinearPerpetual"}],"nextPageCursor":"stalled"}}`)
	}))
	defer server.Close()
	generic, _ := New("bybit")
	a := generic.(*publicAdapter)
	a.base = server.URL
	items, err := a.bybitInstruments(context.Background(), "linear")
	if items != nil || err == nil || !strings.Contains(err.Error(), "no progress") || hits.Load() != 2 {
		t.Fatalf("unbounded or silently incomplete pagination: %+v %v hits=%d", items, err, hits.Load())
	}
}

func TestCatalogDeadlineRetainsDiscoveredMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/exchangeInfo" {
			fmt.Fprint(w, `{"symbols":[{"symbol":"BTCUSDT","isSpotTradingAllowed":true}]}`)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	generic, _ := New("binance")
	a := generic.(*publicAdapter)
	a.base, a.futuresBase, a.inverseBase = server.URL, server.URL, server.URL
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	items, err := a.Instruments(ctx)
	if err != nil || len(items) != 1 || !strings.Contains(items[0].ProviderMetadata["catalogWarning"], "deadline") {
		t.Fatalf("later category timeout hid discovered spot market: %+v %v", items, err)
	}
	if !a.fetched.IsZero() {
		t.Fatal("deadline-truncated catalog was cached")
	}
}

func TestBinanceInverseHistoryPagesWithin200Days(t *testing.T) {
	var requests atomic.Int32
	start := BucketStart(1600000000, 604800)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		q := r.URL.Query()
		from, _ := strconv.ParseInt(q.Get("startTime"), 10, 64)
		to, _ := strconv.ParseInt(q.Get("endTime"), 10, 64)
		if r.URL.Path != "/dapi/v1/klines" || to-from >= 200*86400*1000 {
			t.Errorf("COIN-M REST window exceeded: %s %v", r.URL.Path, q)
		}
		rows := [][]any{}
		for timestamp := from; timestamp <= to; timestamp += 604800000 {
			rows = append(rows, []any{timestamp, "100", "103", "99", "101", "99999", timestamp + 604800000 - 1, "3.25"})
		}
		json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	generic, _ := New("binance")
	a := generic.(*publicAdapter)
	a.inverseBase = server.URL
	key := domain.FeedKey{Exchange: "binance", Market: "perpetual", Instrument: "BTCUSD_PERP", Timeframe: "1w"}
	bars, err := a.History(context.Background(), key, start, start+300*604800, 300)
	if err != nil || len(bars) != 300 || requests.Load() != 11 || bars[299].Time != start+299*604800 || bars[299].Volume != 3.25 {
		t.Fatalf("long history truncated: bars=%d requests=%d err=%v", len(bars), requests.Load(), err)
	}
}

func TestMarketSocketsDoNotMixIdenticalSymbolsAndCleanUp(t *testing.T) {
	for _, provider := range []string{"binance", "bybit"} {
		t.Run(provider, func(t *testing.T) {
			var sockets atomic.Int32
			upgrader := websocket.Upgrader{}
			bucket := BucketStart(time.Now().Unix(), 60)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/ws/") {
					category := strings.TrimPrefix(r.URL.Path, "/ws/")
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					sockets.Add(1)
					defer conn.Close()
					var request map[string]any
					if conn.ReadJSON(&request) != nil {
						return
					}
					price, symbol, volume, other := "100", "BTCUSDT", "3.25", "99999"
					if category == "linear" {
						price = "200"
					} else if category == "inverse" {
						price, symbol, volume, other = "300", "BTCUSD", "99999", "3.25"
						if provider == "binance" {
							symbol += "_PERP"
						}
					}
					if provider == "binance" {
						conn.WriteJSON(map[string]any{"e": "kline", "E": time.Now().UnixMilli() + 1000, "s": symbol, "k": map[string]any{"t": bucket * 1000, "i": "1m", "o": price, "h": price, "l": price, "c": price, "v": volume, "q": other, "x": false}})
					} else {
						conn.WriteJSON(map[string]any{"topic": "kline.1." + symbol, "ts": time.Now().UnixMilli() + 1000, "data": []map[string]any{{"start": bucket * 1000, "interval": "1", "open": price, "high": price, "low": price, "close": price, "volume": volume, "turnover": other, "confirm": false}}})
					}
					for {
						if _, _, err = conn.ReadMessage(); err != nil {
							return
						}
					}
				}
				// No history bars; the only emitted bars must come from each socket.
				if provider == "binance" {
					fmt.Fprint(w, `[]`)
				} else {
					fmt.Fprint(w, `{"retCode":0,"result":{"list":[]}}`)
				}
			}))
			defer server.Close()
			generic, _ := New(provider)
			a := generic.(*publicAdapter)
			a.base, a.futuresBase, a.inverseBase = server.URL, server.URL, server.URL
			ws := "ws" + strings.TrimPrefix(server.URL, "http")
			a.ws, a.futuresWS, a.inverseWS = ws+"/ws/spot", ws+"/ws/linear", ws+"/ws/inverse"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type received struct {
				category string
				candle   domain.Candle
			}
			result, done := make(chan received, 16), make(chan error, 3)
			for _, category := range []string{"spot", "linear", "inverse"} {
				key := domain.FeedKey{Exchange: provider, Market: "perpetual", Instrument: "BTCUSDT", Timeframe: "1m"}
				if category == "spot" {
					key.Market = "spot"
				} else if category == "inverse" {
					key.Instrument = "BTCUSD"
					if provider == "binance" {
						key.Instrument += "_PERP"
					}
				}
				go func() {
					done <- a.Stream(ctx, key, func(bars []domain.Candle, _ domain.Health) {
						for _, bar := range bars {
							select {
							case result <- received{category, bar}:
							case <-ctx.Done():
							}
						}
					})
				}()
			}
			seen := map[string]bool{}
			deadline := time.After(5 * time.Second)
			for len(seen) < 3 {
				select {
				case item := <-result:
					want := map[string]float64{"spot": 100, "linear": 200, "inverse": 300}[item.category]
					if item.candle.Close != want || item.candle.Volume != 3.25 {
						t.Fatalf("category data crossed sockets or volume mapping failed: %+v", item)
					}
					seen[item.category] = true
				case err := <-done:
					t.Fatalf("stream failed: %v", err)
				case <-deadline:
					t.Fatal("market streams never reconciled")
				}
			}
			if sockets.Load() != 3 || a.Diagnostics().Sockets != 3 {
				t.Fatalf("markets must own separate sockets: %d %+v", sockets.Load(), a.Diagnostics())
			}
			cancel()
			for i := 0; i < 3; i++ {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("market stream did not cancel")
				}
			}
			until := time.Now().Add(time.Second)
			for a.Diagnostics().Sockets != 0 && time.Now().Before(until) {
				time.Sleep(5 * time.Millisecond)
			}
			if a.Diagnostics().Sockets != 0 {
				t.Fatal("last market unsubscribe retained sockets")
			}
		})
	}
}

func TestDocumentedPerpetualEndpoints(t *testing.T) {
	generic, _ := New("binance")
	a := generic.(*publicAdapter)
	if a.socketFor(domain.FeedKey{Market: "perpetual", Instrument: "XAGUSDT"}).endpoint() != "wss://fstream.binance.com/market/ws" {
		t.Fatal("Binance klines must use the market routed endpoint")
	}
	if a.socketFor(domain.FeedKey{Market: "perpetual", Instrument: "BTCUSD_PERP"}).endpoint() != "wss://dstream.binance.com/ws" {
		t.Fatal("Binance inverse routed to linear endpoint")
	}
	if marketCategory(domain.FeedKey{Market: "perpetual", Instrument: "BTCUSD"}) != "inverse" {
		t.Fatal("migrated inverse native ID was treated as linear")
	}
}
