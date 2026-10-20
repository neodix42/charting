package exchange

import (
	"context"
	"cryptodesk/internal/domain"
	"errors"
	"fmt"
	"net/url"
)

// Discovery combines public spot and perpetual products. A failed category is
// reported on successful rows so a regional futures restriction does not also
// hide working spot markets. Neither adapter nor service caches partial lists.
func (a *publicAdapter) allInstruments(ctx context.Context) ([]domain.Instrument, error) {
	var result []domain.Instrument
	var failures []error
	seen := map[string]bool{}
	for _, category := range []string{"spot", "linear", "inverse"} {
		var items []domain.Instrument
		var err error
		if a.provider == "binance" {
			items, err = a.binanceInstruments(ctx, category)
		} else {
			items, err = a.bybitInstruments(ctx, category)
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, context.Canceled
		}
		if err != nil {
			label := category + " perpetual"
			if category == "spot" {
				label = "spot"
			}
			failures = append(failures, fmt.Errorf("%s %s symbols unavailable: %w", a.provider, label, err))
			continue
		}
		for _, item := range items {
			id := item.Market + "/" + item.NativeID
			if !seen[id] {
				seen[id] = true
				result = append(result, item)
			}
		}
	}
	return result, errors.Join(failures...)
}

func instrument(market, id, base, quote, price, size, status string, metadata map[string]string) domain.Instrument {
	return domain.Instrument{NativeID: id, Market: market, BaseAsset: base, QuoteAsset: quote,
		PriceIncrement: price, SizeIncrement: size, Availability: status,
		SupportedIntervals: append([]string(nil), domain.Intervals...), ProviderMetadata: metadata}
}

// Reviewed against Binance's public REST schemas and TradFi perpetual enum on
// 2026-10-20: /api/v3/exchangeInfo, /fapi/v1/exchangeInfo, /dapi/v1/exchangeInfo.
func (a *publicAdapter) binanceInstruments(ctx context.Context, category string) ([]domain.Instrument, error) {
	base, path, market := a.base, "/api/v3/exchangeInfo", "spot"
	q := url.Values{"permissions": {"SPOT"}, "showPermissionSets": {"false"}}
	if category != "spot" {
		market, q = "perpetual", nil
		base, path = a.futuresBase, "/fapi/v1/exchangeInfo"
		if category == "inverse" {
			base, path = a.inverseBase, "/dapi/v1/exchangeInfo"
		}
	}
	var payload struct {
		Symbols []struct {
			Symbol, Status, ContractStatus, BaseAsset, QuoteAsset, ContractType, MarginAsset string
			IsSpotTradingAllowed                                                             bool `json:"isSpotTradingAllowed"`
			Filters                                                                          []struct{ FilterType, TickSize, StepSize string }
		}
	}
	if err := a.getAt(ctx, base, path, q, &payload); err != nil {
		return nil, err
	}
	var result []domain.Instrument
	for _, v := range payload.Symbols {
		status := v.Status
		if status == "" {
			status = v.ContractStatus
		}
		if market == "spot" {
			if !v.IsSpotTradingAllowed {
				continue
			}
		} else if (v.ContractType != "PERPETUAL" && v.ContractType != "TRADIFI_PERPETUAL") || status != "TRADING" {
			continue
		}
		price, size := "", ""
		for _, f := range v.Filters {
			if f.FilterType == "PRICE_FILTER" {
				price = f.TickSize
			}
			if f.FilterType == "LOT_SIZE" {
				size = f.StepSize
			}
		}
		result = append(result, instrument(market, v.Symbol, v.BaseAsset, v.QuoteAsset, price, size, status,
			map[string]string{"category": category, "contractType": v.ContractType, "settleCoin": v.MarginAsset}))
	}
	return result, nil
}

// Bybit spot is unpaginated; both derivative categories use nextPageCursor.
// https://bybit-exchange.github.io/docs/v5/market/instrument (2026-10-20).
func (a *publicAdapter) bybitInstruments(ctx context.Context, category string) ([]domain.Instrument, error) {
	q := url.Values{"category": {category}}
	market := "spot"
	if category != "spot" {
		market = "perpetual"
		q.Set("limit", "1000")
		q.Set("status", "Trading")
	}
	var result []domain.Instrument
	seen, cursors := map[string]bool{}, map[string]bool{}
	for page := 0; page < 30; page++ {
		var payload struct {
			Result struct {
				NextPageCursor string `json:"nextPageCursor"`
				List           []struct {
					Symbol, BaseCoin, QuoteCoin, Status, ContractType, SettleCoin string
					PriceFilter                                                   struct{ TickSize string }
					LotSizeFilter                                                 struct{ BasePrecision, QtyStep string }
				}
			}
		}
		if err := a.get(ctx, "/v5/market/instruments-info", q, &payload); err != nil {
			return nil, err
		}
		progress := 0
		for _, v := range payload.Result.List {
			if seen[v.Symbol] {
				continue
			}
			seen[v.Symbol] = true
			progress++
			if market == "perpetual" && (v.Status != "Trading" || (v.ContractType != "LinearPerpetual" && v.ContractType != "InversePerpetual")) {
				continue
			}
			size := v.LotSizeFilter.BasePrecision
			if size == "" {
				size = v.LotSizeFilter.QtyStep
			}
			result = append(result, instrument(market, v.Symbol, v.BaseCoin, v.QuoteCoin, v.PriceFilter.TickSize, size, v.Status,
				map[string]string{"category": category, "contractType": v.ContractType, "settleCoin": v.SettleCoin}))
		}
		next := payload.Result.NextPageCursor
		if category == "spot" || next == "" {
			return result, nil
		}
		if next == q.Get("cursor") || cursors[next] || progress == 0 {
			return nil, &Error{Kind: "internal", Message: "Bybit " + category + " instrument pagination made no progress"}
		}
		cursors[next] = true
		q.Set("cursor", next)
	}
	return nil, &Error{Kind: "internal", Message: "Bybit " + category + " instrument pagination exceeded bounded request budget"}
}
