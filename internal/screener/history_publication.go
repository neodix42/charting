package screener

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Downloads, refresh and enrichment share the same recoverable publication.
// The private journal owns complete result/original hard links independently
// of workers' scratch directories. Only committed results replace old ranges.
var historyPublicationMu sync.Mutex

const historyPublicationFolder = ".cryptodesk-history-publications"

type historyOriginal struct {
	Name             string `json:"name"`
	Checksum         string `json:"checksum"`
	MetadataChecksum string `json:"metadataChecksum,omitempty"`
}

type historyPublication struct {
	Version          int               `json:"version"`
	Name             string            `json:"name"`
	Checksum         string            `json:"checksum"`
	MetadataChecksum string            `json:"metadataChecksum"`
	Sources          []historyOriginal `json:"sources"`
}

func canonicalHistoryName(symbol, timeframe, from, to string, sources []downloadSource, metadata fileMetadata) (string, error) {
	addBounds := func(start, end string) error {
		for _, value := range []string{start, end} {
			if value == "" {
				continue
			}
			date, err := time.Parse("2006-01-02", value)
			if err != nil || date.Year() < 1900 || date.Year() > 9999 {
				return errors.New("invalid history filename UTC date")
			}
		}
		if start != "" && (from == "" || start < from) {
			from = start
		}
		if end != "" && (to == "" || end > to) {
			to = end
		}
		return nil
	}
	if err := addBounds(from, to); err != nil {
		return "", err
	}
	for _, source := range sources {
		if err := addBounds(source.From, source.To); err != nil {
			return "", err
		}
		if match := downloadFilename.FindStringSubmatch(strings.ToLower(source.Name)); match != nil {
			if match[1] != symbol || match[2] != timeframe {
				return "", errors.New("history sources belong to different assets or timeframes")
			}
			if err := addBounds(match[3], match[4]); err != nil {
				return "", err
			}
		}
	}
	if metadata.LastTimestamp > 0 {
		if metadata.FirstTimestamp < 0 || metadata.FirstTimestamp > metadata.LastTimestamp {
			return "", errors.New("invalid history timestamp bounds")
		}
		first := time.UnixMilli(metadata.FirstTimestamp).UTC().Format("2006-01-02")
		last := time.UnixMilli(metadata.LastTimestamp).UTC().AddDate(0, 0, 1).Format("2006-01-02")
		if err := addBounds(first, last); err != nil {
			return "", err
		}
	}
	name := symbol + "-" + timeframe + "-" + from + "-" + to + ".csv"
	if from >= to || downloadFilename.FindStringSubmatch(name) == nil {
		return "", errors.New("invalid canonical history filename")
	}
	return name, nil
}

func publishHistory(directory, name, staging string, sources []downloadSource, originals map[string]os.FileInfo, metadata fileMetadata) (string, error) {
	historyPublicationMu.Lock()
	defer historyPublicationMu.Unlock()
	if !historyCSVName(name) {
		return "", errors.New("invalid history publication filename")
	}
	if err := checkHistoryDirectory(directory, false); err != nil {
		return "", err
	}
	if err := checkHistoryDirectory(metadataDirectory(directory), true); err != nil {
		return "", err
	}
	journal := historyPublication{Version: 1, Name: name, Sources: []historyOriginal{}}
	seen := map[string]bool{}
	for _, source := range sources {
		if !historyCSVName(source.Name) || source.Input != filepath.Join(directory, source.Name) || seen[source.Name] || originals[source.Input] == nil {
			return "", errors.New("invalid or duplicate original history path")
		}
		seen[source.Name] = true
		if err := unchangedFile(source.Input, originals[source.Input]); err != nil {
			return "", err
		}
		journal.Sources = append(journal.Sources, historyOriginal{Name: source.Name})
	}
	if err := checkHistoryFamily(directory, journal); err != nil {
		return "", err
	}
	if !seen[name] {
		for _, path := range []string{filepath.Join(directory, name), historyMetadataPath(directory, name)} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return "", errors.New("history destination already exists outside the source plan; existing files kept")
			}
		}
	}
	resultInfo, checksum, err := historyFingerprint(staging)
	if err != nil {
		return "", err
	}
	if metadata.Checksum != "" && metadata.Checksum != checksum {
		return "", errors.New("completed history checksum does not match its metadata")
	}
	journal.Checksum = checksum
	metadata.Version, metadata.Filename = 1, name
	metadata.Bytes, metadata.Modified, metadata.Checksum = resultInfo.Size(), resultInfo.ModTime().UnixNano(), checksum
	if err := validateHistoryMetadata(metadata); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(metadata)
	if err != nil || len(encoded) > 1024*1024 || len(metadata.Recipes) > 2000 {
		return "", errors.New("history publication metadata is invalid or exceeds its limit")
	}
	base := filepath.Join(directory, historyPublicationFolder)
	if err := checkHistoryDirectory(base, true); err != nil {
		return "", err
	}
	if err := syncHistoryDirectory(directory); err != nil {
		return "", err
	}
	prepared, err := os.MkdirTemp(base, ".prepare-")
	if err != nil {
		return "", err
	}
	// Incomplete preparation never touches visible files. Retain private
	// originals on failure; recovery ignores preparation directories.
	if err := os.Link(staging, filepath.Join(prepared, "result.csv")); err != nil {
		return "", err
	}
	for i, source := range sources {
		if err := unchangedFile(source.Input, originals[source.Input]); err != nil {
			return "", err
		}
		backup := historySourceBackup(prepared, i)
		if err := os.Link(source.Input, backup); err != nil {
			return "", fmt.Errorf("preserve original history: %w", err)
		}
		_, journal.Sources[i].Checksum, err = historyFingerprint(backup)
		if err != nil {
			return "", err
		}
		if source.Metadata.Checksum != "" && metadataMatches(source.Metadata, originals[source.Input]) && source.Metadata.Checksum != journal.Sources[i].Checksum {
			return "", errors.New("original history no longer matches its saved checksum")
		}
		if err := unchangedFile(source.Input, originals[source.Input]); err != nil {
			return "", err
		}
		oldMetadata := historyMetadataPath(directory, source.Name)
		if _, err := os.Lstat(oldMetadata); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return "", err
		}
		backupMetadata := historyMetadataBackup(prepared, i)
		if err := os.Link(oldMetadata, backupMetadata); err != nil {
			return "", err
		}
		info, sum, err := historyFingerprint(backupMetadata)
		if err != nil || info.Size() > 1024*1024 {
			return "", errors.New("original history metadata is not a bounded regular file")
		}
		journal.Sources[i].MetadataChecksum = sum
	}
	if err := writeSyncedExclusive(filepath.Join(prepared, "metadata.json"), append(encoded, '\n')); err != nil {
		return "", err
	}
	metadataDigest := sha256.Sum256(append(encoded, '\n'))
	journal.MetadataChecksum = hex.EncodeToString(metadataDigest[:])
	journalData, err := json.Marshal(journal)
	if err != nil {
		return "", err
	}
	if err := writeSyncedExclusive(filepath.Join(prepared, "journal.json"), append(journalData, '\n')); err != nil {
		return "", err
	}
	if err := syncHistoryDirectory(prepared); err != nil {
		return "", err
	}
	transaction := filepath.Join(base, strings.TrimPrefix(filepath.Base(prepared), ".prepare-"))
	if err := os.Rename(prepared, transaction); err != nil {
		return "", err
	}
	if err := syncHistoryDirectory(base); err != nil {
		return "", err
	}
	backup := ""
	if len(sources) > 0 {
		backup = historySourceBackup(transaction, 0)
	}
	return backup, finishHistoryPublication(directory, transaction, journal)
}

func recoverHistoryPublications(directory string) error {
	historyPublicationMu.Lock()
	defer historyPublicationMu.Unlock()
	base := filepath.Join(directory, historyPublicationFolder)
	if _, err := os.Lstat(base); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := checkHistoryDirectory(base, false); err != nil {
		return err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		transaction := filepath.Join(base, entry.Name())
		if err := checkHistoryDirectory(transaction, false); err != nil {
			return err
		}
		if info, err := os.Lstat(filepath.Join(transaction, "complete")); err == nil {
			if !info.Mode().IsRegular() || info.Size() != int64(len("complete\n")) {
				return errors.New("invalid history publication completion marker")
			}
			value, err := os.ReadFile(filepath.Join(transaction, "complete"))
			if err != nil || string(value) != "complete\n" {
				return errors.New("invalid history publication completion marker")
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		var journal historyPublication
		if info, err := os.Lstat(filepath.Join(transaction, "journal.json")); err != nil || !info.Mode().IsRegular() {
			return errors.New("history publication journal must be a regular file")
		}
		if err := readState(filepath.Join(transaction, "journal.json"), 8*1024*1024, &journal); err != nil {
			return err
		}
		if err := finishHistoryPublication(directory, transaction, journal); err != nil {
			return fmt.Errorf("recover history %s: %w", journal.Name, err)
		}
	}
	return nil
}

func finishHistoryPublication(directory, transaction string, journal historyPublication) error {
	if journal.Version != 1 || !historyCSVName(journal.Name) || len(journal.Sources) > 10000 {
		return errors.New("invalid history publication journal")
	}
	seen := map[string]bool{}
	for _, source := range journal.Sources {
		if !historyCSVName(source.Name) || seen[source.Name] {
			return errors.New("invalid history publication source")
		}
		seen[source.Name] = true
	}
	if err := checkHistoryDirectory(directory, false); err != nil {
		return err
	}
	if err := checkHistoryDirectory(metadataDirectory(directory), false); err != nil {
		return err
	}
	if err := checkHistoryFamily(directory, journal); err != nil {
		return err
	}
	result := filepath.Join(transaction, "result.csv")
	resultInfo, checksum, err := historyFingerprint(result)
	if err != nil || checksum != journal.Checksum {
		return errors.New("prepared history was changed; originals retained")
	}
	output := filepath.Join(directory, journal.Name)
	current, err := os.Lstat(output)
	published := err == nil && current.Mode().IsRegular() && os.SameFile(current, resultInfo)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !published && !seen[journal.Name] && err == nil {
		return errors.New("history destination was created outside the publication; originals retained")
	}
	newMetadata := filepath.Join(transaction, "metadata.json")
	newMetadataInfo, newMetadataChecksum, err := historyFingerprint(newMetadata)
	if err != nil || newMetadataInfo.Size() > 1024*1024 || newMetadataChecksum != journal.MetadataChecksum {
		return errors.New("prepared history metadata is invalid")
	}
	var metadata fileMetadata
	if err := readState(newMetadata, 1024*1024, &metadata); err != nil || metadata.Version != 1 || metadata.Filename != journal.Name || metadata.Checksum != journal.Checksum || metadata.Bytes != resultInfo.Size() || metadata.Modified != resultInfo.ModTime().UnixNano() {
		return errors.New("prepared history metadata does not describe the result")
	}
	if err := validateHistoryMetadata(metadata); err != nil {
		return err
	}
	metadataOutput := historyMetadataPath(directory, journal.Name)
	metadataInfo, metadataErr := os.Lstat(metadataOutput)
	metadataPublished := metadataErr == nil && metadataInfo.Mode().IsRegular() && os.SameFile(metadataInfo, newMetadataInfo)
	if metadataErr != nil && !errors.Is(metadataErr, os.ErrNotExist) {
		return metadataErr
	}
	if !metadataPublished && !seen[journal.Name] && metadataErr == nil {
		return errors.New("history destination metadata changed outside the publication")
	}
	// Validate every input before changing any visible file. During recovery
	// old inputs may be absent only after the complete result was published.
	for i, source := range journal.Sources {
		if !(published && source.Name == journal.Name) {
			if err := verifyHistoryOriginal(filepath.Join(directory, source.Name), historySourceBackup(transaction, i), source.Checksum, published); err != nil {
				return err
			}
		}
		if !(metadataPublished && source.Name == journal.Name) {
			if err := verifyHistoryMetadata(directory, transaction, i, source, published); err != nil {
				return err
			}
		}
	}
	if !published {
		if err := replaceHistoryLink(result, output, filepath.Join(transaction, "publish.csv")); err != nil {
			return fmt.Errorf("publish completed history: %w", err)
		}
		if err := syncHistoryDirectory(directory); err != nil {
			return err
		}
	}
	if !metadataPublished {
		if err := replaceHistoryLink(newMetadata, metadataOutput, filepath.Join(transaction, "publish.json")); err != nil {
			return fmt.Errorf("publish history metadata: %w", err)
		}
		if err := syncHistoryDirectory(metadataDirectory(directory)); err != nil {
			return err
		}
	}
	for i, source := range journal.Sources {
		if source.Name == journal.Name {
			continue
		}
		old := filepath.Join(directory, source.Name)
		if err := verifyHistoryOriginal(old, historySourceBackup(transaction, i), source.Checksum, true); err != nil {
			return err
		}
		if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := verifyHistoryMetadata(directory, transaction, i, source, true); err != nil {
			return err
		}
		if err := os.Remove(historyMetadataPath(directory, source.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := syncHistoryDirectory(directory); err != nil {
		return err
	}
	if err := syncHistoryDirectory(metadataDirectory(directory)); err != nil {
		return err
	}
	marker := filepath.Join(transaction, ".complete.tmp")
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeSyncedExclusive(marker, []byte("complete\n")); err != nil {
		return err
	}
	if err := os.Rename(marker, filepath.Join(transaction, "complete")); err != nil {
		return err
	}
	return syncHistoryDirectory(transaction)
}

func historyCSVName(name string) bool {
	return name != "" && name == filepath.Base(name) && !strings.HasPrefix(name, ".") && strings.EqualFold(filepath.Ext(name), ".csv")
}

func historyMetadataPath(directory, name string) string {
	return filepath.Join(metadataDirectory(directory), dataFileID(directory, name)+".json")
}

func historySourceBackup(transaction string, index int) string {
	return filepath.Join(transaction, fmt.Sprintf("source-%04d.csv", index))
}

func historyMetadataBackup(transaction string, index int) string {
	return filepath.Join(transaction, fmt.Sprintf("source-%04d.json", index))
}

func validateHistoryMetadata(metadata fileMetadata) error {
	if len(metadata.Recipes) > 2000 || len(metadata.Coverage) > 10000 {
		return errors.New("history metadata exceeds its recipe or coverage limit")
	}
	for _, span := range metadata.Coverage {
		if span.From < -8640000000000000 || span.To <= span.From || span.To > 8640000000000000 {
			return errors.New("history metadata contains an invalid coverage range")
		}
	}
	for _, recipe := range metadata.Recipes {
		if _, err := (&Manager{}).validateEnrichment(recipe); err != nil {
			return fmt.Errorf("history metadata indicator recipe: %w", err)
		}
	}
	return nil
}

func checkHistoryDirectory(path string, create bool) error {
	if create {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("history publication folder must be a real directory")
	}
	return nil
}

func checkHistoryFamily(directory string, journal historyPublication) error {
	match := downloadFilename.FindStringSubmatch(strings.ToLower(journal.Name))
	if match == nil {
		return nil
	}
	allowed := map[string]bool{journal.Name: true}
	for _, source := range journal.Sources {
		other := downloadFilename.FindStringSubmatch(strings.ToLower(source.Name))
		if other == nil || other[1] != match[1] || other[2] != match[2] {
			return errors.New("publication sources belong to different assets or timeframes")
		}
		allowed[source.Name] = true
	}
	names, err := csvNames(directory)
	if err != nil {
		return err
	}
	for _, name := range names {
		other := downloadFilename.FindStringSubmatch(strings.ToLower(name))
		if other != nil && other[1] == match[1] && other[2] == match[2] && !allowed[name] {
			return errors.New("another history range appeared during processing; existing files kept")
		}
	}
	return nil
}

func historyFingerprint(path string) (os.FileInfo, string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", errors.New("history publication input must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, "", errors.New("history publication input changed while opening")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return nil, "", err
	}
	if err := unchangedFile(path, info); err != nil {
		return nil, "", err
	}
	return info, hex.EncodeToString(digest.Sum(nil)), nil
}

func verifyHistoryOriginal(path, backup, checksum string, allowMissing bool) error {
	backupInfo, digest, err := historyFingerprint(backup)
	if err != nil || checksum == "" || digest != checksum {
		return errors.New("original history backup changed; publication stopped")
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return nil
	}
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(current, backupInfo) {
		return errors.New("original history changed outside the publication; existing files kept")
	}
	return nil
}

func verifyHistoryMetadata(directory, transaction string, index int, source historyOriginal, allowMissing bool) error {
	path := historyMetadataPath(directory, source.Name)
	if source.MetadataChecksum != "" {
		return verifyHistoryOriginal(path, historyMetadataBackup(transaction, index), source.MetadataChecksum, allowMissing)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("original history metadata appeared outside the publication")
	}
	return nil
}

func replaceHistoryLink(source, destination, temporary string) error {
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(source, temporary); err != nil {
		return err
	}
	return os.Rename(temporary, destination)
}

func syncHistoryDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = errors.Join(directory.Sync(), directory.Close())
	// Windows does not support fsync on directory handles. Journal/data files
	// are separately synced and replacement uses the filesystem rename.
	if runtime.GOOS == "windows" {
		return nil
	}
	return err
}
