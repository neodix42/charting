package trading

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveTradeScreenPreservesOtherImagesAndEntryCondition(t *testing.T) {
	profile := t.TempDir()
	store := NewEntryConditions(profile)
	trade := Trade{ID: "dukascopy:" + strings.Repeat("a", 32), Provider: "dukascopy"}
	_, url := screenFixture(t)
	if _, err := store.Set(trade.ID, "Preserved condition"); err != nil {
		t.Fatal(err)
	}
	first, err := store.AddScreen(trade, "first.png", url)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddScreen(trade, "second.png", url)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RemoveScreen("hyperliquid:"+strings.Repeat("b", 32), first.ID); err == nil {
		t.Fatal("image removal crossed the trade boundary")
	}
	if after, _ := os.ReadFile(store.path); !bytes.Equal(before, after) {
		t.Fatal("rejected removal changed annotations")
	}
	if removed, err := store.RemoveScreen(trade.ID, first.ID); err != nil || !removed.Removed || removed.Warning != "" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(profile, "trade-screens", "dukascopy", strings.Repeat("a", 32), first.ID+".png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("removed original remained on disk", err)
	}
	store = NewEntryConditions(profile)
	metadata, err := store.MetadataSnapshot()
	if err != nil || metadata[trade.ID].EntryCondition != "Preserved condition" || len(metadata[trade.ID].Screens) != 1 || metadata[trade.ID].Screens[0].ID != second.ID {
		t.Fatal("removal changed another image/note or did not survive reopen", metadata, err)
	}
	if _, err := store.ReadScreen(trade.ID, first.ID); err == nil {
		t.Fatal("removed image is still reachable")
	}
	if original, err := store.ReadScreen(trade.ID, second.ID); err != nil || original != url {
		t.Fatal("other image was changed", err)
	}
	if removed, err := store.RemoveScreen(trade.ID, second.ID); err != nil || !removed.Removed || removed.Warning != "" {
		t.Fatal(err)
	}
	metadata, err = store.MetadataSnapshot()
	if err != nil || metadata[trade.ID].EntryCondition != "Preserved condition" || len(metadata[trade.ID].Screens) != 0 {
		t.Fatal("last image removal discarded condition", metadata, err)
	}
}

func TestRemoveTradeScreenFailedMetadataCommitKeepsOriginalAndMissingImageCanBeRemoved(t *testing.T) {
	store := NewEntryConditions(t.TempDir())
	trade := Trade{ID: "hyperliquid:" + strings.Repeat("c", 32), Provider: "hyperliquid"}
	_, url := screenFixture(t)
	screen, err := store.AddScreen(trade, "image.png", url)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	failed := errors.New("simulated durable metadata write failure")
	if removed, err := store.removeScreen(trade.ID, screen.ID, func(tradeAnnotations) error { return failed }, os.Remove); !errors.Is(err, failed) || removed.Removed {
		t.Fatal("write failure was hidden", err)
	}
	if after, _ := os.ReadFile(store.path); !bytes.Equal(before, after) {
		t.Fatal("failed metadata write changed annotations")
	}
	if original, err := store.ReadScreen(trade.ID, screen.ID); err != nil || original != url {
		t.Fatal("original deleted before metadata commit succeeded", err)
	}
	directory, err := store.screenDirectory(trade.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, screen.ID+".png")); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.RemoveScreen(trade.ID, screen.ID); err != nil || !removed.Removed {
		t.Fatal("broken image association could not be removed", err)
	}
	metadata, err := store.MetadataSnapshot()
	if _, exists := metadata[trade.ID]; err != nil || exists {
		t.Fatal("empty image-only annotation remained", metadata, err)
	}
}

func TestRemoveTradeScreenReportsCommittedRemovalWhenFileCleanupFails(t *testing.T) {
	store := NewEntryConditions(t.TempDir())
	trade := Trade{ID: "hyperliquid:" + strings.Repeat("e", 32), Provider: "hyperliquid"}
	_, url := screenFixture(t)
	screen, err := store.AddScreen(trade, "image.png", url)
	if err != nil {
		t.Fatal(err)
	}
	failed := errors.New("simulated file cleanup failure")
	result, err := store.removeScreen(trade.ID, screen.ID, func(data tradeAnnotations) error { return WriteJournal(store.path, data, false) }, func(string) error { return failed })
	if err != nil || !result.Removed || !strings.Contains(result.Warning, failed.Error()) {
		t.Fatal("committed removal was reported as failure or cleanup warning was lost", result, err)
	}
	if _, err := store.ReadScreen(trade.ID, screen.ID); err == nil {
		t.Fatal("removed association stayed accessible after cleanup failure")
	}
	metadata, err := store.MetadataSnapshot()
	if err != nil || len(metadata) != 0 {
		t.Fatal("committed removal was rolled back after file cleanup failed", metadata, err)
	}
	directory, err := store.screenDirectory(trade.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, screen.ID+".png")); err != nil {
		t.Fatal("cleanup failure must preserve the unreachable original", err)
	}
}
