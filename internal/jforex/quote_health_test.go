package jforex

import (
	"context"
	"math"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func streamSide(value float64) *float64 { return &value }

func TestStreamQuotesRetainActualTimeAndDoNotRegressOrShareMutableSides(t *testing.T) {
	const now = int64(1795680000000)
	current := &domain.Quote{Bid: streamSide(1.1001), Ask: streamSide(1.1003), Time: now - 2000}
	first, err := streamQuote(current, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	*current.Bid = 9
	if *first.Bid != 1.1001 {
		t.Fatal("SDK input mutated retained quote")
	}
	older := &domain.Quote{Bid: streamSide(1), Ask: streamSide(1.1), Time: now - 3000}
	retained, err := streamQuote(older, first, now)
	if err != nil || retained.Time != first.Time || *retained.Bid != 1.1001 {
		t.Fatal(retained, err)
	}
	*retained.Ask = 10
	if *first.Ask != 1.1003 {
		t.Fatal("consumer mutated the retained quote")
	}
	partial, err := streamQuote(&domain.Quote{Ask: streamSide(1.2), Time: now}, first, now)
	if err != nil || partial.Bid != nil || partial.Ask == nil {
		t.Fatal("missing side fabricated", partial, err)
	}
	if missing, err := streamQuote(nil, first, now); err != nil || missing != nil {
		t.Fatal("bars-only event fabricated a quote")
	}
}

func TestStreamQuotesRejectInvalidPricesAndFutureTimes(t *testing.T) {
	const now = int64(1795680000000)
	for _, quote := range []*domain.Quote{
		{Bid: streamSide(0), Ask: streamSide(1), Time: now},
		{Bid: streamSide(1), Ask: streamSide(-1), Time: now},
		{Bid: streamSide(math.Inf(1)), Time: now},
		{Ask: streamSide(math.NaN()), Time: now},
		{Bid: streamSide(2), Ask: streamSide(1), Time: now},
		{Bid: streamSide(1), Ask: streamSide(2), Time: now + 5001},
		{Bid: streamSide(1), Ask: streamSide(2), Time: 0},
	} {
		if _, err := streamQuote(quote, nil, now); err == nil {
			t.Fatalf("accepted invalid quote: %+v", quote)
		}
	}
}

func TestSDKQuoteHealthTravelsThroughStreamWithoutTurningBarsIntoQuotes(t *testing.T) {
	m, _ := testManager(t, "quote-health")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type update struct {
		bars   []domain.Candle
		health domain.Health
	}
	updates := make(chan update, 10)
	done := make(chan error, 1)
	go func() {
		done <- m.Stream(ctx, forexKey(), func(bars []domain.Candle, health domain.Health) { updates <- update{bars, health} })
	}()
	quotes := 0
	barsSeen := false
	deadline := time.After(5 * time.Second)
	for quotes < 2 || !barsSeen {
		select {
		case value := <-updates:
			if len(value.bars) > 0 {
				barsSeen = true
				if value.health.Quote != nil {
					t.Fatal("historical bars manufactured a current quote")
				}
			}
			if value.health.Quote != nil {
				quotes++
				if *value.health.Quote.Bid != 1.1001 || *value.health.Quote.Ask != 1.1003 || value.health.Quote.Time != 1792375200000 {
					t.Fatalf("SDK quote changed time or regressed: %+v", value.health.Quote)
				}
			}
		case err := <-done:
			t.Fatal("stream ended before actual quotes", err)
		case <-deadline:
			t.Fatal("stream did not publish SDK Bid/Ask")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled quote subscription retained work")
	}
}
