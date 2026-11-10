package candlestorage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/candlecache"
	"cryptodesk/internal/domain"
)

func configuredDeferredProfile(t *testing.T) (string, string, domain.Candle) {
	t.Helper()
	profile := t.TempDir()
	directory := filepath.Join(profile, "candles")
	m := New(profile, nil)
	if _, err := m.Configure(true, directory); err != nil {
		t.Fatal(err)
	}
	bar := managerBar(0)
	m.Put(managerKey(), []domain.Candle{bar})
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	return profile, directory, bar
}

func waitDeferred(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background cache work did not finish")
	}
}

func TestDeferredCacheIsResponsiveAndPreservesQueuedCorrections(t *testing.T) {
	profile, _, initial := configuredDeferredProfile(t)
	entered, release := make(chan struct{}), make(chan struct{})
	m := newWithOpen(profile, nil, true, func(ctx context.Context, directory string) (*candlecache.Store, error) {
		close(entered)
		select {
		case <-release:
			return candlecache.OpenContext(ctx, directory)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	t.Cleanup(func() { _ = m.Close() })
	waitDeferred(t, entered)
	if pending, errorText := m.StartupState(); !pending || errorText != "" {
		t.Fatalf("pending state missing: %t %q", pending, errorText)
	}
	if info := managerInfo(t, m); !info.Enabled || !info.Pending || info.Error != "" {
		t.Fatalf("startup info blocked or unavailable: %+v", info)
	}
	if bars, err := m.Range(managerKey(), 0, 0, 1000); err != nil || len(bars) != 0 {
		t.Fatalf("unvalidated cache was served: %+v %v", bars, err)
	}
	next, correction := managerBar(1), initial
	correction.Close = 101.75
	m.Put(managerKey(), []domain.Candle{next})
	m.Put(managerKey(), []domain.Candle{correction})
	close(release)
	waitDeferred(t, m.Ready())
	if pending, errorText := m.StartupState(); pending || errorText != "" {
		t.Fatalf("restoration did not settle: %t %q", pending, errorText)
	}
	if got := managerRange(t, m, managerKey()); !reflect.DeepEqual(got, []domain.Candle{correction, next}) {
		t.Fatalf("queued accepted bars or ordering were lost: %+v", got)
	}
	if managerInfo(t, m).Pending {
		t.Fatal("completed cache still pending")
	}
}

func TestDeferredCacheCloseCancelsValidationWithoutPublishingLateStore(t *testing.T) {
	profile, _, _ := configuredDeferredProfile(t)
	entered := make(chan struct{})
	var late *candlecache.Store
	m := newWithOpen(profile, nil, true, func(ctx context.Context, directory string) (*candlecache.Store, error) {
		close(entered)
		<-ctx.Done()
		var err error
		late, err = candlecache.Open(directory)
		return late, err
	})
	waitDeferred(t, entered)
	closed := make(chan struct{})
	go func() { _ = m.Close(); close(closed) }()
	waitDeferred(t, closed)
	waitDeferred(t, m.Ready())
	if m.active.Load() || m.store != nil {
		t.Fatal("cancelled background cache was published")
	}
	if _, err := late.Range(managerKey(), 0, 0, 100); !errors.Is(err, candlecache.ErrClosed) {
		t.Fatalf("late store was not closed: %v", err)
	}
	if _, err := m.Info(); !errors.Is(err, candlecache.ErrClosed) {
		t.Fatalf("closed manager accepted info: %v", err)
	}
}

func TestDeferredCachePendingReadsAndCloseRemainResponsiveWithFullQueue(t *testing.T) {
	profile, _, _ := configuredDeferredProfile(t)
	entered := make(chan struct{})
	m := newWithOpen(profile, nil, true, func(ctx context.Context, _ string) (*candlecache.Store, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	waitDeferred(t, entered)
	for i := 0; i < cap(m.jobs); i++ {
		m.Put(managerKey(), []domain.Candle{managerBar(i)})
	}
	writer := make(chan struct{})
	go func() { m.Put(managerKey(), []domain.Candle{managerBar(200)}); close(writer) }()
	// The full bounded queue may hold the submission mutex. Pending metadata
	// and reads must still avoid both that mutex and the validation worker.
	read := make(chan struct{})
	go func() {
		_, _ = m.Info()
		_, _ = m.Range(managerKey(), 0, 0, 100)
		close(read)
	}()
	waitDeferred(t, read)
	closed := make(chan struct{})
	go func() { _ = m.Close(); close(closed) }()
	waitDeferred(t, closed)
	waitDeferred(t, writer)
}

func TestDeferredCacheFailurePreservesOriginalAndReportsMemoryOnlyState(t *testing.T) {
	profile := t.TempDir()
	directory := filepath.Join(profile, "candles")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("foreign or corrupt database; preserve me")
	path := filepath.Join(directory, candlecache.DatabaseFilename)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := savePreferences(filepath.Join(profile, "candle-storage.json"), preferences{Enabled: true, Directory: directory}); err != nil {
		t.Fatal(err)
	}
	m := NewDeferred(profile, nil)
	defer m.Close()
	waitDeferred(t, m.Ready())
	if pending, message := m.StartupState(); pending || !strings.Contains(message, "original file preserved") {
		t.Fatalf("background error missing: %t %q", pending, message)
	}
	if info := managerInfo(t, m); !info.Enabled || info.Pending || info.Error == "" {
		t.Fatalf("failed validation masqueraded as available: %+v", info)
	}
	m.Put(managerKey(), []domain.Candle{managerBar(0)})
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("invalid cache was replaced: %q %v", got, err)
	}
}

func TestDeferredCacheConfigureQueuesAfterValidationAndAcceptedWrites(t *testing.T) {
	profile, originalDirectory, _ := configuredDeferredProfile(t)
	entered, release := make(chan struct{}), make(chan struct{})
	m := newWithOpen(profile, nil, true, func(ctx context.Context, directory string) (*candlecache.Store, error) {
		close(entered)
		select {
		case <-release:
			return candlecache.OpenContext(ctx, directory)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer m.Close()
	waitDeferred(t, entered)
	next := managerBar(1)
	m.Put(managerKey(), []domain.Candle{next})
	selected := filepath.Join(profile, "selected")
	configured := make(chan error, 1)
	go func() { _, err := m.Configure(true, selected); configured <- err }()
	close(release)
	if err := <-configured; err != nil {
		t.Fatal(err)
	}
	if info := managerInfo(t, m); info.Directory != selected || info.Pending {
		t.Fatalf("late startup check replaced configured destination: %+v", info)
	}
	if bars := managerRange(t, m, managerKey()); len(bars) != 0 {
		t.Fatalf("pending bars crossed a folder boundary: %+v", bars)
	}
	old, err := candlecache.Open(originalDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if bars, err := old.Range(managerKey(), next.Time, next.Time, 10); err != nil || !reflect.DeepEqual(bars, []domain.Candle{next}) {
		t.Fatalf("accepted pending write did not stay in original folder: %+v %v", bars, err)
	}
}
