package storage

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cryptodesk/internal/domain"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private", "workspace.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}
func loadTest(t *testing.T, s *Store) Loaded {
	t.Helper()
	l, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func fixture(t *testing.T, file string) domain.Workspace {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../examples", file))
	if err != nil {
		t.Fatal(err)
	}
	w, err := domain.DecodeWorkspace(b)
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func TestFirstRunAndExistingSmallWorkspace(t *testing.T) {
	s, path := openTest(t)
	first := loadTest(t, s)
	if first.Revision != 1 || len(first.Workspace.Tabs) != 1 || len(first.Workspace.Tabs[0].Charts) != 1 {
		t.Fatal("wrong first run", first)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	existing := loadTest(t, s)
	if !reflect.DeepEqual(first, existing) {
		t.Fatal("existing one-chart state replaced")
	}
	w := fixture(t, "intermediate-two-tabs.json")
	ack, err := s.Save(existing.Revision, "session", 1, w)
	if err != nil {
		t.Fatal(err)
	}
	if ack.AcceptedEditSequence != 1 || ack.Revision != existing.Revision+1 {
		t.Fatal("bad ack", ack)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actual := loadTest(t, s)
	if !reflect.DeepEqual(actual.Workspace, w) || len(actual.Workspace.Tabs) != 2 || len(actual.Workspace.Tabs[0].Charts) != 3 || len(actual.Workspace.Tabs[1].Charts) != 1 {
		t.Fatal("small workspace was expanded or changed")
	}
}
func TestAllExamplesDatabaseRoundtrip(t *testing.T) {
	for _, file := range []string{"starter-one-tab-one-chart.json", "intermediate-two-tabs.json", "capacity-20-tabs-180-charts.json"} {
		t.Run(file, func(t *testing.T) {
			s, path := openTest(t)
			l := loadTest(t, s)
			w := fixture(t, file)
			ack, err := s.Save(l.Revision, "test", 1, w)
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			actual := loadTest(t, s)
			if actual.Revision != ack.Revision || !reflect.DeepEqual(actual.Workspace, w) {
				t.Fatal("database roundtrip changed workspace")
			}
			out, err := s.Export()
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := domain.DecodeWorkspace(out)
			if err != nil || !reflect.DeepEqual(w, decoded) {
				t.Fatal("export changed portable workspace", err)
			}
		})
	}
}
func TestOrderingConflictsAndConcurrentSaves(t *testing.T) {
	s, path := openTest(t)
	l := loadTest(t, s)
	w := l.Workspace
	w.Tabs[0].Name = "newest"
	ack, err := s.Save(l.Revision, "same-session", 2, w)
	if err != nil {
		t.Fatal(err)
	}
	w.Tabs[0].Name = "stale"
	for _, request := range []struct{ rev, seq int64 }{{l.Revision, 3}, {ack.Revision, 1}, {ack.Revision, 2}} {
		if _, err = s.Save(request.rev, "same-session", request.seq, w); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale request accepted: %v", err)
		}
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Save(ack.Revision, "same-session", 1, w); !errors.Is(err, ErrConflict) {
		t.Fatal("sequence ordering lost on restart", err)
	}
	l = loadTest(t, s)
	if l.Workspace.Tabs[0].Name != "newest" {
		t.Fatal("stale save overwrote current state")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, session := range []string{"a", "b"} {
		wg.Go(func() { _, err := s.Save(l.Revision, session, 1, l.Workspace); results <- err })
	}
	wg.Wait()
	close(results)
	accepted, conflicts := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatal("revision guard lost", accepted, conflicts)
	}
}
func TestInvalidImportsPreserveCurrentDocument(t *testing.T) {
	s, _ := openTest(t)
	l := loadTest(t, s)
	before, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*domain.Workspace){
		"zero tabs": func(w *domain.Workspace) { w.Tabs = nil },
		"21 tabs": func(w *domain.Workspace) {
			for len(w.Tabs) < 21 {
				w.Tabs = append(w.Tabs, domain.NewTab("x"))
			}
		},
		"zero charts": func(w *domain.Workspace) { w.Tabs[0].Charts = nil },
		"10 charts": func(w *domain.Workspace) {
			for len(w.Tabs[0].Charts) < 10 {
				w.Tabs[0].Charts = append(w.Tabs[0].Charts, domain.NewChart())
			}
		},
		"duplicate ids":  func(w *domain.Workspace) { w.Tabs[0].Charts[0].ID = w.ID },
		"future version": func(w *domain.Workspace) { w.SchemaVersion++ },
	} {
		t.Run(name, func(t *testing.T) {
			b, _ := json.Marshal(l.Workspace)
			w, _ := domain.DecodeWorkspace(b)
			mutate(&w)
			if _, err := s.Import(l.Revision, w); err == nil {
				t.Fatal("invalid import accepted")
			}
			after, _ := s.Export()
			if string(before) != string(after) {
				t.Fatal("invalid import changed saved document")
			}
		})
	}
	malicious := strings.Replace(string(before), `"market": "spot"`, `"market": "spot", "endpoint": "http://unsafe.invalid"`, 1)
	if _, err = domain.DecodeWorkspace([]byte(malicious)); err == nil {
		t.Fatal("arbitrary endpoint accepted")
	}
	after, _ := s.Export()
	if string(before) != string(after) {
		t.Fatal("failed decoding changed current document")
	}
}
func TestBackupsResetRecoveryAndBounds(t *testing.T) {
	s, _ := openTest(t)
	original := loadTest(t, s)
	imported, err := s.Import(original.Revision, fixture(t, "intermediate-two-tabs.json"))
	if err != nil {
		t.Fatal(err)
	}
	backups, err := s.Backups()
	if err != nil || len(backups) != 1 || backups[0].Reason != "before import" {
		t.Fatal("import backup missing", backups, err)
	}
	recovered, err := s.Recover(imported.Revision, backups[0].ID)
	if err != nil || !reflect.DeepEqual(recovered.Workspace, original.Workspace) {
		t.Fatal("backup not recovered", err)
	}
	reset, err := s.Reset(recovered.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(reset.Workspace.Tabs) != 1 || len(reset.Workspace.Tabs[0].Charts) != 1 || reset.Workspace.ID == original.Workspace.ID {
		t.Fatal("reset must create new one-chart workspace")
	}
	for i := 0; i < 8; i++ {
		reset, err = s.Reset(reset.Revision)
		if err != nil {
			t.Fatal(err)
		}
	}
	backups, err = s.Backups()
	if err != nil || len(backups) != maxBackups {
		t.Fatal("backups not bounded", len(backups), err)
	}
	if _, err = s.Recover(reset.Revision-1, backups[0].ID); !errors.Is(err, ErrConflict) {
		t.Fatal("stale recovery accepted", err)
	}
}
func TestCorruptAndFutureDocumentsAreNeverDefaulted(t *testing.T) {
	for name, document := range map[string]string{"corrupt": "{broken", "future": `{"schemaVersion":2}`} {
		t.Run(name, func(t *testing.T) {
			s, _ := openTest(t)
			l := loadTest(t, s)
			if _, err := s.Import(l.Revision, l.Workspace); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("UPDATE workspace_state SET json_document=?,schema_version=? WHERE id=1", document, map[string]int{"corrupt": 1, "future": 2}[name]); err != nil {
				t.Fatal(err)
			}
			actual, err := s.Load()
			var recovery *RecoveryError
			if !errors.As(err, &recovery) || actual.Recovery == "" || actual.Revision != 2 {
				t.Fatal("saved error silently defaulted", actual, err)
			}
			if _, err = s.Save(2, "default", 1, domain.NewWorkspace()); !errors.As(err, &recovery) {
				t.Fatal("save overwrote invalid state", err)
			}
			var preserved string
			if err = s.db.QueryRow("SELECT json_document FROM workspace_state WHERE id=1").Scan(&preserved); err != nil || preserved != document {
				t.Fatal("source changed", preserved, err)
			}
			backups, err := s.Backups()
			if err != nil || len(backups) != 1 {
				t.Fatal("valid recovery backup missing", err)
			}
			fixed, err := s.Recover(actual.Revision, backups[0].ID)
			if err != nil || !reflect.DeepEqual(fixed.Workspace, l.Workspace) {
				t.Fatal("explicit backup recovery failed", err)
			}
		})
	}
}
func TestPhysicalCorruptionAndFutureDatabasePreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite")
	source := []byte("not a SQLite database\nuser source\n")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("physical corruption accepted")
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != string(source) {
		t.Fatal("corrupt file overwritten", err)
	}
	future := filepath.Join(t.TempDir(), "future.sqlite")
	db, err := sql.Open("sqlite", future)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=99; CREATE TABLE user_source(value TEXT); INSERT INTO user_source VALUES('precious')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(future); err == nil {
		s.Close()
		t.Fatal("future database accepted")
	}
	db, err = sql.Open("sqlite", future)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err = db.QueryRow("SELECT value FROM user_source").Scan(&value); err != nil || value != "precious" {
		t.Fatal("future source changed", err)
	}
}
func TestSingleInstanceAndPrivateDurableConfiguration(t *testing.T) {
	s, path := openTest(t)
	loadTest(t, s)
	if second, err := Open(path); !errors.Is(err, ErrLocked) {
		if second != nil {
			second.Close()
		}
		t.Fatal("second instance admitted", err)
	}
	for _, name := range []string{path, path + ".lock", path + "-wal", path + "-shm"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatal("workspace file readable by other users", name, info.Mode())
		}
	}
	var journal string
	var syncMode int
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatal("WAL not configured", journal, err)
	}
	if err := s.db.QueryRow("PRAGMA synchronous").Scan(&syncMode); err != nil || syncMode != 2 {
		t.Fatal("FULL durability not configured", syncMode, err)
	}
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal("instance lock not released", err)
	}
	defer reopened.Close()
}
func TestFailedMigrationRollsBackDDLAndVersion(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migrate.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = applyMigrations(db, []string{migrationOne, "CREATE TABLE partial(value TEXT); THIS IS INVALID SQL"}); err == nil {
		t.Fatal("bad migration succeeded")
	}
	var version, count int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
		t.Fatal("migration version escaped rollback", version, err)
	}
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('workspace_state','partial','schema_migrations')").Scan(&count); err != nil || count != 0 {
		t.Fatal("migration DDL escaped rollback", count, err)
	}
	if err = applyMigrations(db, []string{migrationOne}); err != nil {
		t.Fatal("retry after rollback failed", err)
	}
}
func TestBusyWriteFailsWithoutAcknowledgingOrChangingData(t *testing.T) {
	s, path := openTest(t)
	l := loadTest(t, s)
	if _, err := s.db.Exec("PRAGMA busy_timeout=20"); err != nil {
		t.Fatal(err)
	}
	blocker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	if _, err = blocker.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	w := l.Workspace
	w.Tabs[0].Name = "unsaved"
	if _, err = s.Save(l.Revision, "busy", 1, w); err == nil {
		t.Fatal("locked writer acknowledged")
	}
	if _, err = blocker.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	actual := loadTest(t, s)
	if actual.Revision != l.Revision || actual.Workspace.Tabs[0].Name == "unsaved" {
		t.Fatal("failed save changed state")
	}
}
func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path, err := DefaultPath()
	if err != nil || path != filepath.Join(os.Getenv("XDG_DATA_HOME"), "cryptodesk", "workspace.sqlite") {
		t.Fatal(path, err)
	}
}
func TestAcknowledgedCommitSurvivesKill(t *testing.T) {
	if path := os.Getenv("CRYPTODESK_STORAGE_KILL_HELPER"); path != "" {
		s, err := Open(path)
		if err != nil {
			panic(err)
		}
		l, err := s.Load()
		if err != nil {
			panic(err)
		}
		l.Workspace.Tabs[0].Name = "acknowledged before kill"
		ack, err := s.Save(l.Revision, "kill-helper", 1, l.Workspace)
		if err != nil {
			panic(err)
		}
		fmt.Printf("ACK %d\n", ack.Revision)
		time.Sleep(time.Hour)
		return
	}
	path := filepath.Join(t.TempDir(), "crash", "workspace.sqlite")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAcknowledgedCommitSurvivesKill$")
	cmd.Env = append(os.Environ(), "CRYPTODESK_STORAGE_KILL_HELPER="+path)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	scanner := bufio.NewScanner(pipe)
	if !scanner.Scan() || scanner.Text() != "ACK 2" {
		t.Fatal("child never acknowledged commit", scanner.Text(), scanner.Err())
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("child was not abruptly terminated")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actual := loadTest(t, s)
	if actual.Revision != 2 || actual.Workspace.Tabs[0].Name != "acknowledged before kill" {
		t.Fatal("acknowledged commit lost after kill")
	}
}

func TestPreVersionedWorkspaceBackedUpDuringMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(migrationOne); err != nil {
		t.Fatal(err)
	}
	w := domain.NewWorkspace()
	w.Tabs[0].Name = "saved before migrations"
	raw, _ := json.Marshal(w)
	if _, err = db.Exec("INSERT INTO workspace_state(id,schema_version,revision,json_document,saved_at) VALUES(1,1,7,?,?)", string(raw), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	actual := loadTest(t, store)
	if actual.Revision != 7 || !reflect.DeepEqual(actual.Workspace, w) {
		t.Fatal("migration changed original document")
	}
	backups, err := store.Backups()
	if err != nil || len(backups) != 1 || backups[0].Reason != "before migration" || backups[0].Revision != 7 {
		t.Fatal("pre-migration document was not backed up", backups, err)
	}
}
