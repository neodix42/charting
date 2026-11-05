package exchange

import (
	"context"
	"cryptodesk/internal/domain"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func hyperliquidTestAdapter(t *testing.T, handler http.HandlerFunc) *publicAdapter {
	t.Helper()
	var adapter *publicAdapter
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/info" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("invalid public info request: %s %s %v", r.Method, r.URL, r.Header)
		}
		// These tests exercise payloads, not wall-clock provider budget delays.
		adapter.requestMu.Lock()
		adapter.nextRequest = time.Time{}
		adapter.requestMu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	adapter = &publicAdapter{provider: "hyperliquid", base: server.URL, client: server.Client(), requestSlots: make(chan struct{}, 2)}
	return adapter
}

func TestHyperliquidDiscoveryAllSpotPairsAndPerpDexs(t *testing.T) {
	var calls atomic.Int32
	a := hyperliquidTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct{ Type, Dex string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		switch req.Type {
		case "spotMeta":
			// Token indices are deliberately sparse and out of array order.
			fmt.Fprint(w, `{"tokens":[{"name":"HYPE","index":150,"szDecimals":2,"fullName":"Hyperliquid"},{"name":"USDC","index":0,"szDecimals":8},{"name":"PURR","index":1,"szDecimals":0},{"name":"USDH","index":360,"szDecimals":6}],"universe":[{"name":"PURR/USDC","index":0,"tokens":[1,0]},{"name":"@107","index":107,"tokens":[150,0]},{"name":"@222","index":222,"tokens":[150,360]}]}`)
		case "perpDexs":
			fmt.Fprint(w, `[null,{"name":"xyz"},{"name":"cash"},{"name":"xyz"}]`)
		case "meta":
			switch req.Dex {
			case "":
				fmt.Fprint(w, `{"collateralToken":0,"universe":[{"name":"HYPE","szDecimals":2,"maxLeverage":10},{"name":"OLD","szDecimals":0,"isDelisted":true}]}`)
			case "xyz":
				fmt.Fprint(w, `{"collateralToken":0,"universe":[{"name":"xyz:TSLA","szDecimals":3,"maxLeverage":20}]}`)
			case "cash":
				fmt.Fprint(w, `{"collateralToken":360,"universe":[{"name":"cash:TSLA","szDecimals":2,"maxLeverage":10}]}`)
			default:
				t.Errorf("unexpected dex: %q", req.Dex)
			}
		default:
			t.Errorf("unexpected request: %+v", req)
		}
	})
	items, err := a.hyperliquidInstruments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 6 || calls.Load() != 5 {
		t.Fatalf("catalog incomplete or duplicate dex requested: %d rows, %d calls", len(items), calls.Load())
	}
	byID := map[string]domain.Instrument{}
	for _, item := range items {
		byID[item.NativeID] = item
		if len(item.SupportedIntervals) != len(domain.Intervals) {
			t.Errorf("missing intervals: %+v", item)
		}
	}
	if got := byID["@107"]; got.BaseAsset != "HYPE" || got.QuoteAsset != "USDC" || got.SizeIncrement != "0.01" || got.PriceIncrement != "0.000001" || got.ProviderMetadata["displaySymbol"] != "HYPE/USDC" || got.ProviderMetadata["baseFullName"] != "Hyperliquid" {
		t.Errorf("spot pair indices confused with token indices: %+v", got)
	}
	if byID["PURR/USDC"].Market != "spot" || byID["HYPE"].Market != "perpetual" || byID["@222"].QuoteAsset != "USDH" {
		t.Error("spot/perpetual or non-USDC quote mapping lost")
	}
	if got := byID["cash:TSLA"]; got.QuoteAsset != "USDH" || got.BaseAsset != "TSLA" || got.ProviderMetadata["dex"] != "cash" {
		t.Errorf("builder collateral/dex lost: %+v", got)
	}
	if _, ok := byID["OLD"]; ok {
		t.Error("delisted perp was offered as active")
	}
}

func TestHyperliquidPartialDiscoveryPreservesAvailableMarkets(t *testing.T) {
	for _, failed := range []string{"spotMeta", "perpDexs", "xyz"} {
		t.Run(failed, func(t *testing.T) {
			a := hyperliquidTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				var req struct{ Type, Dex string }
				_ = json.NewDecoder(r.Body).Decode(&req)
				if req.Type == failed || req.Dex == failed {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				switch req.Type {
				case "spotMeta":
					fmt.Fprint(w, `{"tokens":[{"name":"USDC","index":0,"szDecimals":8},{"name":"HYPE","index":150,"szDecimals":2}],"universe":[{"name":"@107","index":107,"tokens":[150,0]}]}`)
				case "perpDexs":
					fmt.Fprint(w, `[null,{"name":"xyz"}]`)
				case "meta":
					if req.Dex == "" {
						fmt.Fprint(w, `{"universe":[{"name":"BTC","szDecimals":5}]}`)
					} else {
						fmt.Fprint(w, `{"universe":[{"name":"xyz:TSLA","szDecimals":2}]}`)
					}
				}
			})
			items, err := a.hyperliquidInstruments(context.Background())
			if len(items) == 0 || err == nil || ErrorKind(err) != "restricted" {
				t.Fatalf("partial catalog/error lost: %+v, %v", items, err)
			}
			foundNative := false
			for _, item := range items {
				foundNative = foundNative || item.NativeID == "BTC"
			}
			if !foundNative {
				t.Error("working native catalog lost on category failure")
			}
		})
	}
}

func TestHyperliquidHistoryPreservesCoinAndEveryInterval(t *testing.T) {
	for _, interval := range domain.Intervals {
		if interval == "1w" {
			continue // Native weeks start Thursday; Monday weeks are tested below.
		}
		for _, coin := range []string{"PURR/USDC", "@107", "BTC", "xyz:TSLA"} {
			t.Run(coin+"/"+interval, func(t *testing.T) {
				d := TimeframeSeconds(interval)
				start := BucketStart(time.Now().Add(-30*24*time.Hour).Unix(), d)
				a := hyperliquidTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						Type string
						Req  struct {
							Coin, Interval     string
							StartTime, EndTime int64
						}
					}
					_ = json.NewDecoder(r.Body).Decode(&req)
					if req.Type != "candleSnapshot" || req.Req.Coin != coin || req.Req.Interval != interval || req.Req.StartTime != start*1000 || req.Req.EndTime != (start+2*d)*1000-1 {
						t.Errorf("incorrect public candle request: %+v", req)
					}
					// Reverse ordering and a repeated cumulative bar must not double volume.
					fmt.Fprintf(w, `[{"s":%q,"i":%q,"t":%d,"T":%d,"o":"100.123456789","h":"105","l":"99","c":"104","v":"3.25"},{"s":%q,"i":%q,"t":%d,"T":%d,"o":"100","h":"102","l":"99","c":"101","v":"2.25"},{"s":%q,"i":%q,"t":%d,"T":%d,"o":"100","h":"102","l":"99","c":"101","v":"2.25"}]`, coin, interval, (start+d)*1000, (start+2*d)*1000-1, coin, interval, start*1000, (start+d)*1000-1, coin, interval, start*1000, (start+d)*1000-1)
				})
				bars, err := a.hyperliquidHistory(context.Background(), domain.FeedKey{Exchange: "hyperliquid", Market: "spot", Instrument: coin, Timeframe: interval}, start, start+2*d, 1000)
				if err != nil {
					t.Fatal(err)
				}
				if len(bars) != 2 || bars[0].Time != start || bars[1].Time != start+d || bars[0].Volume != 2.25 || bars[1].Volume != 3.25 || bars[1].Open != 100.123456789 || !bars[0].Closed || bars[0].Quality != "authoritative" {
					t.Fatalf("incorrect normalized bars: %+v", bars)
				}
			})
		}
	}
}

func TestHyperliquidWeeklyUsesMondayDailyAggregation(t *testing.T) {
	day, week := int64(86400), int64(604800)
	start := BucketStart(time.Now().Add(-30*24*time.Hour).Unix(), week)
	for _, coin := range []string{"BTC", "@107", "xyz:TSLA"} {
		t.Run(coin, func(t *testing.T) {
			a := hyperliquidTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Req struct {
						Coin, Interval     string
						StartTime, EndTime int64
					}
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				if req.Req.Coin != coin || req.Req.Interval != "1d" || req.Req.StartTime != start*1000 || req.Req.EndTime != (start+2*week)*1000-1 {
					t.Errorf("native Thursday weeks requested or wrong daily range: %+v", req)
				}
				var rows []map[string]any
				for i := 13; i >= 0; i-- {
					timestamp := start + int64(i)*day
					row := map[string]any{"s": coin, "i": "1d", "t": timestamp * 1000, "T": (timestamp+day)*1000 - 1, "o": fmt.Sprint(100 + i), "h": fmt.Sprint(110 + i), "l": fmt.Sprint(90 + i), "c": fmt.Sprint(101 + i), "v": "0.1"}
					rows = append(rows, row)
					if i == 0 {
						rows = append(rows, row)
					} // overlapping row replaces, not adds.
				}
				_ = json.NewEncoder(w).Encode(rows)
			})
			bars, err := a.hyperliquidHistory(context.Background(), domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: coin, Timeframe: "1w"}, start, start+2*week, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(bars) != 2 || bars[0].Time != start || bars[1].Time != start+week || bars[0].Open != 100 || bars[0].High != 116 || bars[0].Low != 90 || bars[0].Close != 107 || bars[0].Volume != 0.7 || !bars[0].Closed || bars[0].Quality != "authoritative" {
				t.Fatalf("incorrect Monday weekly aggregation: %+v", bars)
			}
		})
	}
}

func TestHyperliquidWeeklyMissingDaysCurrentAndPaging(t *testing.T) {
	day, week := int64(86400), int64(604800)
	start := BucketStart(time.Now().Unix(), week) - 150*week
	now := start + 1001*day + day/2
	var calls int
	a := hyperliquidTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Req struct {
				StartTime, EndTime int64
				Interval           string
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Req.Interval != "1d" || req.Req.EndTime+1-req.Req.StartTime > 1000*day*1000 {
			t.Errorf("daily page not bounded: %+v", req)
		}
		var rows []map[string]any
		for ts := req.Req.StartTime / 1000; ts <= (req.Req.EndTime / 1000); ts += day {
			if ts == start+day {
				continue
			} // missing day cannot become authoritative.
			rows = append(rows, map[string]any{"s": "BTC", "i": "1d", "t": ts * 1000, "T": (ts+day)*1000 - 1, "o": "100", "h": "110", "l": "90", "c": "101", "v": "0.1"})
		}
		_ = json.NewEncoder(w).Encode(rows)
	})
	key := domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1w"}
	bars, err := a.hyperliquidWeeklyHistory(context.Background(), key, start, now+1, now)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(bars) == 0 || bars[0].Quality != "provisional" || bars[0].Volume != 0.6 || bars[len(bars)-1].Closed || bars[len(bars)-1].Quality != "provisional" {
		t.Fatalf("missing/current days claimed complete or pages not bounded: %d calls, first=%+v last=%+v", calls, bars[0], bars[len(bars)-1])
	}
}

func TestHyperliquidRejectsInvalidCandlesAndMarksCurrentProvisional(t *testing.T) {
	start := BucketStart(time.Now().Unix(), 60)
	for _, mode := range []string{"current", "wrongCoin", "wrongInterval", "milliseconds", "wrongCloseTime", "badOHLC", "badVolume", "empty"} {
		t.Run(mode, func(t *testing.T) {
			a := hyperliquidTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "empty" {
					fmt.Fprint(w, `[]`)
					return
				}
				coin, interval, volume, high := "BTC", "1m", "1", "105"
				ms, closeTime := start*1000, (start+60)*1000-1
				switch mode {
				case "wrongCoin":
					coin = "ETH"
				case "wrongInterval":
					interval = "5m"
				case "milliseconds":
					ms++
				case "wrongCloseTime":
					closeTime++
				case "badOHLC":
					high = "98"
				case "badVolume":
					volume = "-1"
				}
				fmt.Fprintf(w, `[{"s":%q,"i":%q,"t":%d,"T":%d,"o":"100","h":%q,"l":"99","c":"101","v":%q}]`, coin, interval, ms, closeTime, high, volume)
			})
			bars, err := a.hyperliquidHistory(context.Background(), domain.FeedKey{Exchange: "hyperliquid", Market: "perpetual", Instrument: "BTC", Timeframe: "1m"}, start, start+60, 1)
			if mode == "current" {
				if err != nil || len(bars) != 1 || bars[0].Closed || bars[0].Quality != "provisional" {
					t.Fatalf("current bar reported complete: %+v %v", bars, err)
				}
			} else if mode == "empty" {
				if err != nil || len(bars) != 0 {
					t.Fatalf("empty history must not invent data: %+v %v", bars, err)
				}
			} else if err == nil {
				t.Fatalf("invalid candle accepted: %+v", bars)
			}
		})
	}
}

func TestHyperliquidRequestErrorsBackoffAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		body, kind string
		status     int
	}{
		{`{"error":"Unknown coin"}`, "unknownInstrument", 200},
		{`"Unknown asset"`, "unknownInstrument", 200},
		{`{"status":"err","response":"Unknown coin"}`, "unknownInstrument", 200},
		{`null`, "internal", 200},
		{`broken`, "internal", 200},
		{``, "restricted", 403},
		{``, "restricted", 451},
		{``, "unknownInstrument", 422},
		{``, "rateLimited", 429},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.status, tc.body), func(t *testing.T) {
			var calls atomic.Int32
			a := hyperliquidTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			var dst []any
			err := a.hyperliquidPost(context.Background(), map[string]string{"type": "meta"}, &dst)
			if err == nil || ErrorKind(err) != tc.kind {
				t.Fatalf("wrong error classification: %v", err)
			}
			if tc.status == 429 {
				if RetryAt(err) < time.Now().Add(28*time.Second).Unix() {
					t.Error("Retry-After lost")
				}
				_ = a.hyperliquidPost(context.Background(), map[string]string{"type": "meta"}, &dst)
				if calls.Load() != 1 {
					t.Error("backoff sent another request")
				}
			}
		})
	}
	a := &publicAdapter{provider: "hyperliquid", base: "http://unreachable.invalid", client: &http.Client{}, requestSlots: make(chan struct{}, 2), nextRequest: time.Now().Add(time.Minute)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var dst []any
	if err := a.hyperliquidPost(ctx, map[string]string{"type": "meta"}, &dst); !errors.Is(err, context.Canceled) || a.requests.Load() != 0 || len(a.requestSlots) != 0 {
		t.Fatalf("cancelled request not cleaned up: %v", err)
	}
	if got := hyperliquidIncrement(8); got != "0.00000001" || strings.Contains(got, "e") {
		t.Fatal(got)
	}
}

func TestHyperliquidPublicHistoryBoundsDefaults(t *testing.T) {
	for _, tc := range []struct {
		name  string
		from  int64
		to    int64
		limit int
		span  int64
	}{
		{"defaults", 0, 0, 0, 300 * 60},
		{"excessiveLimit", 0, 0, 5000, 300 * 60},
		{"boundedRange", 60, 600000, 1000, 1000 * 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			a := hyperliquidTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					Req struct{ StartTime, EndTime int64 }
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				if got := req.Req.EndTime + 1 - req.Req.StartTime; got != tc.span*1000 {
					t.Errorf("unbounded candle request span: %d != %d", got, tc.span*1000)
				}
				fmt.Fprint(w, `[]`)
			})
			key := domain.FeedKey{Exchange: "hyperliquid", Market: "spot", Instrument: "@107", Timeframe: "1m"}
			if _, err := a.History(context.Background(), key, tc.from, tc.to, tc.limit); err != nil || calls != 1 {
				t.Fatalf("public history dispatch failed: %v, %d requests", err, calls)
			}
			if _, err := a.History(context.Background(), key, 1000, 999, 1); err != nil || calls != 1 {
				t.Fatalf("invalid range sent upstream request: %v, %d requests", err, calls)
			}
		})
	}
}

func TestHyperliquidPublicPartialCatalogIsNotCached(t *testing.T) {
	var spotCalls int
	a := hyperliquidTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Type, Dex string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Type {
		case "spotMeta":
			spotCalls++
			fmt.Fprint(w, `{"tokens":[{"name":"USDC","index":0,"szDecimals":8},{"name":"HYPE","index":150,"szDecimals":2}],"universe":[{"name":"@107","index":107,"tokens":[150,0]}]}`)
		case "perpDexs":
			fmt.Fprint(w, `[null,{"name":"xyz"}]`)
		case "meta":
			if req.Dex == "xyz" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprint(w, `{"universe":[{"name":"BTC","szDecimals":5}]}`)
		}
	})
	for attempt := 0; attempt < 2; attempt++ {
		items, err := a.Instruments(context.Background())
		if err != nil || len(items) != 2 || items[0].ProviderMetadata["catalogWarning"] == "" || items[1].ProviderMetadata["catalogWarning"] == "" {
			t.Fatalf("partial catalog lost its warning: %+v, %v", items, err)
		}
	}
	if spotCalls != 2 || !a.fetched.IsZero() {
		t.Fatal("partial catalog cached as complete")
	}
}
