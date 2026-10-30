package screener

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLiveAlertsLegacyPeriodMigrationPreservesStateUntilSaved(t *testing.T) {
	for _, periods := range [][]string{{"h1"}, {"d1", "h1"}} {
		t.Run(strings.Join(periods, "-"), func(t *testing.T) {
			m, l, config, _ := liveAlertsFixture(t)
			id := config.ConditionPeriods[0].ConditionID
			original := l.state
			original.Version = 1
			original.Config = LiveAlertsConfig{Enabled: true, ConditionIDs: []string{id}, Periods: periods, Symbols: []string{"eurusd"}}
			original.Watermarks = map[string]int64{id + "/eurusd/h1": 1704070800000}
			original.Sent = 3
			if err := writeState(l.path, original); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(l.path)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := NewLiveAlerts(m, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			info := restored.Info()
			expectedPeriod := "h1"
			if len(periods) > 1 {
				expectedPeriod = ""
				if info.Config.Enabled || !strings.Contains(info.Error, "Choose one period") {
					t.Fatalf("ambiguous selection was enabled or not explained: %+v", info)
				}
			} else if !info.Config.Enabled {
				t.Fatal("single-period selection did not remain enabled")
			}
			if !reflect.DeepEqual(info.Config.ConditionPeriods, []LiveAlertConditionPeriod{{ConditionID: id, Period: expectedPeriod}}) || info.Sent != 3 || restored.state.Watermarks[id+"/eurusd/h1"] != 1704070800000 {
				t.Fatalf("legacy state lost: %+v", restored.state)
			}
			after, _ := os.ReadFile(l.path)
			if string(before) != string(after) {
				t.Fatal("restore overwrote original version 1 state")
			}
			encoded, err := json.Marshal(info.Config)
			if err != nil || strings.Contains(string(encoded), "conditionIds") || strings.Contains(string(encoded), `"periods"`) {
				t.Fatalf("public response leaked legacy shared selections: %s %v", encoded, err)
			}
			draft := info.Config
			draft.Enabled = false
			if _, err = restored.Configure(draft); err != nil {
				t.Fatal(err)
			}
			var saved liveAlertsState
			if err = readState(l.path, liveAlertsStateBytes, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Version != 2 || !reflect.DeepEqual(saved.Config.ConditionPeriods, draft.ConditionPeriods) || !reflect.DeepEqual(saved.Watermarks, original.Watermarks) {
				t.Fatalf("migration save lost configuration or claims: %+v", saved)
			}
		})
	}
}

func TestLiveAlertsEachConditionRequiresExactlyOneSupportedPeriod(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionPeriods[0].ConditionID
	for _, rules := range [][]LiveAlertConditionPeriod{
		{{ConditionID: id, Period: "h1"}, {ConditionID: id, Period: "d1"}},
		{{ConditionID: id, Period: "m5"}},
		{{ConditionID: id, Period: ""}},
		{{ConditionID: "bad-id", Period: "h1"}},
	} {
		value := config
		value.ConditionPeriods = rules
		if _, err := l.Configure(value); err == nil {
			t.Fatalf("invalid condition assignment accepted: %+v", rules)
		}
	}
	config.ConditionPeriods[0].Period = ""
	config.Enabled = false
	if _, err := l.Configure(config); err != nil {
		t.Fatal("incomplete stopped draft was not saveable", err)
	}
}

func TestLiveAlertsRejectsPeriodWithoutAnyDownloadedSelectedTarget(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	config.ConditionPeriods[0].Period = "d1"
	if _, err := l.Configure(config); err == nil || !strings.Contains(err.Error(), "D1") {
		t.Fatal("condition without an available assigned-period target was enabled", err)
	}
}

func TestLiveAlertsMalformedEnabledLegacySelectionRemainsBlocked(t *testing.T) {
	m, l, _, _ := liveAlertsFixture(t)
	state := l.state
	state.Version = 1
	state.Config = LiveAlertsConfig{Enabled: true, Periods: []string{"d1", "h1"}}
	if err := writeState(l.path, state); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(l.path)
	restored, err := NewLiveAlerts(m, nil)
	if err == nil || !restored.blocked {
		t.Fatal("empty enabled legacy selections restored as active", err)
	}
	defer restored.Close()
	after, _ := os.ReadFile(l.path)
	if string(before) != string(after) {
		t.Fatal("invalid legacy state was overwritten")
	}
}

func TestLiveAlertsDeletedAssignedPeriodHistoryReportsError(t *testing.T) {
	_, l, config, path := liveAlertsFixture(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := l.check(context.Background(), config, time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC).UnixMilli()); err == nil || !strings.Contains(err.Error(), "no downloaded H1 history") {
		t.Fatal("missing target was reported as a successful check", err)
	}
}
