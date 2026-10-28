package main

import (
	"context"
	"time"

	"cryptodesk/internal/telegram"
)

func (a *App) GetTelegramBotSettings() (telegram.Settings, error) {
	return a.telegram.Read()
}

func (a *App) ConfigureTelegramBot(token string) (telegram.Settings, error) {
	return a.telegram.Save(token)
}

func (a *App) ConfigureTelegramBotSettings(token, recipient string) (telegram.Settings, error) {
	return a.telegram.Configure(token, recipient)
}
func (a *App) telegramContext() (context.Context, context.CancelFunc) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, 8*time.Second)
}
func (a *App) GetTelegramBotIdentity(token string) (telegram.BotIdentity, error) {
	ctx, cancel := a.telegramContext()
	defer cancel()
	return a.telegram.BotIdentity(ctx, token)
}
func (a *App) TestTelegramBot() (telegram.TestResult, error) {
	ctx, cancel := a.telegramContext()
	defer cancel()
	return a.telegram.Test(ctx)
}
