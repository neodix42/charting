package main

import "cryptodesk/internal/telegram"

func (a *App) GetTelegramBotSettings() (telegram.Settings, error) {
	return a.telegram.Read()
}

func (a *App) ConfigureTelegramBot(token string) (telegram.Settings, error) {
	return a.telegram.Save(token)
}
