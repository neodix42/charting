// Package candlecache stores optional, provider-separated downloaded candle data.
// It has no dependency on the desktop toolkit or network adapters. Callers must
// pass only genuine provider data after applying their live-update ordering rules.
package candlecache

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"cryptodesk/internal/domain"
	_ "modernc.org/sqlite"
)

const (
	DatabaseFilename = "candles.sqlite3"
	MaxRangeLimit    = 10000
	defaultLimit     = 2000
	maxBatch         = 10000
	databaseVersion  = 1
	applicationID    = 0x43524343 // CRCC, independent of the workspace database.
	lastTimestamp    = int64(32503680000)
)

var ErrClosed = errors.New("candle data storage is closed")

const schema = `CREATE TABLE candle_cache_metadata (
 id INTEGER PRIMARY KEY CHECK(id=1), schema_checksum TEXT NOT NULL
);
CREATE TABLE candles (
 exchange TEXT NOT NULL, market TEXT NOT NULL, instrument TEXT NOT NULL,
 timeframe TEXT NOT NULL, time INTEGER NOT NULL,
 open TEXT NOT NULL, high TEXT NOT NULL, low TEXT NOT NULL,
 close TEXT NOT NULL, volume TEXT NOT NULL,
 closed INTEGER NOT NULL CHECK(closed IN (0,1)), quality TEXT NOT NULL,
 source_revision INTEGER NOT NULL CHECK(source_revision>=0), checksum TEXT NOT NULL,
 PRIMARY KEY(exchange,market,instrument,timeframe,time)
) WITHOUT ROWID;`

// Store serializes bounded reads, atomic writes and shutdown on one connection.
// The database uses WAL and synchronous=FULL; successful Upsert calls are durable.
type Store struct {
	mu     sync.Mutex
	db     *sql.DB
	path   string
	closed bool
}

// NormalizeDirectory resolves a nonempty user-selected path without expanding
// environment variables or tilde prefixes. It never creates a directory.
func NormalizeDirectory(directory string) (string, error) {
	if strings.TrimSpace(directory) == "" || strings.ContainsRune(directory, 0) {
		return "", errors.New("select a nonempty candle data folder")
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve candle data folder: %w", err)
	}
	return filepath.Clean(abs), nil
}

// Open opens only this package's own database. Existing foreign, corrupt or newer
// databases are rejected without reinitializing them or changing their contents.
// Existing directories and unrelated files keep their permissions and contents.
func Open(directory string) (*Store, error) {
	directory, err := NormalizeDirectory(directory)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(directory); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("candle data folder must be a real directory")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect candle data folder: %w", statErr)
	} else if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create candle data folder: %w", err)
	}
	path := filepath.Join(directory, DatabaseFilename)
	_, databaseStatErr := os.Lstat(path)
	// Never follow database/sidecar symlinks or use nonregular files. A filename
	// reserved for SQLite cannot safely also belong to an unrelated directory.
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		info, statErr := os.Lstat(path + suffix)
		if statErr == nil && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("candle database file %s is not a regular file", filepath.Base(path+suffix))
		}
		if suffix != "" && statErr == nil && errors.Is(databaseStatErr, os.ErrNotExist) {
			return nil, fmt.Errorf("candle database sidecar %s exists without its database; preserve it for recovery", filepath.Base(path+suffix))
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
	}
	created := false
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		created = true
		if err = f.Close(); err != nil {
			_ = os.Remove(path)
			return nil, err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create candle database: %w", err)
	}
	if !created {
		// Validate before setting WAL mode, permissions or schema on an existing
		// file. A foreign workspace.sqlite renamed here is never adopted.
		if err = validateExisting(path); err != nil {
			return nil, fmt.Errorf("open candle database (original file preserved): %w", err)
		}
	}
	db, err := sql.Open("sqlite", sqliteURI(path, "rw"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	fail := func(cause error) (*Store, error) {
		_ = db.Close()
		// Never remove or replace any existing database after an error.
		if created {
			for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
				_ = os.Remove(path + suffix)
			}
		}
		return nil, fmt.Errorf("open candle database: %w", cause)
	}
	for _, pragma := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON"} {
		if _, err = db.Exec(pragma); err != nil {
			return fail(err)
		}
	}
	if created {
		if err = initialize(db); err != nil {
			return fail(err)
		}
	}
	s := &Store{db: db, path: path}
	if err = s.privateFiles(); err != nil {
		return fail(err)
	}
	return s, nil
}

func sqliteURI(path, mode string) string {
	normalized := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	u := &url.URL{Scheme: "file", Path: normalized}
	q := url.Values{"mode": {mode}}
	u.RawQuery = q.Encode()
	return u.String()
}

func schemaChecksum() string {
	sum := sha256.Sum256([]byte(schema))
	return hex.EncodeToString(sum[:])
}

func initialize(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(schema); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO candle_cache_metadata(id,schema_checksum) VALUES(1,?)", schemaChecksum()); err != nil {
		return err
	}
	for _, pragma := range []string{fmt.Sprintf("PRAGMA application_id=%d", applicationID), fmt.Sprintf("PRAGMA user_version=%d", databaseVersion)} {
		if _, err = tx.Exec(pragma); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validateExisting(path string) error {
	db, err := sql.Open("sqlite", sqliteURI(path, "ro"))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var id, version int
	if err = db.QueryRow("PRAGMA application_id").Scan(&id); err != nil {
		return err
	}
	if id != applicationID {
		return errors.New("file is not a CryptoDesk candle cache")
	}
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != databaseVersion {
		return fmt.Errorf("candle cache schema version %d is unsupported (expected %d)", version, databaseVersion)
	}
	var result string
	if err = db.QueryRow("PRAGMA quick_check(1)").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("candle cache integrity check: %s", result)
	}
	if err = db.QueryRow("SELECT schema_checksum FROM candle_cache_metadata WHERE id=1").Scan(&result); err != nil {
		return err
	}
	if result != schemaChecksum() {
		return errors.New("candle cache schema checksum mismatch")
	}
	// Check the actual persisted DDL as well as metadata. Changing a column while
	// leaving the schema marker alone must not silently adopt an incompatible DB.
	rows, err := db.Query("SELECT sql FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return err
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var ddl string
		if err = rows.Scan(&ddl); err != nil {
			return err
		}
		actual = append(actual, normalizeSQL(ddl))
	}
	if err = rows.Err(); err != nil {
		return err
	}
	expected := strings.Split(strings.TrimSuffix(strings.TrimSpace(schema), ";"), ";")
	if len(actual) != len(expected) {
		return errors.New("candle cache table definitions do not match the supported schema")
	}
	for i := range expected {
		if actual[i] != normalizeSQL(expected[i]) {
			return errors.New("candle cache table definitions do not match the supported schema")
		}
	}
	return nil
}

func normalizeSQL(sql string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(sql)), " ")
}

func (s *Store) privateFiles() error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Chmod(s.path+suffix, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("protect candle database permissions: %w", err)
		}
	}
	return nil
}

func validateCandle(key domain.FeedKey, c domain.Candle) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.SourceRevision < 0 || len(c.Quality) > 64 || !utf8.ValidString(c.Quality) || strings.ContainsRune(c.Quality, 0) {
		return errors.New("invalid candle source metadata")
	}
	step := map[string]int64{"1m": 60, "5m": 300, "15m": 900, "1h": 3600, "4h": 14400, "1d": 86400, "1w": 604800}[key.Timeframe]
	offset := int64(0)
	if key.Timeframe == "1w" {
		offset = 345600 // Exchange weeks begin on Monday UTC.
	}
	if step == 0 || (c.Time-offset)%step != 0 {
		return errors.New("candle is not on its UTC interval boundary")
	}
	return nil
}

func candleStrings(c domain.Candle) []string {
	return []string{strconv.FormatFloat(c.Open, 'g', -1, 64), strconv.FormatFloat(c.High, 'g', -1, 64), strconv.FormatFloat(c.Low, 'g', -1, 64), strconv.FormatFloat(c.Close, 'g', -1, 64), strconv.FormatFloat(c.Volume, 'g', -1, 64)}
}

func checksum(key domain.FeedKey, timestamp int64, values []string, closed int, quality string, revision int64) string {
	parts := []string{key.Exchange, key.Market, key.Instrument, key.Timeframe, strconv.FormatInt(timestamp, 10)}
	parts = append(parts, values...)
	parts = append(parts, strconv.Itoa(closed), quality, strconv.FormatInt(revision, 10))
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Upsert atomically writes one bounded batch. Repeated timestamps replace the
// prior accepted snapshot, including authoritative REST corrections with a zero
// source revision. Ordering stale live events remains the market service's job.
func (s *Store) Upsert(key domain.FeedKey, bars []domain.Candle) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if len(bars) > maxBatch {
		return fmt.Errorf("candle write batch exceeds %d bars", maxBatch)
	}
	for _, c := range bars {
		if err := validateCandle(key, c); err != nil {
			return fmt.Errorf("invalid cached candle at %d: %w", c.Time, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if len(bars) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO candles(exchange,market,instrument,timeframe,time,open,high,low,close,volume,closed,quality,source_revision,checksum)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(exchange,market,instrument,timeframe,time) DO UPDATE SET
 open=excluded.open,high=excluded.high,low=excluded.low,close=excluded.close,
 volume=excluded.volume,closed=excluded.closed,quality=excluded.quality,
 source_revision=excluded.source_revision,checksum=excluded.checksum`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range bars {
		values := candleStrings(c)
		closed := 0
		if c.Closed {
			closed = 1
		}
		_, err = stmt.Exec(key.Exchange, key.Market, key.Instrument, key.Timeframe, c.Time, values[0], values[1], values[2], values[3], values[4], closed, c.Quality, c.SourceRevision, checksum(key, c.Time, values, closed, c.Quality, c.SourceRevision))
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.privateFiles()
}

// Range returns at most limit candles in ascending UTC time. Both bounds are
// inclusive; a zero bound is unbounded. If more rows match, the latest are
// returned. (0,0) reads the latest saved window; limit=0 selects 2000 bars.
func (s *Store) Range(key domain.FeedKey, from, to int64, limit int) ([]domain.Candle, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	if from < 0 || to < 0 || from > lastTimestamp || to > lastTimestamp || (from > 0 && to > 0 && to < from) || limit < 0 || limit > MaxRangeLimit {
		return nil, errors.New("invalid bounded candle cache range")
	}
	if limit == 0 {
		limit = defaultLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if to == 0 {
		to = lastTimestamp
	}
	rows, err := s.db.Query(`SELECT time,open,high,low,close,volume,closed,quality,source_revision,checksum
 FROM candles WHERE exchange=? AND market=? AND instrument=? AND timeframe=? AND time>=? AND time<=?
 ORDER BY time DESC LIMIT ?`, key.Exchange, key.Market, key.Instrument, key.Timeframe, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Candle, 0)
	for rows.Next() {
		var c domain.Candle
		values := make([]string, 5)
		var closed int
		var savedChecksum string
		if err = rows.Scan(&c.Time, &values[0], &values[1], &values[2], &values[3], &values[4], &closed, &c.Quality, &c.SourceRevision, &savedChecksum); err != nil {
			return nil, err
		}
		if (closed != 0 && closed != 1) || savedChecksum != checksum(key, c.Time, values, closed, c.Quality, c.SourceRevision) {
			return nil, fmt.Errorf("cached candle at %d failed its integrity checksum", c.Time)
		}
		fields := []*float64{&c.Open, &c.High, &c.Low, &c.Close, &c.Volume}
		for i, value := range values {
			if *fields[i], err = strconv.ParseFloat(value, 64); err != nil {
				return nil, fmt.Errorf("cached candle at %d has invalid numeric data: %w", c.Time, err)
			}
		}
		c.Closed = closed == 1
		if err = validateCandle(key, c); err != nil {
			return nil, fmt.Errorf("cached candle at %d is invalid: %w", c.Time, err)
		}
		result = append(result, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

// Close waits for any in-progress write/read and closes idempotently. WAL commits
// remain durable even if checkpointing cannot finish immediately.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

// SizeBytes totals regular files recursively, including SQLite WAL data and any
// other files in the selected folder. Symlinks are never followed or counted.
// A folder that has not been created yet has size zero.
func SizeBytes(directory string) (int64, error) {
	directory, err := NormalizeDirectory(directory)
	if err != nil {
		return 0, err
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, errors.New("candle data folder must be a real directory")
	}
	var size int64
	err = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// SQLite can remove a WAL/SHM file concurrently with a size refresh.
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}
