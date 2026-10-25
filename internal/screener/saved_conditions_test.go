package screener

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func savedConditionManager(t *testing.T, profile string) *Manager {
	t.Helper()
	m := newManager(profile, func() (workerRuntime, error) { return workerRuntime{}, errors.New("no downloader needed") })
	t.Cleanup(m.Close)
	return m
}

func TestSavedConditionsRoundTripSnapshotsAllRowsWithoutChangingDraftOrApplying(t *testing.T) {
	profile := t.TempDir()
	m := savedConditionManager(t, profile)
	request := defaultAnalysisRequest()
	request.Conditions[0].Operator = "occurredWithin"
	request.Conditions[0].Comparison = "crossesAbove"
	request.Conditions[0].Left.Field = "macd_histogram_12_26_9"
	request.Conditions[0].Left.Timeframe = "d1"
	request.Conditions[0].Left.Offset = 2
	request.Conditions[0].Bars = 5
	second := defaultAnalysisRequest().Conditions[0]
	second.ID, second.Operator = "condition-2", "withinDistance"
	second.Right = AnalysisOperand{Kind: "field", Field: "sma_200", Timeframe: "h4", Aggregate: "highest", Window: 20, Offset: 1}
	request.Conditions = append(request.Conditions, second)
	want := copyAnalysisRequest(request)
	saved, err := m.SaveAnalysisCondition("  Trading hours 📈  ", request)
	if err != nil || saved.Name != "Trading hours 📈" || len(saved.ID) != 32 || !reflect.DeepEqual(saved.Request, want) {
		t.Fatalf("saved snapshot: %+v %v", saved, err)
	}
	request.Conditions[0].Left.Field = "changed caller"
	saved.Request.Conditions[1].Right.Field = "changed response"
	info, infoErr := m.Analysis(0, 50)
	if draft, err := m.AnalysisView(); err != nil || !reflect.DeepEqual(draft, defaultAnalysisRequest()) || infoErr != nil || info.Active {
		t.Fatalf("saving a named condition changed the draft or applied it: %+v %v", draft, err)
	}
	m.Close()
	restarted := savedConditionManager(t, profile)
	list, err := restarted.SavedAnalysisConditions()
	if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0].Request, want) {
		t.Fatalf("restart lost rows or parameters: %+v %v", list, err)
	}
	list[0].Request.Conditions[0].Left.Field = "changed list"
	next, err := restarted.SavedAnalysisConditions()
	if err != nil || !reflect.DeepEqual(next[0].Request, want) {
		t.Fatal("list returned mutable stored state")
	}
	if _, err = restarted.SaveAnalysisView(next[0].Request); err != nil {
		t.Fatal(err)
	}
	if loaded, err := restarted.AnalysisView(); err != nil || !reflect.DeepEqual(loaded, want) {
		t.Fatal("saved snapshot cannot be restored as the editable draft")
	}
}

func TestSavedConditionValidationPreservesExistingSets(t *testing.T) {
	m := savedConditionManager(t, t.TempDir())
	request := defaultAnalysisRequest()
	if _, err := m.SaveAnalysisCondition("First", request); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.profile, "screener-saved-conditions.json")
	before, _ := os.ReadFile(path)
	for _, name := range []string{"", " ", "FIRST", "First", "bad\nname", strings.Repeat("名", 101)} {
		if _, err := m.SaveAnalysisCondition(name, request); err == nil {
			t.Fatalf("invalid or duplicate name accepted: %q", name)
		}
	}
	for _, invalid := range []AnalysisRequest{{}, {Conditions: []AnalysisCondition{{ID: "invalid", Operator: "unknown"}}}} {
		if _, err := m.SaveAnalysisCondition("New", invalid); err == nil {
			t.Fatal("invalid rows accepted")
		}
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("validation failure changed previous saved conditions")
	}
	if _, err := m.SaveAnalysisCondition("Second", request); err != nil {
		t.Fatal(err)
	}
	list, err := m.SavedAnalysisConditions()
	if err != nil || len(list) != 2 || list[0].Name != "First" || list[1].Name != "Second" {
		t.Fatalf("separate names did not preserve both sets: %+v %v", list, err)
	}
	m.Close()
	if _, err = m.SaveAnalysisCondition("Closed", request); err == nil {
		t.Fatal("saved to closed manager")
	}
}

func TestSavedConditionsCorruptFutureAndOversizedStateIsNeverOverwritten(t *testing.T) {
	for _, data := range []string{`{"version":2,"conditions":[]}`, `{"version":1,"conditions":[{"id":"bad","name":"Name","request":{"conditions":[]}}]}`, `{broken`, strings.Repeat("x", savedConditionsBytes+1)} {
		t.Run(data[:min(30, len(data))], func(t *testing.T) {
			m := savedConditionManager(t, t.TempDir())
			path := filepath.Join(m.profile, "screener-saved-conditions.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.SavedAnalysisConditions(); err == nil {
				t.Fatal("invalid saved state was accepted")
			}
			if _, err := m.SaveAnalysisCondition("New", defaultAnalysisRequest()); err == nil {
				t.Fatal("invalid saved state was overwritten")
			}
			if after, _ := os.ReadFile(path); string(after) != data {
				t.Fatal("saved evidence was changed")
			}
		})
	}
}
