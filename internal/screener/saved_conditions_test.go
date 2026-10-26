package screener

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	calendar := analysisCondition("equal", analysisField("weekday"), analysisNumber(1))
	calendar.ID = "calendar-equal"
	repeated := analysisCondition("remainedFor", analysisField("hour"), analysisNumber(0))
	repeated.ID, repeated.Comparison = "repeated-equal", "equal"
	request.Conditions = append(request.Conditions, calendar, repeated)
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
			if err := m.DeleteAnalysisCondition(strings.Repeat("a", 32)); err == nil {
				t.Fatal("deletion accepted invalid saved state")
			}
			if after, _ := os.ReadFile(path); string(after) != data {
				t.Fatal("saved evidence was changed")
			}
		})
	}
}

func TestDeleteSavedConditionPersistsWithoutChangingDraftOrData(t *testing.T) {
	profile := t.TempDir()
	m := savedConditionManager(t, profile)
	request := defaultAnalysisRequest()
	request.Conditions[0].Operator = "equal"
	request.Conditions[0].Left.Field = "weekday"
	request.Conditions[0].Right = analysisNumber(1)
	if _, err := m.SaveAnalysisView(request); err != nil {
		t.Fatal(err)
	}
	var saved []SavedAnalysisCondition
	for _, name := range []string{"First", "Delete me", "Last"} {
		condition, err := m.SaveAnalysisCondition(name, request)
		if err != nil {
			t.Fatal(err)
		}
		saved = append(saved, condition)
	}
	dataDirectory := filepath.Join(profile, "screener")
	if err := os.MkdirAll(dataDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(dataDirectory, "btcusd-d1-2024-01-01-2024-01-02.csv")
	const data = "timestamp,close,weekday\n1704067200000,42,1\n"
	if err := os.WriteFile(dataPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteAnalysisCondition(saved[1].ID); err != nil {
		t.Fatal(err)
	}
	m.Close()
	restarted := savedConditionManager(t, profile)
	if list, err := restarted.SavedAnalysisConditions(); err != nil || !reflect.DeepEqual(list, []SavedAnalysisCondition{saved[0], saved[2]}) {
		t.Fatalf("deletion did not preserve the remaining snapshots across restart: %+v %v", list, err)
	}
	if draft, err := restarted.AnalysisView(); err != nil || !reflect.DeepEqual(draft, request) {
		t.Fatalf("deleting a saved snapshot changed the draft: %+v %v", draft, err)
	}
	if got, err := os.ReadFile(dataPath); err != nil || string(got) != data {
		t.Fatalf("deleting a saved snapshot changed downloaded data: %q %v", got, err)
	}
	if info, err := restarted.Analysis(0, 10); err != nil || info.Active {
		t.Fatalf("deleting a saved snapshot started analysis: %+v %v", info, err)
	}
	for _, condition := range []SavedAnalysisCondition{saved[0], saved[2]} {
		if err := restarted.DeleteAnalysisCondition(condition.ID); err != nil {
			t.Fatal(err)
		}
	}
	restarted.Close()
	empty := savedConditionManager(t, profile)
	if list, err := empty.SavedAnalysisConditions(); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("deletion of the last snapshot did not persist an empty list: %+v %v", list, err)
	}
	if _, err := empty.SaveAnalysisCondition("Delete me", request); err != nil {
		t.Fatalf("deleted condition name could not be reused: %v", err)
	}
}

func TestDeleteSavedConditionFailuresPreserveState(t *testing.T) {
	m := savedConditionManager(t, t.TempDir())
	saved, err := m.SaveAnalysisCondition("Keep", defaultAnalysisRequest())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.profile, "screener-saved-conditions.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "bad", strings.Repeat("z", 32), strings.Repeat("0", 32)} {
		if err := m.DeleteAnalysisCondition(id); err == nil {
			t.Fatalf("invalid or missing ID was accepted: %q", id)
		}
		if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
			t.Fatalf("failed deletion changed saved snapshots: %v", err)
		}
	}
	m.Close()
	if err := m.DeleteAnalysisCondition(saved.ID); err == nil {
		t.Fatal("deleted from closed manager")
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Fatalf("deletion after close changed saved snapshots: %v", err)
	}
}

func TestSavedConditionsConcurrentSaveAndDeleteDoNotLoseChanges(t *testing.T) {
	profile := t.TempDir()
	m := savedConditionManager(t, profile)
	const count = 10
	var saved []SavedAnalysisCondition
	for i := 0; i < count; i++ {
		condition, err := m.SaveAnalysisCondition(fmt.Sprintf("Old %d", i), defaultAnalysisRequest())
		if err != nil {
			t.Fatal(err)
		}
		saved = append(saved, condition)
	}
	var wg sync.WaitGroup
	errors := make(chan error, count*2)
	for i, condition := range saved {
		wg.Add(2)
		go func() {
			defer wg.Done()
			errors <- m.DeleteAnalysisCondition(condition.ID)
		}()
		go func() {
			defer wg.Done()
			_, err := m.SaveAnalysisCondition(fmt.Sprintf("New %d", i), defaultAnalysisRequest())
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	m.Close()
	restarted := savedConditionManager(t, profile)
	list, err := restarted.SavedAnalysisConditions()
	if err != nil || len(list) != count {
		t.Fatalf("concurrent mutations lost saved snapshots: %+v %v", list, err)
	}
	for _, condition := range list {
		if !strings.HasPrefix(condition.Name, "New ") {
			t.Fatalf("deleted snapshot returned after concurrent save: %+v", condition)
		}
	}
}
