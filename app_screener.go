package main

import (
	"os"
	"path/filepath"
	"strings"

	"cryptodesk/internal/screener"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) GetScreenerCatalog() screener.Catalog { return a.screener.Catalog() }

func (a *App) GetScreener() screener.Info { return a.screener.Info() }

func (a *App) ConfigureScreenerDirectory(directory string) (screener.Info, error) {
	if strings.TrimSpace(directory) == "" {
		directory = filepath.Join(a.store.Directory(), "screener")
	}
	return a.screener.ConfigureDirectory(directory)
}

func (a *App) ChooseScreenerDirectory() (string, error) {
	initial := a.screener.Info().Directory
	if st, err := os.Stat(initial); err != nil || !st.IsDir() {
		initial = a.store.Directory()
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose Screener download folder", DefaultDirectory: initial, CanCreateDirectories: true,
	})
}

func (a *App) StartScreenerDownload(request screener.Request) (screener.Info, error) {
	return a.screener.Start(request)
}

func (a *App) CancelScreenerDownload() screener.Info { return a.screener.Cancel() }

func (a *App) ResumeScreenerDownload() (screener.Info, error) { return a.screener.Resume() }

func (a *App) GetScreenerView() (screener.ViewState, error) { return a.screener.View() }

func (a *App) SaveScreenerView(view screener.ViewState) (screener.ViewState, error) {
	return a.screener.SaveView(view)
}
