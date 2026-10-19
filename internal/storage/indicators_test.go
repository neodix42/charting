package storage

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"cryptodesk/internal/domain"
)

func TestWeeklyIndicatorsPersistRestoreExportAndRejectInvalidImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	w := loaded.Workspace
	w.Tabs[0].Charts[0].Timeframe = "1w"
	color := "#48a9f8"
	w.Tabs[0].Charts[0].Indicators = []domain.Indicator{
		{ID: domain.ID(), Type: "sma", Parameters: map[string]int{"period": 20}, Color: &color},
		{ID: domain.ID(), Type: "ema", Parameters: map[string]int{"period": 50}},
		{ID: domain.ID(), Type: "ichimoku", Parameters: map[string]int{"conversion": 9, "base": 26, "spanB": 52, "displacement": 26}},
		{ID: domain.ID(), Type: "macd", Parameters: map[string]int{"fast": 12, "slow": 26, "signal": 9}},
	}
	ack, err := s.Save(loaded.Revision, "indicator-test", 1, w)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != ack.Revision || !reflect.DeepEqual(restored.Workspace, w) {
		t.Fatal("indicator configuration changed on restart", restored, err)
	}
	data, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	exported, err := domain.DecodeWorkspace(data)
	if err != nil || !reflect.DeepEqual(exported, w) {
		t.Fatal("weekly indicators did not export exactly", err)
	}
	before := append([]byte(nil), data...)
	var invalid domain.Workspace
	if err = json.Unmarshal(data, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid.Tabs[0].Charts[0].Indicators[3].Parameters["slow"] = 5
	if _, err = s.Import(restored.Revision, invalid); err == nil {
		t.Fatal("accepted invalid MACD")
	}
	after, err := s.Export()
	if err != nil || string(before) != string(after) {
		t.Fatal("invalid indicator import changed original state", err)
	}
}
