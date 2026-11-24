package jforex

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const tickHistoryCapability = "bid-hour-file-v1"
const tickHistoryHour = int64(time.Hour / time.Millisecond)
const tickHistoryMaxRows = int64(1_000_000)
const tickHistoryMaxBytes = int64(64 * 1024 * 1024)

func historicalSourceKey(config configuration) string {
	sum := sha256.Sum256([]byte(config.Environment + "\x00" + config.Username))
	return hex.EncodeToString(sum[:])
}

// HistoricalSource identifies the configured SDK account without starting a
// session or exposing its credentials. It fences private source caches against
// account/environment changes, including changes while a download is queued.
func (m *Manager) HistoricalSource() (sourceKey string, configured bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.loadError != nil || m.config.Username == "" || m.config.Password == "" {
		return "", false
	}
	return historicalSourceKey(m.config), true
}

func tickExportFile(path string, empty bool) (os.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 || strings.IndexByte(path, 0) >= 0 {
		return nil, errors.New("JForex tick output must be an absolute private file path")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect JForex tick output: %w", err)
	}
	// Windows access control does not map to POSIX permission bits. The caller
	// creates this file in its private job directory on every platform.
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 || empty && info.Size() != 0 || info.Size() > tickHistoryMaxBytes {
		return nil, errors.New("JForex tick output must be an existing empty regular private 0600 file, no larger than 64 MiB")
	}
	return info, nil
}

// ExportTicks writes one closed UTC hour to an existing empty private file.
// A failed or canceled export is never a complete dataset; its caller owns and
// discards that scratch file. Successful empty hours contain the CSV header.
func (m *Manager) ExportTicks(ctx context.Context, expectedSourceKey, symbol string, fromMs, toMs int64, output string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" || len(symbol) > 128 || strings.IndexFunc(symbol, func(r rune) bool { return (r < 'A' || r > 'Z') && (r < '0' || r > '9') }) >= 0 {
		return 0, errors.New("Invalid native JForex tick instrument")
	}
	if fromMs < 0 || toMs <= fromMs || fromMs%tickHistoryHour != 0 || toMs-fromMs != tickHistoryHour || toMs > time.Now().UnixMilli()/tickHistoryHour*tickHistoryHour {
		return 0, errors.New("JForex tick history requires exactly one closed UTC hour")
	}
	initial, err := tickExportFile(output, true)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	// Reserve the account before lazy login. Configure cannot switch accounts
	// until the bridge writer has completed or acknowledged its cancellation.
	m.mu.Lock()
	if m.closed || m.loadError != nil || m.config.Username == "" || m.config.Password == "" || expectedSourceKey == "" || expectedSourceKey != historicalSourceKey(m.config) {
		m.mu.Unlock()
		return 0, errors.New("The configured JForex history source changed or is unavailable; restart this download")
	}
	if m.idle != nil {
		m.idle.Stop()
		m.idle = nil
	}
	m.operations++
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.operations--; m.scheduleIdleLocked(); m.mu.Unlock(); m.wg.Done() }()
	p, err := m.ensure(ctx, true, "", false)
	if err != nil {
		m.recordError(err)
		return 0, err
	}
	var version struct {
		TickHistory string `json:"tickHistory"`
	}
	if err = p.call(ctx, map[string]any{"op": "version"}, &version); err != nil {
		return 0, err
	}
	if version.TickHistory != tickHistoryCapability {
		return 0, errors.New("The selected JForex bridge does not support bounded tick history; update the complete matching jforex-runtime and restart CryptoDesk")
	}
	exportID := "ticks-" + strconv.FormatUint(m.next.Add(1), 10)
	var result struct {
		Rows int64 `json:"rows"`
	}
	err = p.call(ctx, map[string]any{"op": "historyTicks", "exportId": exportID, "instrument": symbol, "from": fromMs, "to": toMs, "output": output}, &result)
	if err != nil {
		// Closing the export's channel precedes this acknowledgement. A stalled
		// SDK read can return later but cannot then open/write the canceled file.
		cleanup, finish := context.WithTimeout(context.Background(), 5*time.Second)
		defer finish()
		var stopped struct {
			Cancelled bool `json:"cancelled"`
		}
		if cancelErr := p.call(cleanup, map[string]any{"op": "historyTicksCancel", "exportId": exportID}, &stopped); cancelErr != nil || !stopped.Cancelled {
			p.finish(errors.New("The JForex tick export could not acknowledge cancellation; its connection was closed to stop the writer"))
			// Reap a failed bridge before releasing the account reservation or
			// allowing the caller to discard/reuse its output path.
			<-p.exited
		}
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if result.Rows < 0 || result.Rows > tickHistoryMaxRows {
		return 0, errors.New("JForex tick history exceeded its row limit")
	}
	if err = validateTickExport(ctx, output, initial, fromMs, toMs, result.Rows); err != nil {
		return 0, err
	}
	return result.Rows, nil
}

func validateTickExport(ctx context.Context, path string, initial os.FileInfo, from, to, expectedRows int64) error {
	info, err := tickExportFile(path, false)
	if err != nil || !os.SameFile(initial, info) {
		return errors.New("JForex tick output was replaced or exceeds its private file bounds")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(initial, opened) {
		return errors.New("JForex tick output changed while opening it")
	}
	reader := csv.NewReader(io.LimitReader(file, tickHistoryMaxBytes+1))
	reader.FieldsPerRecord = 5
	header, err := reader.Read()
	if err != nil || strings.Join(header, ",") != "timestamp,askPrice,bidPrice,askVolume,bidVolume" {
		return errors.New("JForex tick output has an invalid CSV header")
	}
	var rows int64
	previous := int64(-1)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || rows >= tickHistoryMaxRows {
			return errors.New("JForex tick output exceeds its CSV bounds")
		}
		at, err := strconv.ParseInt(record[0], 10, 64)
		if err != nil || at < from || at >= to || at < previous {
			return errors.New("JForex tick output has an invalid time or ordering")
		}
		for i, field := range record[1:] {
			number, err := strconv.ParseFloat(field, 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || i < 2 && number == 0 {
				return errors.New("JForex tick output has an invalid price or volume")
			}
		}
		previous = at
		rows++
	}
	if rows != expectedRows {
		return errors.New("JForex tick output row count does not match its completed export")
	}
	return nil
}
