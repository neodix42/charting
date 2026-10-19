package storage

import (
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cryptodesk/internal/domain"
)

func TestMACDPaneHeightsSaveRestartExportImportAndInvalidPreservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := loadTest(t, s)
	w := first.Workspace
	firstFactor, secondFactor := 0.625, 0.375
	w.Tabs[0].Charts[0].Indicators = []domain.Indicator{
		{ID: domain.ID(), Type: "macd", Parameters: map[string]int{"fast": 12, "slow": 26, "signal": 9}, PaneStretchFactor: &firstFactor},
		{ID: domain.ID(), Type: "macd", Parameters: map[string]int{"fast": 5, "slow": 20, "signal": 5}, PaneStretchFactor: &secondFactor},
		{ID: domain.ID(), Type: "sma", Parameters: map[string]int{"period": 20}},
	}
	ack, err := s.Save(first.Revision, "pane-height-test", 1, w)
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
		t.Fatal("independent MACD pane heights changed on restart", restored)
	}
	exported, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	portable, err := domain.DecodeWorkspace(exported)
	if err != nil || !reflect.DeepEqual(portable, w) {
		t.Fatal("MACD pane heights changed on export", err)
	}
	importFactor := 0.1375
	portable.Tabs[0].Charts[0].Indicators[0].PaneStretchFactor = &importFactor
	imported, err := s.Import(restored.Revision, portable)
	if err != nil || !reflect.DeepEqual(imported.Workspace, portable) {
		t.Fatal("MACD pane heights did not import exactly", err)
	}
	before, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*domain.Workspace){
		"zero": func(w *domain.Workspace) {
			factor := 0.0
			w.Tabs[0].Charts[0].Indicators[0].PaneStretchFactor = &factor
		},
		"too large": func(w *domain.Workspace) {
			factor := 1000.1
			w.Tabs[0].Charts[0].Indicators[0].PaneStretchFactor = &factor
		},
		"not finite": func(w *domain.Workspace) {
			factor := math.Inf(1)
			w.Tabs[0].Charts[0].Indicators[0].PaneStretchFactor = &factor
		},
		"overlay": func(w *domain.Workspace) {
			factor := 0.5
			w.Tabs[0].Charts[0].Indicators[2].PaneStretchFactor = &factor
		},
	} {
		t.Run(name, func(t *testing.T) {
			invalid, err := domain.DecodeWorkspace(before)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&invalid)
			if _, err := s.Import(imported.Revision, invalid); err == nil {
				t.Fatal("invalid pane height import accepted")
			}
			after, err := s.Export()
			if err != nil || string(before) != string(after) {
				t.Fatal("invalid pane height import changed saved workspace", err)
			}
		})
	}
	for _, value := range []string{`null`, `"0.5"`} {
		malformed := strings.Replace(string(before), `"paneStretchFactor": 0.1375`, `"paneStretchFactor": `+value, 1)
		if malformed == string(before) {
			t.Fatal("malformed JSON case did not replace the saved factor")
		}
		if _, err := domain.DecodeWorkspace([]byte(malformed)); err == nil {
			t.Fatal("malformed pane height passed portable import validation", value)
		}
		after, err := s.Export()
		if err != nil || string(before) != string(after) {
			t.Fatal("failed pane height decode changed saved workspace", err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	final := loadTest(t, s)
	if final.Revision != imported.Revision || !reflect.DeepEqual(final.Workspace, portable) {
		t.Fatal("imported MACD pane heights changed after restart", final)
	}
}
