package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cryptodesk/internal/screener"
)

func (a *App) GetScreenerLiveAlerts() screener.LiveAlertsInfo {
	if a.liveAlerts == nil {
		return screener.LiveAlertsInfo{Config: screener.LiveAlertsConfig{ConditionIDs: []string{}}, ConditionScopes: []screener.LiveAlertConditionScope{}, Assets: []screener.LiveAlertAsset{}, Recent: []screener.LiveAlertOutcome{}, Checks: []screener.LiveAlertCheck{}, Error: a.liveAlertsError, Status: "Live Alerts settings are unavailable"}
	}
	info := a.liveAlerts.Info()
	if a.liveAlertsError != "" {
		info.Error, info.Config.Enabled = a.liveAlertsError, false
	}
	return info
}

func (a *App) ConfigureScreenerLiveAlerts(config screener.LiveAlertsConfig) (screener.LiveAlertsInfo, error) {
	if a.liveAlerts == nil || a.liveAlertsError != "" {
		return a.GetScreenerLiveAlerts(), errors.New("Live Alerts settings are unavailable; saved data was preserved")
	}
	if config.Enabled {
		settings, err := a.telegram.Read()
		if err != nil || settings.APIToken == "" || settings.Recipient == "" {
			return a.liveAlerts.Info(), errors.New("Configure the Telegram Bot API Token and Recipient in Settings, open the bot and press Start, then use Test before starting alerts")
		}
	}
	return a.liveAlerts.Configure(config)
}

func (a *App) CheckScreenerLiveAlerts() (screener.LiveAlertsInfo, error) {
	if a.liveAlerts == nil || a.liveAlertsError != "" {
		return a.GetScreenerLiveAlerts(), errors.New("Live Alerts settings are unavailable; saved data was preserved")
	}
	return a.liveAlerts.CheckNow()
}

func (a *App) sendScreenerLiveAlert(ctx context.Context, alert screener.LiveAlert) error {
	return a.telegram.SendAlert(ctx, formatScreenerLiveAlert(alert))
}

func formatScreenerLiveAlert(alert screener.LiveAlert) string {
	level := func(value *float64) string {
		if value == nil {
			return "Unavailable (configure valid trade levels in the saved condition)"
		}
		return strconv.FormatFloat(*value, 'g', -1, 64)
	}
	period := time.Hour
	if alert.Timeframe == "d1" {
		period = 24 * time.Hour
	}
	closed := time.UnixMilli(alert.Time).UTC().Add(period)
	profitability := ""
	if stats := alert.Profitability; stats != nil && stats.Matches > 0 {
		profitability = fmt.Sprintf("\nProfitability: %.2f%%, %d / %d hit TP", 100*float64(stats.TakeProfitHits)/float64(stats.Matches), stats.TakeProfitHits, stats.Matches)
	}
	return fmt.Sprintf("CryptoDesk Live Alert\nAsset: %s — %s\nPeriod: %s\nTime (UTC, bar close): %s\nCondition: %s%s\nEntry (signal close): %s\nSL: %s\nTP: %s",
		strings.ToUpper(alert.Symbol), alert.AssetName, alert.Timeframe, closed.Format("2006-01-02 15:04:05"), alert.ConditionName,
		profitability, strconv.FormatFloat(alert.EntryPrice, 'g', -1, 64), level(alert.StopLoss), level(alert.TakeProfit))
}
