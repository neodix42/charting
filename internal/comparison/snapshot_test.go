package comparison

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func snapshotPanel(weeks int, last int64) Result {
	start := last - int64(weeks*7)*day
	p := Result{Weeks: weeks, BaselineTime: start, LastClosedTime: last, Series: []Series{{Asset: Asset{ID: "BTC", Symbol: "BTC"}}, {Asset: Asset{ID: "ETH", Symbol: "ETH"}, Error: "unavailable"}}}
	for at := start; at <= last; at += day {
		v := float64((at-start)/day) * 1.25
		p.Series[0].Points = append(p.Series[0].Points, Point{Time: at, Pct: &v})
	}
	return p
}
func TestSnapshotExportsDisplayedPeriodsAndPreservesExistingFiles(t *testing.T) {
	profile := t.TempDir()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	m := NewWithOptions(Options{Profile: profile, Now: func() time.Time { return now }})
	defer m.Close()
	last := now.Unix()/day*day - day
	panels := []Result{}
	for _, w := range []int{2, 4, 8, 12} {
		panels = append(panels, snapshotPanel(w, last))
	}
	panels[0].Series[0].Points[3].Pct = nil
	paths, err := m.ExportSnapshots(panels)
	if err != nil || len(paths) != 4 {
		t.Fatal(paths, err)
	}
	for i, path := range paths {
		if filepath.Dir(path) != filepath.Join(profile, "snapshots") || !strings.HasSuffix(path, ".txt") {
			t.Fatal(path)
		}
		data, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		r := csv.NewReader(strings.NewReader(string(data)))
		r.Comma = '\t'
		rows, e := r.ReadAll()
		if e != nil || len(rows) != panels[i].Weeks*7+2 {
			t.Fatal(len(rows), e)
		}
		if strings.Join(rows[0], ",") != "Date,BTC,ETH" || rows[1][1] != "0" || rows[1][2] != "NA" {
			t.Fatal(rows[:2])
		}
		if i == 0 && rows[4][1] != "NA" {
			t.Fatal(rows[4])
		}
	}
	original, _ := os.ReadFile(paths[0])
	again, err := m.ExportSnapshots(panels)
	if err != nil || again[0] == paths[0] {
		t.Fatal(again, err)
	}
	after, _ := os.ReadFile(paths[0])
	if string(after) != string(original) {
		t.Fatal("overwrote snapshot")
	}
	shifted := snapshotPanel(4, last-day)
	one, err := m.ExportSnapshots([]Result{shifted})
	if err != nil || !strings.Contains(filepath.Base(one[0]), time.Unix(shifted.BaselineTime, 0).UTC().Format("2006-01-02")+"_w4") {
		t.Fatal(one, err)
	}
	entries, _ := os.ReadDir(filepath.Join(profile, "snapshots"))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatal("temporary file retained", entry.Name())
		}
	}
}
func TestSnapshotSettingsPersistAndRejectCorruption(t *testing.T) {
	profile := t.TempDir()
	m := New(profile, nil)
	defer m.Close()
	folder := filepath.Join(t.TempDir(), "chosen")
	if got, e := m.ConfigureSnapshotDirectory(folder); e != nil || got != folder {
		t.Fatal(got, e)
	}
	other := New(profile, nil)
	defer other.Close()
	if got, e := other.SnapshotDirectory(); e != nil || got != folder {
		t.Fatal(got, e)
	}
	if _, e := m.ConfigureSnapshotDirectory("relative"); e == nil {
		t.Fatal("accepted relative folder")
	}
	last := time.Now().UTC().Unix()/day*day - day
	paths, e := other.ExportSnapshots([]Result{snapshotPanel(2, last)})
	if e != nil || filepath.Dir(paths[0]) != folder {
		t.Fatal("custom folder was not used", paths, e)
	}
	if got, e := m.ConfigureSnapshotDirectory(""); e != nil || got != filepath.Join(profile, "snapshots") {
		t.Fatal("default folder was not restored", got, e)
	}
	bad := []byte(`{"version":99,"directory":"/preserved"}`)
	path := filepath.Join(profile, "comparison-snapshots.json")
	os.WriteFile(path, bad, 0600)
	if _, e := m.ConfigureSnapshotDirectory(""); e == nil {
		t.Fatal("overwrote unsupported settings")
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(bad) {
		t.Fatal("settings changed")
	}
}

func TestSnapshotDisambiguatesSourcesAndKeepsDisplayedPrecision(t *testing.T) {
	last := time.Now().UTC().Unix()/day*day - day
	p := snapshotPanel(2, last)
	p.Series[0].Asset = Asset{ID: "binance|spot|BTCUSDT", Symbol: "BTC", Exchange: "binance", Market: "spot", NativeID: "BTCUSDT", QuoteAsset: "USDT"}
	p.Series[1].Asset = Asset{ID: "coinbase|spot|BTC-USD", Symbol: "BTC", Exchange: "coinbase", Market: "spot", NativeID: "BTC-USD", QuoteAsset: "USD"}
	v := -3.123456789012345
	p.Series[0].Points[1].Pct = &v
	data, err := snapshotTable(p)
	if err != nil || !strings.Contains(string(data), "BTC [binance spot BTCUSDT USDT]\tBTC [coinbase spot BTC-USD USD]") || !strings.Contains(string(data), "-3.123456789012345\tNA") {
		t.Fatal(string(data), err)
	}
	v = math.NaN()
	if _, e := snapshotTable(p); e == nil {
		t.Fatal("accepted nonfinite observation")
	}
}

func TestSnapshotRejectsUnusableFolderAndPreservesSettings(t *testing.T) {
	profile := t.TempDir()
	m := New(profile, nil)
	defer m.Close()
	blocked := filepath.Join(t.TempDir(), "user-file")
	if err := os.WriteFile(blocked, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConfigureSnapshotDirectory(blocked); err == nil {
		t.Fatal("accepted file as output directory")
	}
	data, _ := os.ReadFile(blocked)
	if string(data) != "preserved" {
		t.Fatal("changed existing user file")
	}
	if _, err := os.Stat(filepath.Join(profile, "comparison-snapshots.json")); !os.IsNotExist(err) {
		t.Fatal("saved invalid folder setting", err)
	}
	path := filepath.Join(profile, "comparison-snapshots.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 16385)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SnapshotDirectory(); err == nil {
		t.Fatal("accepted oversized settings")
	}
}
func TestSnapshotRejectsInvalidBatchBeforePublication(t *testing.T) {
	m := New(t.TempDir(), nil)
	defer m.Close()
	last := time.Now().UTC().Unix()/day*day - day
	for _, panels := range [][]Result{nil, {snapshotPanel(2, last), snapshotPanel(4, last)}, {snapshotPanel(2, last+day)}, {snapshotPanel(2, last), snapshotPanel(4, last), snapshotPanel(8, last), snapshotPanel(11, last)}} {
		if _, e := m.ExportSnapshots(panels); e == nil {
			t.Fatal("accepted invalid export")
		}
	}
	p := snapshotPanel(2, last)
	p.Series[0].Points[1].Time = p.BaselineTime
	if _, e := m.ExportSnapshots([]Result{p}); e == nil {
		t.Fatal("accepted duplicate observations")
	}
	if _, e := os.Stat(filepath.Join(m.opts.Profile, "snapshots")); !os.IsNotExist(e) {
		t.Fatal("published invalid batch", e)
	}
}
