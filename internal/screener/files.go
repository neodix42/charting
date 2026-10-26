package screener

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type DataFile struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Symbol         string   `json:"symbol,omitempty"`
	Description    string   `json:"description"`
	Category       string   `json:"category"`
	Timeframe      string   `json:"timeframe,omitempty"`
	From           string   `json:"from,omitempty"`
	To             string   `json:"to,omitempty"`
	FirstTimestamp *int64   `json:"firstTimestamp,omitempty"`
	LastTimestamp  *int64   `json:"lastTimestamp,omitempty"`
	Fields         []string `json:"fields"`
	Bytes          int64    `json:"bytes"`
	Error          string   `json:"error,omitempty"`
}

type FilePage struct {
	Directory string     `json:"directory"`
	Files     []DataFile `json:"files"`
	Total     int        `json:"total"`
	Offset    int        `json:"offset"`
	Limit     int        `json:"limit"`
}

var downloadFilename = regexp.MustCompile(`^([a-z0-9]+)-(tick|s1|m1|m5|m15|m30|h1|h4|d1|mn1)-(\d{4}-\d{2}-\d{2})-(\d{4}-\d{2}-\d{2})(?:-\d+)?\.csv$`)

func dataFileID(directory, name string) string {
	sum := sha256.Sum256([]byte(filepath.Join(directory, name)))
	return hex.EncodeToString(sum[:])
}

// csvNames lists only published regular CSVs, never symlinks, backups or partial
// downloads. ReadDir sorts by name, so pagination and job order are stable.
func csvNames(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list Screener files: %w", err)
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !strings.EqualFold(filepath.Ext(entry.Name()), ".csv") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect Screener file: %w", err)
		}
		if info.Mode().IsRegular() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// Published files, rather than the last download queue, define which assets are
// already present. Multiple ranges/timeframes count once; scratch stays hidden.
func downloadedSymbols(directory string) ([]string, error) {
	names, err := csvNames(directory)
	if err != nil {
		return []string{}, err
	}
	seen := map[string]bool{}
	for _, name := range names {
		if match := downloadFilename.FindStringSubmatch(strings.ToLower(name)); match != nil {
			seen[match[1]] = true
		}
	}
	symbols := make([]string, 0, len(seen))
	for symbol := range seen {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	return symbols, nil
}

func describeFile(directory, name string) DataFile {
	file := DataFile{ID: dataFileID(directory, name), Name: name, Fields: []string{}}
	if match := downloadFilename.FindStringSubmatch(strings.ToLower(name)); match != nil {
		file.Symbol, file.Timeframe, file.From, file.To = match[1], match[2], match[3], match[4]
	}
	asset := describeAsset(file.Symbol, name)
	file.Description, file.Category = asset.description, asset.category
	full := filepath.Join(directory, name)
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() {
		file.Error = "CSV is no longer a regular file"
		return file
	}
	file.Bytes = info.Size()
	input, err := os.Open(full)
	if err != nil {
		file.Error = cleanMessage(err.Error())
		return file
	}
	defer input.Close()
	// Publication can replace the pathname between Lstat and Open. Use the
	// opened file's size so the header and tail describe the same CSV snapshot.
	info, err = input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Error = "CSV is no longer a readable regular file"
		return file
	}
	file.Bytes = info.Size()
	// Bounded head/tail reads keep listing large tick histories cheap, while
	// reporting actual CSV timestamps even before the first refresh.
	buffered := bufio.NewReader(io.LimitReader(input, 1024*1024))
	var prefixBytes int64
	if prefix, _ := buffered.Peek(3); bytes.Equal(prefix, []byte{0xef, 0xbb, 0xbf}) {
		_, _ = buffered.Discard(3)
		prefixBytes = 3
	}
	reader := csv.NewReader(buffered)
	header, err := reader.Read()
	if err != nil || reader.InputOffset() >= 1024*1024 {
		file.Error = "CSV header is invalid, empty or larger than 1 MiB"
		return file
	}
	seen := map[string]bool{}
	for i, field := range header {
		field = strings.TrimPrefix(field, "\ufeff")
		normalized := strings.ToLower(strings.TrimSpace(field))
		if normalized == "" || seen[normalized] {
			file.Error = "CSV contains empty or duplicate field names"
		}
		seen[normalized], header[i] = true, field
	}
	file.Fields = header
	if file.Error == "" {
		file.FirstTimestamp, file.LastTimestamp, err = csvPeriod(input, reader, header, info.Size(), reader.InputOffset()+prefixBytes)
		if err != nil {
			file.Error = cleanMessage(err.Error())
		}
	}
	return file
}

func (m *Manager) Files(offset, limit int) (FilePage, error) {
	if offset < 0 || limit < 1 || limit > 200 {
		return FilePage{}, errors.New("file page requires a nonnegative offset and a limit between 1 and 200")
	}
	m.mu.Lock()
	directory := m.directory
	m.mu.Unlock()
	page := FilePage{Directory: directory, Files: []DataFile{}, Offset: offset, Limit: limit}
	names, err := csvNames(directory)
	if err != nil {
		return page, err
	}
	page.Total = len(names)
	start := min(offset, len(names))
	for _, name := range names[start : start+min(limit, len(names)-start)] {
		page.Files = append(page.Files, describeFile(directory, name))
	}
	return page, nil
}
