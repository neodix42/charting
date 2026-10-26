package storage

import (
	"path/filepath"
	"reflect"
	"testing"

	"cryptodesk/internal/domain"
)

func TestFontSizePersistsAcrossRestartAndPortableImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	initial := loadTest(t, s)
	w := initial.Workspace
	size := 20
	w.FontSize = &size
	ack, err := s.Save(initial.Revision, "font-size", 1, w)
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
		t.Fatal("font preference changed after restart")
	}
	exported, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	portable, err := domain.DecodeWorkspace(exported)
	if err != nil || !reflect.DeepEqual(portable, w) {
		t.Fatal("font preference changed on export", err)
	}
	importSize := 18
	portable.FontSize = &importSize
	imported, err := s.Import(restored.Revision, portable)
	if err != nil || !reflect.DeepEqual(imported.Workspace, portable) {
		t.Fatal("import did not restore the appearance preference", err)
	}
	for _, bad := range []int{0, 9, 25} {
		invalid := imported.Workspace
		invalid.FontSize = &bad
		if _, err = s.Save(imported.Revision, "font-size", 2, invalid); err == nil {
			t.Fatal("invalid font size saved", bad)
		}
		if got := loadTest(t, s); !reflect.DeepEqual(got.Workspace, imported.Workspace) || got.Revision != imported.Revision {
			t.Fatal("rejected font preference changed committed workspace")
		}
	}
}
