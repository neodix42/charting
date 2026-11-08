package screener

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"cryptodesk/internal/domain"
	"cryptodesk/internal/exchange"
	"github.com/shopspring/decimal"
)

// Downloads, alerts and chart reads share series ownership. The CSV and its
// checksum-bound identity are a single journalled publication, including after a
// process interruption. This store is independent of the optional chart cache.
var hyperliquidHistoryOwners publicAlertData
var publicHistoryPublications publicAlertData

func hyperliquidHistoryRoot(profile string) string {
	return filepath.Join(profile, "screener-public-history", "hyperliquid")
}
func hyperliquidHistoryLock(ctx context.Context, root string, asset publicAlertAsset, period string) (func(), error) {
	return hyperliquidHistoryOwners.acquireKey(ctx, root+"/"+publicAlertSymbol(asset.Market, asset.NativeID)+"/"+period)
}
func readHyperliquidRaw(root string, asset publicAlertAsset, period string) ([]domain.Candle, publicAlertRawState, []byte, error) {
	stem := publicAlertSymbol(asset.Market, asset.NativeID) + "-" + period
	rawPath, metaPath := filepath.Join(root, "raw", stem+".csv"), filepath.Join(root, "raw", stem+".json")
	// Readers wait only for an atomic publication, never for a provider request
	// or indicator calculation belonging to an alert/download writer.
	release, err := publicHistoryPublications.acquireKey(context.Background(), rawPath)
	if err != nil {
		return nil, publicAlertRawState{}, nil, err
	}
	defer release()
	state := publicAlertRawState{Source: "hyperliquid", Market: asset.Market, NativeID: asset.NativeID, Period: period}

	if err := publicAlertRecoverRaw(rawPath, metaPath, state); err != nil {
		return nil, state, nil, err
	}
	raw, err := publicAlertReadBounded(rawPath, 32<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil, state, nil, nil
	}
	if err != nil {
		return nil, state, nil, err
	}
	meta, err := publicAlertReadBounded(metaPath, 1<<20)
	if err != nil {
		return nil, state, nil, fmt.Errorf("read retained Hyperliquid identity: %w", err)
	}
	var previous publicAlertRawState
	if json.Unmarshal(meta, &previous) != nil || previous.Source != state.Source || previous.Market != state.Market || previous.NativeID != state.NativeID || previous.Period != state.Period {
		return nil, state, nil, errors.New("retained Hyperliquid history identity is invalid; original preserved")
	}
	digest := sha256.Sum256(raw)
	if previous.Checksum != hex.EncodeToString(digest[:]) {
		return nil, state, nil, errors.New("retained Hyperliquid history checksum is invalid; original preserved")
	}
	bars, err := publicAlertReadRaw(raw, exchange.TimeframeSeconds(publicAlertIntervals[period]))
	return bars, previous, raw, err
}

// mergeHyperliquidHistory imports verified native/derived closed bars from the
// downloader without discarding older history already retained by Live Alerts.
func mergeHyperliquidHistory(ctx context.Context, profile string, asset publicAlertAsset, period string, incoming []domain.Candle) error {
	interval := publicAlertIntervals[period]
	key := domain.FeedKey{Exchange: "hyperliquid", Market: asset.Market, Instrument: asset.NativeID, Timeframe: interval}
	if err := key.Validate(); err != nil {
		return err
	}
	if period != "d1" && period != "w1" {
		return errors.New("downloaded Hyperliquid history supports D1 and W1")
	}
	root := hyperliquidHistoryRoot(profile)
	release, err := hyperliquidHistoryLock(ctx, root, asset, period)
	if err != nil {
		return err
	}
	defer release()
	for _, dir := range []string{filepath.Dir(root), root, filepath.Join(root, "raw")} {
		if err = checkHistoryDirectory(dir, true); err != nil {
			return err
		}
	}
	bars, state, previous, err := readHyperliquidRaw(root, asset, period)
	if err != nil {
		return err
	}
	byTime := map[int64]domain.Candle{}
	for _, bar := range bars {
		byTime[bar.Time] = bar
	}
	step := exchange.TimeframeSeconds(interval)
	for _, bar := range incoming {
		if err = bar.Validate(); err != nil || exchange.BucketStart(bar.Time, step) != bar.Time {
			return errors.New("invalid imported Hyperliquid candle")
		}
		if !bar.Closed {
			continue
		}
		if old, ok := byTime[bar.Time]; ok && old.Quality == "authoritative" && bar.Quality != "authoritative" {
			continue
		}
		bar.SourceRevision = 0
		byTime[bar.Time] = bar
		if bar.Quality == "authoritative" {
			state.CheckedThrough = max(state.CheckedThrough, bar.Time+step)
		}
	}
	bars = bars[:0]
	for _, bar := range byTime {
		bars = append(bars, bar)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Time < bars[j].Time })
	encoded, err := publicAlertRawCSV(bars, true)
	if err != nil {
		return err
	}
	if len(encoded) > 32<<20 {
		return errors.New("retained Hyperliquid history exceeds the 32 MiB per-series bound")
	}
	digest := sha256.Sum256(encoded)
	state.Checksum = hex.EncodeToString(digest[:])
	previousChecksum := ""
	if previous != nil {
		old := sha256.Sum256(previous)
		previousChecksum = hex.EncodeToString(old[:])
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	stem := publicAlertSymbol(asset.Market, asset.NativeID) + "-" + period
	return publicAlertPublishRaw(filepath.Join(root, "raw", stem+".csv"), filepath.Join(root, "raw", stem+".json"), encoded, state, previousChecksum)
}

func hyperliquidWeeklyFromDaily(daily []domain.Candle) []domain.Candle {
	const day, week = int64(86400), int64(7 * 86400)
	daily = append([]domain.Candle(nil), daily...)
	sort.Slice(daily, func(i, j int) bool { return daily[i].Time < daily[j].Time })
	out := []domain.Candle{}
	for i := 0; i < len(daily); {
		first := daily[i]
		start := exchange.BucketStart(first.Time, week)
		bar := first
		volume := decimal.NewFromFloat(first.Volume)
		bar.Time = start
		bar.SourceRevision = 0
		complete := first.Time == start && first.Closed && first.Quality == "authoritative"
		count := 1
		j := i + 1
		for j < len(daily) && exchange.BucketStart(daily[j].Time, week) == start {
			next := daily[j]
			bar.High = max(bar.High, next.High)
			bar.Low = min(bar.Low, next.Low)
			bar.Close = next.Close
			volume = volume.Add(decimal.NewFromFloat(next.Volume))
			complete = complete && next.Time == start+int64(count)*day && next.Closed && next.Quality == "authoritative"
			count++
			j++
		}
		// The raw store contains closed daily bars only. A partial week is retained
		// for chart display but cannot satisfy an authoritative alert close.
		bar.Volume, _ = volume.Float64()
		bar.Closed = daily[j-1].Time+day >= start+week
		bar.Quality = "provisional"
		if complete && count == 7 {
			bar.Quality = "authoritative"
			bar.Closed = true
		}
		out = append(out, bar)
		i = j
	}
	return out
}

// HyperliquidHistory exposes only the matching native market's downloaded D1
// bars or Monday-UTC W1 derivatives. It never synthesizes intraday candles.
func (m *Manager) HyperliquidHistory(key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	return readHyperliquidHistory(context.Background(), m.profile, key, from, to, limit)
}
func readHyperliquidHistory(ctx context.Context, profile string, key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if key.Exchange != "hyperliquid" || (key.Timeframe != "1d" && key.Timeframe != "1w") {
		return nil, nil
	}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	asset := publicAlertAsset{Market: key.Market, NativeID: key.Instrument}
	root := hyperliquidHistoryRoot(profile)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	daily, _, _, err := readHyperliquidRaw(root, asset, "d1")
	if err != nil {
		return nil, err
	}
	bars := daily
	if key.Timeframe == "1w" {
		weekly, _, _, e := readHyperliquidRaw(root, asset, "w1")
		if e != nil {
			return nil, e
		}
		byTime := map[int64]domain.Candle{}
		for _, bar := range weekly {
			byTime[bar.Time] = bar
		}
		for _, bar := range hyperliquidWeeklyFromDaily(daily) {
			old, ok := byTime[bar.Time]
			if !ok || bar.Quality == "authoritative" || old.Quality != "authoritative" {
				byTime[bar.Time] = bar
			}
		}
		bars = nil
		for _, bar := range byTime {
			bars = append(bars, bar)
		}
		sort.Slice(bars, func(i, j int) bool { return bars[i].Time < bars[j].Time })
	}
	out := []domain.Candle{}
	for _, bar := range bars {
		if bar.Time >= from && (to == 0 || bar.Time <= to) {
			bar.SourceRevision = 0
			out = append(out, bar)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// A weekly alert needs native D1 updates only where the shared downloaded
// history has no authoritative daily bar. UTC-week conversion is always local.
func hyperliquidAlertWeeklyHistory(ctx context.Context, adapter exchange.Adapter, profile string, asset publicAlertAsset, from, to int64) ([]domain.Candle, error) {
	const day, week = int64(86400), int64(7 * 86400)
	first := exchange.BucketStart(from, week)
	if first < from {
		first += week
	}
	end := exchange.BucketStart(to-1, week) + week
	key := domain.FeedKey{Exchange: "hyperliquid", Market: asset.Market, Instrument: asset.NativeID, Timeframe: "1d"}
	daily, err := readHyperliquidHistory(ctx, profile, key, first, end-1, 0)
	if err != nil {
		return nil, err
	}
	byTime := map[int64]domain.Candle{}
	for _, bar := range daily {
		byTime[bar.Time] = bar
	}
	added := []domain.Candle{}
	for cursor := first; cursor < end; {
		if bar, ok := byTime[cursor]; ok && bar.Closed && bar.Quality == "authoritative" {
			cursor += day
			continue
		}
		start := cursor
		cursor += day
		for cursor < end && cursor-start < 1000*day {
			bar, ok := byTime[cursor]
			if ok && bar.Closed && bar.Quality == "authoritative" {
				break
			}
			cursor += day
		}
		fetched, e := adapter.History(ctx, key, start, cursor, 1000)
		if e != nil {
			return nil, e
		}
		for _, bar := range fetched {
			if bar.Time >= start && bar.Time < cursor && bar.Closed {
				if e = bar.Validate(); e != nil {
					return nil, e
				}
				byTime[bar.Time] = bar
				added = append(added, bar)
			}
		}
	}
	if len(added) > 0 {
		if err = mergeHyperliquidHistory(ctx, profile, asset, "d1", added); err != nil {
			return nil, err
		}
	}
	daily = nil
	for _, bar := range byTime {
		daily = append(daily, bar)
	}
	return hyperliquidWeeklyFromDaily(daily), nil
}
