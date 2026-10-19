package storage

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"cryptodesk/internal/domain"
)

func TestChartRightGapSaveRestartExportImportAndInvalidPreservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := loadTest(t, s)
	if first.Workspace.ChartRightGap == nil || *first.Workspace.ChartRightGap != (domain.ChartRightGap{Unit: "bars", Value: 10}) {
		t.Fatal("first launch did not persist default right gap", first.Workspace.ChartRightGap)
	}
	w := first.Workspace
	w.ChartRightGap = &domain.ChartRightGap{Unit: "pixels", Value: 350}
	ack, err := s.Save(first.Revision, "right-gap-test", 1, w)
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
	restored := loadTest(t, s)
	if restored.Revision != ack.Revision || !reflect.DeepEqual(restored.Workspace, w) {
		t.Fatal("right gap changed on restart", restored)
	}
	exported, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	portable, err := domain.DecodeWorkspace(exported)
	if err != nil || !reflect.DeepEqual(portable, w) {
		t.Fatal("right gap changed on export", err)
	}
	portable.ChartRightGap = &domain.ChartRightGap{Unit: "bars", Value: 27}
	imported, err := s.Import(restored.Revision, portable)
	if err != nil || !reflect.DeepEqual(imported.Workspace, portable) {
		t.Fatal("right gap did not import exactly", err)
	}
	before, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	for _, gap := range []domain.ChartRightGap{{Unit: "bars", Value: -1}, {Unit: "bars", Value: 501}, {Unit: "pixels", Value: 5001}, {Unit: "unknown", Value: 1}} {
		invalid := portable
		invalid.ChartRightGap = &gap
		if _, err = s.Import(imported.Revision, invalid); err == nil {
			t.Fatal("invalid right gap import accepted", gap)
		}
		after, err := s.Export()
		if err != nil || string(before) != string(after) {
			t.Fatal("invalid right gap import changed saved workspace", err)
		}
	}
	var malformed map[string]json.RawMessage
	if err := json.Unmarshal(before, &malformed); err != nil {
		t.Fatal(err)
	}
	malformed["chartRightGap"] = json.RawMessage(`{"unit":"bars","value":1.5}`)
	data, err := json.Marshal(malformed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.DecodeWorkspace(data); err == nil {
		t.Fatal("malformed right gap passed import validation")
	}
	after, err := s.Export()
	if err != nil || string(before) != string(after) {
		t.Fatal("failed right gap decode changed saved workspace", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if final := loadTest(t, s); final.Revision != imported.Revision || !reflect.DeepEqual(final.Workspace, portable) {
		t.Fatal("imported right gap changed on restart", final)
	}
}
