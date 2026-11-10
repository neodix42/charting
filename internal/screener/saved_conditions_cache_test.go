package screener

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSavedConditionCacheDetectsSameMetadataExternalChanges(t *testing.T) {
	m := savedConditionManager(t, t.TempDir())
	saved, err := m.SaveAnalysisCondition("Alpha", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.profile, "screener-saved-conditions.json")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replace := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	external := bytes.Replace(original, []byte(`"Alpha"`), []byte(`"Bravo"`), 1)
	if len(external) != len(original) || bytes.Equal(external, original) {
		t.Fatal("test must edit content without changing file size")
	}
	replace(external)
	list, err := m.SavedAnalysisConditions()
	if err != nil || len(list) != 1 || list[0].Name != "Bravo" || list[0].ID != saved.ID {
		t.Fatal("external edit hidden by cache", list, err)
	}
	// A corrupt/future file must block edits, even after a valid cache was warm.
	future := bytes.Replace(external, []byte(`"version":1`), []byte(`"version":9`), 1)
	replace(future)
	if _, err := m.SavedAnalysisConditions(); err == nil {
		t.Fatal("future state fell back to a valid cached snapshot")
	}
	if _, err := m.UpdateAnalysisCondition(saved.ID, defaultAnalysisRequest()); err == nil {
		t.Fatal("cached update overwrote a future state")
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, future) {
		t.Fatal("future state was changed", err)
	}
	replace([]byte(`{broken`))
	if _, err := m.SavedAnalysisConditions(); err == nil {
		t.Fatal("corrupt state fell back to a cached snapshot")
	}
	replace(external)
	if list, err := m.SavedAnalysisConditions(); err != nil || len(list) != 1 || list[0].Name != "Bravo" {
		t.Fatal("repaired external state did not invalidate the cache", list, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if list, err := m.SavedAnalysisConditions(); err != nil || len(list) != 0 {
		t.Fatal("deleted state returned cached conditions", list, err)
	}
	if _, err := m.SaveAnalysisCondition("Fresh", defaultAnalysisRequest()); err != nil {
		t.Fatal(err)
	}
	if list, err := m.SavedAnalysisConditions(); err != nil || len(list) != 1 || list[0].Name != "Fresh" {
		t.Fatal("old deleted conditions returned after a fresh save", list, err)
	}
}

func TestSavedConditionCacheRejectsNonRegularReplacement(t *testing.T) {
	m := savedConditionManager(t, t.TempDir())
	if _, err := m.SaveAnalysisCondition("Alpha", defaultAnalysisRequest()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.profile, "screener-saved-conditions.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SavedAnalysisConditions(); err == nil {
		t.Fatal("directory replacement returned cached conditions")
	}
}

func TestSavedConditionSummariesPreserveEvidenceAndIndependentRequests(t *testing.T) {
	m := savedConditionManager(t, t.TempDir())
	request := defaultAnalysisRequest()
	row := profitabilityTestResult("daily", 9, 10)
	row.Timeframe = "d1"
	m.analysis = &analysisState{phase: "completed", asOf: time.Now().UnixMilli(), request: request, results: []AnalysisResult{row}}
	saved, err := m.SaveAnalysisCondition("With evidence", request)
	if err != nil || saved.Analysis == nil {
		t.Fatal(saved, err)
	}
	summaries, err := m.SavedAnalysisConditionSummaries()
	if err != nil || len(summaries) != 1 || summaries[0].Analysis != nil || !reflect.DeepEqual(summaries[0].Request, saved.Request) || summaries[0].ID != saved.ID {
		t.Fatal("invalid summary", summaries, err)
	}
	summaries[0].Request.Conditions[0].Left.Field = "mutated"
	full, err := m.SavedAnalysisConditions()
	if err != nil || len(full) != 1 || !reflect.DeepEqual(full[0], saved) {
		t.Fatal("summary mutated stored request or dropped alert evidence", full, err)
	}
	full[0].Analysis.Assets[0].Matches = 999
	full[0].Request.Conditions[0].Left.Field = "mutated again"
	next, err := m.SavedAnalysisConditions()
	if err != nil || !reflect.DeepEqual(next[0], saved) {
		t.Fatal("full evidence response mutated the cache", next, err)
	}
}

func TestCatalogCopiesCannotChangeTimeframeValidation(t *testing.T) {
	catalog := loadCatalog()
	catalog.Timeframes[0] = "made-up"
	catalog.Groups[0].Name = "mutated"
	catalog.Groups[0].Instruments[0].ID = "mutated"
	next := loadCatalog()
	if next.Timeframes[0] == "made-up" || next.Groups[0].Name == "mutated" || next.Groups[0].Instruments[0].ID == "mutated" || validAnalysisTimeframe("made-up") {
		t.Fatal("public copy changed immutable validation metadata")
	}
	for _, tf := range next.Timeframes {
		if !validAnalysisTimeframe(tf) {
			t.Fatal("valid timeframe lost", tf)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() { _ = validAnalysisTimeframe("d1") }); allocations != 0 {
		t.Fatalf("timeframe lookup allocates %.0f times per result row", allocations)
	}
}
