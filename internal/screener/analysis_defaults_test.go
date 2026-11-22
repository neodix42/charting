package screener

import (
	"reflect"
	"testing"
)

func TestAnalysisNewDefaultsUseRightFieldAndStoredConstantsRemainNumbers(t *testing.T) {
	m := analysisTestManager(t)
	fresh, err := m.AnalysisView()
	if err != nil || len(fresh.Conditions) != 1 || !reflect.DeepEqual(fresh.Conditions[0].Right, analysisField("close")) {
		t.Fatalf("fresh backend draft must use the conventional Close field: %+v %v", fresh, err)
	}
	saved := positiveAnalysisRequest()
	saved.Conditions[0].Right = analysisNumber(42)
	if _, err := m.SaveAnalysisView(saved); err != nil {
		t.Fatal(err)
	}
	condition, err := m.SaveAnalysisCondition("Existing constant", saved)
	if err != nil {
		t.Fatal(err)
	}
	restored := savedConditionManager(t, m.profile)
	draft, err := restored.AnalysisView()
	if err != nil || !reflect.DeepEqual(draft.Conditions[0].Right, analysisNumber(42)) {
		t.Fatalf("saved draft constant changed: %+v %v", draft, err)
	}
	conditions, err := restored.SavedAnalysisConditions()
	if err != nil || len(conditions) != 1 || conditions[0].ID != condition.ID || !reflect.DeepEqual(conditions[0].Request.Conditions[0].Right, analysisNumber(42)) {
		t.Fatalf("saved named constant changed: %+v %v", conditions, err)
	}
}
