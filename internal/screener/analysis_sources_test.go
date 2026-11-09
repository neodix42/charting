package screener

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func analysisHyperliquidFile(t *testing.T, m *Manager, timeframe, contents string) (string, string) {
	t.Helper()
	asset := Instrument{ID: "hyperliquid-" + publicAlertSymbol("perpetual", "BTC"), NativeID: "BTC", Market: "perpetual", Name: "BTC"}
	if err := writeHyperliquidAsset(m.directory, asset); err != nil {
		t.Fatal(err)
	}
	symbol, err := hyperliquidFileSymbol(m.directory, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	name := symbol + "-" + timeframe + "-2024-01-01-2024-01-16.csv"
	writeDataFile(t, m, name, contents)
	return name, asset.ID
}

func TestAnalysisCatalogSourcesLimitFieldsFilesAndCategories(t *testing.T) {
	m := analysisTestManager(t)
	dukasName := "btcusd-d1-2024-01-01-2024-01-16.csv"
	writeDataFile(t, m, dukasName, "timestamp,close,dukas_signal\n1704067200000,100,1\n")
	hyperName, _ := analysisHyperliquidFile(t, m, "w1", "timestamp,close,hyper_signal\n1704067200000,100,1\n")
	for _, source := range []string{"", "dukascopy", "hyperliquid"} {
		catalog, err := m.AnalysisCatalogForSource(source)
		if err != nil || catalog.DataSource != normalizeDataSource(source) || len(catalog.Files) != 1 {
			t.Fatal(catalog, err)
		}
		wantName, ownField, otherField, wantPeriod := dukasName, "dukas_signal", "hyper_signal", "d1"
		if source == "hyperliquid" {
			wantName, ownField, otherField, wantPeriod = hyperName, "hyper_signal", "dukas_signal", "w1"
			if !slices.Contains(catalog.Categories, "Hyperliquid perpetual") || slices.Contains(catalog.Categories, categoryForSymbol(t, "btcusd")) {
				t.Fatal("Hyperliquid catalog contains Dukascopy categories", catalog)
			}
		}
		if catalog.Files[0].Name != wantName || !slices.Contains(catalog.Fields, ownField) || slices.Contains(catalog.Fields, otherField) || !reflect.DeepEqual(catalog.Timeframes, []string{wantPeriod}) {
			t.Fatalf("source-specific field discovery leaked other CSVs: %+v", catalog)
		}
	}
	legacy, err := m.AnalysisCatalog()
	if err != nil || legacy.DataSource != "dukascopy" || legacy.Files[0].Name != dukasName {
		t.Fatal("default catalog did not remain Dukascopy", legacy, err)
	}
	for _, source := range []string{"all", "binance", "bybit", "Hyperliquid"} {
		if _, err := m.AnalysisCatalogForSource(source); err == nil {
			t.Fatal("unsupported catalog source accepted", source)
		}
		request := defaultAnalysisRequest()
		request.DataSource = source
		if _, err := m.StartAnalysis(request); err == nil {
			t.Fatal("unsupported scan source accepted", source)
		}
		if _, err := m.SaveAnalysisView(request); err == nil {
			t.Fatal("unsupported draft source accepted", source)
		}
		if _, err := m.SaveAnalysisCondition(source, request); err == nil {
			t.Fatal("unsupported named source accepted", source)
		}
	}
}

func TestAnalysisSourceScopesBaseCompanionsChartsAndAppliedIdentity(t *testing.T) {
	m := analysisTestManager(t)
	dukasWeekly := scopedIndexFile(t, m, "btcusd", "w1", 1000)
	dukasDaily := scopedIndexFile(t, m, "btcusd", "d1", 1)
	hyperWeekly, hyperID := analysisHyperliquidFile(t, m, "w1", scopedIndexCSV("w1", 100))
	hyperDaily, _ := analysisHyperliquidFile(t, m, "d1", scopedIndexCSV("d1", 200))
	daily := analysisField("close")
	daily.Timeframe = "d1"
	request := AnalysisRequest{DataSource: "hyperliquid", Period: "w1", Categories: []string{"Hyperliquid perpetual"}, Conditions: []AnalysisCondition{analysisCondition("below", analysisField("close"), daily)}}
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	if info.DataSource != "hyperliquid" || info.Total != 1 || info.Failed != 0 || info.TotalMatches != 2 || info.Results[0].Symbol != hyperID {
		t.Fatal("Hyperliquid scan mixed providers", info)
	}
	assertAnalysisIndexedNames(t, m, hyperWeekly, hyperDaily)
	chart, err := m.AnalysisChart(info.Results[0].ID, 0, 0, 0)
	if err != nil || len(chart.Bars) != 1 || chart.Bars[0].Values["d1:close"] != 200 {
		t.Fatal("chart used another source's companion", chart, err)
	}
	request.DataSource, request.Categories = "", nil
	if _, err = m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	info = waitAnalysis(t, m)
	if info.DataSource != "dukascopy" || info.Total != 1 || info.Failed != 0 || info.TotalMatches != 0 || info.Results[0].Symbol != "btcusd" {
		t.Fatal("legacy default source did not isolate Dukascopy", info)
	}
	assertAnalysisIndexedNames(t, m, dukasWeekly, dukasDaily)
}

func TestAnalysisMissingSourceCompanionCannotUseOtherVenue(t *testing.T) {
	m := analysisTestManager(t)
	hyperWeekly, _ := analysisHyperliquidFile(t, m, "w1", scopedIndexCSV("w1", 100))
	scopedIndexFile(t, m, "btcusd", "d1", 200)
	daily := analysisField("close")
	daily.Timeframe = "d1"
	request := AnalysisRequest{DataSource: "hyperliquid", Period: "w1", Conditions: []AnalysisCondition{analysisCondition("below", analysisField("close"), daily)}}
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	info := waitAnalysis(t, m)
	if info.Total != 1 || info.Failed != 1 || !strings.Contains(info.Results[0].Error, "no usable d1 data") {
		t.Fatal("missing source companion was silently supplied by another venue", info)
	}
	assertAnalysisIndexedNames(t, m, hyperWeekly)
}

func TestAnalysisSourcePersistsAndChangingSourceDiscardsIncompatibleEvidence(t *testing.T) {
	profile := t.TempDir()
	m := savedConditionManager(t, profile)
	request := defaultAnalysisRequest()
	request.DataSource = "hyperliquid"
	request.Categories = []string{"Hyperliquid perpetual"}
	if _, err := m.SaveAnalysisView(request); err != nil {
		t.Fatal(err)
	}
	named, err := m.SaveAnalysisCondition("Hyperliquid rule", request)
	if err != nil {
		t.Fatal(err)
	}
	want := copyAnalysisRequest(request)
	m.Close()
	restarted := savedConditionManager(t, profile)
	draft, err := restarted.AnalysisView()
	if err != nil || !reflect.DeepEqual(draft, want) {
		t.Fatal("analysis source lost on restart", draft, want, err)
	}
	saved, err := restarted.SavedAnalysisConditions()
	if err != nil || len(saved) != 1 || saved[0].ID != named.ID || !reflect.DeepEqual(saved[0].Request, want) {
		t.Fatal("named source lost on restart", saved, err)
	}
	encoded, err := os.ReadFile(filepath.Join(profile, "screener-analysis.json"))
	if err != nil || !strings.Contains(string(encoded), `"dataSource":"hyperliquid"`) {
		t.Fatal("source absent from durable draft", string(encoded), err)
	}
	legacy := defaultAnalysisRequest()
	explicit := copyAnalysisRequest(legacy)
	explicit.DataSource = "dukascopy"
	if !sameAnalysisProfitabilityRequest(legacy, explicit) || sameAnalysisProfitabilityRequest(legacy, want) {
		t.Fatal("profitability source identity conflates venues or breaks legacy Dukascopy")
	}
	evidence := &SavedConditionAnalysis{AnalyzedAt: 1, Assets: []SavedConditionProfitability{{Symbol: "btcusd", Timeframe: "d1", FileName: "btcusd-d1-2024-01-01-2024-01-16.csv", Matches: 1, ProfitabilityUnavailable: true}}}
	if err := validateSavedConditionAnalysis(evidence); err != nil {
		t.Fatal(err)
	}
	if err := restarted.writeSavedConditions([]SavedAnalysisCondition{{ID: named.ID, Name: named.Name, Request: legacy, Analysis: evidence}}); err != nil {
		t.Fatal(err)
	}
	changed := copyAnalysisRequest(legacy)
	changed.DataSource = "hyperliquid"
	updated, err := restarted.UpdateAnalysisCondition(named.ID, changed)
	if err != nil || updated.Request.DataSource != "hyperliquid" || updated.Analysis != nil {
		t.Fatal("source change borrowed other-venue evidence", updated, err)
	}
}
