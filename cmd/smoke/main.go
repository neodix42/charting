// smoke is an explicit opt-in public-network check. It never supplies fixtures.
package main

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type smokeLogger func(string, any)
type loggerContextKey struct{}

// The standalone opt-in probe records the actual public REST request, including
// category and symbol filters, without changing adapter endpoint configuration.
type reportingTransport struct{ base http.RoundTripper }

func (t reportingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	if log, ok := req.Context().Value(loggerContextKey{}).(smokeLogger); ok {
		result := map[string]any{"method": req.Method, "url": req.URL.String(), "elapsedMs": time.Since(start).Milliseconds()}
		if resp != nil {
			result["status"] = resp.StatusCode
		}
		if err != nil {
			result["error"] = err.Error()
		}
		log("http", result)
	}
	return resp, err
}

func selectInstrument(items []domain.Instrument, provider, market, requested string) (domain.Instrument, error) {
	preferred := "BTCUSDT"
	if provider == "coinbase" {
		preferred = "BTC-USD"
	}
	if requested != "" {
		preferred = requested
	}
	var selected domain.Instrument
	for _, item := range items {
		available := item.Availability == "TRADING" || item.Availability == "Trading" || item.Availability == "online" || item.Availability == ""
		if item.Market != market || !available || (requested != "" && item.NativeID != requested) {
			continue
		}
		if selected.NativeID == "" || item.NativeID == preferred {
			selected = item
		}
		if selected.NativeID == preferred {
			break
		}
	}
	if selected.NativeID == "" {
		return domain.Instrument{}, fmt.Errorf("no available public %s product matching %q", market, requested)
	}
	return selected, nil
}

func main() {
	providers := flag.String("providers", "binance,bybit,coinbase", "comma-separated public providers")
	market := flag.String("market", "spot", "market to probe: spot or perpetual")
	instrumentID := flag.String("instrument", "", "exact discovered native instrument ID (default: prefer BTC quote in the selected market)")
	duration := flag.Duration("duration", 15*time.Second, "live observation duration per reachable provider")
	streamInterval := flag.String("stream-interval", "1m", "live candle interval (including Monday-based 1w)")
	flag.Parse()
	liveStep := exchange.TimeframeSeconds(*streamInterval)
	if liveStep <= 0 {
		fmt.Fprintf(os.Stderr, "unsupported stream interval %q\n", *streamInterval)
		os.Exit(2)
	}
	if (*market != "spot" && *market != "perpetual") || *duration <= 0 {
		fmt.Fprintln(os.Stderr, "market must be spot or perpetual; duration must be positive")
		os.Exit(2)
	}
	encoder := json.NewEncoder(os.Stdout)
	var logMu sync.Mutex
	http.DefaultTransport = reportingTransport{base: http.DefaultTransport}
	failed := false
	for _, provider := range strings.Split(*providers, ",") {
		provider = strings.TrimSpace(provider)
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second+*duration)
			defer cancel()
			log := smokeLogger(func(stage string, data any) {
				logMu.Lock()
				defer logMu.Unlock()
				_ = encoder.Encode(map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "provider": provider, "market": *market, "stage": stage, "result": data})
			})
			ctx = context.WithValue(ctx, loggerContextKey{}, log)
			log("request", map[string]any{"instrument": *instrumentID, "streamInterval": *streamInterval, "duration": duration.String()})
			a, err := exchange.New(provider)
			if err != nil {
				log("adapter", err.Error())
				failed = true
				return
			}
			items, err := a.Instruments(ctx)
			if err != nil {
				log("discovery", map[string]any{"status": "blocked", "kind": exchange.ErrorKind(err), "error": err.Error()})
				failed = true
				return
			}
			counts := map[string]int{}
			warningSet := map[string]bool{}
			for _, i := range items {
				counts[i.Market+"/"+i.ProviderMetadata["category"]]++
				if warning := i.ProviderMetadata["catalogWarning"]; warning != "" {
					warningSet[warning] = true
				}
			}
			warnings := make([]string, 0, len(warningSet))
			for warning := range warningSet {
				warnings = append(warnings, warning)
			}
			sort.Strings(warnings)
			selected, err := selectInstrument(items, provider, *market, *instrumentID)
			if err != nil {
				log("discovery", map[string]any{"status": "blocked", "error": err.Error(), "products": len(items), "categories": counts, "warnings": warnings})
				failed = true
				return
			}
			instrument := selected.NativeID
			log("discovery", map[string]any{"status": "pass", "products": len(items), "categories": counts, "selected": selected, "warnings": warnings})
			for _, interval := range domain.Intervals {
				k := domain.FeedKey{Exchange: provider, Market: *market, Instrument: instrument, Timeframe: interval}
				now := time.Now().Unix()
				d := exchange.TimeframeSeconds(interval)
				bars, err := a.History(ctx, k, exchange.BucketStart(now, d)-3*d, now+1, 4)
				if err != nil {
					log("history/"+interval, map[string]any{"status": "blocked", "kind": exchange.ErrorKind(err), "error": err.Error()})
					failed = true
					return
				}
				status := "pass"
				if len(bars) == 0 {
					status = "unverified"
					failed = true
				}
				log("history/"+interval, map[string]any{"status": status, "bars": len(bars), "candles": bars})
			}
			liveCtx, stop := context.WithTimeout(ctx, *duration)
			defer stop()
			count := 0
			transitions := 0
			first, last := int64(0), int64(0)
			var firstUpdate, lastUpdate *domain.Candle
			lastHealth := ""
			startBucket := exchange.BucketStart(time.Now().Unix(), liveStep)
			err = a.Stream(liveCtx, domain.FeedKey{Exchange: provider, Market: *market, Instrument: instrument, Timeframe: *streamInterval}, func(b []domain.Candle, h domain.Health) {
				if h.State != lastHealth {
					log("health", h)
					lastHealth = h.State
				}
				for _, c := range b {
					if c.Time >= startBucket && h.LastTradeAt > 0 && (c.Quality == "live" || c.Quality == "provisional") {
						if first == 0 {
							first = c.Time
							firstUpdate = &c
						}
						if last != 0 && c.Time > last {
							transitions++
						}
						last = c.Time
						lastUpdate = &c
						count++
					}
				}
			})
			status := "pass"
			if count == 0 {
				status = "unverified"
				failed = true
			}
			if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				status = "blocked"
				failed = true
			}
			log("stream", map[string]any{"status": status, "instrument": instrument, "category": selected.ProviderMetadata["category"], "interval": *streamInterval, "liveOrProvisionalUpdates": count, "observedBucketTransitions": transitions, "firstUpdate": firstUpdate, "lastUpdate": lastUpdate, "duration": duration.String(), "stop": fmt.Sprint(err)})
		}()
	}
	if failed {
		os.Exit(1)
	}
}
