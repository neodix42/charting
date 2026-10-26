package screener

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func categoryForSymbol(t *testing.T, symbol string) string {
	t.Helper()
	metadata, ok := catalogAssets[symbol]
	if !ok {
		t.Fatalf("test symbol %s is missing from the actual catalog", symbol)
	}
	return metadata.category
}

func TestAnalysisCatalogDescribesFullAssetNamesAndUnknownFiles(t *testing.T) {
	m := analysisTestManager(t)
	writeDataFile(t, m, "arkiususd-m1-2024-01-01-2024-01-02.csv", analysisCSV([]float64{2, 3}))
	writeDataFile(t, m, "customusd-m1-2024-01-01-2024-01-02.csv", analysisCSV([]float64{2, 3}))
	catalog, err := m.AnalysisCatalog()
	if err != nil || len(catalog.Categories) != len(loadCatalog().Groups)+1 {
		t.Fatalf("category catalog: %+v %v", catalog, err)
	}
	for i, group := range loadCatalog().Groups {
		if catalog.Categories[i] != group.Name {
			t.Fatalf("analysis and downloader category ordering differs at %d", i)
		}
	}
	if catalog.Categories[len(catalog.Categories)-1] != otherAssetCategory {
		t.Fatal("unknown CSVs are not selectable as Other")
	}
	if file := catalog.Files[0]; file.Description != "ARK Artificial Intelligence & Robotics UCITS ETF" || file.Category != categoryForSymbol(t, "arkiususd") {
		t.Fatalf("full asset metadata: %+v", file)
	}
	if file := catalog.Files[1]; file.Description != "CUSTOMUSD" || file.Category != otherAssetCategory {
		t.Fatalf("unknown asset metadata: %+v", file)
	}
	info := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), analysisNumber(1)))
	for i, result := range info.Results {
		if result.Description != catalog.Files[i].Description || result.Category != catalog.Files[i].Category {
			t.Fatalf("result lost metadata: %+v", result)
		}
	}
}

func TestAnalysisCategorySelectionIndexesCompanionTimeframesAndExcludesOtherAssets(t *testing.T) {
	m := analysisTestManager(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	writeDataFile(t, m, "btcusd-d1-2024-01-01-2024-01-03.csv", fmt.Sprintf("timestamp,close\n%d,100\n%d,200\n", base, base+86_400_000))
	minuteName := "btcusd-m1-2024-01-01-2024-01-03.csv"
	writeDataFile(t, m, minuteName, fmt.Sprintf("timestamp,close\n%d,101\n%d,201\n", base+86_400_000-60_000, base+2*86_400_000-60_000))
	// Malformed data from another category must not be indexed or scanned.
	writeDataFile(t, m, "eurusd-m1-2024-01-01-2024-01-03.csv", "timestamp,close\ninvalid,2\n")
	daily := analysisField("close")
	daily.Timeframe = "d1"
	request := AnalysisRequest{Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), daily)}, Categories: []string{categoryForSymbol(t, "btcusd")}}
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	request.Categories[0] = categoryForSymbol(t, "eurusd") // Caller cannot change the running snapshot.
	info := waitAnalysis(t, m)
	if info.Total != 2 || info.Failed != 0 || info.TotalMatches != 2 || resultNamed(t, info, minuteName).Matches != 2 {
		t.Fatalf("selected assets lost their companion data or scanned excluded data: %+v", info)
	}
	if _, err := m.StartAnalysis(AnalysisRequest{Conditions: request.Conditions, Categories: []string{}}); err == nil || !strings.Contains(err.Error(), "select at least one") {
		t.Fatalf("empty selection applied to all categories: %v", err)
	}
	if got, _ := m.Analysis(0, 200); !reflect.DeepEqual(got, info) {
		t.Fatal("invalid empty selection discarded existing results")
	}
	request.Categories = []string{categoryForSymbol(t, "arkiususd")}
	if _, err := m.StartAnalysis(request); err == nil || !strings.Contains(err.Error(), "selected asset categories") {
		t.Fatalf("unavailable category scan: %v", err)
	}
}

func TestAnalysisCategorySelectionsRetainNilVersusEmptyAcrossDraftAndNamedSnapshots(t *testing.T) {
	for _, selection := range [][]string{nil, {}, {categoryForSymbol(t, "btcusd"), categoryForSymbol(t, "eurusd")}} {
		t.Run(fmt.Sprintf("categories-%d-nil-%t", len(selection), selection == nil), func(t *testing.T) {
			profile := t.TempDir()
			m := savedConditionManager(t, profile)
			request := defaultAnalysisRequest()
			request.Categories = selection
			want := copyAnalysisRequest(request)
			if _, err := m.SaveAnalysisView(request); err != nil {
				t.Fatal(err)
			}
			saved, err := m.SaveAnalysisCondition("Category snapshot", request)
			if err != nil || !reflect.DeepEqual(saved.Request, want) {
				t.Fatalf("save snapshot: %+v %v", saved, err)
			}
			if len(request.Categories) > 0 {
				request.Categories[0] = otherAssetCategory
				saved.Request.Categories[0] = otherAssetCategory
			}
			m.Close()
			restarted := savedConditionManager(t, profile)
			draft, err := restarted.AnalysisView()
			if err != nil || !reflect.DeepEqual(draft, want) {
				t.Fatalf("draft selection after restart: %+v want %+v %v", draft, want, err)
			}
			sets, err := restarted.SavedAnalysisConditions()
			if err != nil || len(sets) != 1 || !reflect.DeepEqual(sets[0].Request, want) {
				t.Fatalf("saved selection after restart: %+v %v", sets, err)
			}
			data, err := os.ReadFile(filepath.Join(profile, "screener-analysis.json"))
			if err != nil {
				t.Fatal(err)
			}
			var encoded struct{ Request map[string]json.RawMessage }
			if err = json.Unmarshal(data, &encoded); err != nil {
				t.Fatal(err)
			}
			if selection == nil && string(encoded.Request["categories"]) != "null" || selection != nil && len(selection) == 0 && string(encoded.Request["categories"]) != "[]" {
				t.Fatalf("nil and empty selection conflated in JSON: %s", data)
			}
		})
	}
}

func TestAnalysisResultCategoryAndEmptyFiltersApplyBeforePagination(t *testing.T) {
	m := analysisTestManager(t)
	crypto := categoryForSymbol(t, "btcusd")
	forex := categoryForSymbol(t, "eurusd")
	var cryptoInstruments, forexInstruments []Instrument
	for _, group := range loadCatalog().Groups {
		if group.Name == crypto {
			cryptoInstruments = group.Instruments
		}
		if group.Name == forex {
			forexInstruments = group.Instruments
		}
	}
	for i, instrument := range cryptoInstruments[:14] {
		text := analysisCSV([]float64{2, 3})
		if i == 12 {
			text = analysisCSV([]float64{0.75, 0.75})
		}
		if i == 13 {
			text = "timestamp,close\ninvalid,3\n"
		}
		writeDataFile(t, m, instrument.ID+"-m1-2024-01-01-2024-01-02.csv", text)
	}
	for _, instrument := range forexInstruments[:3] {
		writeDataFile(t, m, instrument.ID+"-m1-2024-01-01-2024-01-02.csv", analysisCSV([]float64{2, 3}))
	}
	writeDataFile(t, m, "customusd-m1-2024-01-01-2024-01-02.csv", analysisCSV([]float64{2, 3}))
	all := startTestAnalysis(t, m, analysisCondition("above", analysisField("close"), analysisNumber(1)))
	if all.Total != 18 || all.TotalMatches != 32 || all.Failed != 1 {
		t.Fatalf("baseline results: %+v", all)
	}
	filter := AnalysisFilter{Categories: []string{crypto}}
	first, err := m.AnalysisFiltered(0, 10, filter)
	if err != nil || first.TotalResults != 13 || len(first.Results) != 10 || first.Total != all.Total || first.TotalMatches != all.TotalMatches {
		t.Fatalf("first filtered page: %+v %v", first, err)
	}
	second, err := m.AnalysisFiltered(10, 10, filter)
	if err != nil || second.TotalResults != 13 || len(second.Results) != 3 {
		t.Fatalf("second filtered page: %+v %v", second, err)
	}
	seen := map[string]bool{}
	failures := 0
	for _, result := range append(first.Results, second.Results...) {
		if result.Category != crypto || seen[result.ID] || result.Matches == 0 && result.Status != "failed" {
			t.Fatalf("bad filtered result: %+v", result)
		}
		seen[result.ID] = true
		if result.Status == "failed" {
			failures++
		}
	}
	if failures != 1 {
		t.Fatal("hiding zero matches also hid a data error")
	}
	filter.IncludeEmpty = true
	if page, err := m.AnalysisFiltered(10, 10, filter); err != nil || page.TotalResults != 14 || len(page.Results) != 4 {
		t.Fatalf("including zero matches: %+v %v", page, err)
	}
	for _, test := range []struct {
		categories []string
		total      int
	}{{[]string{forex}, 3}, {[]string{otherAssetCategory}, 1}, {nil, 17}, {[]string{}, 0}} {
		page, err := m.AnalysisFiltered(0, 10, AnalysisFilter{Categories: test.categories})
		if err != nil || page.TotalResults != test.total || len(page.Results) != min(10, test.total) {
			t.Fatalf("category filter %v: %+v %v", test.categories, page, err)
		}
	}
	if page, err := m.AnalysisFiltered(100, 10, filter); err != nil || len(page.Results) != 0 || page.TotalResults != 14 {
		t.Fatalf("past last filtered page: %+v %v", page, err)
	}
	for _, categories := range [][]string{{"not a category"}, {crypto, crypto}} {
		if _, err := m.AnalysisFiltered(0, 10, AnalysisFilter{Categories: categories}); err == nil {
			t.Fatal("invalid result categories accepted")
		}
		request := defaultAnalysisRequest()
		request.Categories = categories
		if _, err := m.SaveAnalysisView(request); err == nil {
			t.Fatal("invalid category draft persisted")
		}
	}
}
