package candlecache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cryptodesk/internal/domain"
)

func TestCancelledCacheOpenPreservesMissingAndExistingDirectories(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	missing := filepath.Join(t.TempDir(), "not-created")
	if _, err := OpenContext(ctx, missing); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled open was accepted: %v", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled open changed filesystem: %v", err)
	}
	directory := t.TempDir()
	s, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Upsert(testKey(), []domain.Candle{testCandle(0)}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(directory, DatabaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenContext(ctx, directory); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(directory, DatabaseFilename))
	if err != nil || string(before) != string(after) {
		t.Fatalf("cancelled open changed existing data: %v", err)
	}
}
