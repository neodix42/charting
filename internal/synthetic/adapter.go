// Package synthetic exposes Dukascopy native instruments and explicitly labelled
// calculated, downloaded ratio history without depending on the desktop shell.
package synthetic

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"cryptodesk/internal/screener"
)

type HistoryReader func(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error)
type Adapter struct {
	native  exchange.Adapter
	history HistoryReader
	poll    time.Duration
}

func New(native exchange.Adapter, history HistoryReader) *Adapter {
	return &Adapter{native: native, history: history, poll: 30 * time.Second}
}

func IsFamily(key domain.FeedKey) bool {
	_, ok := screener.SyntheticDefinitionFor(key.Instrument)
	return key.Exchange == "dukascopy" && ok
}

// Catalog decorates existing native identities in place and adds only missing
// family identities. Native compatibility remains in the underlying adapter.
func Catalog(items []domain.Instrument, warning string) []domain.Instrument {
	out := make([]domain.Instrument, 0, len(items)+3)
	found := map[string]bool{}
	for _, item := range items {
		if definition, ok := screener.SyntheticDefinitionFor(item.NativeID); ok {
			if found[definition.ID] {
				continue
			}
			found[definition.ID] = true
			item = decorate(item, definition, true, warning)
		}
		out = append(out, item)
	}
	for _, definition := range screener.SyntheticDefinitions() {
		if found[definition.ID] {
			continue
		}
		market := "cfd"
		if definition.ID == "xauxag" {
			market = "metal"
		}
		item := domain.Instrument{NativeID: strings.ToUpper(definition.ID), Market: market, Availability: "available", PriceIncrement: "0.00000001"}
		switch definition.ID {
		case "btcusd":
			item.BaseAsset, item.QuoteAsset, item.PriceIncrement = "BTC", "USD", "0.01"
		case "btcxau":
			item.BaseAsset, item.QuoteAsset = "BTC", "XAU"
		case "xauspx":
			item.BaseAsset, item.QuoteAsset = "XAU", "SPX"
		case "ethbtc":
			item.BaseAsset, item.QuoteAsset, item.PriceIncrement = "ETH", "BTC", "0.0000000001"
		case "xauxag":
			item.BaseAsset, item.QuoteAsset, item.PriceIncrement = "XAU", "XAG", "0.000001"
		}
		out = append(out, decorate(item, definition, false, warning))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NativeID < out[j].NativeID })
	return out
}

func decorate(item domain.Instrument, definition screener.SyntheticDefinition, native bool, warning string) domain.Instrument {
	metadata := map[string]string{}
	for key, value := range item.ProviderMetadata {
		metadata[key] = value
	}
	metadata["syntheticCategory"], metadata["category"] = "true", screener.SyntheticCategory
	metadata["description"], metadata["displaySymbol"] = definition.Name, strings.ToUpper(definition.ID)
	metadata["synthetic"], metadata["tradable"] = "false", "false"
	if native {
		metadata["tradable"] = "true"
	}
	if definition.Denominator != "" {
		metadata["formula"] = strings.ToUpper(definition.Numerator) + " / " + strings.ToUpper(definition.Denominator)
	}
	if definition.Calculated {
		metadata["synthetic"], metadata["tradable"], metadata["assetType"] = "true", "false", "synthetic"
		metadata["approximate"], metadata["volumeUnit"], metadata["dataMode"] = "true", "unavailable", "downloaded closed candles"
		metadata["observationResolution"] = "Sampled BID ratios; downloaded history records hourly or tick resolution"
		metadata["sdkSymbol"] = item.BaseAsset + "/" + item.QuoteAsset
	}
	if warning != "" {
		metadata["catalogWarning"] = warning
	}
	item.ProviderMetadata = metadata
	item.SupportedIntervals = []string{"1d", "1w"}
	return item
}

func (a *Adapter) Instruments(ctx context.Context) ([]domain.Instrument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := a.native.Instruments(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	warning := ""
	if err != nil {
		warning = "JForex native discovery unavailable; synthetic history choices remain available. " + err.Error()
	}
	return Catalog(items, warning), nil
}

func (a *Adapter) nativeAvailable(key domain.FeedKey) bool {
	if check, ok := a.native.(interface{ NativeInstrumentAvailable(string, string) bool }); ok {
		return check.NativeInstrumentAvailable(key.Instrument, key.Market)
	}
	return true
}

func (a *Adapter) History(ctx context.Context, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	definition, family := screener.SyntheticDefinitionFor(key.Instrument)
	if key.Exchange != "dukascopy" || !family || key.Timeframe != "1d" && key.Timeframe != "1w" {
		return a.native.History(ctx, key, from, to, limit)
	}
	var native []domain.Candle
	if !definition.Calculated && a.nativeAvailable(key) {
		var err error
		native, err = a.native.History(ctx, key, from, to, limit)
		if err != nil {
			return nil, err
		}
	}
	if a.history == nil {
		if len(native) > 0 {
			return native, nil
		}
		return nil, screener.ErrSyntheticHistoryUnavailable
	}
	retained, err := a.history(ctx, key, from, to, limit)
	if err != nil {
		if len(native) > 0 && errors.Is(err, screener.ErrSyntheticHistoryUnavailable) {
			return native, nil
		}
		return nil, err
	}
	byTime := map[int64]domain.Candle{}
	for _, bar := range retained {
		if bar.Closed {
			byTime[bar.Time] = bar
		}
	}
	fullNativeFrom := int64(0)
	start, startErr := time.Parse(time.RFC3339Nano, definition.NativeHistoryFromTime)
	if startErr != nil {
		start, startErr = time.Parse("2006-01-02", definition.NativeHistoryFrom)
	}
	if startErr == nil {
		step := exchange.TimeframeSeconds(key.Timeframe)
		fullNativeFrom = exchange.BucketStart(start.Unix(), step)
		if start.After(time.Unix(fullNativeFrom, 0)) {
			fullNativeFrom += step
		}
	}
	for _, bar := range native {
		if old, ok := byTime[bar.Time]; !ok || !old.Synthetic || bar.Time >= fullNativeFrom {
			byTime[bar.Time] = bar
		}
	}
	out := make([]domain.Candle, 0, len(byTime))
	for _, bar := range byTime {
		if bar.Time >= from && (to == 0 || bar.Time < to) {
			out = append(out, bar)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (a *Adapter) Stream(ctx context.Context, key domain.FeedKey, emit func([]domain.Candle, domain.Health)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	definition, family := screener.SyntheticDefinitionFor(key.Instrument)
	if key.Exchange != "dukascopy" || !family || !definition.Calculated && a.nativeAvailable(key) || key.Timeframe != "1d" && key.Timeframe != "1w" {
		return a.native.Stream(ctx, key, emit)
	}
	if err := key.Validate(); err != nil {
		return err
	}
	timer := time.NewTicker(a.poll)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !definition.Calculated && a.nativeAvailable(key) {
			timer.Stop()
			return a.native.Stream(ctx, key, emit)
		}
		now := time.Now()
		bars, err := a.History(ctx, key, 0, now.Unix()+1, 1000)
		if err := ctx.Err(); err != nil {
			return err
		}
		health := domain.Health{State: "provisional", Reason: "Synthetic approximate BID observations from downloaded closed history; refresh in Screener or Live Alerts. No broker execution or traded volume.", LastMessageAt: now.UnixMilli()}
		if !definition.Calculated && len(bars) > 0 && !bars[len(bars)-1].Synthetic {
			health.Reason = "Downloaded native Dukascopy closed history; no live broker quote connection."
		}
		if err != nil {
			health.State, health.Reason = "unavailable", err.Error()
		} else if len(bars) > 0 {
			health.LastTradeAt = (bars[len(bars)-1].Time + exchange.TimeframeSeconds(key.Timeframe)) * 1000
		}
		emit(bars, health)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *Adapter) Diagnostics() exchange.Diagnostics {
	if native, ok := a.native.(interface{ Diagnostics() exchange.Diagnostics }); ok {
		return native.Diagnostics()
	}
	return exchange.Diagnostics{}
}
