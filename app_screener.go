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

func (a *App) GetScreenerIndicators() screener.IndicatorCatalog { return a.screener.Indicators() }

func (a *App) GetScreenerFiles(offset, limit int) (screener.FilePage, error) {
	return a.screener.Files(offset, limit)
}

func (a *App) GetScreenerEnrichment(fileIDs []string) (screener.EnrichmentInfo, error) {
	return a.screener.Enrichment(fileIDs)
}

func (a *App) StartScreenerEnrichment(request screener.EnrichmentRequest) (screener.EnrichmentInfo, error) {
	return a.screener.StartEnrichment(request)
}

func (a *App) CancelScreenerEnrichment() screener.EnrichmentInfo {
	return a.screener.CancelEnrichment()
}

func (a *App) GetScreenerRefresh(fileIDs []string) (screener.RefreshInfo, error) {
	return a.screener.Refresh(fileIDs)
}

func (a *App) StartScreenerRefresh() (screener.RefreshInfo, error) { return a.screener.StartRefresh() }

func (a *App) CancelScreenerRefresh() screener.RefreshInfo { return a.screener.CancelRefresh() }

func (a *App) GetScreenerAnalysisCatalog() (screener.AnalysisCatalog, error) {
	return a.screener.AnalysisCatalog()
}

func (a *App) GetScreenerAnalysisView() (screener.AnalysisRequest, error) {
	return a.screener.AnalysisView()
}

func (a *App) SaveScreenerAnalysisView(request screener.AnalysisRequest) (screener.AnalysisRequest, error) {
	return a.screener.SaveAnalysisView(request)
}

func (a *App) GetScreenerSavedConditions() ([]screener.SavedAnalysisCondition, error) {
	return a.screener.SavedAnalysisConditions()
}

func (a *App) SaveScreenerAnalysisCondition(name string, request screener.AnalysisRequest) (screener.SavedAnalysisCondition, error) {
	return a.screener.SaveAnalysisCondition(name, request)
}

func (a *App) StartScreenerAnalysis(request screener.AnalysisRequest) (screener.AnalysisInfo, error) {
	return a.screener.StartAnalysis(request)
}

func (a *App) GetScreenerAnalysis(offset, limit int) (screener.AnalysisInfo, error) {
	return a.screener.Analysis(offset, limit)
}

func (a *App) CancelScreenerAnalysis() screener.AnalysisInfo { return a.screener.CancelAnalysis() }

func (a *App) GetScreenerAnalysisChart(resultID string, occurrence, before, after int) (screener.AnalysisChart, error) {
	return a.screener.AnalysisChart(resultID, occurrence, before, after)
}
