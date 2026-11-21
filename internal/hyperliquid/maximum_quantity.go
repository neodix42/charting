package hyperliquid

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// maximumOrderQuantity is independent of the ticket's entered quantity. It
// budgets the entire fill at the IOC's worst allowed price, leaves the taker
// fee in collateral, and never assumes more depth than the public book reports.
// This is a snapshot calculation; an IOC cannot guarantee a future full fill.
func (s *Store) maximumOrderQuantity(ctx context.Context, v Settings, r OrderRequest, m instrument, metrics *Metrics, entry decimal.Decimal, existingPosition bool) (string, string, string) {
	unavailable := func(reason string) (string, string, string) { return "", "", reason }
	balance, ok := metricNumber(metrics.AvailableBalance, false)
	if metrics.AvailableBalanceError != "" || !ok {
		return unavailable("A verified available balance is required to calculate maximum quantity")
	}
	if !entry.IsPositive() {
		return unavailable("A fresh executable entry quote is required to calculate maximum quantity")
	}
	leverage := r.Leverage
	if r.Market == "spot" {
		leverage = 1
	}
	if leverage < 1 || leverage > m.maxLeverage {
		return unavailable("Select a valid leverage to calculate maximum quantity")
	}
	if balance.IsZero() && !existingPosition {
		return "0", "No available trading balance", ""
	}

	var fees struct {
		CrossRate     string `json:"userCrossRate"`
		SpotCrossRate string `json:"userSpotCrossRate"`
	}
	if err := s.post(ctx, v, "/info", map[string]string{"type": "userFees", "user": v.AccountAddress}, &fees); err != nil {
		return unavailable("The account's taker fee could not be verified; maximum quantity is unavailable")
	}
	rawFee := fees.CrossRate
	if r.Market == "spot" {
		rawFee = fees.SpotCrossRate
	}
	fee, feeOK := metricNumber(rawFee, false)
	if !feeOK || fee.GreaterThan(decimal.NewFromInt(1)) {
		return unavailable("The account's taker fee is invalid; maximum quantity is unavailable")
	}
	feeBasis := "account taker fee"
	if m.dex != "" {
		// Official fee formula permits deployer scales through 3; scales >=1
		// charge 2*scale. The public metadata does not expose the current scale.
		// Six times the user's rate bounds it without guessing a builder's fees.
		fee = fee.Mul(decimal.NewFromInt(6))
		feeBasis = "conservative HIP-3 fee reserve (six times the account taker rate)"
	}
	upperPrice := entry.Mul(decimal.RequireFromString("1.01"))
	limitPrice := upperPrice
	if r.Side == "sell" {
		limitPrice = entry.Mul(decimal.RequireFromString("0.99"))
	}
	precision := priceDecimals(limitPrice, m.szDecimals, r.Market == "spot")
	if r.Side == "buy" {
		limitPrice = limitPrice.RoundFloor(precision)
	} else {
		limitPrice = limitPrice.RoundCeil(precision)
	}
	if !limitPrice.IsPositive() {
		return unavailable("The IOC price cannot be represented at the instrument's precision")
	}
	if r.Side == "buy" {
		upperPrice = limitPrice
	}

	marginPrice := upperPrice
	executionLoss := decimal.Zero
	providerMaximum := decimal.Zero
	if r.Market != "spot" {
		// Initial margin is based on mark, which can exceed the executable quote.
		// activeAssetData is read only; its configured leverage/capacity is not
		// reused for a different leverage selected in this ticket.
		var active struct {
			User      string   `json:"user"`
			Coin      string   `json:"coin"`
			MarkPrice string   `json:"markPx"`
			Maximum   []string `json:"maxTradeSzs"`
			Leverage  struct {
				Type  string `json:"type"`
				Value int    `json:"value"`
			} `json:"leverage"`
		}
		if err := s.post(ctx, v, "/info", map[string]string{"type": "activeAssetData", "user": v.AccountAddress, "coin": r.Instrument}, &active); err != nil || !strings.EqualFold(active.User, v.AccountAddress) || active.Coin != r.Instrument {
			return unavailable("The instrument's mark price could not be verified; maximum quantity is unavailable")
		}
		mark, markOK := metricNumber(active.MarkPrice, false)
		if !markOK || !mark.IsPositive() {
			return unavailable("The instrument's mark price is invalid; maximum quantity is unavailable")
		}
		marginPrice = mark
		// Reuse this exact mark read for stop-risk projections. Taking the
		// smaller price avoids overstating isolated collateral when entry and
		// mark differ in either direction.
		metrics.riskMarginPrice = decimal.Min(entry, mark)
		// A fill away from mark immediately consumes collateral as unrealized
		// loss, in addition to mark-based initial margin. Reserving only
		// notional/leverage can therefore run out of margin mid-fill.
		if r.Side == "buy" {
			executionLoss = decimal.Max(decimal.Zero, limitPrice.Sub(mark))
		} else {
			executionLoss = decimal.Max(decimal.Zero, mark.Sub(limitPrice))
		}
		if existingPosition {
			if active.Leverage.Type != "isolated" || active.Leverage.Value != leverage || len(active.Maximum) != 2 {
				return unavailable("An existing position requires verified isolated capacity at the selected leverage; match its current leverage or close the position")
			}
			index := 0
			if r.Side == "sell" {
				index = 1
			}
			capacity, valid := metricNumber(active.Maximum[index], false)
			if !valid {
				return unavailable("The provider's existing-position trade capacity is invalid")
			}
			// Capacity includes position netting at the current isolated leverage.
			// Discount that capacity to reserve fees without reconstructing its
			// collateral release or treating a closing order as new exposure.
			providerMaximum = capacity.Mul(mark).DivRound(mark.Add(executionLoss.Add(upperPrice.Mul(fee)).Mul(decimal.NewFromInt(int64(leverage)))), 32)
		}
	}
	var book struct {
		Coin   string `json:"coin"`
		Time   int64  `json:"time"`
		Levels [][]struct {
			Price string `json:"px"`
			Size  string `json:"sz"`
		} `json:"levels"`
	}
	if err := s.post(ctx, v, "/info", map[string]string{"type": "l2Book", "coin": r.Instrument}, &book); err != nil || book.Coin != r.Instrument || !s.freshObservation(book.Time) || len(book.Levels) != 2 {
		return unavailable("Fresh order-book depth is required to calculate a fully funded maximum quantity")
	}
	side := 0
	if r.Side == "buy" {
		side = 1
	}
	depth, previous := decimal.Zero, decimal.Zero
	for i, level := range book.Levels[side] {
		price, priceOK := metricNumber(level.Price, false)
		size, sizeOK := metricNumber(level.Size, false)
		if !priceOK || !sizeOK || !price.IsPositive() || !size.IsPositive() || i > 0 && (r.Side == "buy" && price.LessThan(previous) || r.Side == "sell" && price.GreaterThan(previous)) {
			return unavailable("Order-book depth is incomplete or invalid; maximum quantity is unavailable")
		}
		previous = price
		if r.Side == "buy" && price.GreaterThan(limitPrice) || r.Side == "sell" && price.LessThan(limitPrice) {
			break
		}
		depth = depth.Add(size)
	}
	cost := marginPrice.DivRound(decimal.NewFromInt(int64(leverage)), 32).Add(executionLoss).Add(upperPrice.Mul(fee))
	if r.Market == "spot" && r.Side == "sell" {
		cost = decimal.NewFromInt(1)
	}
	maximum := decimal.Min(balance.DivRound(cost, 32), depth).Truncate(int32(m.szDecimals))
	if existingPosition {
		maximum = decimal.Min(providerMaximum, depth).Truncate(int32(m.szDecimals))
	}
	if r.Market != "spot" && !existingPosition {
		if m.marginTableError != "" {
			return unavailable(m.marginTableError)
		}
		var problem string
		maximum, problem = maximumWithinMarginTiers(maximum, marginPrice, leverage, m.szDecimals, m.marginTiers)
		if problem != "" {
			return unavailable(problem)
		}
	}
	if r.Market != "spot" {
		// Contract specifications express this ceiling by the instrument's
		// maximum leverage, not by the user's selected leverage.
		// https://hyperliquid.gitbook.io/hyperliquid-docs/trading/contract-specifications
		ceiling := int64(500000)
		switch {
		case m.maxLeverage >= 25:
			ceiling = 30000000
		case m.maxLeverage >= 20:
			ceiling = 5000000
		case m.maxLeverage >= 10:
			ceiling = 2000000
		}
		maximum = decimal.Min(maximum, decimal.NewFromInt(ceiling).DivRound(decimal.Max(marginPrice, upperPrice), 32)).Truncate(int32(m.szDecimals))
	}
	// Orders below the provider's minimum notional cannot execute as entries.
	if maximum.Mul(entry).LessThan(decimal.NewFromInt(10)) {
		maximum = decimal.Zero
	}
	if !s.freshObservation(book.Time) || !s.freshObservation(metrics.MetricsTime) {
		return unavailable("Account data or order-book depth aged; refresh maximum quantity")
	}
	basis := fmt.Sprintf("Rounded down to %d size decimals; available balance funds mark-based margin, adverse execution at the 1%% IOC cap and %s; capped by visible in-cap book depth", m.szDecimals, feeBasis)
	if r.Market == "spot" && r.Side == "buy" {
		basis = "Available quote tokens fund the entire order at the 1% IOC cap with an account taker fee reserve, rounded down and capped by visible in-cap ask depth"
	}
	if r.Market == "spot" && r.Side == "sell" {
		basis = "Available base tokens, rounded down to instrument precision and capped by visible in-cap bid depth; fees are deducted from quote proceeds"
	}
	if existingPosition {
		basis = "Provider maxTradeSzs at the matching current isolated leverage, reduced to reserve taker fees, rounded down and capped by visible in-cap book depth"
	}
	return maximum.String(), basis + ". Snapshot estimate; moving prices/liquidity can still cause a partial fill.", ""
}

func maximumWithinMarginTiers(quantity, price decimal.Decimal, leverage, sizeDecimals int, tiers []marginTier) (decimal.Decimal, string) {
	if len(tiers) == 0 || len(tiers) > 100 {
		return decimal.Zero, "The instrument's margin tiers are unavailable"
	}
	previousLower, previousLeverage := decimal.Zero, 1001
	for i, tier := range tiers {
		lower, ok := metricNumber(tier.LowerBound, false)
		if !ok || tier.MaxLeverage < 1 || tier.MaxLeverage > 1000 || tier.MaxLeverage > previousLeverage || i == 0 && !lower.IsZero() || i > 0 && !lower.GreaterThan(previousLower) {
			return decimal.Zero, "The instrument's margin tiers are invalid"
		}
		if leverage > tier.MaxLeverage {
			if lower.IsZero() {
				return decimal.Zero, ""
			}
			// The next lower-leverage tier starts at lower. Keep one size quantum
			// below it when division produces an exact representable boundary.
			bound := lower.DivRound(price, 32).Truncate(int32(sizeDecimals))
			if bound.Mul(price).GreaterThanOrEqual(lower) {
				bound = bound.Sub(decimal.New(1, -int32(sizeDecimals)))
			}
			return decimal.Min(quantity, bound), ""
		}
		previousLower, previousLeverage = lower, tier.MaxLeverage
	}
	return quantity, ""
}
