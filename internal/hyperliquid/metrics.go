package hyperliquid

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type Metrics struct {
	EntryPrice               string `json:"entryPrice,omitempty"`
	AvailableBalance         string `json:"availableBalance,omitempty"`
	AvailableBalanceCurrency string `json:"availableBalanceCurrency,omitempty"`
	AvailableBalanceError    string `json:"availableBalanceError,omitempty"`
	AvailableBalanceBasis    string `json:"availableBalanceBasis,omitempty"`
	AccountBalance           string `json:"accountBalance,omitempty"`
	Equity                   string `json:"equity,omitempty"`
	UsedMargin               string `json:"usedMargin,omitempty"`
	MarginRequired           string `json:"marginRequired,omitempty"`
	MarginRequiredCurrency   string `json:"marginRequiredCurrency,omitempty"`
	MarginError              string `json:"marginError,omitempty"`
	MarginBasis              string `json:"marginBasis,omitempty"`
	LiquidationPrice         string `json:"liquidationPrice,omitempty"`
	LiquidationError         string `json:"liquidationError,omitempty"`
	LiquidationBasis         string `json:"liquidationBasis,omitempty"`
	LiquidationKind          string `json:"liquidationKind"`
	MetricsTime              int64  `json:"metricsTime"`
}

type marginTier struct {
	LowerBound  string `json:"lowerBound"`
	MaxLeverage int    `json:"maxLeverage"`
}

type marginTable struct {
	MarginTiers []marginTier `json:"marginTiers"`
}

// OrderMetrics is a read-only account/position projection. It never reserves a
// confirmation, updates leverage, signs an exchange action, or writes a nonce.
// Quantity can be blank/invalid while editing; verified balances still return.
func (s *Store) OrderMetrics(ctx context.Context, r OrderRequest) (Metrics, error) {
	if r.Side != "buy" && r.Side != "sell" {
		return Metrics{}, errors.New("Choose Buy or Sell")
	}
	if r.Instrument == "" || len(r.Instrument) > 128 {
		return Metrics{}, errors.New("Choose a valid instrument")
	}
	s.mu.Lock()
	v, problem := s.settings, s.lastError
	s.mu.Unlock()
	if problem != "" {
		return Metrics{}, errors.New(problem)
	}
	if r.Market == "spot" && v.Network == "testnet" {
		return Metrics{}, errors.New("Spot chart IDs belong to mainnet; choose mainnet for spot trading")
	}
	m, err := s.market(ctx, v, r)
	if err != nil {
		return Metrics{}, err
	}
	quote, quoteTime, quoteErr := s.quoteWithTime(ctx, v, r)
	if quoteErr != nil {
		quote = decimal.Zero
	}
	value := s.orderMetrics(ctx, v, r, m, quote, quoteTime)
	if err := ctx.Err(); err != nil {
		return Metrics{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings != v {
		return Metrics{}, errors.New("Hyperliquid settings changed; refresh order metrics")
	}
	return value, nil
}

func metricNumber(raw string, signed bool) (decimal.Decimal, bool) {
	if raw == "" || len(raw) > 64 || strings.ContainsAny(raw, "eE+") {
		return decimal.Zero, false
	}
	n, err := decimal.NewFromString(raw)
	return n, err == nil && (signed || !n.IsNegative())
}

func tableFor(id *int, maximum int, raw []json.RawMessage) ([]marginTier, string) {
	if id == nil {
		return nil, "The instrument's maintenance-margin table is unavailable"
	}
	for _, entry := range raw {
		var row []json.RawMessage
		if json.Unmarshal(entry, &row) != nil || len(row) != 2 {
			continue
		}
		var found int
		if json.Unmarshal(row[0], &found) != nil || found != *id {
			continue
		}
		var table marginTable
		if json.Unmarshal(row[1], &table) != nil {
			return nil, "The maintenance-margin table is invalid"
		}
		return table.MarginTiers, ""
	}
	// Official margin-tiers specification: IDs below 50 identify a single tier
	// whose max leverage equals the ID. Never substitute asset max leverage for
	// an unidentified/missing tiered table.
	if *id > 0 && *id < 50 && *id == maximum {
		return []marginTier{{LowerBound: "0", MaxLeverage: *id}}, ""
	}
	return nil, "The selected maintenance-margin table is unavailable"
}

type positionState struct {
	Position struct {
		Coin string `json:"coin"`
		Size string `json:"szi"`
	} `json:"position"`
}
type clearinghouseState struct {
	Time           *int64           `json:"time"`
	Withdrawable   string           `json:"withdrawable"`
	AssetPositions *[]positionState `json:"assetPositions"`
	MarginSummary  struct {
		AccountValue    string `json:"accountValue"`
		TotalRawUSD     string `json:"totalRawUsd"`
		TotalMarginUsed string `json:"totalMarginUsed"`
	} `json:"marginSummary"`
}

func (s *Store) orderMetrics(ctx context.Context, v Settings, r OrderRequest, m instrument, entry decimal.Decimal, observedAt int64) (out Metrics) {
	out = Metrics{LiquidationKind: "unavailable", MetricsTime: s.now().UnixMilli()}
	defer func() {
		if entry.IsPositive() && !s.freshObservation(observedAt) {
			out.EntryPrice = ""
			if r.Market != "spot" {
				out.MarginRequired = ""
				out.MarginError = "The entry quote aged while account state was read; refresh to estimate margin"
				out.LiquidationPrice = ""
				out.LiquidationKind = "unavailable"
				out.LiquidationError = "The entry quote aged while account state was read; refresh to estimate liquidation"
			}
		}
	}()
	if entry.IsPositive() {
		out.EntryPrice = entry.String()
	}
	if r.Market == "spot" {
		out.LiquidationKind = "not-applicable"
		out.LiquidationError = "Spot holdings do not have a leveraged liquidation price"
		out.LiquidationBasis = "Unleveraged spot holdings"
		out.MarginError = "Spot trades do not require margin"
		out.MarginBasis = "Unleveraged spot holdings"
	}
	setUnavailable := func(problem string) Metrics {
		out.AvailableBalanceError = problem
		if r.Market != "spot" {
			out.MarginError = problem
			out.LiquidationError = problem
		}
		return out
	}
	if v.AccountAddress == "" {
		return setUnavailable("Configure the trading account address to retrieve account metrics")
	}
	if m.accountCurrency == "" || m.valuationError != "" {
		return setUnavailable("The instrument's collateral/quote currency could not be verified; account metrics are unavailable")
	}
	var mode string
	if err := s.post(ctx, v, "/info", map[string]string{"type": "userAbstraction", "user": v.AccountAddress}, &mode); err != nil {
		return setUnavailable("Hyperliquid account mode could not be verified; account metrics are unavailable")
	}
	classic := mode == "disabled"
	shared := mode == "unifiedAccount" || mode == "portfolioMargin" || mode == "dexAbstraction"
	if !classic && !shared {
		return setUnavailable("Hyperliquid returned an unknown account mode; account metrics are unavailable")
	}
	if r.Market == "spot" || shared {
		token, currency := m.balanceToken, m.balanceCurrency
		if r.Market != "spot" {
			if m.collateralToken == nil {
				return setUnavailable("The collateral token could not be identified")
			}
			token, currency = *m.collateralToken, m.accountCurrency
		}
		out.AvailableBalanceCurrency = currency
		if currency == "" {
			return setUnavailable("The balance token's currency could not be verified")
		}
		var spot struct {
			Balances *[]struct {
				Token *int   `json:"token"`
				Total string `json:"total"`
				Hold  string `json:"hold"`
			} `json:"balances"`
		}
		if err := s.post(ctx, v, "/info", map[string]string{"type": "spotClearinghouseState", "user": v.AccountAddress}, &spot); err != nil || spot.Balances == nil {
			return setUnavailable("Hyperliquid token balances are unavailable")
		}
		total, hold, found := decimal.Zero, decimal.Zero, false
		for _, balance := range *spot.Balances {
			if balance.Token == nil || *balance.Token < 0 {
				return setUnavailable("Hyperliquid token balance identities are incomplete; a missing token cannot be assumed to have a zero balance")
			}
			if *balance.Token == token {
				if found {
					return setUnavailable("Hyperliquid returned duplicate token balances")
				}
				found = true
				var ok, holdOK bool
				total, ok = metricNumber(balance.Total, true)
				hold, holdOK = metricNumber(balance.Hold, false)
				if !ok || !holdOK {
					return setUnavailable("Hyperliquid token balance values are invalid")
				}
			}
		}
		out.AccountBalance = total.String()
		if shared {
			out.AvailableBalanceBasis = "Spot collateral holdings; account margin/borrowing reservations are not resolved"
			out.AvailableBalanceError = "This account uses shared collateral; token holdings are not a verified free trading balance"
			if r.Market != "spot" {
				out.MarginError = "Additional isolated margin cannot be projected for this shared-collateral account mode"
				out.LiquidationError = "A new isolated liquidation price cannot be projected for this shared-collateral account mode"
			}
			return out
		}
		if total.LessThan(hold) {
			out.AvailableBalanceError = "Held tokens exceed the token balance; free token balance is unavailable"
		} else {
			out.AvailableBalance = total.Sub(hold).String()
			out.AvailableBalanceBasis = "Available spot tokens: total minus held orders"
		}
		return out
	}
	out.AvailableBalanceCurrency = m.accountCurrency
	out.MarginRequiredCurrency = m.accountCurrency
	out.AvailableBalanceBasis = "Withdrawable collateral in the selected perpetual DEX; not an account-wide cash total"
	var state clearinghouseState
	if err := s.post(ctx, v, "/info", map[string]string{"type": "clearinghouseState", "user": v.AccountAddress, "dex": m.dex}, &state); err != nil {
		return setUnavailable("Hyperliquid perpetual account state is unavailable")
	}
	if state.Time == nil || *state.Time < s.now().Add(-15*time.Second).UnixMilli() || *state.Time > s.now().Add(5*time.Second).UnixMilli() {
		return setUnavailable("Hyperliquid perpetual account state is stale or has no timestamp")
	}
	out.MetricsTime = *state.Time
	if amount, ok := metricNumber(state.Withdrawable, false); ok {
		out.AvailableBalance = amount.String()
	} else {
		out.AvailableBalanceError = "Hyperliquid did not provide a valid withdrawable collateral balance"
	}
	if amount, ok := metricNumber(state.MarginSummary.TotalRawUSD, true); ok {
		out.AccountBalance = amount.String()
	}
	if amount, ok := metricNumber(state.MarginSummary.AccountValue, true); ok {
		out.Equity = amount.String()
	}
	if amount, ok := metricNumber(state.MarginSummary.TotalMarginUsed, false); ok {
		out.UsedMargin = amount.String()
	}
	if state.AssetPositions == nil {
		out.MarginError = "Existing position state is unavailable; additional margin cannot be projected"
		out.LiquidationError = "Existing position state is unavailable; a new liquidation price cannot be projected"
		return out
	}
	for _, existing := range *state.AssetPositions {
		size, ok := metricNumber(existing.Position.Size, true)
		if existing.Position.Coin == "" || !ok {
			out.MarginError = "Position data is incomplete; additional margin cannot be projected"
			out.LiquidationError = "Position data is incomplete; a new liquidation price cannot be projected"
			return out
		}
		if existing.Position.Coin == r.Instrument && !size.IsZero() {
			out.MarginError = "An existing position will be netted; additional order margin is unavailable"
			out.LiquidationError = "An existing position will be netted; its liquidation price is not the proposed order's liquidation estimate"
			return out
		}
	}
	if !entry.IsPositive() {
		out.MarginError = "A fresh executable entry quote is required to estimate margin"
		out.LiquidationError = "A fresh executable entry quote is required to estimate liquidation"
		return out
	}
	quantity, quantityErr := positive(r.Quantity)
	if quantityErr != nil || !quantity.Equal(quantity.Truncate(int32(m.szDecimals))) {
		out.MarginError = "Enter a positive quantity at the instrument's supported precision"
		out.LiquidationError = "Enter a valid quantity to estimate liquidation"
		return out
	}
	if r.Leverage < 1 || r.Leverage > m.maxLeverage {
		out.MarginError = "Select a valid leverage to estimate margin"
		out.LiquidationError = "Select a valid leverage to estimate liquidation"
		return out
	}
	margin := quantity.Mul(entry).DivRound(decimal.NewFromInt(int64(r.Leverage)), 32)
	out.MarginRequired = margin.String()
	out.MarginBasis = "Proposed fully filled isolated order: quantity × entry / selected leverage; excludes fees and slippage"
	if m.marginTableError != "" {
		out.LiquidationError = m.marginTableError
		return out
	}
	price, problem := isolatedLiquidation(quantity, entry, margin, r.Side, r.Leverage, m.marginTiers)
	if problem != "" {
		if problem == marginTierLeverageError || strings.Contains(problem, "maintenance-margin tiers are invalid") || strings.Contains(problem, "maintenance-margin tiers are not monotonic") {
			out.MarginRequired = ""
			out.MarginError = problem
		}
		out.LiquidationError = problem
		return out
	}
	out.LiquidationPrice = price.String()
	out.LiquidationKind = "estimate"
	out.LiquidationBasis = "Proposed new isolated position from a verified flat instrument, funded at initial margin; maintenance tiers included, fees/funding/slippage excluded"
	return out
}

const marginTierLeverageError = "The proposed position size requires lower leverage in its margin tier"

func isolatedLiquidation(quantity, entry, margin decimal.Decimal, side string, leverage int, raw []marginTier) (decimal.Decimal, string) {
	// Official formulas: liquidation equity equals tier maintenance, and
	// maintenance = notional * (1 / (2 * tier max leverage)) - deduction.
	// Deduction carries lower tiers forward continuously. Solve each tier then
	// select by NOTIONAL AT THE LIQUIDATION PRICE, not by entry notional:
	// https://hyperliquid.gitbook.io/hyperliquid-docs/trading/liquidations
	// https://hyperliquid.gitbook.io/hyperliquid-docs/trading/margin-tiers
	if len(raw) == 0 || len(raw) > 100 {
		return decimal.Zero, "The maintenance-margin tiers are unavailable"
	}
	type tier struct {
		lower, rate, deduction decimal.Decimal
		maximum                int
	}
	tiers := make([]tier, 0, len(raw))
	deduction, previousLower, previousRate := decimal.Zero, decimal.Zero, decimal.Zero
	for i, item := range raw {
		lower, ok := metricNumber(item.LowerBound, false)
		if !ok || item.MaxLeverage < 1 || item.MaxLeverage > 1000 || i == 0 && !lower.IsZero() || i > 0 && !lower.GreaterThan(previousLower) {
			return decimal.Zero, "The maintenance-margin tiers are invalid"
		}
		rate := decimal.NewFromInt(1).DivRound(decimal.NewFromInt(int64(2*item.MaxLeverage)), 32)
		if i > 0 {
			if rate.LessThan(previousRate) {
				return decimal.Zero, "The maintenance-margin tiers are not monotonic"
			}
			deduction = deduction.Add(lower.Mul(rate.Sub(previousRate)))
		}
		tiers = append(tiers, tier{lower, rate, deduction, item.MaxLeverage})
		previousLower, previousRate = lower, rate
	}
	entryNotional := quantity.Mul(entry)
	for i, item := range tiers {
		if entryNotional.GreaterThanOrEqual(item.lower) && (i == len(tiers)-1 || entryNotional.LessThan(tiers[i+1].lower)) && leverage > item.maximum {
			return decimal.Zero, marginTierLeverageError
		}
	}
	sign := decimal.NewFromInt(1)
	if side == "sell" {
		sign = decimal.NewFromInt(-1)
	}
	for i, item := range tiers {
		numerator := sign.Mul(quantity).Mul(entry).Sub(margin).Sub(item.deduction)
		denominator := quantity.Mul(sign.Sub(item.rate))
		candidate := numerator.DivRound(denominator, 32)
		if !candidate.IsPositive() {
			continue
		}
		notional := candidate.Mul(quantity)
		if notional.GreaterThanOrEqual(item.lower) && (i == len(tiers)-1 || notional.LessThan(tiers[i+1].lower)) {
			return candidate, ""
		}
	}
	return decimal.Zero, "No positive liquidation threshold exists in this proposed isolated, fee-free model"
}

func (s *Store) freshObservation(observedAt int64) bool {
	now := s.now()
	return observedAt > 0 && observedAt >= now.Add(-15*time.Second).UnixMilli() && observedAt <= now.Add(5*time.Second).UnixMilli()
}
