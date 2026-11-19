package storage

import (
	"reflect"
	"testing"

	"cryptodesk/internal/domain"
)

func TestCalendarOpenCloseIndicatorsSurviveSaveRestartAndExport(t *testing.T) {
	s, path := openTest(t)
	loaded := loadTest(t, s)
	w := loaded.Workspace
	color := "#123abc"
	for _, prefix := range []string{"today", "previous_day", "two_days_ago", "three_days_ago", "week", "previous_week"} {
		for _, field := range []string{"open", "close"} {
			w.Tabs[0].Charts[0].Indicators = append(w.Tabs[0].Charts[0].Indicators, domain.Indicator{ID: domain.ID(), Type: prefix + "_" + field, Parameters: map[string]int{}, Color: &color})
		}
	}
	ack, err := s.Save(loaded.Revision, "calendar-open-close", 1, w)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	actual := loadTest(t, restored)
	if actual.Revision != ack.Revision || !reflect.DeepEqual(actual.Workspace, w) {
		t.Fatal("restart changed calendar open/close settings", actual)
	}
	exported, err := restored.Export()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := domain.DecodeWorkspace(exported)
	if err != nil || !reflect.DeepEqual(decoded, w) {
		t.Fatal("portable export changed calendar open/close settings", err)
	}
}
