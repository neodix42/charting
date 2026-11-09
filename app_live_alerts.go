package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cryptodesk/internal/chartimage"
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

// Progress polling never waits for saved-condition evidence or CSV inventory.
func (a *App) GetScreenerLiveAlertsProgress() screener.LiveAlertsInfo {
	if a.liveAlerts == nil {
		return a.GetScreenerLiveAlerts()
	}
	info := a.liveAlerts.ProgressInfo()
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

func (a *App) CheckScreenerLiveAlert(alertID string) (screener.LiveAlertsInfo, error) {
	if a.liveAlerts == nil || a.liveAlertsError != "" {
		return a.GetScreenerLiveAlerts(), errors.New("Live Alerts settings are unavailable; saved data was preserved")
	}
	return a.liveAlerts.CheckAlertNow(alertID)
}

func (a *App) sendScreenerLiveAlert(ctx context.Context, alert screener.LiveAlert) error {
	if a.telegram == nil {
		return errors.New("Telegram settings are unavailable")
	}
	return deliverScreenerLiveAlert(ctx, alert, a.renderScreenerLiveAlert, a.telegram.SendAlertWithChart)
}

var errSignalChartRuntime = errors.New("signal chart runtime unavailable")

// Keep rendering failures separate from uncertain Telegram writes. A chart can
// fall back to text before delivery starts; an attempted upload is never retried.
func deliverScreenerLiveAlert(ctx context.Context, alert screener.LiveAlert, render func(context.Context, screener.LiveAlert) ([]byte, error), send func(context.Context, string, []byte, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var image []byte
	reason := ""
	if alert.Chart == nil || alert.ChartError != "" {
		reason = "Chart data for the matched bar could not be prepared."
	} else {
		var err error
		image, err = render(ctx, alert)
		if err != nil || len(image) == 0 {
			image, reason = nil, "The signal chart could not be rendered."
			if errors.Is(err, errSignalChartRuntime) {
				reason = "The chart renderer is unavailable; install the complete matching CryptoDesk package."
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return send(ctx, formatScreenerLiveAlert(alert), image, reason)
}

func (a *App) renderScreenerLiveAlert(ctx context.Context, alert screener.LiveAlert) ([]byte, error) {
	if a.screener == nil {
		return nil, errSignalChartRuntime
	}
	node, directory, browser, err := a.screener.SignalChartRuntime()
	if err != nil {
		return nil, errSignalChartRuntime
	}
	theme, fontSize := "light", 15
	if a.store != nil {
		if loaded, err := a.store.Load(); err == nil {
			theme = loaded.Workspace.Theme
			if loaded.Workspace.FontSize != nil {
				fontSize = *loaded.Workspace.FontSize
			}
		}
	}
	a.mu.Lock()
	if a.chartRenderer == nil {
		a.chartRenderer = chartimage.New()
	}
	renderer := a.chartRenderer
	a.mu.Unlock()
	source := alert.DataSource
	if source == "" {
		source = "dukascopy"
	}
	condition := alert.SavedConditionName
	if condition == "" {
		condition = alert.ConditionName
	}
	return renderer.Render(ctx, chartimage.Runtime{Node: node, Directory: directory, Browser: browser}, chartimage.Request{
		Chart: alert.Chart, Title: alert.Symbol + " — " + alert.AssetName,
		Source: source, Market: alert.Market, Condition: condition, Theme: theme, FontSize: fontSize,
	})
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
	} else if alert.Timeframe == "w1" {
		period = 7 * 24 * time.Hour
	}
	closed := time.UnixMilli(alert.Time).UTC().Add(period)
	profitability := ""
	if stats := alert.Profitability; stats != nil && stats.Matches > 0 {
		profitability = fmt.Sprintf("\nProfitability: %.2f%%, %d / %d hit TP", 100*float64(stats.TakeProfitHits)/float64(stats.Matches), stats.TakeProfitHits, stats.Matches)
	}
	source := ""
	symbol := strings.ToUpper(alert.Symbol)
	if alert.DataSource != "" && alert.DataSource != "dukascopy" {
		source = fmt.Sprintf("\nSource: %s\nMarket: %s", alert.DataSource, alert.Market)
		symbol = alert.Symbol
	}
	conditionName := alert.ConditionName
	alertName := ""
	if alert.AlertName != "" {
		alertName = "\nAlert: " + alert.AlertName
	}
	if alert.SavedConditionName != "" {
		conditionName = alert.SavedConditionName
	}
	return fmt.Sprintf("CryptoDesk Live Alert\nAsset: %s — %s%s\nPeriod: %s\nTime (UTC, bar close): %s%s\nCondition: %s%s\nEntry (signal close): %s\nSL: %s\nTP: %s",
		symbol, alert.AssetName, source, alert.Timeframe, closed.Format("2006-01-02 15:04:05"), alertName, conditionName,
		profitability, strconv.FormatFloat(alert.EntryPrice, 'g', -1, 64), level(alert.StopLoss), level(alert.TakeProfit))
}
