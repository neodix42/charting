package jforex

import (
	"errors"
	"math"

	"cryptodesk/internal/domain"
)

func copyStreamQuote(quote *domain.Quote) *domain.Quote {
	if quote == nil {
		return nil
	}
	copy := *quote
	if quote.Bid != nil {
		bid := *quote.Bid
		copy.Bid = &bid
	}
	if quote.Ask != nil {
		ask := *quote.Ask
		copy.Ask = &ask
	}
	return &copy
}

// Quotes retain the SDK observation time. A heartbeat cannot refresh them, and
// an older tick cannot replace the more recent top of book for this subscription.
func streamQuote(quote, previous *domain.Quote, now int64) (*domain.Quote, error) {
	if quote == nil {
		return nil, nil
	}
	valid := func(side *float64) bool {
		return side == nil || *side > 0 && !math.IsNaN(*side) && !math.IsInf(*side, 0)
	}
	if quote.Time <= 0 || quote.Time > now+5000 || !valid(quote.Bid) || !valid(quote.Ask) || quote.Bid != nil && quote.Ask != nil && *quote.Ask < *quote.Bid {
		return nil, errors.New("The JForex bridge returned an invalid Bid/Ask quote")
	}
	if previous != nil && quote.Time < previous.Time {
		return copyStreamQuote(previous), nil
	}
	return copyStreamQuote(quote), nil
}
