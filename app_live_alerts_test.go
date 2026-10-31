//go:build bindings

package main

import (
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/screener"
	"cryptodesk/internal/telegram"
)

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
