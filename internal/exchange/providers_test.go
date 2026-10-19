package exchange

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Synthetic wire envelopes follow the current official schemas, reviewed 2026-10-19:
// Binance rest-api.md and web-socket-streams.md (binance/binance-spot-api-docs).
// https://bybit-exchange.github.io/docs/v5/market/kline
// https://bybit-exchange.github.io/docs/v5/websocket/public/kline
// https://docs.cdp.coinbase.com/api-reference/advanced-trade-api/advanced-trade-asyncapi.json
// https://docs.cdp.coinbase.com/api-reference/advanced-trade-api/rest-api/public/get-public-product-candles
// Values below are deliberately synthetic; these are not recorded live responses.
func TestProviderHistoriesAllIntervals(t *testing.T) {
	for _, provider := range []string{"binance", "bybit", "coinbase"} {
		for _, interval := range domain.Intervals {
			if provider == "coinbase" && interval == "1w" {
				continue // Derived daily-to-weekly history has separate coverage.
			}
			t.Run(provider+"/"+interval, func(t *testing.T) {
				d := TimeframeSeconds(interval)
				start := int64(1792368000)
				symbol := "BTCUSDT"
				if provider == "coinbase" {
					symbol = "BTC-USD"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					q := r.URL.Query()
					w.Header().Set("Content-Type", "application/json")
					switch provider {
					case "binance":
						if q.Get("interval") != interval || q.Get("symbol") != symbol {
							t.Errorf("bad Binance params %v", q)
						}
						fmt.Fprintf(w, `[[%d,"100.123456789","103","99","101","3.25",%d],[%d,"100","102","99","101","2.25",%d],[%d,"100","102","99","101","2.25",%d]]`, (start+d)*1000, (start+2*d)*1000-1, start*1000, (start+d)*1000-1, start*1000, (start+d)*1000-1)
					case "bybit":
						if q.Get("category") != "spot" || q.Get("interval") != bybitIntervals[interval] {
							t.Errorf("bad Bybit params %v", q)
						}
						fmt.Fprintf(w, `{"retCode":0,"result":{"category":"spot","list":[["%d","100.123456789","103","99","101","3.25","999"],["%d","100","102","99","101","2.25","999"],["%d","100","102","99","101","2.25","999"]]}}`, (start+d)*1000, start*1000, start*1000)
					case "coinbase":
						if q.Get("granularity") != coinbaseIntervals[interval] || q.Get("limit") != "350" {
							t.Errorf("bad Coinbase params %v", q)
						}
						fmt.Fprintf(w, `{"candles":[{"start":"%d","open":"100.123456789","high":"103","low":"99","close":"101","volume":"3.25"},{"start":"%d","open":"100","high":"102","low":"99","close":"101","volume":"2.25"},{"start":"%d","open":"100","high":"102","low":"99","close":"101","volume":"2.25"}]}`, start+d, start, start)
					}
				}))
				defer server.Close()
				generic, _ := New(provider)
				a := generic.(*publicAdapter)
				a.base = server.URL
				bars, e := a.History(context.Background(), domain.FeedKey{Exchange: provider, Market: "spot", Instrument: symbol, Timeframe: interval}, start, start+2*d, 1000)
				if e != nil {
					t.Fatal(e)
				}
				if len(bars) != 2 || bars[0].Time != start || bars[1].Time != start+d || bars[1].Volume != 3.25 || bars[1].Open != 100.123456789 {
					t.Fatalf("bad OHLCV: %+v", bars)
				}
			})
		}
	}
}
func TestNativeEnvelopesAllIntervals(t *testing.T) {
	for _, interval := range domain.Intervals {
		for _, provider := range []string{"binance", "bybit"} {
			t.Run(provider+"/"+interval, func(t *testing.T) {
				k := domain.FeedKey{Exchange: provider, Market: "spot", Instrument: "BTCUSDT", Timeframe: interval}
				var wire string
				if provider == "binance" {
					wire = fmt.Sprintf(`{"stream":"btcusdt@kline_%s","data":{"e":"kline","E":1792368001000,"s":"BTCUSDT","k":{"t":1792368000000,"i":"%s","o":"100","h":"103","l":"99","c":"102","v":"5","x":true}}}`, interval, interval)
				} else {
					wire = fmt.Sprintf(`{"topic":"kline.%s.BTCUSDT","ts":1792368001000,"data":[{"start":1792368000000,"interval":"%s","open":"100","high":"103","low":"99","close":"102","volume":"5","turnover":"999","confirm":true,"timestamp":1792368000500}]}`, bybitIntervals[interval], bybitIntervals[interval])
				}
				out, e := ParseNative(provider, []byte(wire), k)
				if e != nil {
					t.Fatal(e)
				}
				if len(out) != 1 || out[0].Time != 1792368000 || out[0].Volume != 5 || !out[0].Closed || out[0].SourceRevision != 1792368001000 {
					t.Fatalf("wrong wire mapping: %+v", out)
				}
			})
		}
	}
}
func TestCoinbaseTradeEnvelopeAllIntervals(t *testing.T) {
	wire := []byte(`{"channel":"market_trades","timestamp":"2026-10-19T00:00:00Z","sequence_num":1,"events":[{"type":"update","trades":[{"trade_id":"2","product_id":"BTC-USD","price":"101","size":"0.2","side":"SELL","time":"2026-10-19T00:00:45Z"},{"trade_id":"1","product_id":"BTC-USD","price":"100","size":"0.1","side":"BUY","time":"2026-10-19T00:00:05Z"},{"trade_id":"1","product_id":"BTC-USD","price":"100","size":"0.1","side":"BUY","time":"2026-10-19T00:00:05Z"}]}]}`)
	trades, e := ParseTrades(wire, "BTC-USD")
	if e != nil {
		t.Fatal(e)
	}
	for _, interval := range domain.Intervals {
		a := NewTradeAggregator(TimeframeSeconds(interval), 1792367940)
		var c domain.Candle
		for _, event := range trades {
			v, changed, e := a.Apply(event)
			if e != nil {
				t.Fatal(e)
			}
			if changed {
				c = v
			}
		}
		if c.Open != 100 || c.Close != 101 || c.Volume != 0.3 {
			t.Fatalf("%s wrong trade aggregation %+v", interval, c)
		}
	}
}
func TestErrorBodiesAndRetryCancellation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, kind string
	}{{"restricted", 451, `{}`, "restricted"}, {"rate", 429, `{}`, "rateLimited"}, {"bybit", 200, `{"retCode":10006,"retMsg":"Too many visits"}`, "rateLimited"}, {"binance", 200, `{"code":-1121,"msg":"Invalid symbol"}`, "unknownInstrument"}, {"coinbase", 200, `{"error":"NOT_FOUND","message":"Unknown product"}`, "offline"}} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			generic, _ := New("binance")
			a := generic.(*publicAdapter)
			a.base = server.URL
			var dst any
			err := a.get(context.Background(), "/test", nil, &dst)
			if ErrorKind(err) != tc.kind {
				t.Fatalf("got %v kind %s", err, ErrorKind(err))
			}
			if hits.Load() != 1 {
				t.Fatal("restriction or rate limit retried")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, time.Minute); err != context.Canceled {
		t.Fatalf("cancellation %v", err)
	}
}
func TestCoinbaseAliasRejectedAndExceptions(t *testing.T) {
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	k := domain.FeedKey{Exchange: "coinbase", Market: "spot", Instrument: "BTC-USDC", Timeframe: "1m"}
	if err := a.validate(k); ErrorKind(err) != "unknownInstrument" || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("alias silently substituted: %v", err)
	}
	for _, id := range []string{"BTC-USD", "USDT-USDC", "EURC-USDC"} {
		k.Instrument = id
		if e := a.validate(k); e != nil {
			t.Fatal(e)
		}
	}
}
func TestInstrumentDiscoveryMetadataCacheAndCopies(t *testing.T) {
	for _, provider := range []string{"binance", "bybit", "coinbase"} {
		t.Run(provider, func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				switch provider {
				case "binance":
					fmt.Fprint(w, `{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","isSpotTradingAllowed":true,"filters":[{"filterType":"PRICE_FILTER","tickSize":"0.01"},{"filterType":"LOT_SIZE","stepSize":"0.0001"}]}]}`)
				case "bybit":
					if r.URL.Query().Get("category") != "spot" {
						t.Error("not spot")
					}
					fmt.Fprint(w, `{"retCode":0,"result":{"list":[{"symbol":"BTCUSDT","status":"Trading","baseCoin":"BTC","quoteCoin":"USDT","priceFilter":{"tickSize":"0.01"},"lotSizeFilter":{"basePrecision":"0.0001"}}]}}`)
				case "coinbase":
					json.NewEncoder(w).Encode(map[string]any{"products": []map[string]any{{"product_id": "BTC-USDC", "product_type": "SPOT", "base_currency_id": "BTC", "quote_currency_id": "USDC", "price_increment": "0.01", "base_increment": "0.0001", "status": "online"}}})
				}
			}))
			defer server.Close()
			generic, _ := New(provider)
			a := generic.(*publicAdapter)
			a.base = server.URL
			items, e := a.Instruments(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if len(items) != 1 || items[0].PriceIncrement != "0.01" || items[0].SizeIncrement != "0.0001" {
				t.Fatalf("bad metadata %+v", items)
			}
			items[0].SupportedIntervals[0] = "oops"
			if provider == "coinbase" {
				if items[0].Availability != "unsupported-public-alias" {
					t.Fatal("alias discovery not labelled")
				}
				items[0].ProviderMetadata["publicAlias"] = "oops"
			}
			again, e := a.Instruments(context.Background())
			if e != nil || hits != 1 || again[0].SupportedIntervals[0] != "1m" || again[0].ProviderMetadata["publicAlias"] == "oops" {
				t.Fatalf("cache mutation or duplicate fetch: %+v %v hits%d", again, e, hits)
			}
		})
	}
}
func TestCoinbaseCursorPaginationAndNoProgress(t *testing.T) {
	for _, stall := range []bool{false, true} {
		t.Run(fmt.Sprint(stall), func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if hits == 1 {
					fmt.Fprint(w, `{"products":[{"product_id":"BTC-USD","product_type":"SPOT","base_currency_id":"BTC","quote_currency_id":"USD"}],"pagination":{"has_next":true,"next_cursor":"cursor-2"}}`)
				} else {
					if r.URL.Query().Get("cursor") != "cursor-2" || r.URL.Query().Has("offset") {
						t.Errorf("cursor query wrong: %s", r.URL.RawQuery)
					}
					if stall {
						fmt.Fprint(w, `{"products":[],"pagination":{"has_next":true,"next_cursor":"cursor-2"}}`)
					} else {
						fmt.Fprint(w, `{"products":[{"product_id":"ETH-USD","product_type":"SPOT","base_currency_id":"ETH","quote_currency_id":"USD"}],"pagination":{"has_next":false}}`)
					}
				}
			}))
			defer server.Close()
			generic, _ := New("coinbase")
			a := generic.(*publicAdapter)
			a.base = server.URL
			items, e := a.Instruments(context.Background())
			if stall {
				if e == nil {
					t.Fatal("non-progressing pagination accepted")
				}
			} else if e != nil || len(items) != 2 {
				t.Fatalf("cursor failed %+v %v", items, e)
			}
			if hits != 2 {
				t.Fatalf("unbounded cursor requests %d", hits)
			}
		})
	}
}
func TestRateLimitRetryAfterSuppressesFurtherRequests(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}))
	defer server.Close()
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	a.base = server.URL
	for i := 0; i < 2; i++ {
		var out any
		err := a.get(context.Background(), "/limited", nil, &out)
		if ErrorKind(err) != "rateLimited" || RetryAt(err) <= time.Now().Unix() {
			t.Fatalf("retry policy lost: %v", err)
		}
	}
	if hits != 1 {
		t.Fatalf("ignored Retry-After; made %d requests", hits)
	}
}
func TestReconnectHistoryPaginationOver350Buckets(t *testing.T) {
	start := int64(1792368000)
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		var from, to int64
		fmt.Sscan(r.URL.Query().Get("start"), &from)
		fmt.Sscan(r.URL.Query().Get("end"), &to)
		if to-from > 350*60 {
			t.Errorf("provider page exceeded max: %d", to-from)
		}
		fmt.Fprint(w, `{"candles":[`)
		for bucket := from; bucket < to; bucket += 60 {
			if bucket != from {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"start":"%d","open":"100","high":"101","low":"99","close":"100","volume":"1"}`, bucket)
		}
		fmt.Fprint(w, `]}`)
	}))
	defer server.Close()
	generic, _ := New("coinbase")
	a := generic.(*publicAdapter)
	a.base = server.URL
	k := domain.FeedKey{Exchange: "coinbase", Market: "spot", Instrument: "BTC-USD", Timeframe: "1m"}
	bars, clipped, e := a.reconcileHistory(context.Background(), k, start, start+701*60)
	if e != nil || clipped || len(bars) != 701 || hits != 3 {
		t.Fatalf("large gap not repaired: bars%d clipped%v requests%d err%v", len(bars), clipped, hits, e)
	}
	for i, c := range bars {
		if c.Time != start+int64(i)*60 {
			t.Fatal("noncontiguous fixture repair")
		}
	}
}
