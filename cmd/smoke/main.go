// smoke is an explicit opt-in public-network check. It never supplies fixtures.
package main

import (
	"context"
	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	providers := flag.String("providers", "binance,bybit,coinbase", "comma-separated public spot providers")
	duration := flag.Duration("duration", 15*time.Second, "live observation duration per reachable provider")
	streamInterval := flag.String("stream-interval", "1m", "live candle interval (including Monday-based 1w)")
	flag.Parse()
	liveStep := exchange.TimeframeSeconds(*streamInterval)
	if liveStep <= 0 {
		fmt.Fprintf(os.Stderr, "unsupported stream interval %q\n", *streamInterval)
		os.Exit(2)
	}
	encoder := json.NewEncoder(os.Stdout)
	failed := false
	for _, provider := range strings.Split(*providers, ",") {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second+*duration)
			defer cancel()
			log := func(stage string, data any) {
				_ = encoder.Encode(map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "provider": provider, "stage": stage, "result": data})
			}
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
			preferred := "BTCUSDT"
			if provider == "coinbase" {
				preferred = "BTC-USD"
			}
			var instrument string
			for _, i := range items {
				available := i.Availability == "TRADING" || i.Availability == "Trading" || i.Availability == "online" || i.Availability == ""
				if available && (instrument == "" || i.NativeID == preferred) {
					instrument = i.NativeID
				}
				if instrument == preferred {
					break
				}
			}
			if instrument == "" {
				log("discovery", "no available public spot product")
				failed = true
				return
			}
			log("discovery", map[string]any{"status": "pass", "products": len(items), "selected": instrument})
			for _, interval := range domain.Intervals {
				k := domain.FeedKey{Exchange: provider, Market: "spot", Instrument: instrument, Timeframe: interval}
				now := time.Now().Unix()
				d := exchange.TimeframeSeconds(interval)
				bars, err := a.History(ctx, k, exchange.BucketStart(now, d)-3*d, now+1, 4)
				if err != nil {
					log("history/"+interval, map[string]any{"status": "blocked", "kind": exchange.ErrorKind(err), "error": err.Error()})
					failed = true
					return
				}
				log("history/"+interval, map[string]any{"status": "pass", "bars": len(bars), "candles": bars})
			}
			liveCtx, stop := context.WithTimeout(ctx, *duration)
			defer stop()
			count := 0
			transitions := 0
			first, last := int64(0), int64(0)
			lastHealth := ""
			startBucket := exchange.BucketStart(time.Now().Unix(), liveStep)
			err = a.Stream(liveCtx, domain.FeedKey{Exchange: provider, Market: "spot", Instrument: instrument, Timeframe: *streamInterval}, func(b []domain.Candle, h domain.Health) {
				if h.State != lastHealth {
					log("health", h)
					lastHealth = h.State
				}
				for _, c := range b {
					if c.Time >= startBucket && h.LastTradeAt > 0 && (c.Quality == "live" || c.Quality == "provisional") {
						if first == 0 {
							first = c.Time
						}
						if last != 0 && c.Time > last {
							transitions++
						}
						last = c.Time
						count++
					}
				}
			})
			status := "pass"
			if count == 0 {
				status = "unverified"
				failed = true
			}
			log("stream", map[string]any{"status": status, "interval": *streamInterval, "liveOrProvisionalUpdates": count, "observedBucketTransitions": transitions, "duration": duration.String(), "stop": fmt.Sprint(err)})
		}()
	}
	if failed {
		os.Exit(1)
	}
}
