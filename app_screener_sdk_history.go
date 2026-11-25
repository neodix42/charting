package main

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"cryptodesk/internal/domain"
)

type dukascopyHistoryClient interface {
	HistoricalSource() (string, bool)
	ExportTicks(context.Context, string, string, int64, int64, string) (int64, error)
	DownloadHistory(context.Context, domain.FeedKey, int64, int64, int) ([]domain.Candle, error)
}

// The Node workers receive source identity and private export paths, never the
// credentials or a second connection implementation. Native charts and history
// downloads share the same account-scoped SDK and its durable candle cache.
type dukascopyScreenerHistory struct{ client dukascopyHistoryClient }

func (p dukascopyScreenerHistory) HistoricalSource() (string, bool) {
	return p.client.HistoricalSource()
}

func (p dukascopyScreenerHistory) ExportTicks(ctx context.Context, source, symbol string, from, to int64, output string) (int64, error) {
	return p.client.ExportTicks(ctx, source, symbol, from, to, output)
}

func (p dukascopyScreenerHistory) ExportCandles(ctx context.Context, source, symbol string, from, to int64, output string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	const day = int64(86_400_000)
	if from < 0 || from%day != 0 || to%day != 0 || to <= from || to-from > 366*day || to > time.Now().UTC().Truncate(24*time.Hour).UnixMilli() {
		return 0, errors.New("SDK candle export requires a bounded closed UTC daily range")
	}
	market := "cfd"
	switch strings.ToLower(symbol) {
	case "btcusd":
	case "xauxag":
		market = "metal"
	default:
		return 0, errors.New("SDK native daily export supports BTCUSD and XAUXAG only")
	}
	checkSource := func() error {
		current, configured := p.client.HistoricalSource()
		if !configured || source == "" || current != source {
			return errors.New("Dukascopy history account changed; saved history was preserved")
		}
		return ctx.Err()
	}
	if err := checkSource(); err != nil {
		return 0, err
	}
	info, err := os.Lstat(output)
	if !filepath.IsAbs(output) || err != nil || !info.Mode().IsRegular() || info.Size() != 0 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return 0, errors.New("SDK output must be an existing empty private regular file")
	}
	key := domain.FeedKey{Exchange: "dukascopy", Market: market, Instrument: strings.ToUpper(symbol), Timeframe: "1d"}
	bars, err := p.client.DownloadHistory(ctx, key, from/1000, to/1000, 1000)
	if err != nil {
		return 0, err
	}
	if err := checkSource(); err != nil {
		return 0, err
	}
	if len(bars) > 366 {
		return 0, errors.New("SDK daily export exceeded its bounded candle count")
	}
	previous := int64(-1)
	for _, bar := range bars {
		if err := bar.Validate(); err != nil {
			return 0, err
		}
		if !bar.Closed || bar.Time*1000 < from || (bar.Time+86400)*1000 > to || bar.Time%86400 != 0 || bar.Time <= previous {
			return 0, errors.New("SDK returned unordered, open or out-of-range daily candles")
		}
		previous = bar.Time
	}
	file, err := os.OpenFile(output, os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	if current, err := file.Stat(); err != nil || !os.SameFile(info, current) || current.Size() != 0 {
		return 0, errors.New("SDK output changed before export")
	}
	writer := csv.NewWriter(file)
	// SDK quote volume and historical export volume are not silently mixed.
	// Native OHLC stays exact; volume-dependent enrichment remains unavailable.
	if err := writer.Write([]string{"timestamp", "open", "high", "low", "close"}); err != nil {
		return 0, err
	}
	for _, bar := range bars {
		if err := checkSource(); err != nil {
			return 0, err
		}
		fields := []string{strconv.FormatInt(bar.Time*1000, 10)}
		for _, value := range []float64{bar.Open, bar.High, bar.Low, bar.Close} {
			fields = append(fields, strconv.FormatFloat(value, 'g', -1, 64))
		}
		if err := writer.Write(fields); err != nil {
			return 0, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return 0, err
	}
	if err := checkSource(); err != nil {
		return 0, err
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	return int64(len(bars)), nil
}
