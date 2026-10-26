// Package domain contains portable application values with no GUI dependencies.
package domain

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	SchemaVersion  = 1
	MaxTabs        = 20
	MaxCharts      = 9
	MaxIndicators  = 12
	MaxImportBytes = 5 * 1024 * 1024
)

type Workspace struct {
	SchemaVersion   int            `json:"schemaVersion"`
	App             string         `json:"app"`
	ID              string         `json:"id"`
	Theme           string         `json:"theme"`
	FontSize        *int           `json:"fontSize,omitempty"`
	DisplayTimezone string         `json:"displayTimezone"`
	ChartRightGap   *ChartRightGap `json:"chartRightGap,omitempty"`
	ActiveTabID     string         `json:"activeTabId"`
	Window          Window         `json:"window"`
	Tabs            []Tab          `json:"tabs"`
}

// ChartRightGap applies to all charts. Omission in a legacy workspace means the
// default ten bars, and remains omitted when that document is exported again.
type ChartRightGap struct {
	Unit  string `json:"unit"`
	Value int    `json:"value"`
}

type Window struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`
}
type Tab struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Grid   Grid    `json:"grid"`
	Charts []Chart `json:"charts"`
}
type Grid struct {
	Rows            int       `json:"rows"`
	Columns         int       `json:"columns"`
	RowFractions    []float64 `json:"rowFractions"`
	ColumnFractions []float64 `json:"columnFractions"`
}
type Chart struct {
	ID         string      `json:"id"`
	Exchange   string      `json:"exchange"`
	Market     string      `json:"market"`
	Instrument string      `json:"instrument"`
	Timeframe  string      `json:"timeframe"`
	ChartType  string      `json:"chartType"`
	ShowVolume bool        `json:"showVolume"`
	PriceScale PriceScale  `json:"priceScale"`
	View       View        `json:"view"`
	Indicators []Indicator `json:"indicators,omitempty"`
	Drawings   []Drawing   `json:"drawings,omitempty"`
}
type PriceScale struct {
	Mode        string      `json:"mode"`
	AutoScale   bool        `json:"autoScale"`
	ManualRange *PriceRange `json:"manualRange"`
}
type PriceRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}
type View struct {
	Mode        string  `json:"mode"`
	FromSec     *int64  `json:"fromSec"`
	ToSec       *int64  `json:"toSec"`
	BarSpacing  float64 `json:"barSpacing"`
	RightOffset float64 `json:"rightOffset"`
}

// ID returns a cryptographically random RFC 4122 version 4 UUID.
func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("cannot generate workspace UUID: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func NewChart() Chart {
	return Chart{ID: ID(), Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Timeframe: "1h", ChartType: "candlestick", ShowVolume: true, PriceScale: PriceScale{Mode: "linear", AutoScale: true}, View: View{Mode: "live", BarSpacing: 6, RightOffset: 10}}
}
func NewTab(name string) Tab {
	return Tab{ID: ID(), Name: name, Grid: Grid{Rows: 1, Columns: 1, RowFractions: []float64{1}, ColumnFractions: []float64{1}}, Charts: []Chart{NewChart()}}
}

// NewWorkspace is used only when no workspace has ever been saved, or on explicit reset.
func NewWorkspace() Workspace {
	tab := NewTab("Tab 1")
	fontSize := 13
	return Workspace{SchemaVersion: SchemaVersion, App: "cryptodesk", ID: ID(), Theme: "dark", FontSize: &fontSize, DisplayTimezone: "UTC", ChartRightGap: &ChartRightGap{Unit: "bars", Value: 10}, ActiveTabID: tab.ID, Window: Window{Width: 1600, Height: 1000}, Tabs: []Tab{tab}}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func text(s string, max int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "<>") && !strings.ContainsFunc(s, unicode.IsControl)
}
func member(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func validTracks(v []float64, count int) bool {
	if len(v) != count {
		return false
	}
	sum := 0.0
	for _, n := range v {
		if !finite(n) || n <= 0 || n > 1 {
			return false
		}
		sum += n
	}
	return math.Abs(sum-1) <= 1e-6
}
func Validate(w Workspace) error {
	bad := func(format string, args ...any) error { return fmt.Errorf("invalidWorkspace: "+format, args...) }
	if w.SchemaVersion != SchemaVersion {
		return bad("unsupported schemaVersion %d", w.SchemaVersion)
	}
	if w.App != "cryptodesk" {
		return bad("app must be cryptodesk")
	}
	if !member(w.Theme, "dark", "light") {
		return bad("invalid theme")
	}
	if w.FontSize != nil && (*w.FontSize < 10 || *w.FontSize > 24) {
		return bad("font size must be a whole number from 10 to 24")
	}
	if !text(w.DisplayTimezone, 80) {
		return bad("invalid display timezone")
	}
	if _, err := time.LoadLocation(w.DisplayTimezone); err != nil {
		return bad("unknown display timezone %q", w.DisplayTimezone)
	}
	if gap := w.ChartRightGap; gap != nil {
		if !member(gap.Unit, "bars", "pixels") {
			return bad("chart right gap unit must be bars or pixels")
		}
		limit := 500
		if gap.Unit == "pixels" {
			limit = 5000
		}
		if gap.Value < 0 || gap.Value > limit {
			return bad("chart right gap must be between 0 and %d %s", limit, gap.Unit)
		}
	}
	if w.Window.Width < 400 || w.Window.Width > 16384 || w.Window.Height < 300 || w.Window.Height > 16384 {
		return bad("invalid window dimensions")
	}
	if len(w.Tabs) < 1 || len(w.Tabs) > MaxTabs {
		return bad("workspace must contain 1–20 tabs")
	}
	ids := make(map[string]bool)
	addID := func(id string) bool {
		key := strings.ToLower(id)
		if !uuidPattern.MatchString(id) || ids[key] {
			return false
		}
		ids[key] = true
		return true
	}
	if !addID(w.ID) {
		return bad("invalid workspace UUID")
	}
	active := false
	for i, tab := range w.Tabs {
		if !addID(tab.ID) {
			return bad("tab %d UUID is invalid or duplicated", i+1)
		}
		if tab.ID == w.ActiveTabID {
			active = true
		}
		if !text(tab.Name, 80) {
			return bad("tab %d name must be nonblank and at most 80 characters", i+1)
		}
		g := tab.Grid
		if !member(fmt.Sprintf("%dx%d", g.Rows, g.Columns), "1x1", "1x2", "2x1", "2x2", "2x3", "3x2", "3x3") {
			return bad("unsupported grid preset")
		}
		if !validTracks(g.RowFractions, g.Rows) || !validTracks(g.ColumnFractions, g.Columns) {
			return bad("grid track fractions must match dimensions and sum to 1")
		}
		if len(tab.Charts) < 1 || len(tab.Charts) > MaxCharts || len(tab.Charts) > g.Rows*g.Columns {
			return bad("tab must contain 1–9 charts fitting its grid")
		}
		for _, chart := range tab.Charts {
			if !addID(chart.ID) {
				return bad("chart UUID is invalid or duplicated")
			}
			if len(chart.Indicators) > MaxIndicators {
				return bad("maximum %d indicators per chart", MaxIndicators)
			}
			for _, indicator := range chart.Indicators {
				if !addID(indicator.ID) {
					return bad("indicator UUID is invalid or duplicated")
				}
				if err := validateIndicator(indicator); err != nil {
					return bad("%s", err)
				}
			}
			if len(chart.Drawings) > MaxDrawings {
				return bad("maximum %d drawings per chart", MaxDrawings)
			}
			for _, drawing := range chart.Drawings {
				if !addID(drawing.ID) {
					return bad("drawing UUID is invalid or duplicated")
				}
				if err := validateDrawing(drawing); err != nil {
					return bad("%s", err)
				}
			}
			if !member(chart.Exchange, "binance", "bybit", "coinbase") || !supportedMarket(chart.Exchange, chart.Market) {
				return bad("unsupported exchange or market")
			}
			if !text(chart.Instrument, 128) {
				return bad("instrument must be nonblank and at most 128 characters")
			}
			if strings.Contains(chart.Instrument, "://") {
				return bad("instrument cannot be an endpoint URL")
			}
			if !member(chart.Timeframe, "1m", "5m", "15m", "1h", "4h", "1d", "1w") || !member(chart.ChartType, "candlestick", "line") {
				return bad("unsupported timeframe or chart type")
			}
			p := chart.PriceScale
			if !member(p.Mode, "linear", "log") {
				return bad("unsupported price scale")
			}
			if p.ManualRange != nil && (!finite(p.ManualRange.Min) || !finite(p.ManualRange.Max) || p.ManualRange.Min <= 0 || p.ManualRange.Min >= p.ManualRange.Max) {
				return bad("invalid manual price range")
			}
			if !p.AutoScale && p.ManualRange == nil {
				return bad("manual price scale needs a range")
			}
			v := chart.View
			if !member(v.Mode, "live", "historical") || !finite(v.BarSpacing) || v.BarSpacing < 1 || v.BarSpacing > 100 || !finite(v.RightOffset) || v.RightOffset < -5000 || v.RightOffset > 5000 {
				return bad("invalid chart viewport")
			}
			for _, anchor := range []*int64{v.FromSec, v.ToSec} {
				if anchor != nil && (*anchor < 0 || *anchor > 32503680000) {
					return bad("time anchor is outside supported range")
				}
			}
			if v.Mode == "historical" && (v.FromSec == nil || v.ToSec == nil || *v.FromSec >= *v.ToSec) {
				return bad("historical viewport requires fromSec < toSec")
			}
		}
	}
	if !active {
		return bad("activeTabId must identify an existing tab")
	}
	return nil
}

// DecodeWorkspace enforces the complete portable schema, including required fields,
// unknown properties, nullability and semantics, before returning a workspace.
func DecodeWorkspace(data []byte) (Workspace, error) {
	var w Workspace
	if len(data) > MaxImportBytes {
		return w, errors.New("invalidWorkspace: import exceeds 5 MiB")
	}
	if !utf8.Valid(data) {
		return w, errors.New("invalidWorkspace: JSON must be UTF-8")
	}
	if err := uniqueJSONKeys(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return w, fmt.Errorf("invalidWorkspace: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return Workspace{}, fmt.Errorf("invalidWorkspace: %w", err)
	}
	var tail any
	if err := dec.Decode(&tail); err != io.EOF {
		return Workspace{}, errors.New("invalidWorkspace: expected one JSON document")
	}
	if err := requiredFields(data, reflect.TypeOf(w), "workspace"); err != nil {
		return Workspace{}, err
	}
	if err := Validate(w); err != nil {
		return Workspace{}, err
	}
	return w, nil
}
func requiredFields(raw json.RawMessage, t reflect.Type, path string) error {
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		return requiredFields(raw, t.Elem(), path)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("invalidWorkspace: %s cannot be null", path)
	}
	switch t.Kind() {
	case reflect.Struct:
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		allowed := make(map[string]bool, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			allowed[strings.Split(t.Field(i).Tag.Get("json"), ",")[0]] = true
		}
		for key := range obj {
			if !allowed[key] {
				return fmt.Errorf("invalidWorkspace: unknown property %s.%s", path, key)
			}
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			name := tag[0]
			v, ok := obj[name]
			if !ok {
				if len(tag) > 1 && tag[1] == "omitempty" {
					continue
				}
				return fmt.Errorf("invalidWorkspace: missing %s.%s", path, name)
			}
			if len(tag) > 1 && tag[1] == "omitempty" && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return fmt.Errorf("invalidWorkspace: optional %s.%s must be omitted rather than null", path, name)
			}
			if err := requiredFields(v, f.Type, path+"."+name); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for i, v := range values {
			if err := requiredFields(v, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for name, value := range values {
			if err := requiredFields(value, t.Elem(), path+"."+name); err != nil {
				return err
			}
		}
	}
	return nil
}

// encoding/json accepts duplicate and case-insensitive object names by default;
// portable imports must have an unambiguous, exact schema shape instead.
func uniqueJSONKeys(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds workspace limits")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid object key")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON property %q", key)
			}
			seen[key] = true
			if err := uniqueJSONKeys(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := uniqueJSONKeys(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = dec.Token()
	return err
}
