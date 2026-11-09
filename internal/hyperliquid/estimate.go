package hyperliquid

import (
	"context"
	"errors"
	"strings"
	"unicode"
)

type spotToken struct {
	Index      *int   `json:"index"`
	Name       string `json:"name"`
	SzDecimals int    `json:"szDecimals"`
}

func tokenCurrency(tokens []spotToken, index int) (string, string) {
	if index < 0 {
		return "", "Hyperliquid returned an invalid collateral/quote token index; profit currency is unavailable"
	}
	name, count := "", 0
	for _, token := range tokens {
		if token.Index != nil && *token.Index == index {
			name = strings.TrimSpace(token.Name)
			count++
		}
	}
	if count != 1 || name == "" || len(name) > 128 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", "Hyperliquid collateral/quote token metadata is missing or ambiguous; profit currency is unavailable"
	}
	return name, ""
}

func (s *Store) collateralCurrency(ctx context.Context, v Settings, index *int) (string, string) {
	// Missing is distinct from token index zero. Never infer a DEX's settlement
	// token from its name, oracle denomination, dollar peg, or wallet balances.
	// Official HIP-3 spec allows any permissionless quote asset as collateral:
	// https://hyperliquid.gitbook.io/hyperliquid-docs/hyperliquid-improvement-proposals-hips/hip-3-builder-deployed-perpetuals
	if index == nil {
		return "", "Hyperliquid metadata does not identify the collateral token; profit currency is unavailable"
	}
	var meta struct {
		Tokens []spotToken `json:"tokens"`
	}
	if err := s.post(ctx, v, "/info", map[string]string{"type": "spotMeta"}, &meta); err != nil {
		return "", "Hyperliquid collateral token lookup failed; profit currency is unavailable"
	}
	return tokenCurrency(meta.Tokens, *index)
}

func profitFactor(m instrument) string {
	if m.accountCurrency == "" || m.valuationError != "" {
		return ""
	}
	// Orders are in underlying/base units. Linear price PnL per one unit is
	// one settlement/quote token per price unit, before fees/funding/slippage.
	// Core perps may use a USDT oracle while settling USDC; the official quanto
	// specification explicitly applies NO USDC/USDT FX conversion:
	// https://hyperliquid.gitbook.io/hyperliquid-docs/trading/contract-specifications
	// HIP-3 inherits the same HyperCore perpetual contract/order stack with its
	// own collateral asset; its token name comes from the actual DEX metadata.
	return "1"
}

// Estimate reads public instrument metadata and a fresh executable quote only.
// It does not inspect API-wallet approval, create confirmation tokens, reserve
// nonces, sign an action, or query private account balances.
func (s *Store) Estimate(ctx context.Context, market, native, side string) (Estimate, error) {
	if side != "buy" && side != "sell" {
		return Estimate{}, errors.New("Choose Buy or Sell")
	}
	if native == "" || len(native) > 128 {
		return Estimate{}, errors.New("Choose a valid instrument")
	}
	s.mu.Lock()
	v, settingsError := s.settings, s.lastError
	s.mu.Unlock()
	if settingsError != "" {
		return Estimate{}, errors.New(settingsError)
	}
	if market == "spot" && v.Network == "testnet" {
		return Estimate{}, errors.New("Spot chart IDs belong to mainnet and can identify a different token on testnet; choose mainnet for spot trading")
	}
	r := OrderRequest{Market: market, Instrument: native, Side: side}
	m, err := s.market(ctx, v, r)
	if err != nil {
		return Estimate{}, err
	}
	price, observedAt, err := s.quoteWithTime(ctx, v, r)
	if err != nil {
		return Estimate{}, err
	}
	if err := ctx.Err(); err != nil {
		return Estimate{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings != v {
		return Estimate{}, errors.New("Hyperliquid settings changed; refresh the trade estimate")
	}
	return Estimate{ReferencePrice: price.String(), AccountCurrency: m.accountCurrency, ProfitPerPriceUnit: profitFactor(m), ValuationTime: observedAt, ValuationError: m.valuationError, MaxLeverage: m.maxLeverage}, nil
}
