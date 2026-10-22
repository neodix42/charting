package storage

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"cryptodesk/internal/domain"
)

func TestDrawingsPersistRestartExportImportAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	w := loaded.Workspace
	w.Tabs[0].Charts[0].Drawings = []domain.Drawing{
		{ID: domain.ID(), Type: "arrowUp", Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Points: []domain.DrawingPoint{{Time: 1700000000, Price: 42123.5}}},
		{ID: domain.ID(), Type: "arrowDown", Exchange: "binance", Market: "perpetual", Instrument: "BTCUSDT", Points: []domain.DrawingPoint{{Time: 1700003600, Price: 42500}}},
		{ID: domain.ID(), Type: "horizontal", Exchange: "coinbase", Market: "spot", Instrument: "ETH-USD", Points: []domain.DrawingPoint{{Time: 1700000000, Price: 2100.25}}},
		{ID: domain.ID(), Type: "trendline", Exchange: "bybit", Market: "perpetual", Instrument: "ETHUSDT", Points: []domain.DrawingPoint{{Time: 1700000000, Price: 2100}, {Time: 1700010000, Price: 2200}}},
	}
	// The current symbol differs from several drawing scopes; hidden drawings
	// must still be committed and restored for a later selection change.
	w.Tabs[0].Charts[0].Instrument = "SOLUSDT"
	ack, err := s.Save(loaded.Revision, "drawing-test", 1, w)
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
	restored, err := s.Load()
	if err != nil || restored.Revision != ack.Revision || !reflect.DeepEqual(w, restored.Workspace) {
		t.Fatal("drawing geometry or scope changed on restart", err)
	}
	before, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	exported, err := domain.DecodeWorkspace(before)
	if err != nil || !reflect.DeepEqual(w, exported) {
		t.Fatal("exported drawings differ from the saved workspace", err)
	}
	var invalid domain.Workspace
	if err = json.Unmarshal(before, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid.Tabs[0].Charts[0].Drawings[3].Points[1] = invalid.Tabs[0].Charts[0].Drawings[3].Points[0]
	if _, err = s.Import(restored.Revision, invalid); err == nil {
		t.Fatal("accepted an invalid trendline import")
	}
	if _, err = s.Save(restored.Revision, "drawing-test", 2, invalid); err == nil {
		t.Fatal("accepted an invalid trendline save")
	}
	after, err := s.Export()
	if err != nil || string(before) != string(after) {
		t.Fatal("invalid drawings changed committed state", err)
	}
	backups, err := s.Backups()
	if err != nil || len(backups) != 0 {
		t.Fatal("invalid drawings created a replacement backup", err)
	}
	exported.Tabs[0].Charts[0].Drawings[0].Points[0] = domain.DrawingPoint{Time: 1700007200, Price: 43000.125}
	imported, err := s.Import(restored.Revision, exported)
	if err != nil || !reflect.DeepEqual(imported.Workspace, exported) {
		t.Fatal("valid drawing import failed", err)
	}
	committed, err := s.Load()
	if err != nil || !reflect.DeepEqual(committed.Workspace, exported) {
		t.Fatal("imported anchors were not committed", err)
	}
	backups, err = s.Backups()
	if err != nil || len(backups) != 1 {
		t.Fatal("drawing import did not preserve the previous workspace backup", err)
	}
	recovered, err := s.Recover(imported.Revision, backups[0].ID)
	if err != nil || !reflect.DeepEqual(recovered.Workspace, w) {
		t.Fatal("backup recovery did not restore the previous drawings", err)
	}
}
