package domain

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func cloneWorkspace(t *testing.T, w Workspace) Workspace {
	t.Helper()
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	v, err := DecodeWorkspace(b)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestFirstLaunchAndNewTab(t *testing.T) {
	w := NewWorkspace()
	if err := Validate(w); err != nil {
		t.Fatal(err)
	}
	if len(w.Tabs) != 1 || len(w.Tabs[0].Charts) != 1 || w.Tabs[0].Grid.Rows != 1 || w.Tabs[0].Grid.Columns != 1 {
		t.Fatal("first launch must contain exactly one tab/chart in 1×1")
	}
	chart := w.Tabs[0].Charts[0]
	if chart.Exchange != "binance" || chart.Instrument != "BTCUSDT" || chart.Timeframe != "1h" {
		t.Fatal("unexpected default", chart)
	}
	tab := NewTab("New tab")
	if len(tab.Charts) != 1 || tab.Grid.Rows != 1 || tab.Grid.Columns != 1 {
		t.Fatal("new tab must contain exactly one chart")
	}
	w.Tabs[0].Grid = Grid{Rows: 3, Columns: 3, RowFractions: []float64{.3, .3, .4}, ColumnFractions: []float64{.3, .3, .4}}
	if err := Validate(w); err != nil || len(w.Tabs[0].Charts) != 1 {
		t.Fatal("larger grids add placeholders only", err)
	}
}

func TestThemeWorkspaceRoundTrip(t *testing.T) {
	if NewWorkspace().Theme != "dark" {
		t.Fatal("the existing default theme must remain dark")
	}
	for _, theme := range []string{"dark", "light", "matrix", "cyberpunk", "red-sands"} {
		t.Run(theme, func(t *testing.T) {
			workspace := NewWorkspace()
			workspace.Theme = theme
			encoded, err := json.Marshal(workspace)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := DecodeWorkspace(encoded)
			if err != nil {
				t.Fatalf("saved theme cannot be restored: %v", err)
			}
			if !reflect.DeepEqual(workspace, restored) {
				t.Fatal("theme round-trip changed chart settings or appearance")
			}
		})
	}
}
func TestEveryExampleExactRoundTrip(t *testing.T) {
	files, err := filepath.Glob("../../examples/*.json")
	if err != nil || len(files) != 3 {
		t.Fatalf("fixtures: %v %v", files, err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			w, err := DecodeWorkspace(b)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(w)
			if err != nil {
				t.Fatal(err)
			}
			var source, result any
			if err = json.Unmarshal(b, &source); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(encoded, &result); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(source, result) {
				t.Fatal("roundtrip changed workspace fields")
			}
		})
	}
}
func TestInvalidWorkspaceSemantics(t *testing.T) {
	cases := map[string]func(*Workspace){
		"zero tabs": func(w *Workspace) { w.Tabs = nil },
		"twenty one tabs": func(w *Workspace) {
			for len(w.Tabs) < 21 {
				w.Tabs = append(w.Tabs, NewTab("More"))
			}
		},
		"zero charts": func(w *Workspace) { w.Tabs[0].Charts = nil },
		"ten charts": func(w *Workspace) {
			w.Tabs[0].Grid = Grid{3, 3, []float64{.3, .3, .4}, []float64{.3, .3, .4}}
			for len(w.Tabs[0].Charts) < 10 {
				w.Tabs[0].Charts = append(w.Tabs[0].Charts, NewChart())
			}
		},
		"charts exceed grid":               func(w *Workspace) { w.Tabs[0].Charts = append(w.Tabs[0].Charts, NewChart()) },
		"missing active":                   func(w *Workspace) { w.ActiveTabID = ID() },
		"duplicate chart id":               func(w *Workspace) { w.Tabs[0].Charts[0].ID = w.ID },
		"case-insensitive uuid uniqueness": func(w *Workspace) { w.Tabs[0].Charts[0].ID = strings.ToUpper(w.ID) },
		"invalid uuid":                     func(w *Workspace) { w.ID = "fake-id" },
		"future schema":                    func(w *Workspace) { w.SchemaVersion = 2 },
		"wrong app":                        func(w *Workspace) { w.App = "another-app" },
		"empty name":                       func(w *Workspace) { w.Tabs[0].Name = "  " },
		"long name":                        func(w *Workspace) { w.Tabs[0].Name = strings.Repeat("a", 81) },
		"control name":                     func(w *Workspace) { w.Tabs[0].Name = "abc\x00" },
		"executable name":                  func(w *Workspace) { w.Tabs[0].Name = "<script>alert(1)</script>" },
		"unknown timezone":                 func(w *Workspace) { w.DisplayTimezone = "NoSuch/Timezone" },
		"window too small":                 func(w *Workspace) { w.Window.Width = 399 },
		"unknown theme":                    func(w *Workspace) { w.Theme = "blue" },
		"unsupported grid":                 func(w *Workspace) { w.Tabs[0].Grid.Rows = 1; w.Tabs[0].Grid.Columns = 3 },
		"wrong fractions":                  func(w *Workspace) { w.Tabs[0].Grid.RowFractions = []float64{.9} },
		"wrong fraction length":            func(w *Workspace) { w.Tabs[0].Grid.RowFractions = []float64{.5, .5} },
		"nonfinite fractions":              func(w *Workspace) { w.Tabs[0].Grid.RowFractions = []float64{math.NaN()} },
		"unsupported exchange":             func(w *Workspace) { w.Tabs[0].Charts[0].Exchange = "other" },
		"unsupported market":               func(w *Workspace) { w.Tabs[0].Charts[0].Market = "futures" },
		"endpoint instrument":              func(w *Workspace) { w.Tabs[0].Charts[0].Instrument = "https://evil.invalid" },
		"unsupported interval":             func(w *Workspace) { w.Tabs[0].Charts[0].Timeframe = "2m" },
		"unknown type":                     func(w *Workspace) { w.Tabs[0].Charts[0].ChartType = "custom" },
		"unknown scale":                    func(w *Workspace) { w.Tabs[0].Charts[0].PriceScale.Mode = "custom" },
		"manual without range":             func(w *Workspace) { w.Tabs[0].Charts[0].PriceScale.AutoScale = false },
		"reversed price range":             func(w *Workspace) { w.Tabs[0].Charts[0].PriceScale.ManualRange = &PriceRange{Min: 2, Max: 1} },
		"historical without anchors":       func(w *Workspace) { w.Tabs[0].Charts[0].View.Mode = "historical" },
		"reversed time anchors": func(w *Workspace) {
			a, b := int64(2), int64(1)
			w.Tabs[0].Charts[0].View = View{Mode: "historical", FromSec: &a, ToSec: &b, BarSpacing: 6}
		},
		"negative anchor":    func(w *Workspace) { a := int64(-1); w.Tabs[0].Charts[0].View.FromSec = &a },
		"nonfinite viewport": func(w *Workspace) { w.Tabs[0].Charts[0].View.BarSpacing = math.Inf(1) },
		"offscreen offset":   func(w *Workspace) { w.Tabs[0].Charts[0].View.RightOffset = 5001 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			w := NewWorkspace()
			mutate(&w)
			if err := Validate(w); err == nil {
				t.Fatal("invalid workspace accepted")
			}
		})
	}
}
func TestStrictJSONShape(t *testing.T) {
	base, _ := json.Marshal(NewWorkspace())
	cases := map[string][]byte{
		"duplicate property":  []byte(strings.Replace(string(base), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1)),
		"case alias property": []byte(strings.Replace(string(base), `"schemaVersion":1`, `"schemaVersion":1,"SchemaVersion":1`, 1)),
		"unknown endpoint":    []byte(strings.Replace(string(base), `"market":"spot"`, `"market":"spot","endpoint":"https://evil.invalid"`, 1)),
		"missing boolean":     []byte(strings.Replace(string(base), `"showVolume":true,`, "", 1)),
		"null boolean":        []byte(strings.Replace(string(base), `"showVolume":true`, `"showVolume":null`, 1)),
		"null object":         []byte(strings.Replace(string(base), `"window":{"width":1600,"height":1000,"maximized":false}`, `"window":null`, 1)),
		"trailing document":   append(append([]byte(nil), base...), []byte(` {}`)...),
		"too large":           []byte(strings.Repeat(" ", MaxImportBytes+1)),
		"invalid utf8":        append([]byte{0xff}, base...),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeWorkspace(b); err == nil {
				t.Fatal("invalid JSON accepted")
			}
		})
	}
}
