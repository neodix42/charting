package storage

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cryptodesk/internal/domain"
)

func TestMovingAverageWidthsSaveRestartExportImportAndRejectInvalidChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := loadTest(t, s)
	w := first.Workspace
	smaWidth, emaWidth := 4, 3
	w.Tabs[0].Charts[0].Indicators = []domain.Indicator{
		{ID: domain.ID(), Type: "sma", Parameters: map[string]int{"period": 20}, LineWidth: &smaWidth},
		{ID: domain.ID(), Type: "ema", Parameters: map[string]int{"period": 50}, LineWidth: &emaWidth},
		{ID: domain.ID(), Type: "ema", Parameters: map[string]int{"period": 1}},
		{ID: domain.ID(), Type: "macd", Parameters: map[string]int{"fast": 12, "slow": 26, "signal": 9}},
	}
	ack, err := s.Save(first.Revision, "indicator-width-test", 1, w)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored := loadTest(t, s)
	if restored.Revision != ack.Revision || !reflect.DeepEqual(restored.Workspace, w) {
		t.Fatal("independent SMA/EMA widths changed on restart", restored)
	}
	data, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	portable, err := domain.DecodeWorkspace(data)
	if err != nil || !reflect.DeepEqual(portable, w) {
		t.Fatal("SMA/EMA widths changed during export", err)
	}
	importWidth := 1
	portable.Tabs[0].Charts[0].Indicators[0].LineWidth = &importWidth
	imported, err := s.Import(restored.Revision, portable)
	if err != nil || !reflect.DeepEqual(imported.Workspace, portable) {
		t.Fatal("SMA/EMA widths did not import exactly", err)
	}
	before, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*domain.Workspace){
		"zero SMA width":      func(w *domain.Workspace) { width := 0; w.Tabs[0].Charts[0].Indicators[0].LineWidth = &width },
		"excessive EMA width": func(w *domain.Workspace) { width := 5; w.Tabs[0].Charts[0].Indicators[1].LineWidth = &width },
		"MACD width":          func(w *domain.Workspace) { width := 2; w.Tabs[0].Charts[0].Indicators[3].LineWidth = &width },
	} {
		t.Run(name, func(t *testing.T) {
			invalid, err := domain.DecodeWorkspace(before)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&invalid)
			if _, err := s.Save(imported.Revision, "invalid-width-test", 1, invalid); err == nil {
				t.Fatal("invalid line width save accepted")
			}
			if _, err := s.Import(imported.Revision, invalid); err == nil {
				t.Fatal("invalid line width import accepted")
			}
			after, err := s.Export()
			if err != nil || string(before) != string(after) {
				t.Fatal("invalid line width changed saved workspace", err)
			}
		})
	}
	for _, width := range []string{`null`, `1.5`, `"2"`, `1e999`} {
		input := strings.Replace(string(before), `"lineWidth": 1`, `"lineWidth": `+width, 1)
		if input == string(before) {
			t.Fatal("strict width import test did not modify JSON")
		}
		if _, err := domain.DecodeWorkspace([]byte(input)); err == nil {
			t.Fatal("malformed width import accepted", width)
		}
		after, err := s.Export()
		if err != nil || string(before) != string(after) {
			t.Fatal("failed width decode changed saved workspace", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	final := loadTest(t, s)
	if final.Revision != imported.Revision || !reflect.DeepEqual(final.Workspace, portable) {
		t.Fatal("imported moving average widths changed after restart", final)
	}
}
