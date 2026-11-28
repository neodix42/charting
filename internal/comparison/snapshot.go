package comparison

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type snapshotSettings struct {
	Version   int    `json:"version"`
	Directory string `json:"directory"`
}

func (m *Manager) snapshotDirectory() (string, error) {
	path := filepath.Join(m.opts.Profile, "comparison-snapshots.json")
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Join(m.opts.Profile, "snapshots"), nil
	}
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > 16384 {
		return "", errors.New("invalid Comparison snapshot settings; original preserved")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return "", errors.New("Comparison snapshot settings changed while opening; original preserved")
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil {
		return "", err
	}
	if len(data) > 16384 {
		return "", errors.New("Comparison snapshot settings exceed 16 KiB; original preserved")
	}
	var value snapshotSettings
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&value); err != nil {
		return "", err
	}
	if dec.Decode(new(any)) != io.EOF || value.Version != 1 || !filepath.IsAbs(value.Directory) {
		return "", errors.New("unsupported Comparison snapshot settings; original preserved")
	}
	return value.Directory, nil
}
func (m *Manager) SnapshotDirectory() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotDirectory()
}
func (m *Manager) ConfigureSnapshotDirectory(directory string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", errors.New("comparison is closed")
	}
	if _, err := m.snapshotDirectory(); err != nil {
		return "", err
	}
	directory = strings.TrimSpace(directory)
	if directory == "" {
		directory = filepath.Join(m.opts.Profile, "snapshots")
	}
	if !filepath.IsAbs(directory) || strings.ContainsRune(directory, 0) {
		return "", errors.New("Snapshot folder must be an absolute path")
	}
	directory = filepath.Clean(directory)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	probe, err := os.CreateTemp(directory, ".snapshot-check-*")
	if err != nil {
		return "", err
	}
	probe.Close()
	os.Remove(probe.Name())
	if err = os.MkdirAll(m.opts.Profile, 0700); err != nil {
		return "", err
	}
	data, _ := json.Marshal(snapshotSettings{1, directory})
	f, err := os.CreateTemp(m.opts.Profile, ".comparison-snapshots-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(m.opts.Profile, "comparison-snapshots.json")); err != nil {
		return "", err
	}
	if directory, e := os.Open(m.opts.Profile); e == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return directory, nil
}

func snapshotTable(panel Result) ([]byte, error) {
	if panel.Weeks < 1 || panel.Weeks > 104 || panel.BaselineTime < 0 || panel.BaselineTime%day != 0 || panel.LastClosedTime != panel.BaselineTime+int64(panel.Weeks*7)*day || len(panel.Series) < 1 || len(panel.Series) > 20 {
		return nil, errors.New("invalid Comparison snapshot period")
	}
	var out bytes.Buffer
	writer := csv.NewWriter(&out)
	writer.Comma = '\t'
	header := []string{"Date"}
	columns := make([]map[int64]*float64, len(panel.Series))
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, series := range panel.Series {
		counts[series.Asset.Symbol]++
	}
	for i, series := range panel.Series {
		id := series.Asset.ID
		if id == "" {
			id = series.Asset.Symbol
		}
		if id == "" || seen[id] {
			return nil, errors.New("duplicate or missing snapshot asset")
		}
		seen[id] = true
		label := series.Asset.Symbol
		if counts[label] > 1 {
			label += fmt.Sprintf(" [%s %s %s %s]", series.Asset.Exchange, series.Asset.Market, series.Asset.NativeID, series.Asset.QuoteAsset)
		}
		if label == "" || strings.ContainsAny(label, "\t\r\n\x00") {
			return nil, errors.New("invalid snapshot asset label")
		}
		header = append(header, label)
		columns[i] = map[int64]*float64{}
		if len(series.Points) > panel.Weeks*7+1 {
			return nil, errors.New("too many snapshot observations")
		}
		var previous int64 = -1
		for _, point := range series.Points {
			if point.Time < panel.BaselineTime || point.Time > panel.LastClosedTime || point.Time%day != 0 || point.Time <= previous {
				return nil, errors.New("invalid snapshot chronology")
			}
			previous = point.Time
			if point.Pct != nil && (math.IsNaN(*point.Pct) || math.IsInf(*point.Pct, 0)) {
				return nil, errors.New("invalid snapshot percentage")
			}
			if series.Error == "" {
				columns[i][point.Time] = point.Pct
			}
		}
	}
	_ = writer.Write(header)
	for at := panel.BaselineTime; at <= panel.LastClosedTime; at += day {
		row := []string{time.Unix(at, 0).UTC().Format("2006-01-02")}
		for _, column := range columns {
			value := "NA"
			if pct := column[at]; pct != nil {
				value = strconv.FormatFloat(*pct, 'g', -1, 64)
			}
			row = append(row, value)
		}
		_ = writer.Write(row)
	}
	writer.Flush()
	return out.Bytes(), writer.Error()
}

// ExportSnapshots writes the displayed observations without requesting provider data.
// Hard-link publication is atomic and cannot overwrite an existing user file.
func (m *Manager) ExportSnapshots(panels []Result) (paths []string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("comparison is closed")
	}
	if len(panels) != 1 && len(panels) != 4 {
		return nil, errors.New("snapshot requires one chart or the four overview periods")
	}
	tables := make([][]byte, len(panels))
	seen := map[int]bool{}
	for i, panel := range panels {
		if seen[panel.Weeks] || panel.LastClosedTime != panels[0].LastClosedTime || panel.LastClosedTime > m.opts.Now().UTC().Unix()/day*day-day {
			return nil, errors.New("invalid snapshot cutoff")
		}
		seen[panel.Weeks] = true
		if len(panels) == 4 && panel.Weeks != 2 && panel.Weeks != 4 && panel.Weeks != 8 && panel.Weeks != 12 {
			return nil, errors.New("invalid overview snapshot periods")
		}
		if i > 0 {
			if len(panel.Series) != len(panels[0].Series) {
				return nil, errors.New("snapshot assets differ")
			}
			for j, series := range panel.Series {
				if series.Asset.ID != panels[0].Series[j].Asset.ID || series.Asset.Symbol != panels[0].Series[j].Asset.Symbol {
					return nil, errors.New("snapshot assets differ")
				}
			}
		}
		tables[i], err = snapshotTable(panel)
		if err != nil {
			return nil, err
		}
	}
	directory, err := m.snapshotDirectory()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	staged := []string{}
	defer func() {
		for _, path := range staged {
			os.Remove(path)
		}
		if err != nil {
			for _, path := range paths {
				os.Remove(path)
			}
			paths = nil
		}
	}()
	for _, data := range tables {
		f, e := os.CreateTemp(directory, ".comparison-snapshot-*")
		if e != nil {
			return paths, e
		}
		staged = append(staged, f.Name())
		e = f.Chmod(0600)
		if e == nil {
			_, e = f.Write(data)
		}
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return paths, e
		}
		if ce != nil {
			return paths, ce
		}
	}
	for i, panel := range panels {
		base := fmt.Sprintf("%s_w%d", time.Unix(panel.BaselineTime, 0).UTC().Format("2006-01-02"), panel.Weeks)
		published := false
		for suffix := 0; suffix < 10000; suffix++ {
			name := base
			if suffix > 0 {
				name += "_" + strconv.Itoa(suffix+1)
			}
			path := filepath.Join(directory, name+".txt")
			e := os.Link(staged[i], path)
			if errors.Is(e, os.ErrExist) {
				continue
			}
			if e != nil {
				return paths, fmt.Errorf("publish snapshot: %w", e)
			}
			paths = append(paths, path)
			published = true
			break
		}
		if !published {
			return paths, errors.New("snapshot filename limit reached; choose another folder")
		}
	}
	return paths, nil
}
