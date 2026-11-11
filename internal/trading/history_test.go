package trading

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRiskUsesActualFillSizeDirectionAndKnownCurrencyFactor(t *testing.T) {
	for _, test := range []struct{ entry, target, size, factor, side, want string }{{"100", "90", "2", "1", "long", "-20"}, {"100", "90", "2", ".9", "short", "18"}, {"1.1003", "1.09", "1000", ".9", "long", "-9.27"}, {"100", "110", ".4", "1", "long", "4"}, {"100", "0", "1", "1", "long", ""}, {"100", "110", "1", "", "long", ""}, {"1e10000000", "110", "1", "1", "long", ""}} {
		if got := Risk(test.entry, test.target, test.size, test.factor, test.side); got != test.want {
			t.Fatalf("%+v: %q", test, got)
		}
	}
}
func TestTradeJournalClaimsArePrivateAtomicAndOriginBounded(t *testing.T) {
	dir := t.TempDir()
	id := "abcdef0123456789abcdef0123456789"
	path := filepath.Join(dir, id+".json")
	value := Trade{ID: "hyperliquid:" + id, Status: "open", BalanceAtOpen: "0"}
	if err := WriteJournal(path, value, true); err != nil {
		t.Fatal(err)
	}
	if err := WriteJournal(path, value, true); err == nil {
		t.Fatal("exclusive claim replayed")
	}
	value.Status = "closed"
	if err := WriteJournal(path, value, false); err != nil {
		t.Fatal(err)
	}
	var got Trade
	if err := ReadJournal(path, &got); err != nil || got.Status != "closed" || got.BalanceAtOpen != "0" {
		t.Fatal(got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "external-order.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	ids, err := JournalIDs(dir)
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatal(ids, err)
	}
	if ValidIntent("../../external") || ValidIntent("123") {
		t.Fatal("unsafe intent accepted")
	}
}

func TestRiskUsesRemainingOpenSizeAndOriginalClosedSize(t *testing.T) {
	row := Trade{EntryPrice: "100", StopLoss: "110", TakeProfit: "90", Size: "2", RemainingSize: "0.5", Direction: "short", Status: "open"}
	ApplyRisk(&row, ".9")
	if row.SLAmount != "-4.5" || row.TPAmount != "4.5" {
		t.Fatal(row)
	}
	row.Status = "closed"
	row.RemainingSize = "0"
	ApplyRisk(&row, ".9")
	if row.SLAmount != "-18" || row.TPAmount != "18" {
		t.Fatal(row)
	}
}
