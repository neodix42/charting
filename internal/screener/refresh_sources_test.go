package screener

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestDukascopyRefreshExcludesHyperliquidHistories(t *testing.T) {
	m, _, _ := refreshManager(t, false)
	name := "hyperliquid-perpetual" + strings.Repeat("a", 64) + "-d1-2024-01-01-2024-01-04.csv"
	path := writeDataFile(t, m, name, refreshCSV)
	if _, err := m.StartRefresh(); err == nil || !strings.Contains(err.Error(), "no Dukascopy") {
		t.Fatalf("Hyperliquid-only folder offered Dukascopy refresh: %v", err)
	}
	writeDataFile(t, m, refreshName, refreshCSV)
	if _, err := m.StartRefresh(); err != nil {
		t.Fatal(err)
	}
	info := waitRefresh(t, m, []string{dataFileID(m.directory, name), dataFileID(m.directory, refreshName)})
	if info.Total != 1 || info.Completed != 1 || len(info.Jobs) != 1 || strings.HasPrefix(info.Jobs[0].Name, "hyperliquid-") {
		t.Fatalf("mixed-source refresh: %+v", info)
	}
	if err := m.refreshFileRequiredProgress(context.Background(), 0, m.directory, RefreshJob{Name: name}, nil, 0, nil, nil); err == nil {
		t.Fatal("direct Dukascopy refresh accepted Hyperliquid history")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != refreshCSV {
		t.Fatalf("Hyperliquid history was modified: %v", err)
	}
}
