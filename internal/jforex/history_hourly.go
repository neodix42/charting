package jforex

import (
	"context"
	"errors"
	"time"

	"cryptodesk/internal/domain"
)

// DownloadHourlyHistory is separate from chart-cache coverage: verified full
// session hours intentionally omit native session-boundary candles that remain
// valid chart observations. The caller owns its source-fenced history cache.
func (m *Manager) DownloadHourlyHistory(ctx context.Context, key domain.FeedKey, from, to int64, limit int, fullOpenHours bool) ([]domain.Candle, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	const hour = int64(3600)
	if key.Timeframe != "1h" || key.Market != "forex" && key.Market != "metal" && key.Market != "cfd" || from < 0 || to <= from || from%hour != 0 || to%hour != 0 || to-from > 1000*hour || to > time.Now().Unix()/hour*hour || limit < 1 || limit > 1000 || !fullOpenHours && (key.Instrument != "BTCUSD" || key.Market != "cfd") {
		return nil, errors.New("JForex hourly downloads require at most 1000 closed UTC hours and a verified session policy")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	p, release, err := m.begin(ctx, true, "")
	if err != nil {
		return nil, err
	}
	defer release()
	var version struct {
		HourlyHistory       string `json:"hourlyHistory"`
		NativeHourlyHistory string `json:"nativeHourlyHistory"`
	}
	if err = p.call(ctx, map[string]any{"op": "version"}, &version); err != nil {
		return nil, err
	}
	if fullOpenHours && version.HourlyHistory != "bid-full-open-hour-v1" || !fullOpenHours && version.NativeHourlyHistory != "bid-native-hour-v1" {
		return nil, errors.New("The selected JForex bridge does not support the requested hourly history session contract; update the matching runtime")
	}
	var bars []domain.Candle
	if err = p.call(ctx, map[string]any{"op": "history", "feedKey": key, "from": from, "to": to, "limit": limit, "download": true, "fullOpenHours": fullOpenHours}, &bars); err != nil {
		return nil, err
	}
	if len(bars) > limit {
		return nil, errors.New("JForex hourly history exceeded its bounded candle count")
	}
	previous := int64(-1)
	for _, bar := range bars {
		if err := bar.Validate(); err != nil {
			return nil, err
		}
		if !bar.Closed || bar.Time < from || bar.Time+hour > to || bar.Time%hour != 0 || bar.Time <= previous {
			return nil, errors.New("JForex hourly history contains open, unordered or out-of-range candles")
		}
		previous = bar.Time
	}
	return normalizeBars(key, bars)
}

// InspectHistoricalCalendar returns only bounded provider rule/date metadata;
// it does not read account fields, export prices or alter trading state.
func (m *Manager) InspectHistoricalCalendar(ctx context.Context, key domain.FeedKey, from, to int64) (map[string]any, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if key.Timeframe != "1h" || from < 0 || to <= from || from%3600 != 0 || to%3600 != 0 || to-from > 1000*3600 || to > time.Now().Unix()/3600*3600 {
		return nil, errors.New("Calendar diagnostics require at most1000closed UTC hours")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	p, release, err := m.begin(ctx, true, "")
	if err != nil {
		return nil, err
	}
	defer release()
	var version struct {
		Calendar string `json:"historicalCalendar"`
	}
	if err = p.call(ctx, map[string]any{"op": "version"}, &version); err != nil {
		return nil, err
	}
	if version.Calendar != "read-calendar-proof-v1" {
		return nil, errors.New("The selected JForex bridge does not support bounded historical calendar diagnostics")
	}
	var result map[string]any
	if err = p.call(ctx, map[string]any{"op": "historyCalendar", "feedKey": key, "from": from, "to": to}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (m *Manager) InspectHourlyCandleData(ctx context.Context, key domain.FeedKey, from, to int64, filter string) (map[string]any, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if key.Timeframe != "1h" || from < 0 || to <= from || from%3600 != 0 || to%3600 != 0 || to-from > 1000*3600 || to > time.Now().Unix()/3600*3600 || filter != "WEEKENDS" && filter != "NO_FILTER" {
		return nil, errors.New("Hourly diagnostic requires bounded closed hours and a supported filter")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	p, release, err := m.begin(ctx, true, "")
	if err != nil {
		return nil, err
	}
	defer release()
	var result map[string]any
	if err = p.call(ctx, map[string]any{"op": "historyCandleDiagnostic", "feedKey": key, "from": from, "to": to, "filter": filter}, &result); err != nil {
		return nil, err
	}
	return result, nil
}
