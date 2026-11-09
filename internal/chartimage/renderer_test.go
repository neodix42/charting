package chartimage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/screener"
)

func sampleRequest() Request {
	chart := &screener.AnalysisChart{Symbol: "BTC/USDC", Name: "Bitcoin", Timeframe: "d1", Fields: []string{"open", "high", "low", "close", "volume", "ema_20", "rsi_14"}}
	for i := range 121 {
		price := 80000 + 1500*math.Sin(float64(i)*0.3) + float64(i)*125
		close := price + 350*math.Sin(float64(i))
		chart.Bars = append(chart.Bars, screener.AnalysisBar{Index: int64(i), Time: time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC).UnixMilli(), Values: map[string]float64{"open": price, "high": math.Max(price, close) + 250, "low": math.Min(price, close) - 250, "close": close, "volume": 100 + float64(i%11)*20, "ema_20": price - 500, "rsi_14": 50 + 20*math.Sin(float64(i)*0.15)}})
	}
	chart.MatchTime, chart.MatchIndex = chart.Bars[120].Time, 120
	stop := chart.Bars[120].Values["close"] - 1500
	chart.Signal = &screener.AnalysisSignalLevels{Entry: chart.Bars[120].Values["close"], StopLoss: &stop}
	return Request{Chart: chart, Title: "BTC/USDC · Bitcoin", Source: "hyperliquid", Market: "perpetual", Condition: "Close > EMA (20) · RSI (14) > 50", Theme: "light", FontSize: 15}
}

func TestRejectsOpenFutureBarsAndHistoricalOutcomes(t *testing.T) {
	for name, change := range map[string]func(*screener.AnalysisChart){
		"future":    func(c *screener.AnalysisChart) { c.Bars[120].Time++ },
		"unsorted":  func(c *screener.AnalysisChart) { c.Bars[10], c.Bars[11] = c.Bars[11], c.Bars[10] },
		"nonfinite": func(c *screener.AnalysisChart) { c.Bars[10].Values["close"] = math.Inf(1) },
		"trade":     func(c *screener.AnalysisChart) { c.Trade = &screener.AnalysisTradeLevels{} },
		"outcome":   func(c *screener.AnalysisChart) { c.TradeExecution = &screener.AnalysisTradeExecution{} },
		"profit": func(c *screener.AnalysisChart) {
			c.ProfitableMatches = []screener.AnalysisProfitableMatch{{Time: c.MatchTime}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := sampleRequest()
			change(request.Chart)
			if _, err := New().Render(context.Background(), Runtime{}, request); err == nil {
				t.Fatal("invalid live chart accepted")
			}
		})
	}
	if err := validate(sampleRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestQueueWaitIsCancelable(t *testing.T) {
	r := New()
	r.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Render(ctx, Runtime{}, sampleRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	for range cap(r.pending) {
		r.pending <- struct{}{}
	}
	if _, err := r.Render(context.Background(), Runtime{}, sampleRequest()); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("unbounded pending queue: %v", err)
	}
}

func TestRejectsRepeatedOversizedFieldBeforeEncoding(t *testing.T) {
	request := sampleRequest()
	field := strings.Repeat("x", 20000)
	request.Chart.Fields = []string{field}
	for index := range request.Chart.Bars {
		request.Chart.Bars[index].Values = map[string]float64{field: 1}
	}
	if err := validate(request); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized repeated key accepted: %v", err)
	}
	// The normal 64-field bound still permits all the snapshot indicators.
	request = sampleRequest()
	for len(request.Chart.Fields) < 64 {
		field := fmt.Sprintf("rsi_%d", len(request.Chart.Fields))
		request.Chart.Fields = append(request.Chart.Fields, field)
		for _, bar := range request.Chart.Bars {
			bar.Values[field] = 1
		}
	}
	if err := validate(request); err != nil {
		t.Fatal(err)
	}
}

func TestBoundedOutputAndPrivateEnvironment(t *testing.T) {
	b := &limitedBuffer{max: 4}
	if _, err := b.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("5")); err == nil {
		t.Fatal("unbounded output")
	}
	t.Setenv("NODE_OPTIONS", "--require /untrusted.js")
	t.Setenv("TELEGRAM_BOT_TOKEN", "secret")
	environment := strings.Join(renderEnvironment("/private/chart"), "\n")
	if strings.Contains(environment, "secret") || strings.Contains(environment, "NODE_OPTIONS") {
		t.Fatal("renderer inherited credentials or Node startup code")
	}
	if !strings.Contains(environment, "TMPDIR=/private/chart") {
		t.Fatal("browser profile is outside the private tree")
	}
}

// Production-like opt-in: build the frontend bundle and private Screener runtime
// first. No server, exchange connection, desktop window, or Telegram send occurs.
func TestHeadlessSharedAnalysisPlot(t *testing.T) {
	if os.Getenv("CRYPTODESK_TEST_CHART_RENDER") != "1" {
		t.Skip("requires built frontend and private Chromium runtime")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("local smoke runtime is Linux amd64")
	}
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	directory := os.Getenv("CRYPTODESK_CHART_RUNTIME")
	if directory == "" {
		directory = filepath.Join(repository, ".tools", "screener-runtime-linux-amd64")
	}
	// Exercise production discovery too: future package-pin updates must not
	// silently leave Telegram charts on an obsolete browser revision.
	t.Setenv("CRYPTODESK_SCREENER_RUNTIME", directory)
	manager := screener.New(t.TempDir())
	defer manager.Close()
	node, runtimeDirectory, browser, err := manager.SignalChartRuntime()
	if err != nil {
		t.Fatal(err)
	}
	runtimePaths := Runtime{Node: node, Directory: runtimeDirectory, Browser: browser}
	private, err := os.MkdirTemp("/tmp", "cd-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(private) })
	t.Setenv("TMPDIR", private)
	image, err := New().Render(context.Background(), runtimePaths, sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(image))
	if err != nil {
		t.Fatal(err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != 1440 || bounds.Dy() < 650 || bounds.Dy() > 8500 {
		t.Fatalf("unexpected image bounds %v", bounds)
	}
	// Candles/volume include both up and down colours; indicator lines, pane
	// separators, labels, and the marker make the result materially nonblank.
	colours := map[uint32]bool{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 3 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 3 {
			r, g, b, _ := decoded.At(x, y).RGBA()
			colours[r<<16|g<<8|b] = true
		}
	}
	if len(colours) < 100 {
		t.Fatalf("chart appears blank: %d sampled colours", len(colours))
	}
	if entries, e := os.ReadDir(private); e != nil || len(entries) != 0 {
		t.Fatalf("renderer left temporary chart or browser data: %v %v", entries, e)
	}
	if target := os.Getenv("CRYPTODESK_CHART_SAMPLE"); target != "" {
		if err := os.WriteFile(target, image, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, sample := range []struct {
		name, theme string
		fields      []string
		levels      *screener.AnalysisSignalLevels
	}{
		{"dark-exits", "dark", []string{"open", "high", "low", "close", "ema_20", "rsi_14"}, &screener.AnalysisSignalLevels{Entry: 90000, StopLoss: floatPointer(88000), TakeProfit: floatPointer(94000)}},
		{"price-only", "light", []string{"close"}, &screener.AnalysisSignalLevels{Entry: 90000, TakeProfit: floatPointer(94000)}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			request := sampleRequest()
			request.Theme = sample.theme
			request.Chart.Fields = sample.fields
			request.Chart.Signal = sample.levels
			for _, bar := range request.Chart.Bars {
				for field := range bar.Values {
					keep := false
					for _, selected := range sample.fields {
						if field == selected {
							keep = true
						}
					}
					if !keep {
						delete(bar.Values, field)
					}
				}
			}
			image, e := New().Render(context.Background(), runtimePaths, request)
			if e != nil {
				t.Fatal(e)
			}
			if _, e := png.DecodeConfig(bytes.NewReader(image)); e != nil {
				t.Fatal(e)
			}
			if entries, e := os.ReadDir(private); e != nil || len(entries) != 0 {
				t.Fatalf("renderer left temporary data: %v %v", entries, e)
			}
		})
	}
	t.Run("cancel-and-cleanup", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		if _, err := New().Render(ctx, runtimePaths, sampleRequest()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("renderer did not cancel promptly")
		}
		if entries, e := os.ReadDir(private); e != nil || len(entries) != 0 {
			t.Fatalf("canceled renderer left temporary data: %v %v", entries, e)
		}
	})
	t.Run("invalid-chart-fails-readiness", func(t *testing.T) {
		request := sampleRequest()
		request.Chart.Fields = []string{"rsi_14"}
		for index := range request.Chart.Bars {
			request.Chart.Bars[index].Values = map[string]float64{"rsi_14": 50}
		}
		if _, err := New().Render(context.Background(), runtimePaths, request); err == nil {
			t.Fatal("rendered a chart without price data")
		}
		if entries, e := os.ReadDir(private); e != nil || len(entries) != 0 {
			t.Fatalf("failed renderer left temporary data: %v %v", entries, e)
		}
	})
}

func floatPointer(value float64) *float64 { return &value }
