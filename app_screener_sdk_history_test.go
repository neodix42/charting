package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cryptodesk/internal/domain"
)

type screenerSDKClientFixture struct {
	source        string
	configured    bool
	bars          []domain.Candle
	key           domain.FeedKey
	calls         int
	fail          error
	changeAccount bool
}

func (f *screenerSDKClientFixture) HistoricalSource() (string, bool) { return f.source, f.configured }
func (f *screenerSDKClientFixture) ExportTicks(context.Context, string, string, int64, int64, string) (int64, error) {
	return 0, errors.New("unexpected tick export")
}
func (f *screenerSDKClientFixture) DownloadHistory(_ context.Context, key domain.FeedKey, _, _ int64, limit int) ([]domain.Candle, error) {
	f.calls++
	f.key = key
	if limit != 1000 {
		return nil, errors.New("unbounded SDK history request")
	}
	if f.changeAccount {
		f.source = "different-account"
	}
	return f.bars, f.fail
}
func sdkExportTarget(t *testing.T) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "export.csv")
	if err := os.WriteFile(name, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return name
}
func sdkFixture() *screenerSDKClientFixture {
	return &screenerSDKClientFixture{source: strings.Repeat("a", 64), configured: true, bars: []domain.Candle{{Time: 1704067200, Open: 5, High: 7, Low: 4, Close: 6, Volume: 123, Closed: true}}}
}

func TestScreenerSDKNativeExportUsesClosedBidHistoryWithoutInventedVolume(t *testing.T) {
	for _, symbol := range []string{"btcusd", "xauxag"} {
		t.Run(symbol, func(t *testing.T) {
			fixture := sdkFixture()
			provider := dukascopyScreenerHistory{client: fixture}
			output := sdkExportTarget(t)
			rows, err := provider.ExportCandles(context.Background(), fixture.source, symbol, 1704067200000, 1704153600000, output)
			if err != nil || rows != 1 {
				t.Fatal(rows, err)
			}
			body, err := os.ReadFile(output)
			if err != nil || string(body) != "timestamp,open,high,low,close\n1704067200000,5,7,4,6\n" {
				t.Fatal(string(body), err)
			}
			market := "cfd"
			if symbol == "xauxag" {
				market = "metal"
			}
			if fixture.key != (domain.FeedKey{Exchange: "dukascopy", Market: market, Instrument: strings.ToUpper(symbol), Timeframe: "1d"}) {
				t.Fatal(fixture.key)
			}
		})
	}
}
func TestScreenerSDKNativeExportRejectsInvalidPeriodsAndSourceChanges(t *testing.T) {
	for _, kind := range []string{"account-before", "account-during", "canceled", "open", "misaligned", "duplicate", "out-of-range", "provider-error", "existing-file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			fixture := sdkFixture()
			provider := dukascopyScreenerHistory{client: fixture}
			output := sdkExportTarget(t)
			source := fixture.source
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "account-before":
				source = "wrong"
			case "account-during":
				fixture.changeAccount = true
			case "canceled":
				cancel()
			case "open":
				fixture.bars[0].Closed = false
			case "misaligned":
				fixture.bars[0].Time++
			case "duplicate":
				fixture.bars = append(fixture.bars, fixture.bars[0])
			case "out-of-range":
				fixture.bars[0].Time += 86400
			case "provider-error":
				fixture.fail = errors.New("source unavailable")
			case "existing-file":
				if err := os.WriteFile(output, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "original.csv")
				if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(t.TempDir(), "link.csv")
				if err := os.Symlink(target, link); err != nil {
					t.Skip("platform cannot create test symlink")
				}
				output = link
			}
			before, _ := os.ReadFile(output)
			if _, err := provider.ExportCandles(ctx, source, "btcusd", 1704067200000, 1704153600000, output); err == nil {
				t.Fatal("unsafe SDK export accepted")
			}
			after, _ := os.ReadFile(output)
			if string(before) != string(after) {
				t.Fatal("rejected export modified the original file")
			}
		})
	}
	for _, span := range [][2]int64{{1704067200001, 1704153600000}, {1704067200000, 1704153600001}, {1704067200000, 1704067200000}, {1704067200000, 1704067200000 + 367*86400000}} {
		f := sdkFixture()
		if _, err := (dukascopyScreenerHistory{client: f}).ExportCandles(context.Background(), f.source, "btcusd", span[0], span[1], sdkExportTarget(t)); err == nil || f.calls != 0 {
			t.Fatal("invalid range reached SDK", span, err)
		}
	}
}
