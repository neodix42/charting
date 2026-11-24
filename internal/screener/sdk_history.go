package screener

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"cryptodesk/internal/exchange"
)

// DukascopyHistoryProvider keeps account credentials and SDK ownership in Go.
// Exports write into the exclusive empty file supplied by this coordinator.
type DukascopyHistoryProvider interface {
	HistoricalSource() (sourceKey string, configured bool)
	ExportTicks(ctx context.Context, expectedSourceKey, symbol string, fromMs, toMs int64, output string) (int64, error)
	ExportCandles(ctx context.Context, expectedSourceKey, symbol string, fromMs, toMs int64, output string) (int64, error)
}

type providerSDKSession struct {
	SourceKey string `json:"sourceKey"`
}

const sdkHistoryHour = int64(time.Hour / time.Millisecond)
const sdkHistoryDay = 24 * sdkHistoryHour
const sdkHistoryMaxBytes = 64 << 20
const sdkHistoryMaxRows = 1_000_000

func validSDKSourceKey(key string) bool {
	if len(key) != 64 || key != strings.ToLower(key) {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

func (r *providerReplies) validateSDKHistoryRequest(event providerEvent) error {
	if r.state.SDK == nil || r.state.history == nil || !validSDKSourceKey(r.state.SDK.SourceKey) {
		return errors.New("Dukascopy SDK history is not configured for this batch")
	}
	if event.From < 0 || event.To <= event.From {
		return errors.New("invalid Dukascopy SDK history bounds")
	}
	now := r.state.limiter.timeNow().UnixMilli()
	switch event.Timeframe {
	case "tick":
		switch event.Symbol {
		case "btcusd", "xauusd", "xagusd", "ethusd", "usa500idxusd", "xauxag":
		default:
			return errors.New("unsupported Dukascopy SDK tick source")
		}
		if event.From%sdkHistoryHour != 0 || event.To-event.From != sdkHistoryHour || event.To > now/sdkHistoryHour*sdkHistoryHour {
			return errors.New("Dukascopy SDK ticks require one complete closed UTC hour")
		}
	case "d1":
		if event.Symbol != "btcusd" && event.Symbol != "xauxag" {
			return errors.New("unsupported Dukascopy SDK daily source")
		}
		if event.From%sdkHistoryDay != 0 || event.To%sdkHistoryDay != 0 || event.To-event.From > 366*sdkHistoryDay || event.To > now/sdkHistoryDay*sdkHistoryDay {
			return errors.New("Dukascopy SDK candles require at most 366 complete closed UTC days")
		}
	default:
		return errors.New("unsupported Dukascopy SDK history timeframe")
	}
	return nil
}

func (r *providerReplies) checkSDKSource() error {
	return checkSDKHistoryContext(r.ctx)
}

func checkSDKHistoryContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state := sessionFromContext(ctx)
	if state == nil || state.SDK == nil || state.history == nil {
		return nil
	}
	key, configured := state.history.HistoricalSource()
	if !configured || key != state.SDK.SourceKey {
		return errors.New("Dukascopy SDK account source changed; restart the history batch for the current account")
	}
	return nil
}

func (r *providerReplies) exportSDKHistory(event providerEvent) (filename string, rows int64, exportErr error) {
	if err := r.checkSDKSource(); err != nil {
		return "", 0, err
	}
	// SDK history shares FIFO spacing and HTTP 429 pauses with all workers.
	// A Jetta-specific HTTP 403 does not prohibit the authenticated SDK service.
	if err := r.state.limiter.permitInternal(r.ctx, r.waiting, true); err != nil {
		return "", 0, err
	}
	if err := r.checkSDKSource(); err != nil {
		return "", 0, err
	}
	file, err := os.CreateTemp(r.state.Directory, "sdk-history-*.csv")
	if err != nil {
		return "", 0, fmt.Errorf("create SDK history export: %w", err)
	}
	filename = file.Name()
	owned := filename
	initial, statErr := file.Stat()
	closeErr := file.Close()
	r.mu.Lock()
	// Node removes each consumed export. Keep cleanup bookkeeping bounded by
	// outstanding files instead of retaining years of hourly path strings.
	remaining := r.temporary[:0]
	for _, previous := range r.temporary {
		if _, err := os.Lstat(previous); !errors.Is(err, os.ErrNotExist) {
			remaining = append(remaining, previous)
		}
	}
	r.temporary = remaining
	r.temporary = append(r.temporary, filename)
	r.mu.Unlock()
	defer func() {
		if exportErr != nil {
			_ = os.Remove(owned)
		}
	}()
	if statErr != nil {
		return "", 0, statErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	if event.Timeframe == "tick" {
		rows, err = r.state.history.ExportTicks(r.ctx, r.state.SDK.SourceKey, event.Symbol, event.From, event.To, filename)
	} else {
		rows, err = r.state.history.ExportCandles(r.ctx, r.state.SDK.SourceKey, event.Symbol, event.From, event.To, filename)
	}
	if err != nil {
		var providerError *exchange.Error
		if errors.As(err, &providerError) && providerError.Kind == "rateLimited" {
			retryAfter := ""
			if now := r.state.limiter.timeNow().Unix(); providerError.RetryAt > now {
				retryAfter = strconv.FormatInt(providerError.RetryAt-now, 10)
			}
			if _, observeErr := r.state.limiter.observe(429, retryAfter); observeErr != nil {
				return "", 0, errors.Join(err, observeErr)
			}
		}
		return "", 0, err
	}
	if err = r.checkSDKSource(); err != nil {
		return "", 0, err
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return "", 0, fmt.Errorf("inspect SDK history export: %w", err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(initial, info) || info.Mode().Perm() != initial.Mode().Perm() || info.Size() < 1 || info.Size() > sdkHistoryMaxBytes || rows < 0 || rows > sdkHistoryMaxRows {
		return "", 0, errors.New("Dukascopy SDK history export violates file or row limits")
	}
	return filename, rows, nil
}
