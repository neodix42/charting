//go:build bindings

package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/screener"
	"cryptodesk/internal/telegram"
)

func TestLiveAlertChartDeliveryPreservesSignalAndDoesNotRetryUpload(t *testing.T) {
	chart := &screener.AnalysisChart{Symbol: "@268", MatchTime: 123}
	alert := screener.LiveAlert{Symbol: "@268", AssetName: "AAPL/USDC", DataSource: "hyperliquid", Chart: chart}
	image := []byte("rendered PNG")
	renders, sends := 0, 0
	uploadError := errors.New("upload outcome uncertain")
	err := deliverScreenerLiveAlert(context.Background(), alert, func(ctx context.Context, got screener.LiveAlert) ([]byte, error) {
		renders++
		if got.Chart != chart || got.Symbol != "@268" || ctx.Err() != nil {
			t.Fatal("renderer lost the immutable matched snapshot", got)
		}
		return image, nil
	}, func(_ context.Context, text string, got []byte, reason string) error {
		sends++
		if text != formatScreenerLiveAlert(alert) || !bytes.Equal(got, image) || reason != "" {
			t.Fatal("photo changed the signal or added a false fallback", text, reason)
		}
		return uploadError
	})
	if !errors.Is(err, uploadError) || renders != 1 || sends != 1 {
		t.Fatal("uncertain upload retried or replaced", renders, sends, err)
	}
}

func TestLiveAlertChartFailureUsesSafeExplicitTextFallback(t *testing.T) {
	for _, test := range []struct {
		name        string
		chart       *screener.AnalysisChart
		chartError  string
		renderError error
		image       []byte
		wantReason  string
		wantRenders int
	}{
		{name: "no snapshot", wantReason: "Chart data for the matched bar could not be prepared."},
		{name: "snapshot error", chart: &screener.AnalysisChart{}, chartError: "/private/path/provider?token=secret", wantReason: "Chart data for the matched bar could not be prepared."},
		{name: "renderer error", chart: &screener.AnalysisChart{}, renderError: errors.New("/private/runtime token=secret"), image: []byte("partial PNG"), wantReason: "The signal chart could not be rendered.", wantRenders: 1},
		{name: "empty image", chart: &screener.AnalysisChart{}, wantReason: "The signal chart could not be rendered.", wantRenders: 1},
		{name: "missing runtime", chart: &screener.AnalysisChart{}, renderError: errSignalChartRuntime, wantReason: "The chart renderer is unavailable; install the complete matching CryptoDesk package.", wantRenders: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			alert := screener.LiveAlert{Symbol: "eurusd", Chart: test.chart, ChartError: test.chartError}
			renders, sends := 0, 0
			err := deliverScreenerLiveAlert(context.Background(), alert, func(context.Context, screener.LiveAlert) ([]byte, error) {
				renders++
				return test.image, test.renderError
			}, func(_ context.Context, text string, image []byte, reason string) error {
				sends++
				if len(image) != 0 || text != formatScreenerLiveAlert(alert) || reason != test.wantReason {
					t.Fatal("unsafe or ambiguous fallback", text, reason, len(image))
				}
				return nil
			})
			if err != nil || sends != 1 || renders != test.wantRenders {
				t.Fatal("fallback delivery failed", renders, sends, err)
			}
		})
	}
}

func TestLiveAlertChartCancellationDoesNotSendFallback(t *testing.T) {
	for _, cancelBefore := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		if cancelBefore {
			cancel()
		}
		renders, sends := 0, 0
		err := deliverScreenerLiveAlert(ctx, screener.LiveAlert{Chart: &screener.AnalysisChart{}}, func(context.Context, screener.LiveAlert) ([]byte, error) {
			renders++
			cancel()
			return nil, context.Canceled
		}, func(context.Context, string, []byte, string) error {
			sends++
			return nil
		})
		cancel()
		if !errors.Is(err, context.Canceled) || sends != 0 || cancelBefore && renders != 0 || !cancelBefore && renders != 1 {
			t.Fatal("cancelled render sent a notification", cancelBefore, renders, sends, err)
		}
	}
}

func TestLiveAlertChartUnavailableFacadeDoesNotPanic(t *testing.T) {
	app := &App{}
	if err := app.sendScreenerLiveAlert(context.Background(), screener.LiveAlert{}); err == nil {
		t.Fatal("accepted missing Telegram settings")
	}
	if _, err := app.renderScreenerLiveAlert(context.Background(), screener.LiveAlert{}); !errors.Is(err, errSignalChartRuntime) {
		t.Fatal("missing chart runtime was not classified", err)
	}
}

func TestLiveAlertMessageIncludesAssetConditionClosedTimeAndPrices(t *testing.T) {
	stop, target := 90.0, 135.0
	alert := screener.LiveAlert{Symbol: "btcusd", AssetName: "Bitcoin vs US Dollar", Timeframe: "h1", ConditionName: "Close < SMA & bullish cross", Time: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC).UnixMilli(), EntryPrice: 105, StopLoss: &stop, TakeProfit: &target}
	message := formatScreenerLiveAlert(alert)
	for _, part := range []string{"BTCUSD — Bitcoin vs US Dollar", "Period: h1", "Time (UTC, bar close): 2026-10-28 13:00:00", "Condition: Close < SMA & bullish cross", "Entry (signal close): 105", "SL: 90", "TP: 135"} {
		if !strings.Contains(message, part) {
			t.Fatalf("alert omitted %q: %s", part, message)
		}
	}
	alert.Timeframe, alert.StopLoss, alert.TakeProfit = "d1", nil, nil
	message = formatScreenerLiveAlert(alert)
	if !strings.Contains(message, "2026-10-29 12:00:00") || strings.Count(message, "Unavailable") != 2 {
		t.Fatal("daily close or missing trade levels misrepresented", message)
	}
}

func TestLiveAlertsCannotEnableWithoutConfiguredTelegram(t *testing.T) {
	profile := t.TempDir()
	manager := screener.New(profile)
	defer manager.Close()
	app := &App{screener: manager, telegram: telegram.NewStore(profile)}
	alerts, err := screener.NewLiveAlerts(manager, app.sendScreenerLiveAlert)
	if err != nil {
		t.Fatal(err)
	}
	app.liveAlerts = alerts
	defer alerts.Close()
	if info := app.GetScreenerLiveAlerts(); info.Config.Enabled || info.Running {
		t.Fatal("new alerts started without explicit enable")
	}
	_, err = app.ConfigureScreenerLiveAlerts(screener.LiveAlertsConfig{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "Telegram") || app.GetScreenerLiveAlerts().Config.Enabled {
		t.Fatal("enabled alerts without a token and recipient", err)
	}
}

func TestSingleLiveAlertFacadeQueuesOnlyTheNamedAlertIdentity(t *testing.T) {
	app := &App{}
	if _, err := app.CheckScreenerLiveAlert(strings.Repeat("a", 32)); err == nil {
		t.Fatal("unavailable Live Alerts facade accepted a row check")
	}
	manager := screener.New(t.TempDir())
	defer manager.Close()
	request, err := manager.AnalysisView()
	if err != nil {
		t.Fatal(err)
	}
	request.DataSource, request.Period = "hyperliquid", "d1"
	saved, err := manager.SaveAnalysisCondition("Shared reusable rule", request)
	if err != nil {
		t.Fatal(err)
	}
	alerts, err := screener.NewLiveAlerts(manager, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer alerts.Close()
	entries := []screener.LiveAlertDefinition{
		{ID: strings.Repeat("a", 32), Name: "First row", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true},
		{ID: strings.Repeat("b", 32), Name: "Second row", ConditionID: saved.ID, Period: "d1", DataSource: "hyperliquid", Enabled: true},
	}
	// No scheduler or delivery transport is started: exercise the real queue and
	// bound routing contract without network calls or messages.
	if _, err = alerts.Configure(screener.LiveAlertsConfig{Enabled: true, Alerts: entries}); err != nil {
		t.Fatal(err)
	}
	app.liveAlerts = alerts
	if _, err = app.CheckScreenerLiveAlert(saved.ID); err == nil {
		t.Fatal("facade treated the reusable rule ID as an alert row")
	}
	queued, err := app.CheckScreenerLiveAlert(entries[1].ID)
	if err != nil || len(queued.Checks) != 2 || queued.Checks[0].ProgressState != "" || queued.Checks[1].ProgressState != "queued" || queued.Checks[1].AlertID != entries[1].ID || len(queued.Config.Alerts) != 2 {
		t.Fatal("bound row check widened or rewrote alert configuration", queued, err)
	}
}

func TestLiveAlertMessageIncludesRecordedAnalysisProfitability(t *testing.T) {
	alert := screener.LiveAlert{Symbol: "eurusd", AssetName: "Euro vs US Dollar", Timeframe: "h1", ConditionName: "Saved strategy", EntryPrice: 1.1, Profitability: &screener.SavedConditionProfitability{Symbol: "eurusd", Timeframe: "h1", Matches: 11, TakeProfitHits: 10, ProfitablePercent: 1000.0 / 11}}
	message := formatScreenerLiveAlert(alert)
	if !strings.Contains(message, "Profitability: 90.91%, 10 / 11 hit TP") {
		t.Fatal("recorded profitability missing or incorrectly rounded", message)
	}
	alert.Profitability = nil
	if strings.Contains(formatScreenerLiveAlert(alert), "Profitability:") {
		t.Fatal("unrecorded profitability was invented")
	}
}

func TestLiveAlertMessageIdentifiesPublicMarketAndWeeklyClose(t *testing.T) {
	alert := screener.LiveAlert{Symbol: "xyz:TSLA", AssetName: "Tesla", DataSource: "hyperliquid", Market: "perpetual", Timeframe: "w1", Time: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC).UnixMilli(), EntryPrice: 100}
	message := formatScreenerLiveAlert(alert)
	for _, part := range []string{"Asset: xyz:TSLA — Tesla", "Source: hyperliquid", "Market: perpetual", "2026-11-09 00:00:00"} {
		if !strings.Contains(message, part) {
			t.Fatalf("public weekly alert omitted %q: %s", part, message)
		}
	}
	alert.DataSource = "dukascopy"
	if strings.Contains(formatScreenerLiveAlert(alert), "Source:") {
		t.Fatal("legacy Dukascopy formatting changed")
	}
}

func TestLiveAlertMessageSeparatesNamedAlertFromReusableSavedCondition(t *testing.T) {
	alert := screener.LiveAlert{Symbol: "BTCUSDT", AssetName: "Bitcoin", DataSource: "binance", Market: "spot", Timeframe: "h1", AlertName: "My Binance hourly", ConditionName: "My Binance hourly", SavedConditionName: "Reusable bullish rule", EntryPrice: 100}
	message := formatScreenerLiveAlert(alert)
	if !strings.Contains(message, "Alert: My Binance hourly\nCondition: Reusable bullish rule") || strings.Contains(message, "Condition: My Binance hourly") {
		t.Fatal("named alert obscured its original saved condition", message)
	}
}
