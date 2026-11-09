package storage

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cryptodesk/internal/domain"
)

func TestIndicatorVisibilityPersistsThroughRestartExportImportWithoutSharedMaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadTest(t, s)
	w := loaded.Workspace
	w.Tabs[0].Charts[0].Indicators = []domain.Indicator{
		{ID: domain.ID(), Type: "ichimoku", Parameters: map[string]int{"conversion": 9, "base": 26, "spanB": 52, "displacement": 26}, Visibility: map[string]bool{"conversion": false, "spanA": false, "spanB": false, "lagging": false, "cloud": false}},
		{ID: domain.ID(), Type: "macd", Parameters: map[string]int{"fast": 12, "slow": 26, "signal": 9}, Visibility: map[string]bool{"macd": false, "signal": true, "histogram": false, "zero": false}},
		{ID: domain.ID(), Type: "macd", Parameters: map[string]int{"fast": 3, "slow": 6, "signal": 2}},
	}
	ack, err := s.Save(loaded.Revision, "indicator-visibility", 1, w)
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
		t.Fatal("visibility changed during restart", restored)
	}
	data, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	portable, err := domain.DecodeWorkspace(data)
	if err != nil || !reflect.DeepEqual(portable, w) {
		t.Fatal("visibility changed during export", err)
	}
	// Mutations of a previous Load or caller's Save input cannot alter storage.
	restored.Workspace.Tabs[0].Charts[0].Indicators[0].Visibility["base"] = false
	w.Tabs[0].Charts[0].Indicators[1].Visibility["signal"] = false
	unchanged, err := s.Export()
	if err != nil || string(unchanged) != string(data) {
		t.Fatal("caller-owned visibility map changed saved state", err)
	}
	portable.Tabs[0].Charts[0].Indicators[0].Visibility["base"] = false
	imported, err := s.Import(ack.Revision, portable)
	if err != nil || !reflect.DeepEqual(imported.Workspace, portable) {
		t.Fatal("visibility did not import exactly", err)
	}
	before, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := domain.DecodeWorkspace(before)
	if err != nil {
		t.Fatal(err)
	}
	invalid.Tabs[0].Charts[0].Indicators[0].Visibility["unknown"] = false
	if _, err = s.Save(imported.Revision, "invalid-visibility", 1, invalid); err == nil {
		t.Fatal("invalid visibility save accepted")
	}
	if _, err = s.Import(imported.Revision, invalid); err == nil {
		t.Fatal("invalid visibility import accepted")
	}
	for _, flag := range []string{`null`, `0`, `"false"`, `{}`} {
		input := strings.Replace(string(before), `"conversion": false`, `"conversion": `+flag, 1)
		if input == string(before) {
			t.Fatal("strict import test failed to mutate flag")
		}
		if _, err = domain.DecodeWorkspace([]byte(input)); err == nil {
			t.Fatal("malformed visibility JSON accepted", flag)
		}
	}
	after, err := s.Export()
	if err != nil || string(after) != string(before) {
		t.Fatal("rejected visibility change altered saved state", err)
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
		t.Fatal("imported visibility changed after restart", final)
	}
}
