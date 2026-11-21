package hyperliquid

import (
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"
)

// sizeRisk uses the same immutable account, executable quote, metadata and
// funded maximum as the ticket. It never updates account leverage or reserves
// an order. A stop-loss estimate cannot reuse the account-wide JForex margin
// buffer: Hyperliquid entries use isolated margin and tiered maintenance.
func (out *Metrics) sizeRisk(r OrderRequest, m instrument, entry decimal.Decimal, existingPosition bool) {
	fail := func(reason string) { out.RiskSizingError = reason }
	if existingPosition {
		fail("An existing position will be netted; a new isolated position's 50% risk and liquidation cannot be projected")
		return
	}
	if out.MaximumQuantityError != "" {
		fail(out.MaximumQuantityError)
		return
	}
	equity, validEquity := metricNumber(out.Equity, false)
	if !validEquity || !equity.IsPositive() {
		fail("A verified positive equity balance for the selected perpetual DEX is required")
		return
	}
	maximum, validMaximum := metricNumber(out.MaximumQuantity, false)
	if !validMaximum || out.EntryPrice == "" || !entry.IsPositive() || r.Leverage < 1 || r.Leverage > m.maxLeverage {
		fail("Fresh executable entry, selected leverage and funded maximum quantity are required")
		return
	}
	if m.marginTableError != "" {
		fail(m.marginTableError)
		return
	}
	tiers, problem := normalizedMarginTiers(m.marginTiers)
	if problem != "" {
		fail(problem)
		return
	}
	if r.StopLoss == "" {
		fail("Enter a stop-loss price to calculate the 50%-equity target")
		return
	}
	stop, err := checkPrice(r.StopLoss, m.szDecimals, false)
	if err != nil {
		fail("Enter a positive stop-loss price at the instrument's supported precision")
		return
	}
	if r.Side == "buy" && !stop.LessThan(entry) || r.Side == "sell" && !stop.GreaterThan(entry) {
		fail("Stop loss must be below Buy entry or above Sell entry")
		return
	}
	lossPerUnit := entry.Sub(stop).Abs()
	target := equity.Mul(decimal.RequireFromString("0.5")).DivRound(lossPerUnit, 32).Truncate(int32(m.szDecimals))
	out.RiskSizingQuantity = target.String()
	out.RiskSizingBasis = fmt.Sprintf("At entry %s, stop %s and isolated leverage %d×: targets a gross loss of 50%% of the selected DEX's verified collateral equity. Isolated collateral is conservatively limited to the lower of entry and the verified mark, divided by leverage. Rounded down and capped by the funded maximum, visible liquidity, instrument limits and maintenance tiers. Stop must trigger before estimated isolated liquidation. Fees, funding, trigger/fill slippage and later account/market changes are excluded from the stop-loss amount; this is advisory sizing.", entry, stop, r.Leverage)
	cap := decimal.Min(target, maximum).Truncate(int32(m.szDecimals))
	if !cap.IsPositive() || cap.Mul(entry).LessThan(decimal.NewFromInt(10)) {
		out.RiskSizingSafeQuantity, out.RiskSizingLoss = "0", "0"
		fail("No funded quantity meets Hyperliquid's minimum order notional within the 50%-equity target")
		return
	}
	if !out.riskMarginPrice.IsPositive() {
		fail("A verified mark snapshot is required to project isolated margin and liquidation")
		return
	}
	safe := riskQuantityBeforeLiquidation(cap, entry, stop, out.riskMarginPrice, r.Leverage, m.szDecimals, tiers)
	if !safe.IsPositive() || safe.Mul(entry).LessThan(decimal.NewFromInt(10)) {
		out.RiskSizingSafeQuantity, out.RiskSizingLoss = "0", "0"
		fail("The stop is at or beyond isolated liquidation for every eligible size at this leverage; lower leverage or choose a nearer stop")
		return
	}
	margin := safe.Mul(out.riskMarginPrice).DivRound(decimal.NewFromInt(int64(r.Leverage)), 32)
	liquidation, liquidationProblem := isolatedLiquidation(safe, entry, margin, r.Side, r.Leverage, m.marginTiers)
	if liquidationProblem != "" {
		// A fully funded 1x long can have no positive liquidation threshold in
		// this model; zero honestly represents that boundary, not unavailable.
		if r.Side != "buy" || r.Leverage != 1 || liquidationProblem != "No positive liquidation threshold exists in this proposed isolated, fee-free model" {
			fail(liquidationProblem)
			return
		}
		liquidation = decimal.Zero
	}
	out.RiskSizingSafeQuantity = safe.String()
	out.RiskSizingLoss = safe.Mul(lossPerUnit).String()
	out.RiskSizingLiquidationPrice = liquidation.String()
	out.RiskSizingCanTarget = safe.Equal(target)
	if !out.RiskSizingCanTarget {
		out.RiskSizingBasis += " The full 50% target is constrained by funded capacity or isolated liquidation; the displayed size has a lower gross loss."
	}
}

// Validity is monotonic in size: tier maintenance per unit never falls as a
// position grows. Binary search integer lot counts so boundary flooring never
// selects one increment beyond a supported tier or liquidation threshold.
func riskQuantityBeforeLiquidation(cap, entry, stop, fundingPrice decimal.Decimal, leverage, sizeDecimals int, tiers []normalizedMarginTier) decimal.Decimal {
	unit := decimal.New(1, -int32(sizeDecimals))
	low := new(big.Int)
	high := cap.DivRound(unit, 0).BigInt()
	one := big.NewInt(1)
	marginPerUnit := fundingPrice.DivRound(decimal.NewFromInt(int64(leverage)), 32)
	lossPerUnit := entry.Sub(stop).Abs()
	allowed := func(quantity decimal.Decimal) bool {
		entryNotional, stopNotional := quantity.Mul(entry), quantity.Mul(stop)
		entryTier, stopTier := tiers[0], tiers[0]
		for _, tier := range tiers {
			if entryNotional.GreaterThanOrEqual(tier.lower) {
				entryTier = tier
			}
			if stopNotional.GreaterThanOrEqual(tier.lower) {
				stopTier = tier
			}
		}
		if leverage > entryTier.maximum {
			return false
		}
		maintenance := stopNotional.Mul(stopTier.rate).Sub(stopTier.deduction)
		equityAtStop := quantity.Mul(marginPerUnit.Sub(lossPerUnit))
		return equityAtStop.GreaterThan(maintenance)
	}
	for low.Cmp(high) < 0 {
		mid := new(big.Int).Add(low, high)
		mid.Add(mid, one).Rsh(mid, 1)
		quantity := decimal.NewFromBigInt(mid, -int32(sizeDecimals))
		if allowed(quantity) {
			low.Set(mid)
		} else {
			high.Sub(mid, one)
		}
	}
	return decimal.NewFromBigInt(low, -int32(sizeDecimals))
}
