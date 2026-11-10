package main

import (
	"os"
	"path/filepath"
	"strings"

	"cryptodesk/internal/screener"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) GetScreenerCatalog() screener.Catalog {
	if err := a.awaitService("screener"); err != nil {
		return screener.Catalog{}
	}
	return a.screener.Catalog()
}

func (a *App) GetScreenerCatalogForSource(source string) (screener.Catalog, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.Catalog{}, err
	}

	return a.screener.CatalogForSource(a.ctx, source)
}

func (a *App) GetScreenerFilesForSource(offset, limit int, period, source string) (screener.FilePage, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.FilePage{}, err
	}

	return a.screener.FilesForSource(offset, limit, period, source)
}

func (a *App) GetScreener() screener.Info {
	if err := a.awaitService("screener"); err != nil {
		return screener.Info{Error: err.Error()}
	}
	return a.screener.Info()
}

func (a *App) ConfigureScreenerDirectory(directory string) (screener.Info, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.Info{}, err
	}

	if strings.TrimSpace(directory) == "" {
		directory = filepath.Join(a.store.Directory(), "screener")
	}
	return a.screener.ConfigureDirectory(directory)
}

func (a *App) ConfigureScreenerConcurrency(concurrency int) (screener.Info, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.Info{}, err
	}

	return a.screener.ConfigureConcurrency(concurrency)
}

func (a *App) ChooseScreenerDirectory() (string, error) {
	if err := a.awaitService("screener"); err != nil {
		return "", err
	}

	initial := a.screener.Info().Directory
	if st, err := os.Stat(initial); err != nil || !st.IsDir() {
		initial = a.store.Directory()
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose Screener download folder", DefaultDirectory: initial, CanCreateDirectories: true,
	})
}

func (a *App) StartScreenerDownload(request screener.Request) (screener.Info, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.Info{}, err
	}

	return a.screener.Start(request)
}

func (a *App) CancelScreenerDownload() screener.Info {
	if err := a.awaitService("screener"); err != nil {
		return screener.Info{Error: err.Error()}
	}
	return a.screener.Cancel()
}

func (a *App) ResumeScreenerDownload() (screener.Info, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.Info{}, err
	}
	return a.screener.Resume()
}

func (a *App) GetScreenerView() (screener.ViewState, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.ViewState{}, err
	}
	return a.screener.View()
}

func (a *App) SaveScreenerView(view screener.ViewState) (screener.ViewState, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.ViewState{}, err
	}

	return a.screener.SaveView(view)
}

func (a *App) GetScreenerIndicators() screener.IndicatorCatalog {
	if err := a.awaitService("screener"); err != nil {
		return screener.IndicatorCatalog{}
	}
	return a.screener.Indicators()
}

func (a *App) GetScreenerFiles(offset, limit int) (screener.FilePage, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.FilePage{}, err
	}

	return a.screener.Files(offset, limit)
}

func (a *App) GetScreenerFilesForPeriod(offset, limit int, period string) (screener.FilePage, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.FilePage{}, err
	}

	return a.screener.FilesForPeriod(offset, limit, period)
}

func (a *App) GetScreenerEnrichment(fileIDs []string) (screener.EnrichmentInfo, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.EnrichmentInfo{}, err
	}

	return a.screener.Enrichment(fileIDs)
}

func (a *App) StartScreenerEnrichment(request screener.EnrichmentRequest) (screener.EnrichmentInfo, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.EnrichmentInfo{}, err
	}

	return a.screener.StartEnrichment(request)
}

func (a *App) CancelScreenerEnrichment() screener.EnrichmentInfo {
	if err := a.awaitService("screener"); err != nil {
		return screener.EnrichmentInfo{}
	}

	return a.screener.CancelEnrichment()
}

func (a *App) GetScreenerRefresh(fileIDs []string) (screener.RefreshInfo, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.RefreshInfo{}, err
	}

	return a.screener.Refresh(fileIDs)
}

func (a *App) StartScreenerRefresh() (screener.RefreshInfo, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.RefreshInfo{}, err
	}
	return a.screener.StartRefresh()
}

func (a *App) CancelScreenerRefresh() screener.RefreshInfo {
	if err := a.awaitService("screener"); err != nil {
		return screener.RefreshInfo{}
	}
	return a.screener.CancelRefresh()
}

func (a *App) GetScreenerAnalysisCatalog() (screener.AnalysisCatalog, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.AnalysisCatalog{}, err
	}

	return a.screener.AnalysisCatalog()
}

func (a *App) GetScreenerAnalysisCatalogForSource(source string) (screener.AnalysisCatalog, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.AnalysisCatalog{}, err
	}

	return a.screener.AnalysisCatalogForSource(source)
}

func (a *App) GetScreenerAnalysisView() (screener.AnalysisRequest, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.AnalysisRequest{}, err
	}

	return a.screener.AnalysisView()
}

func (a *App) SaveScreenerAnalysisView(request screener.AnalysisRequest) (screener.AnalysisRequest, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.AnalysisRequest{}, err
	}

	return a.screener.SaveAnalysisView(request)
}

func (a *App) GetScreenerSavedConditions() ([]screener.SavedAnalysisCondition, error) {
	if err := a.awaitService("screener"); err != nil {
		return nil, err
	}

	return a.screener.SavedAnalysisConditions()
}

func (a *App) GetScreenerSavedConditionSummaries() ([]screener.SavedAnalysisCondition, error) {
	if err := a.awaitService("screener"); err != nil {
		return nil, err
	}
	return a.screener.SavedAnalysisConditionSummaries()
}

func (a *App) SaveScreenerAnalysisCondition(name string, request screener.AnalysisRequest) (screener.SavedAnalysisCondition, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.SavedAnalysisCondition{}, err
	}

	return a.screener.SaveAnalysisCondition(name, request)
}

func (a *App) UpdateScreenerAnalysisCondition(id string, request screener.AnalysisRequest) (screener.SavedAnalysisCondition, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.SavedAnalysisCondition{}, err
	}

	return a.screener.UpdateAnalysisCondition(id, request)
}

func (a *App) DeleteScreenerAnalysisCondition(id string) error {
	if err := a.awaitService("screener"); err != nil {
		return err
	}

	return a.screener.DeleteAnalysisCondition(id)
}

func (a *App) RenameScreenerAnalysisCondition(id, name string) (screener.SavedAnalysisCondition, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.SavedAnalysisCondition{}, err
	}

	return a.screener.RenameAnalysisCondition(id, name)
}

func (a *App) StartScreenerAnalysis(request screener.AnalysisRequest) (screener.AnalysisInfo, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.AnalysisInfo{}, err
	}

	return a.screener.StartAnalysis(request)
}

func (a *App) GetScreenerAnalysis(offset, limit int) (screener.AnalysisInfo, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.AnalysisInfo{}, err
	}

	return a.screener.Analysis(offset, limit)
}

func (a *App) GetScreenerAnalysisFiltered(offset, limit int, filter screener.AnalysisFilter) (screener.AnalysisInfo, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.AnalysisInfo{}, err
	}

	return a.screener.AnalysisFiltered(offset, limit, filter)
}

func (a *App) CancelScreenerAnalysis() screener.AnalysisInfo {
	if err := a.awaitService("screener"); err != nil {
		return screener.AnalysisInfo{}
	}
	return a.screener.CancelAnalysis()
}

func (a *App) GetScreenerAnalysisChart(resultID string, occurrence, before, after int) (screener.AnalysisChart, error) {
	if err := a.awaitService("screener"); err != nil {
		return screener.AnalysisChart{}, err
	}

	return a.screener.AnalysisChart(resultID, occurrence, before, after)
}
