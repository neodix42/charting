// Package storage owns durable workspace transactions, migrations and process locking.
// It intentionally has no dependency on Wails or the network.
package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"cryptodesk/internal/domain"
	_ "modernc.org/sqlite"
)

const databaseVersion = 1
const maxBackups = 5

var (
	ErrConflict = errors.New("conflict: workspace revision or edit sequence is stale; reload before saving")
	ErrLocked   = errors.New("CryptoDesk is already running for this workspace; close the other instance first")
	ErrClosed   = errors.New("saveFailed: workspace storage is closed")
)

type Loaded struct {
	Workspace domain.Workspace `json:"workspace"`
	Revision  int64            `json:"revision"`
	Recovery  string           `json:"recovery"`
}
type Ack struct {
	Revision             int64 `json:"revision"`
	AcceptedEditSequence int64 `json:"acceptedEditSequence"`
}
type Backup struct {
	ID        int64  `json:"id"`
	Revision  int64  `json:"revision"`
	CreatedAt string `json:"createdAt"`
	Reason    string `json:"reason"`
}

// RecoveryError means a saved document exists and must never be replaced implicitly.
// The Store remains usable for explicit backup recovery, import or reset.
type RecoveryError struct{ Reason string }

func (e *RecoveryError) Error() string {
	return "Saved workspace needs recovery: " + e.Reason + ". Original data has been preserved."
}

type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	lock   *os.File
	path   string
	closed bool
}

func DefaultPath() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "cryptodesk", "workspace.sqlite"), nil
}

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, fmt.Errorf("open workspace directory: %w", err)
	}
	lock, err := os.OpenFile(abs+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, ErrLocked
	}
	fail := func(e error) (*Store, error) {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
		return nil, e
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fail(err)
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return fail(err)
	}
	if err = f.Close(); err != nil {
		return fail(err)
	}
	source := (&url.URL{Scheme: "file", Path: abs}).String()
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return fail(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	dbFail := func(e error) (*Store, error) {
		db.Close()
		return fail(fmt.Errorf("open workspace database (original file preserved): %w", e))
	}
	// One connection and a Store mutex serialize all writers; FULL applies to this
	// connection, and WAL commits remain recoverable with the companion files.
	for _, stmt := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON"} {
		if _, err = db.Exec(stmt); err != nil {
			return dbFail(err)
		}
	}
	if err = applyMigrations(db, []string{migrationOne}); err != nil {
		return dbFail(err)
	}
	s := &Store{db: db, lock: lock, path: abs}
	s.privateFiles()
	return s, nil
}

const migrationOne = `
CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS workspace_state(
 id INTEGER PRIMARY KEY CHECK(id=1), schema_version INTEGER NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>=1), json_document TEXT NOT NULL, saved_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workspace_backups(
 id INTEGER PRIMARY KEY AUTOINCREMENT, revision INTEGER NOT NULL,
 json_document TEXT NOT NULL, created_at TEXT NOT NULL, reason TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workspace_sessions(
 session_id TEXT PRIMARY KEY, edit_sequence INTEGER NOT NULL, revision INTEGER NOT NULL
);`

// All pending migrations share one transaction. Even a failure after DDL restores
// the prior schema and user_version instead of exposing a half-upgraded database.
func applyMigrations(db *sql.DB, migrations []string) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > databaseVersion {
		return fmt.Errorf("database version %d is newer than supported version %d", version, databaseVersion)
	}
	if version >= len(migrations) {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// An upgrade from a pre-versioned database may already contain user state.
	// Keep its document in the same transaction before any migration modifies it.
	var hasState int
	if err = tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='workspace_state'").Scan(&hasState); err != nil {
		return err
	}
	if hasState != 0 {
		revision, document, _, readErr := readCurrent(tx)
		if readErr != nil && readErr != sql.ErrNoRows {
			return readErr
		}
		if readErr == nil {
			if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS workspace_backups(id INTEGER PRIMARY KEY AUTOINCREMENT, revision INTEGER NOT NULL, json_document TEXT NOT NULL, created_at TEXT NOT NULL, reason TEXT NOT NULL)`); err != nil {
				return err
			}
			if err = addBackup(tx, revision, document, "before migration"); err != nil {
				return err
			}
		}
	}
	for i := version; i < len(migrations); i++ {
		if _, err = tx.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err = tx.Exec("INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", i+1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if _, err = tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", i+1)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) privateFiles() {
	for _, suffix := range []string{"", ".lock", "-wal", "-shm"} {
		_ = os.Chmod(s.path+suffix, 0600)
	}
}
func (s *Store) check() error {
	if s.closed {
		return ErrClosed
	}
	return nil
}

func readCurrent(tx *sql.Tx) (int64, string, int, error) {
	var revision int64
	var document string
	var version int
	err := tx.QueryRow("SELECT revision,json_document,schema_version FROM workspace_state WHERE id=1").Scan(&revision, &document, &version)
	return revision, document, version, err
}
func loadDocument(revision int64, document string, version int) (Loaded, error) {
	if version != domain.SchemaVersion {
		e := &RecoveryError{Reason: fmt.Sprintf("unsupported saved schemaVersion %d", version)}
		return Loaded{Revision: revision, Recovery: e.Error()}, e
	}
	w, err := domain.DecodeWorkspace([]byte(document))
	if err != nil {
		e := &RecoveryError{Reason: err.Error()}
		return Loaded{Revision: revision, Recovery: e.Error()}, e
	}
	return Loaded{Workspace: w, Revision: revision}, nil
}
func (s *Store) Load() (Loaded, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return Loaded{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Loaded{}, err
	}
	defer tx.Rollback()
	rev, doc, ver, err := readCurrent(tx)
	if err == sql.ErrNoRows {
		// The only automatic first-run path is an absent workspace row.
		w := domain.NewWorkspace()
		data, _ := json.Marshal(w)
		if _, err = tx.Exec("INSERT INTO workspace_state(id,schema_version,revision,json_document,saved_at) VALUES(1,?,?,?,?)", domain.SchemaVersion, 1, string(data), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return Loaded{}, err
		}
		if err = tx.Commit(); err != nil {
			return Loaded{}, err
		}
		s.privateFiles()
		return Loaded{Workspace: w, Revision: 1}, nil
	}
	if err != nil {
		return Loaded{}, err
	}
	return loadDocument(rev, doc, ver)
}

func (s *Store) Save(expected int64, session string, seq int64, w domain.Workspace) (Ack, error) {
	if err := domain.Validate(w); err != nil {
		return Ack{}, err
	}
	if strings.TrimSpace(session) == "" || len(session) > 128 || seq < 1 {
		return Ack{}, errors.New("invalidWorkspace: save requires a session ID and positive edit sequence")
	}
	data, err := json.Marshal(w)
	if err != nil {
		return Ack{}, err
	}
	if len(data) > domain.MaxImportBytes {
		return Ack{}, errors.New("invalidWorkspace: workspace exceeds 5 MiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.check(); err != nil {
		return Ack{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Ack{}, err
	}
	defer tx.Rollback()
	rev, doc, ver, err := readCurrent(tx)
	if err != nil {
		return Ack{}, err
	}
	if expected != rev {
		return Ack{}, ErrConflict
	}
	// A save from a frontend default may not overwrite corrupt/future data.
	if _, err = loadDocument(rev, doc, ver); err != nil {
		return Ack{}, err
	}
	var last int64
	err = tx.QueryRow("SELECT edit_sequence FROM workspace_sessions WHERE session_id=?", session).Scan(&last)
	if err != nil && err != sql.ErrNoRows {
		return Ack{}, err
	}
	if seq <= last {
		return Ack{}, ErrConflict
	}
	rev++
	if _, err = tx.Exec("UPDATE workspace_state SET schema_version=?,revision=?,json_document=?,saved_at=? WHERE id=1", domain.SchemaVersion, rev, string(data), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return Ack{}, fmt.Errorf("saveFailed: %w", err)
	}
	if _, err = tx.Exec("INSERT INTO workspace_sessions(session_id,edit_sequence,revision) VALUES(?,?,?) ON CONFLICT(session_id) DO UPDATE SET edit_sequence=excluded.edit_sequence,revision=excluded.revision", session, seq, rev); err != nil {
		return Ack{}, err
	}
	// Persist bounded session ordering information across normal app restarts.
	if _, err = tx.Exec("DELETE FROM workspace_sessions WHERE session_id NOT IN (SELECT session_id FROM workspace_sessions ORDER BY revision DESC LIMIT 128)"); err != nil {
		return Ack{}, err
	}
	if err = tx.Commit(); err != nil {
		return Ack{}, fmt.Errorf("saveFailed: %w", err)
	}
	s.privateFiles()
	return Ack{Revision: rev, AcceptedEditSequence: seq}, nil
}
func (s *Store) Export() ([]byte, error) {
	loaded, err := s.Load()
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(loaded.Workspace, "", "  ")
}
func (s *Store) Import(expected int64, w domain.Workspace) (Loaded, error) {
	return s.replace(expected, w, "before import")
}
func (s *Store) Reset(expected int64) (Loaded, error) {
	return s.replace(expected, domain.NewWorkspace(), "before reset")
}
func (s *Store) replace(expected int64, w domain.Workspace, reason string) (Loaded, error) {
	if err := domain.Validate(w); err != nil {
		return Loaded{}, err
	}
	data, err := json.Marshal(w)
	if err != nil {
		return Loaded{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.check(); err != nil {
		return Loaded{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Loaded{}, err
	}
	defer tx.Rollback()
	rev, doc, _, err := readCurrent(tx)
	if err != nil {
		return Loaded{}, err
	}
	if rev != expected {
		return Loaded{}, ErrConflict
	}
	if err = addBackup(tx, rev, doc, reason); err != nil {
		return Loaded{}, err
	}
	rev++
	if _, err = tx.Exec("UPDATE workspace_state SET schema_version=?,revision=?,json_document=?,saved_at=? WHERE id=1", domain.SchemaVersion, rev, string(data), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return Loaded{}, err
	}
	if err = tx.Commit(); err != nil {
		return Loaded{}, err
	}
	s.privateFiles()
	return Loaded{Workspace: w, Revision: rev}, nil
}
func addBackup(tx *sql.Tx, revision int64, document, reason string) error {
	if _, err := tx.Exec("INSERT INTO workspace_backups(revision,json_document,created_at,reason) VALUES(?,?,?,?)", revision, document, time.Now().UTC().Format(time.RFC3339Nano), reason); err != nil {
		return err
	}
	_, err := tx.Exec("DELETE FROM workspace_backups WHERE id NOT IN (SELECT id FROM workspace_backups ORDER BY id DESC LIMIT ?)", maxBackups)
	return err
}
func (s *Store) Backups() ([]Backup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT id,revision,created_at,reason,json_document FROM workspace_backups ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Backup{}
	for rows.Next() {
		var b Backup
		var doc string
		if err = rows.Scan(&b.ID, &b.Revision, &b.CreatedAt, &b.Reason, &doc); err != nil {
			return nil, err
		}
		if _, err = domain.DecodeWorkspace([]byte(doc)); err == nil {
			result = append(result, b)
		}
	}
	return result, rows.Err()
}
func (s *Store) Recover(expected int64, backupID int64) (Loaded, error) {
	// Read a consistent exported document; replacement takes a second transaction
	// with the caller's expected revision, so concurrent saves are never lost.
	s.mu.Lock()
	if err := s.check(); err != nil {
		s.mu.Unlock()
		return Loaded{}, err
	}
	var doc string
	err := s.db.QueryRow("SELECT json_document FROM workspace_backups WHERE id=?", backupID).Scan(&doc)
	s.mu.Unlock()
	if err != nil {
		return Loaded{}, err
	}
	w, err := domain.DecodeWorkspace([]byte(doc))
	if err != nil {
		return Loaded{}, err
	}
	return s.replace(expected, w, "before recovery")
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.db.Close()
	unlockErr := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	closeErr := s.lock.Close()
	return errors.Join(err, unlockErr, closeErr)
}
