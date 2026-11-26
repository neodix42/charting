package comparison

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"cryptodesk/internal/domain"
)

var publicProviders = []string{"hyperliquid", "binance", "bybit", "coinbase"}

// MarketID identifies an exact public provider/market/native instrument. It is
// opaque to view storage; requests still resolve it against verified discovery.
func MarketID(provider, market, native string) string {
	return provider + "|" + market + "|" + url.QueryEscape(native)
}

// A saved source may disappear from discovery. Preserve its exact identity,
// but display its native market name rather than the opaque storage key.
func unavailableSelection(id string) Asset {
	asset := Asset{ID: id, Symbol: id, Name: assetName(id, "")}
	if validMarketID(id) {
		parts := strings.Split(id, "|")
		native, _ := url.QueryUnescape(parts[2])
		asset.Symbol, asset.Name, asset.Exchange, asset.Market, asset.NativeID = native, native, parts[0], parts[1], native
	}
	return asset
}
func validMarketID(id string) bool {
	if len(id) > 512 {
		return false
	}
	parts := strings.Split(id, "|")
	if len(parts) != 3 {
		return false
	}
	switch parts[0] {
	case "hyperliquid", "binance", "bybit", "coinbase":
	default:
		return false
	}
	native, err := url.QueryUnescape(parts[2])
	if err != nil || MarketID(parts[0], parts[1], native) != id {
		return false
	}
	return (domain.FeedKey{Exchange: parts[0], Market: parts[1], Instrument: native, Timeframe: "1d"}).Validate() == nil
}
func validBase(base string) bool {
	if len(base) == 0 || len(base) > 128 || !utf8.ValidString(base) {
		return false
	}
	for _, r := range base {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && !strings.ContainsRune("._-", r) {
			return false
		}
	}
	return true
}
func cloneAssets(assets []Asset) []Asset {
	result := append([]Asset{}, assets...)
	for i := range result {
		if key := result[i].FeedKey; key != nil {
			copy := *key
			result[i].FeedKey = &copy
		}
	}
	return result
}
func cloneCatalog(value Catalog) Catalog {
	return Catalog{Assets: cloneAssets(value.Assets), Markets: cloneAssets(value.Markets), Warnings: append([]string{}, value.Warnings...), AutoError: value.AutoError}
}
func (m *Manager) Catalog(ctx context.Context) (Catalog, error) {
	ctx, done, err := m.operation(ctx)
	if err != nil {
		return Catalog{}, err
	}
	defer done()
	return m.loadCatalog(ctx)
}
func (m *Manager) loadCatalog(ctx context.Context) (Catalog, error) {
	select {
	case m.catalogGate <- struct{}{}:
	case <-ctx.Done():
		return Catalog{}, ctx.Err()
	}
	defer func() { <-m.catalogGate }()
	m.mu.Lock()
	cached, at := cloneCatalog(m.catalog), m.catalogAt
	m.mu.Unlock()
	if !at.IsZero() && m.opts.Now().Sub(at) < time.Minute {
		return cached, ctx.Err()
	}
	if m.opts.Source == nil {
		return Catalog{}, errors.New("comparison market data is unavailable")
	}
	type discovery struct {
		items []domain.Instrument
		err   error
	}
	loaded := make([]discovery, len(publicProviders))
	var workers sync.WaitGroup
	for index, provider := range publicProviders {
		workers.Add(1)
		go func(i int, p string) {
			defer workers.Done()
			loaded[i].items, loaded[i].err = m.opts.Source.Instruments(ctx, p)
		}(index, provider)
	}
	workers.Wait()
	if ctx.Err() != nil {
		return Catalog{}, ctx.Err()
	}
	result := Catalog{Assets: []Asset{}, Markets: []Asset{}, Warnings: []string{}}
	markets := map[string]Asset{}
	auto := map[string]Asset{}
	ranks := map[string]int{}
	hyperComplete := true
	for index, provider := range publicProviders {
		data := loaded[index]
		warning := ""
		if data.err != nil {
			warning = providerName(provider) + " discovery is unavailable: " + data.err.Error()
		}
		for _, item := range data.items {
			if detail := item.ProviderMetadata["catalogWarning"]; detail != "" {
				if warning == "" {
					warning = providerName(provider) + " discovery is incomplete: " + detail
				}
				break
			}
		}
		if provider == "hyperliquid" && (warning != "" || len(data.items) == 0) {
			hyperComplete = false
			result.AutoError = "Automatic source selection is unavailable because Hyperliquid discovery is incomplete; choose an explicit source"
			if warning == "" {
				warning = "Hyperliquid returned an empty catalog; automatic asset absence cannot be verified"
			}
		}
		if warning != "" {
			result.Warnings = append(result.Warnings, warning)
		}
		if data.err != nil {
			continue
		}
		for _, item := range data.items {
			base := strings.ToUpper(item.BaseAsset)
			if !validBase(base) || !supportsDaily(item) || !available(item.Availability) || !usdQuote(item.QuoteAsset) {
				continue
			}
			key := domain.FeedKey{Exchange: provider, Market: item.Market, Instrument: item.NativeID, Timeframe: "1d"}
			if key.Validate() != nil {
				continue
			}
			candidate := Asset{ID: MarketID(provider, item.Market, item.NativeID), Symbol: base, Name: assetName(base, item.ProviderMetadata["baseFullName"]), Exchange: provider, Market: item.Market, NativeID: item.NativeID, QuoteAsset: item.QuoteAsset, FeedKey: &key}
			if renamedToncoin(item, provider, m.opts.Now()) {
				candidate.Symbol, candidate.DisplaySymbol, candidate.Name = "TON", "TON (GRAM)", "Gram (formerly Toncoin)"
			}
			if old, found := markets[candidate.ID]; !found || assetOrder(candidate) < assetOrder(old) {
				markets[candidate.ID] = candidate
			}
			rank, eligible := autoRank(item, provider)
			if !eligible || !symbolPattern.MatchString(candidate.Symbol) {
				continue
			}
			// The legacy automatic route has exactly its old preference policy. New
			// explicit choices are never selected as a consequence of provider errors.
			old, found := auto[candidate.Symbol]
			if !found || rank < ranks[candidate.Symbol] || rank == ranks[candidate.Symbol] && assetOrder(candidate) < assetOrder(old) {
				auto[candidate.Symbol], ranks[candidate.Symbol] = candidate, rank
			}
		}
	}
	if len(markets) == 0 {
		return Catalog{}, fmt.Errorf("No public daily markets are available: %s", strings.Join(result.Warnings, "; "))
	}
	for _, candidate := range markets {
		result.Markets = append(result.Markets, candidate)
	}
	if hyperComplete {
		for _, candidate := range auto {
			candidate.ID = candidate.Symbol
			result.Assets = append(result.Assets, candidate)
		}
	}
	sort.Slice(result.Assets, func(i, j int) bool { return result.Assets[i].Symbol < result.Assets[j].Symbol })
	sort.Slice(result.Markets, func(i, j int) bool {
		a, b := result.Markets[i], result.Markets[j]
		if a.Symbol != b.Symbol {
			return a.Symbol < b.Symbol
		}
		return a.ID < b.ID
	})
	if len(result.Warnings) == 0 {
		m.mu.Lock()
		if ctx.Err() == nil && !m.closed {
			m.catalog, m.catalogAt = cloneCatalog(result), m.opts.Now()
		}
		m.mu.Unlock()
	}
	return result, ctx.Err()
}
func providerName(provider string) string {
	switch provider {
	case "hyperliquid":
		return "Hyperliquid"
	case "binance":
		return "Binance"
	case "bybit":
		return "Bybit"
	case "coinbase":
		return "Coinbase"
	}
	return provider
}
func autoRank(item domain.Instrument, provider string) (int, bool) {
	if provider == "binance" {
		return 3, item.Market == "spot" && item.QuoteAsset == "USDT"
	}
	if provider != "hyperliquid" {
		return 0, false
	}
	switch item.Market {
	case "spot":
		return 1, true
	case "perpetual":
		if item.NativeID == item.BaseAsset && !strings.Contains(item.NativeID, ":") {
			return 0, true
		}
		if strings.Contains(item.NativeID, ":") && strings.SplitN(item.NativeID, ":", 2)[1] == item.BaseAsset {
			return 2, true
		}
	}
	return 0, false
}
