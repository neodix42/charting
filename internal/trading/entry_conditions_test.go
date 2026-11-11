package trading

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestEntryConditionsAreLazyPrivateAndPersistAcrossReopen(t *testing.T) {
	profile := t.TempDir()
	store := NewEntryConditions(profile)
	if notes, err := store.Snapshot(); err != nil || len(notes) != 0 {
		t.Fatal(notes, err)
	}
	path := filepath.Join(profile, "trades.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("reading empty annotations created a file", err)
	}
	id := "dukascopy:" + strings.Repeat("a", 32)
	other := "hyperliquid:" + strings.Repeat("a", 32)
	text := "  Пробой уровня <entry> & \"confirmation\" ✓  "
	if saved, err := store.Set(id, text); err != nil || saved != text {
		t.Fatal(saved, err)
	}
	if _, err := store.Set(other, "Different trade"); err != nil {
		t.Fatal(err)
	}
	reopened := NewEntryConditions(profile)
	notes, err := reopened.Snapshot()
	if err != nil || notes[id] != text || notes[other] != "Different trade" {
		t.Fatal(notes, err)
	}
	notes[id] = "Do not mutate saved state"
	if _, err = reopened.Set(other, ""); err != nil {
		t.Fatal(err)
	}
	notes, err = reopened.Snapshot()
	if err != nil || notes[id] != text || len(notes) != 1 {
		t.Fatal(notes, err)
	}
	if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("annotations are not private", info, err)
	}
}

func TestEntryConditionsValidateTextAndIDsWithoutLosingExistingNotes(t *testing.T) {
	store := NewEntryConditions(t.TempDir())
	id := "hyperliquid:" + strings.Repeat("b", 32)
	limit := strings.Repeat("я", 100)
	if _, err := store.Set(id, limit); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{strings.Repeat("a", 101), "two\nlines", "two\rlines", string([]byte{0xff})} {
		if _, err := store.Set(id, text); err == nil {
			t.Fatal("invalid note accepted")
		}
	}
	for _, bad := range []string{"../../trades", "binance:" + strings.Repeat("b", 32), "hyperliquid:123"} {
		if _, err := store.Set(bad, "note"); err == nil {
			t.Fatal("invalid trade ID accepted", bad)
		}
	}
	if notes, err := store.Snapshot(); err != nil || notes[id] != limit || len(notes) != 1 {
		t.Fatal(notes, err)
	}
}

func TestEntryConditionsSerializeConcurrentEditsWithoutDroppingOtherRows(t *testing.T) {
	store := NewEntryConditions(t.TempDir())
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("dukascopy:%032x", i)
			if _, err := store.Set(id, fmt.Sprintf("Condition %d", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	notes, err := store.Snapshot()
	if err != nil || len(notes) != 24 {
		t.Fatal(len(notes), err)
	}
	for i := range 24 {
		if notes[fmt.Sprintf("dukascopy:%032x", i)] != fmt.Sprintf("Condition %d", i) {
			t.Fatal("concurrent edit lost", i)
		}
	}
}

func TestEntryConditionsPreserveMalformedOrFutureFiles(t *testing.T) {
	for _, original := range []string{`not json`, `{"version":3,"trades":{}}`, `{"version":1,"trades":{},"future":"keep me"}`, `{"version":1,"trades":{"external":{"entryCondition":"keep me"}}}`} {
		t.Run(original, func(t *testing.T) {
			profile := t.TempDir()
			path := filepath.Join(profile, "trades.json")
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			store := NewEntryConditions(profile)
			if _, err := store.Snapshot(); err == nil {
				t.Fatal("invalid schema accepted")
			}
			if _, err := store.Set("hyperliquid:"+strings.Repeat("c", 32), "overwrite"); err == nil {
				t.Fatal("invalid file overwritten")
			}
			if after, _ := os.ReadFile(path); string(after) != original {
				t.Fatal("original file changed")
			}
		})
	}
}
