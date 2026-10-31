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

func TestLiveAlertsLegacyScopeMigrationPreservesStateUntilSaved(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			m, l, config, _ := liveAlertsFixture(t)
			id := config.ConditionIDs[0]
			original := l.state
			original.Version = version
			original.Config = LiveAlertsConfig{Enabled: true, Symbols: []string{"eurusd"}}
			if version == 1 {
				original.Config.ConditionIDs = []string{id}
				original.Config.Periods = []string{"d1", "h1"}
			} else {
				original.Config.ConditionPeriods = []LiveAlertConditionPeriod{{ConditionID: id, Period: "d1"}}
			}
			original.Watermarks = map[string]int64{id + "/eurusd/h1": 1704070800000}
			original.Sent = 3
			original.Recent = []LiveAlertOutcome{{Symbol: "eurusd", Timeframe: "h1", Time: 1704070800000, Status: "claimed"}}
			if err := writeState(l.path, original); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(l.path)
			restored, err := NewLiveAlerts(m, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			info := restored.Info()
			if info.Config.Enabled || !strings.Contains(info.Error, "inherits each saved condition") || !reflect.DeepEqual(info.Config.ConditionIDs, []string{id}) || info.Sent != 3 || restored.state.Watermarks[id+"/eurusd/h1"] != 1704070800000 || info.Recent[0].Status != "uncertain" {
				t.Fatalf("migration lost state or expanded an active scope: %+v", info)
			}
			after, _ := os.ReadFile(l.path)
			if string(before) != string(after) {
				t.Fatal("restore rewrote original legacy state")
			}
			encoded, _ := json.Marshal(info.Config)
			if !strings.Contains(string(encoded), "conditionIds") || strings.Contains(string(encoded), "conditionPeriods") || strings.Contains(string(encoded), "symbols") || strings.Contains(string(encoded), `"periods"`) {
				t.Fatal("canonical response retains obsolete choices", string(encoded))
			}
			if _, err = restored.Configure(info.Config); err != nil {
				t.Fatal(err)
			}
			var saved liveAlertsState
			if err = readState(l.path, liveAlertsStateBytes, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Version != 3 || !reflect.DeepEqual(saved.Config.ConditionIDs, []string{id}) || !reflect.DeepEqual(saved.Watermarks, original.Watermarks) || saved.Recent[0].Status != "uncertain" {
				t.Fatal("migration save lost identities or delivery history", saved)
			}
		})
	}
}

func TestLiveAlertsCanonicalSelectionRequiresDistinctSavedConditions(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	for _, ids := range [][]string{{id, id}, {"bad-id"}, {}} {
		value := config
		value.ConditionIDs = ids
		if _, err := l.Configure(value); err == nil {
			t.Fatal("invalid condition selection accepted", ids)
		}
	}
	config.Enabled = false
	config.ConditionIDs = []string{}
	if _, err := l.Configure(config); err != nil {
		t.Fatal("empty stopped draft could not be saved", err)
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
		t.Fatal("empty enabled legacy selection restored", err)
	}
	defer restored.Close()
	after, _ := os.ReadFile(l.path)
	if string(before) != string(after) {
		t.Fatal("invalid legacy state was overwritten")
	}
}

func TestLiveAlertsDeletedInheritedHistoryReportsError(t *testing.T) {
	_, l, config, path := liveAlertsFixture(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := l.check(context.Background(), config, time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC).UnixMilli()); err == nil || !strings.Contains(err.Error(), "no downloaded H1 assets") {
		t.Fatal("missing inherited target reported success", err)
	}
}
