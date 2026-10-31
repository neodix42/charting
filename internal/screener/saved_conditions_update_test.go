package screener

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestUpdateSavedConditionPreservesIdentityOrderAndCompatibleEvidence(t *testing.T) {
	m := analysisTestManager(t)
	request := defaultAnalysisRequest()
	request.Period = "d1"
	request.ProfitabilityFilter = 80
	row := profitabilityTestResult("daily", 9, 10)
	row.Timeframe = "d1"
	m.analysis = &analysisState{phase: "completed", asOf: time.Now().UnixMilli(), request: request, results: []AnalysisResult{row}}
	first, err := m.SaveAnalysisCondition("First", request)
	if err != nil {
		t.Fatal(err)
	}
	target, err := m.SaveAnalysisCondition("Middle", request)
	if err != nil {
		t.Fatal(err)
	}
	last, err := m.SaveAnalysisCondition("Last", request)
	if err != nil {
		t.Fatal(err)
	}
	originalEvidence := copySavedConditionAnalysis(target.Analysis)
	request.ProfitabilityFilter = 90
	request.Conditions[0].ID = "loaded-row"
	updated, err := m.UpdateAnalysisCondition(target.ID, request)
	if err != nil || updated.ID != target.ID || updated.Name != target.Name || !reflect.DeepEqual(updated.Request, request) || !reflect.DeepEqual(updated.Analysis, originalEvidence) {
		t.Fatal("filter-only update changed identity or lost evidence", updated, err)
	}
	// Responses and requests are independent snapshots.
	request.Conditions[0].Right.Value = 999
	updated.Analysis.Assets[0].Matches = 999
	list, err := m.SavedAnalysisConditions()
	if err != nil || len(list) != 3 || !reflect.DeepEqual(list[0], first) || list[1].ID != target.ID || list[1].Request.Conditions[0].Right.Value == 999 || !reflect.DeepEqual(list[1].Analysis, originalEvidence) || !reflect.DeepEqual(list[2], last) {
		t.Fatal("updating moved/changed other conditions or retained mutable data", list, err)
	}
	// A changed source period cannot inherit either the old scope or a current
	// completed scan of the original period.
	newRequest := copyAnalysisRequest(list[1].Request)
	newRequest.Period = "h1"
	updated, err = m.UpdateAnalysisCondition(target.ID, newRequest)
	if err != nil || updated.Analysis != nil || updated.Request.Period != "h1" {
		t.Fatal("changed request retained stale historical scope", updated, err)
	}
	profile := m.profile
	m.Close()
	restarted := savedConditionManager(t, profile)
	list, err = restarted.SavedAnalysisConditions()
	if err != nil || list[1].ID != target.ID || list[1].Name != target.Name || list[1].Analysis != nil || !reflect.DeepEqual(list[1].Request, newRequest) {
		t.Fatal("restart lost in-place update", list, err)
	}
	if info, err := restarted.Analysis(0, 10); err != nil || info.Total != 0 || info.Active {
		t.Fatal("saving an update started analysis", info, err)
	}
}

func TestSaveAsNewAfterRestartReusesMatchingScopeWithoutChangingOriginal(t *testing.T) {
	m := analysisTestManager(t)
	request := defaultAnalysisRequest()
	request.Period = "h1"
	request.ProfitabilityFilter = 0
	row := profitabilityTestResult("unsimulated", 0, 4)
	row.Simulation = nil
	m.analysis = &analysisState{phase: "completed", asOf: time.Now().UnixMilli(), request: request, results: []AnalysisResult{row}}
	original, err := m.SaveAnalysisCondition("Original", request)
	if err != nil || original.Analysis == nil {
		t.Fatal(original, err)
	}
	profile := m.profile
	m.Close()
	restarted := savedConditionManager(t, profile)
	request.ProfitabilityFilter = 85
	request.Conditions[0].ID = "copied-row"
	copy, err := restarted.SaveAnalysisCondition("Copy", request)
	if err != nil || copy.ID == original.ID || !reflect.DeepEqual(copy.Analysis, original.Analysis) || !copy.Analysis.Assets[0].ProfitabilityUnavailable {
		t.Fatal("Save as new lost persisted matching assets", copy, err)
	}
	list, err := restarted.SavedAnalysisConditions()
	if err != nil || len(list) != 2 || !reflect.DeepEqual(list[0], original) || !reflect.DeepEqual(list[1], copy) {
		t.Fatal("Save as new changed the original", list, err)
	}
	// Updating a different rule can inherit a semantically equal saved peer.
	different := copyAnalysisRequest(request)
	different.Conditions[0].Right.Value = 123
	other, err := restarted.SaveAnalysisCondition("Different", different)
	if err != nil || other.Analysis != nil {
		t.Fatal("different rule reused unrelated evidence", other, err)
	}
	reused, err := restarted.UpdateAnalysisCondition(other.ID, request)
	if err != nil || !reflect.DeepEqual(reused.Analysis, original.Analysis) {
		t.Fatal("update failed to reuse compatible saved evidence", reused, err)
	}
}

func TestSavedConditionSelectsNewestCompatibleEvidenceAndRetainsItDuringIncompleteScan(t *testing.T) {
	m := analysisTestManager(t)
	request := defaultAnalysisRequest()
	oldRow := profitabilityTestResult("older", 8, 10)
	oldTime := time.Now().UnixMilli() - 1000
	m.analysis = &analysisState{phase: "completed", asOf: oldTime, request: request, results: []AnalysisResult{oldRow}}
	original, err := m.SaveAnalysisCondition("Original", request)
	if err != nil {
		t.Fatal(err)
	}
	newRow := profitabilityTestResult("newer", 10, 11)
	m.analysis = &analysisState{phase: "completed", asOf: oldTime + 100, request: request, results: []AnalysisResult{newRow}}
	newer, err := m.SaveAnalysisCondition("Newer", request)
	if err != nil || newer.Analysis == nil || newer.Analysis.Assets[0].FileName != newRow.Name {
		t.Fatal("newer scan did not replace older peer evidence", newer, err)
	}
	for _, state := range []*analysisState{
		{phase: "completed", asOf: oldTime + 200, request: request, results: []AnalysisResult{{Status: "failed"}}},
		{active: true, phase: "scanning", asOf: oldTime + 200, request: request, results: []AnalysisResult{oldRow}},
		{phase: "cancelled", asOf: oldTime + 200, request: request, results: []AnalysisResult{oldRow}},
		{phase: "completed", asOf: oldTime - 100, request: request, results: []AnalysisResult{oldRow}},
	} {
		m.analysis = state
		updated, err := m.UpdateAnalysisCondition(original.ID, request)
		if err != nil || !reflect.DeepEqual(updated.Analysis, newer.Analysis) {
			t.Fatal("incomplete/older scan replaced newest saved evidence", updated, err)
		}
	}
	// A completed zero-match scan is valid evidence of an empty inherited scope.
	m.analysis = &analysisState{phase: "completed", asOf: oldTime + 300, request: request, results: []AnalysisResult{{Status: "completed"}}}
	updated, err := m.UpdateAnalysisCondition(original.ID, request)
	if err != nil || updated.Analysis == nil || len(updated.Analysis.Assets) != 0 || updated.Analysis.AnalyzedAt != oldTime+300 {
		t.Fatal("completed empty match scope was ignored", updated, err)
	}
}

func TestUpdateSavedConditionRejectsInvalidRequestsAndIDsWithoutChangingState(t *testing.T) {
	m := analysisTestManager(t)
	request := defaultAnalysisRequest()
	saved, err := m.SaveAnalysisCondition("Saved", request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.profile, "screener-saved-conditions.json")
	before, _ := os.ReadFile(path)
	for _, id := range []string{"", "bad", strings.Repeat("z", 32), strings.Repeat("0", 32)} {
		if _, err = m.UpdateAnalysisCondition(id, request); err == nil {
			t.Fatal("invalid/missing ID accepted", id)
		}
	}
	invalid := copyAnalysisRequest(request)
	invalid.ProfitabilityFilter = 84
	if _, err = m.UpdateAnalysisCondition(saved.ID, invalid); err == nil {
		t.Fatal("invalid threshold accepted")
	}
	invalid = copyAnalysisRequest(request)
	invalid.Conditions[0].Operator = "unknown"
	if _, err = m.UpdateAnalysisCondition(saved.ID, invalid); err == nil {
		t.Fatal("invalid condition accepted")
	}
	m.Close()
	if _, err = m.UpdateAnalysisCondition(saved.ID, request); err == nil {
		t.Fatal("updated a closed manager")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("failed update changed saved conditions")
	}
}

func TestUpdateSavedConditionWriteFailurePreservesOriginal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not prevent writes on Windows")
	}
	m := analysisTestManager(t)
	request := defaultAnalysisRequest()
	saved, err := m.SaveAnalysisCondition("Saved", request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.profile, "screener-saved-conditions.json")
	before, _ := os.ReadFile(path)
	if err = os.Chmod(m.profile, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(m.profile, 0700) })
	if probe, err := os.CreateTemp(m.profile, "permission-probe-*"); err == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
		t.Skip("process can write despite directory permissions")
	}
	if _, err = m.UpdateAnalysisCondition(saved.ID, request); err != nil {
		t.Fatal("unchanged update unnecessarily required a write", err)
	}
	request.ProfitabilityFilter = 90
	if _, err = m.UpdateAnalysisCondition(saved.ID, request); err == nil || !strings.Contains(err.Error(), "update named condition") {
		t.Fatal("failed write was not reported", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("failed write changed original condition")
	}
}
