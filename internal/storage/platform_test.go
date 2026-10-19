package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestEscapedDatabaseFilenameTargetsTheOriginalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile # 25% & + space", "workspace ü.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadTest(t, s)
	loaded.Workspace.Tabs[0].Name = "escaped path"
	if _, err := s.Save(loaded.Revision, "escaped-path", 1, loaded.Workspace); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("SQLite did not write the original filename: %v", err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := loadTest(t, s); got.Workspace.Tabs[0].Name != "escaped path" {
		t.Fatal("saved state opened under another filename")
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatal("the stable lockfile was removed", err)
	}
}

func TestWorkspaceLockExcludesAnotherProcessWithoutWaiting(t *testing.T) {
	if path := os.Getenv("CRYPTODESK_STORAGE_LOCK_HELPER"); path != "" {
		started := time.Now()
		store, err := Open(path)
		if store != nil {
			store.Close()
		}
		if !errors.Is(err, ErrLocked) || time.Since(started) > 2*time.Second {
			t.Fatalf("contender did not fail immediately with ErrLocked: %v", err)
		}
		fmt.Println("EXCLUSIVE LOCK PRESERVED")
		return
	}
	store, path := openTest(t)
	loadTest(t, store)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkspaceLockExcludesAnotherProcessWithoutWaiting$")
	child.Env = append(os.Environ(), "CRYPTODESK_STORAGE_LOCK_HELPER="+path)
	output, err := child.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("EXCLUSIVE LOCK PRESERVED")) {
		t.Fatalf("another process bypassed or blocked on the workspace lock: %v\n%s", err, output)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal("kernel lock was not released on close", err)
	}
	reopened.Close()
}
