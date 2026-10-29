package storage

import (
	"path/filepath"
	"reflect"
	"testing"

	"cryptodesk/internal/domain"
)

func TestMixedMarketsSaveRestartExportImportAndInvalidPreservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			s.Close()
		}
	})
	first := loadTest(t, s)
	w := first.Workspace
	w.Tabs[0].Grid = domain.Grid{Rows: 2, Columns: 3, RowFractions: []float64{.5, .5}, ColumnFractions: []float64{1.0 / 3, 1.0 / 3, 1.0 / 3}}
	for len(w.Tabs[0].Charts) < 5 {
		w.Tabs[0].Charts = append(w.Tabs[0].Charts, domain.NewChart())
	}
	w.Tabs[0].Charts[1].Market = "perpetual" // Same Binance native symbol as spot.
	w.Tabs[0].Charts[2].Exchange, w.Tabs[0].Charts[2].Market, w.Tabs[0].Charts[2].Instrument = "bybit", "perpetual", "XAGUSDT"
	w.Tabs[0].Charts[3].Exchange, w.Tabs[0].Charts[3].Instrument = "coinbase", "BTC-USD"
	forex := &w.Tabs[0].Charts[4]
	forex.Exchange, forex.Market, forex.Instrument = "dukascopy", "forex", "EURUSD"
	forex.Drawings = []domain.Drawing{{ID: domain.ID(), Type: "horizontal", Exchange: forex.Exchange, Market: forex.Market, Instrument: forex.Instrument, Points: []domain.DrawingPoint{{Time: 1700000000, Price: 1.05}}}}
	ack, err := s.Save(first.Revision, "mixed-markets", 1, w)
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
	restored := loadTest(t, s)
	if restored.Revision != ack.Revision || !reflect.DeepEqual(restored.Workspace, w) {
		t.Fatal("restart changed market identities", restored)
	}
	exported, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	portable, err := domain.DecodeWorkspace(exported)
	if err != nil || !reflect.DeepEqual(portable, w) {
		t.Fatal("export changed market identities", portable, err)
	}
	portable.Tabs[0].Charts[0].Market = "perpetual"
	imported, err := s.Import(restored.Revision, portable)
	if err != nil || !reflect.DeepEqual(imported.Workspace, portable) {
		t.Fatal("perpetual import changed workspace", imported, err)
	}
	before, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	for _, invalidMarket := range []string{"perpetual", "futures", ""} {
		invalid, err := domain.DecodeWorkspace(before)
		if err != nil {
			t.Fatal(err)
		}
		invalid.Tabs[0].Charts[3].Market = invalidMarket // Coinbase supports spot only.
		if _, err := s.Save(imported.Revision, "invalid-market", 1, invalid); err == nil {
			t.Fatal("unsupported market save accepted", invalidMarket)
		}
		if _, err := s.Import(imported.Revision, invalid); err == nil {
			t.Fatal("unsupported market import accepted", invalidMarket)
		}
		after, err := s.Export()
		if err != nil || string(after) != string(before) {
			t.Fatal("rejected market changed saved workspace", err)
		}
	}
}
