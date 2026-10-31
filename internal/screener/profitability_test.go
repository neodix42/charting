package screener

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func profitabilityTestResult(id string, wins, matches int64) AnalysisResult {
	return AnalysisResult{ID: id, Name: id + ".csv", Symbol: "eurusd", Timeframe: "h1", Status: "completed", Matches: matches, Simulation: &AnalysisSimulationSummary{TakeProfitHits: wins, ProfitablePercent: 100 * float64(wins) / float64(matches)}}
}
func TestProfitabilityFilterUsesExactInclusiveCountsBeforePaginationAndKeepsErrors(t *testing.T) {
	m := analysisTestManager(t)
	rows := []AnalysisResult{}
	for i := 0; i < 14; i++ {
		rows = append(rows, profitabilityTestResult(fmt.Sprint(i), int64(85+i), 100))
	}
	rows = append(rows, profitabilityTestResult("perfect", 1, 1), profitabilityTestResult("below", 84, 100), AnalysisResult{ID: "unknown", Status: "completed", Matches: 10}, AnalysisResult{ID: "broken", Status: "failed", Error: "missing companion"})
	m.analysis = &analysisState{phase: "completed", request: AnalysisRequest{ProfitabilityFilter: 85}, results: rows}
	first, err := m.AnalysisFiltered(0, 10, AnalysisFilter{})
	if err != nil || first.ProfitabilityFilter != 85 || first.TotalResults != 16 || len(first.Results) != 10 || first.Results[0].ID != "perfect" || first.Results[9].ID != "5" {
		t.Fatalf("filter/sort/paging: %+v %v", first, err)
	}
	second, err := m.AnalysisFiltered(10, 10, AnalysisFilter{})
	if err != nil || len(second.Results) != 6 || second.Results[4].ID != "0" || second.Results[5].ID != "broken" {
		t.Fatalf("boundary/error disappeared: %+v %v", second, err)
	}
	threshold := 100
	perfect, err := m.AnalysisFiltered(0, 10, AnalysisFilter{ProfitabilityFilter: &threshold})
	if err != nil || len(perfect.Results) != 2 || perfect.Results[0].ID != "perfect" {
		t.Fatalf("100 mustretainperfect+errors: %+v %v", perfect, err)
	}
	threshold = 0
	all, err := m.AnalysisFiltered(0, 200, AnalysisFilter{ProfitabilityFilter: &threshold})
	if err != nil || len(all.Results) != 18 || m.analysis.request.ProfitabilityFilter != 85 {
		t.Fatalf("disabled viewfilter changed applied rule: %+v %v", all, err)
	}
	// Displayed percentages cannot round a below-threshold fraction into scope.
	below := profitabilityTestResult("rounded", 849999, 1000000)
	below.Simulation.ProfitablePercent = 85
	if analysisResultMeetsProfitability(below, 85) {
		t.Fatal("rounded percentage admitted below-minimum counts")
	}
	enormous := profitabilityTestResult("large", math.MaxInt64, math.MaxInt64)
	if !analysisResultMeetsProfitability(enormous, 100) {
		t.Fatal("large valid counts overflowed threshold comparison")
	}
}

func TestProfitabilityFilterDraftNamedRuleValidationAndLegacyDefaults(t *testing.T) {
	m := analysisTestManager(t)
	fresh, err := m.AnalysisView()
	if err != nil || fresh.ProfitabilityFilter != 85 {
		t.Fatal("fresh draft mustdefault85", fresh, err)
	}
	for _, value := range []int{0, 5, 85, 95, 100} {
		request := defaultAnalysisRequest()
		request.ProfitabilityFilter = value
		if _, err = m.SaveAnalysisView(request); err != nil {
			t.Fatal(err)
		}
		named, err := m.SaveAnalysisCondition(fmt.Sprintf("threshold-%d", value), request)
		if err != nil || named.Request.ProfitabilityFilter != value {
			t.Fatal(named, err)
		}
	}
	before, _ := os.ReadFile(filepath.Join(m.profile, "screener-analysis.json"))
	for _, value := range []int{-5, 1, 84, 101} {
		request := defaultAnalysisRequest()
		request.ProfitabilityFilter = value
		if _, err = m.SaveAnalysisView(request); err == nil {
			t.Fatal("invalid filter accepted", value)
		}
		if _, err = m.SaveAnalysisCondition("bad", request); err == nil {
			t.Fatal("invalid saved filter accepted", value)
		}
		if _, err = m.AnalysisFiltered(0, 10, AnalysisFilter{ProfitabilityFilter: &value}); err == nil {
			t.Fatal("invalid result filter accepted", value)
		}
	}
	after, _ := os.ReadFile(filepath.Join(m.profile, "screener-analysis.json"))
	if string(before) != string(after) {
		t.Fatal("invalid edit changed old draft")
	}
	if err = os.WriteFile(filepath.Join(m.profile, "screener-analysis.json"), []byte(`{"version":1,"request":{"conditions":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := m.AnalysisView()
	if err != nil || legacy.ProfitabilityFilter != 0 {
		t.Fatal("legacy missing filter mustremainoff", legacy, err)
	}
}

func TestCompletedSimulationEvidencePersistsBeforeAndAfterConditionSave(t *testing.T) {
	m := analysisTestManager(t)
	profile := m.profile
	first := simulationBar(100, 105, 95, 100)
	first.signal = 1
	second := simulationBar(100, 112, 95, 105)
	name := "eurusd-m1-2024-01-01-2024-01-02.csv"
	writeDataFile(t, m, name, simulationCSV([]simulationTestBar{first, second}, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()))
	request := simulationRequest(1)
	request.ProfitabilityFilter = 85
	before, err := m.SaveAnalysisCondition("before scan", request)
	if err != nil || before.Analysis != nil {
		t.Fatal("unanalyzed rule invented evidence", before, err)
	}
	if _, err = m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	result := waitAnalysis(t, m)
	if result.Error != "" || len(result.Results) != 1 || result.Results[0].Simulation.TakeProfitHits != 1 {
		t.Fatal("simulation failed", result)
	}
	saved, err := m.SavedAnalysisConditions()
	if err != nil || len(saved) != 1 || saved[0].Analysis == nil || len(saved[0].Analysis.Assets) != 1 || saved[0].Analysis.Assets[0].Matches != 1 || saved[0].Analysis.Assets[0].TakeProfitHits != 1 || saved[0].Analysis.Assets[0].ProfitablePercent != 100 {
		t.Fatalf("pre-saved rule lost completed evidence: %+v %v", saved, err)
	}
	recorded := copySavedConditionAnalysis(saved[0].Analysis)
	clone := copyAnalysisRequest(request)
	clone.ProfitabilityFilter = 100
	clone.Conditions[0].ID = "new-row-id"
	after, err := m.SaveAnalysisCondition("after scan", clone)
	if err != nil || !reflect.DeepEqual(after.Analysis, recorded) {
		t.Fatalf("compatible completed evidence not attached: %+v %v", after, err)
	}
	modified := copyAnalysisRequest(clone)
	modified.TradeSimulation.Duration = 2
	different, err := m.SaveAnalysisCondition("different simulation", modified)
	if err != nil || different.Analysis != nil {
		t.Fatal("unrelated simulation reused old evidence", different, err)
	}
	if _, err = m.RenameAnalysisCondition(before.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	saved[0].Analysis.Assets[0].Matches = 999
	fresh, err := m.SavedAnalysisConditions()
	if err != nil || fresh[0].Analysis.Assets[0].Matches != 1 {
		t.Fatal("caller mutated stored evidence", fresh, err)
	}
	// A failed reanalysis must preserve the last complete successful evidence.
	if err = os.WriteFile(filepath.Join(m.directory, name), []byte("timestamp,open,high,low,close\nbroken,1,2,1,2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	failed := waitAnalysis(t, m)
	if failed.Failed != 1 {
		t.Fatal("fixture did not fail", failed)
	}
	fresh, err = m.SavedAnalysisConditions()
	if err != nil || !reflect.DeepEqual(fresh[0].Analysis, recorded) {
		t.Fatal("failed scan replaced complete evidence", fresh, err)
	}
	m.Close()
	restarted := savedConditionManager(t, profile)
	restored, err := restarted.SavedAnalysisConditions()
	if err != nil || restored[0].Name != "renamed" || !reflect.DeepEqual(restored[0].Analysis, recorded) {
		t.Fatal("restart lost evidence/name", restored, err)
	}
}

func TestIncompleteEvidenceCannotLeakAndBestFileRetainsEveryEligiblePair(t *testing.T) {
	rows := []AnalysisResult{profitabilityTestResult("lower", 9, 10), profitabilityTestResult("higher", 10, 11)}
	a := &analysisState{phase: "completed", asOf: time.Now().UnixMilli(), results: rows}
	snapshot, err := completedAnalysisProfitability(a)
	if err != nil || len(snapshot.Assets) != 1 || snapshot.Assets[0].FileName != "higher.csv" || snapshot.Assets[0].TakeProfitHits != 10 || snapshot.Assets[0].Matches != 11 {
		t.Fatal(snapshot, err)
	}
	for _, modify := range []func(){func() { a.active = true }, func() { a.active = false; a.phase = "cancelled" }, func() { a.phase = "completed"; a.results[0].Status = "running" }} {
		modify()
		if got, err := completedAnalysisProfitability(a); got != nil || err != nil {
			t.Fatal("incomplete scan became evidence", got, err)
		}
	}
	a.results[0].Status = "failed"
	if got, err := completedAnalysisProfitability(a); err != nil || got == nil || got.FailedFiles != 1 || len(got.Assets) != 1 || got.Assets[0].FileName != "higher.csv" {
		t.Fatal("unrelated failed file hid a successful matching asset", got, err)
	}
	malformed := &SavedConditionAnalysis{AnalyzedAt: time.Now().UnixMilli(), Assets: []SavedConditionProfitability{{Symbol: "eurusd", Timeframe: "h1", FileName: "file.csv", Matches: 11, TakeProfitHits: 10, ProfitablePercent: 100}}}
	if validateSavedConditionAnalysis(malformed) == nil {
		t.Fatal("forged percentage accepted")
	}
}

func TestCompletedMatchingEvidencePreservesThirtyProfitableAssetsWithFailedFile(t *testing.T) {
	for _, counts := range []struct{ qualified, failed int }{{30, 1}, {26, 4}} {
		t.Run(fmt.Sprintf("%dqualified-%dfailed", counts.qualified, counts.failed), func(t *testing.T) {
			m := analysisTestManager(t)
			start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
			text := fmt.Sprintf("timestamp,open,high,low,close,stop,profit,signal\n%d,100,105,95,100,90,110,1\n%d,100,112,95,105,90,110,0\n", start, start+86400000)
			for i := 0; i < counts.qualified; i++ {
				writeDataFile(t, m, fmt.Sprintf("asset%d-d1-2024-01-01-2024-01-02.csv", i), text)
			}
			for i := 0; i < counts.failed; i++ {
				writeDataFile(t, m, fmt.Sprintf("broken%d-d1-2024-01-01-2024-01-02.csv", i), "timestamp,open,high,low,close,signal\nbroken,1,2,0,1,1\n")
			}
			request := simulationRequest(1)
			request.Period, request.ProfitabilityFilter = "d1", 80
			saved, err := m.SaveAnalysisCondition("Safe5 equivalent", request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.StartAnalysis(request); err != nil {
				t.Fatal(err)
			}
			result := waitAnalysis(t, m)
			if result.Error != "" || result.MatchedFiles != counts.qualified || result.Failed != counts.failed || result.TotalResults != counts.qualified+counts.failed {
				t.Fatal("unexpected scan results", result)
			}
			conditions, err := m.SavedAnalysisConditions()
			if err != nil || len(conditions) != 1 || conditions[0].ID != saved.ID || conditions[0].Analysis == nil || len(conditions[0].Analysis.Assets) != counts.qualified || conditions[0].Analysis.FailedFiles != counts.failed {
				t.Fatalf("completed successes lost beside failed input: %+v %v", conditions, err)
			}
			for _, row := range conditions[0].Analysis.Assets {
				if row.Timeframe != "d1" || row.Matches != 1 || row.TakeProfitHits != 1 || row.ProfitablePercent != 100 || row.ProfitabilityUnavailable {
					t.Fatal("matching evidence was not recorded accurately", row)
				}
			}
			profile := m.profile
			m.Close()
			restarted := savedConditionManager(t, profile)
			restored, err := restarted.SavedAnalysisConditions()
			if err != nil || !reflect.DeepEqual(restored, conditions) {
				t.Fatal("restart lost successful partial-file scan scope", restored, err)
			}
		})
	}
}

func TestCompletedUnsimulatedAnalysisRecordsOnlyMatchingAssetsOfSavedPeriod(t *testing.T) {
	m := analysisTestManager(t)
	dailyCSV := func(values []float64) string {
		var text strings.Builder
		text.WriteString("timestamp,open,high,low,close,volume\n")
		for i, value := range values {
			fmt.Fprintf(&text, "%d,%g,%g,%g,%g,10\n", int64(1704067200000)+int64(i)*86400000, value, value+1, value-0.5, value)
		}
		return text.String()
	}
	writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-02.csv", dailyCSV([]float64{1, 2, 3, 4}))
	writeDataFile(t, m, "eurusd-h1-2024-01-01-2024-01-02.csv", candleCSV)
	writeDataFile(t, m, "gbpusd-d1-2024-01-01-2024-01-02.csv", dailyCSV([]float64{1, 2, 3, 4}))
	request := AnalysisRequest{Period: "d1", Conditions: []AnalysisCondition{analysisCondition("above", analysisField("close"), analysisNumber(3))}}
	if _, err := m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	result := waitAnalysis(t, m)
	if result.Error != "" || result.Total != 2 {
		t.Fatal(result)
	}
	saved, err := m.SaveAnalysisCondition("no simulation", request)
	if err != nil || saved.Analysis == nil || len(saved.Analysis.Assets) != 2 {
		t.Fatal("unsimulated successful matches were not recorded", saved, err)
	}
	for _, row := range saved.Analysis.Assets {
		if row.Timeframe != "d1" || row.Matches != 1 || !row.ProfitabilityUnavailable {
			t.Fatal("wrong matching scope or invented profitability", row)
		}
	}
	// Change the close rule to match just the EURUSD file; its historical h1
	// sibling remains outside the saved d1 scope.
	if err = os.WriteFile(filepath.Join(m.directory, "gbpusd-d1-2024-01-01-2024-01-02.csv"), []byte(dailyCSV([]float64{1, 2, 3})), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.StartAnalysis(request); err != nil {
		t.Fatal(err)
	}
	_ = waitAnalysis(t, m)
	conditions, err := m.SavedAnalysisConditions()
	if err != nil || len(conditions) != 1 || len(conditions[0].Analysis.Assets) != 1 || conditions[0].Analysis.Assets[0].Symbol != "eurusd" || conditions[0].Analysis.Assets[0].Timeframe != "d1" || !conditions[0].Analysis.Assets[0].ProfitabilityUnavailable {
		t.Fatal("nonmatching asset or wrong period entered inherited scope", conditions, err)
	}
	profile := m.profile
	m.Close()
	restarted := savedConditionManager(t, profile)
	restored, err := restarted.SavedAnalysisConditions()
	if err != nil || !reflect.DeepEqual(restored, conditions) {
		t.Fatal("restart lost unsimulated matching scope", restored, err)
	}
}

func TestCompletedMatchingEvidenceDistinguishesMissingSimulationFromMeasuredZero(t *testing.T) {
	unknown := profitabilityTestResult("unknown", 0, 5)
	unknown.Simulation = nil
	unavailable := profitabilityTestResult("unavailable", 0, 3)
	unavailable.Symbol = "gbpusd"
	unavailable.Simulation.Unavailable = 3
	zero := profitabilityTestResult("measured-zero", 0, 2)
	zero.Symbol = "usdjpy"
	a := &analysisState{phase: "completed", asOf: time.Now().UnixMilli(), results: []AnalysisResult{unknown, unavailable, zero}}
	snapshot, err := completedAnalysisProfitability(a)
	if err != nil || snapshot == nil || len(snapshot.Assets) != 3 {
		t.Fatal(snapshot, err)
	}
	for _, row := range snapshot.Assets {
		if row.ProfitabilityUnavailable != (row.Symbol != "usdjpy") || row.ProfitablePercent != 0 || row.TakeProfitHits != 0 {
			t.Fatal("unknown outcomes confused with measured zero", row)
		}
	}
	// Prefer measured evidence, including measured zero, over an unsimulated
	// overlapping file for the same symbol and period.
	zero.Symbol = "eurusd"
	a.results = []AnalysisResult{unknown, zero}
	snapshot, err = completedAnalysisProfitability(a)
	if err != nil || len(snapshot.Assets) != 1 || snapshot.Assets[0].FileName != zero.Name || snapshot.Assets[0].ProfitabilityUnavailable {
		t.Fatal("known zero lost to unknown outcomes", snapshot, err)
	}
	malformed := copySavedConditionAnalysis(snapshot)
	malformed.Assets[0].ProfitabilityUnavailable = true
	malformed.Assets[0].TakeProfitHits, malformed.Assets[0].ProfitablePercent = 1, 50
	if validateSavedConditionAnalysis(malformed) == nil {
		t.Fatal("unavailable evidence accepted fabricated statistics")
	}
	malformed = copySavedConditionAnalysis(snapshot)
	malformed.FailedFiles = -1
	if validateSavedConditionAnalysis(malformed) == nil {
		t.Fatal("negative failed file count accepted")
	}
}

func TestSavedConditionsRecoverOnlyMissingCompatibleFinishedEvidence(t *testing.T) {
	m := analysisTestManager(t)
	request := defaultAnalysisRequest()
	request.Period = "d1"
	saved, err := m.SaveAnalysisCondition("missing", request)
	if err != nil {
		t.Fatal(err)
	}
	otherRequest := copyAnalysisRequest(request)
	otherRequest.Period = "h1"
	if _, err = m.SaveAnalysisCondition("different period", otherRequest); err != nil {
		t.Fatal(err)
	}
	row := profitabilityTestResult("recorded", 0, 2)
	row.Timeframe, row.Simulation = "d1", nil
	a := &analysisState{active: true, phase: "completed", asOf: time.Now().UnixMilli(), request: request, results: []AnalysisResult{row, {Status: "failed"}}}
	m.analysis = a
	path := filepath.Join(m.profile, "screener-saved-conditions.json")
	before, _ := os.ReadFile(path)
	conditions, err := m.SavedAnalysisConditions()
	if err != nil || conditions[0].Analysis != nil {
		t.Fatal("active scan became scope", conditions, err)
	}
	a.active, a.phase = false, "cancelled"
	conditions, err = m.SavedAnalysisConditions()
	if err != nil || conditions[0].Analysis != nil {
		t.Fatal("cancelled scan became scope", conditions, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("incomplete scan recovery wrote saved state")
	}
	a.phase = "completed"
	conditions, err = m.SavedAnalysisConditions()
	if err != nil || conditions[0].ID != saved.ID || conditions[0].Analysis == nil || len(conditions[0].Analysis.Assets) != 1 || !conditions[0].Analysis.Assets[0].ProfitabilityUnavailable || conditions[0].Analysis.FailedFiles != 1 || conditions[1].Analysis != nil {
		t.Fatal("compatible missing scope was not recovered", conditions, err)
	}
	beforeStat, _ := os.Stat(path)
	// A read must preserve an existing snapshot, even when a different completed
	// result is now available. Normal analysis publication owns refreshes.
	a.asOf++
	a.results = []AnalysisResult{{Status: "completed"}}
	again, err := m.SavedAnalysisConditions()
	afterStat, _ := os.Stat(path)
	if err != nil || !reflect.DeepEqual(again, conditions) || !os.SameFile(beforeStat, afterStat) || beforeStat.ModTime() != afterStat.ModTime() {
		t.Fatal("reading existing evidence replaced or rewrote it", again, err)
	}
}

func TestRecordedProfitabilityWriteFailurePreservesOriginalFileAndSurfacesError(t *testing.T) {
	m := analysisTestManager(t)
	request := defaultAnalysisRequest()
	if _, err := m.SaveAnalysisCondition("saved", request); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.profile, "screener-saved-conditions.json")
	original, _ := os.ReadFile(path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	a := &analysisState{phase: "completed", asOf: time.Now().UnixMilli(), request: request, results: []AnalysisResult{profitabilityTestResult("qualified", 1, 1)}}
	m.mu.Lock()
	err := m.persistCompletedAnalysisProfitabilityLocked(a)
	m.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatal("publication failure was hidden", err)
	}
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		t.Fatal("failed snapshot replaced protected original", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
}
