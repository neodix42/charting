package domain

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func testDrawing(kind string) Drawing {
	d := Drawing{ID: ID(), Type: kind, Exchange: "binance", Market: "spot", Instrument: "BTCUSDT", Points: []DrawingPoint{{Time: 1700000000, Price: 42123.5}}}
	if kind == "trendline" {
		d.Points = append(d.Points, DrawingPoint{Time: 1700003600, Price: 42500})
	}
	return d
}

func TestDrawingsRoundTripAndLegacyCompatibility(t *testing.T) {
	legacy := NewWorkspace()
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"drawings"`) {
		t.Fatal("legacy workspaces must not acquire an empty drawings field")
	}
	decoded, err := DecodeWorkspace(data)
	if err != nil || !reflect.DeepEqual(legacy, decoded) {
		t.Fatal("legacy workspace changed", err)
	}
	for _, kind := range []string{"arrowUp", "arrowDown", "horizontal", "trendline"} {
		legacy.Tabs[0].Charts[0].Drawings = append(legacy.Tabs[0].Charts[0].Drawings, testDrawing(kind))
	}
	// Drawings retain independent instrument scope, so symbol switches can hide
	// and later restore them without deleting geometry or sharing chart state.
	legacy.Tabs[0].Charts[0].Exchange = "coinbase"
	legacy.Tabs[0].Charts[0].Instrument = "ETH-USD"
	legacy.Tabs[0].Charts[0].Drawings[1].Exchange = "bybit"
	legacy.Tabs[0].Charts[0].Drawings[1].Market = "perpetual"
	legacy.Tabs[0].Charts[0].Drawings[1].Instrument = "ETHUSDT"
	if actual := cloneWorkspace(t, legacy); !reflect.DeepEqual(legacy, actual) {
		t.Fatal("drawing types, scope, or anchors changed during roundtrip")
	}
}

func TestDrawingScopeAndAnchorBoundaries(t *testing.T) {
	for _, exchange := range []string{"binance", "bybit", "coinbase"} {
		for _, market := range []string{"spot", "perpetual"} {
			if exchange == "coinbase" && market == "perpetual" {
				continue
			}
			w := NewWorkspace()
			d := testDrawing("trendline")
			d.Exchange, d.Market = exchange, market
			d.Points = []DrawingPoint{{Time: 1, Price: math.SmallestNonzeroFloat64}, {Time: 253402300799, Price: math.MaxFloat64}}
			w.Tabs[0].Charts[0].Drawings = []Drawing{d}
			if actual := cloneWorkspace(t, w); !reflect.DeepEqual(w, actual) {
				t.Fatal("supported scope or boundary anchors changed")
			}
		}
	}
	for _, points := range [][]DrawingPoint{
		{{Time: 1700000000, Price: 100}, {Time: 1700000000, Price: 101}},
		{{Time: 1700000000, Price: 100}, {Time: 1700003600, Price: 100}},
	} {
		w := NewWorkspace()
		d := testDrawing("trendline")
		d.Points = points
		w.Tabs[0].Charts[0].Drawings = []Drawing{d}
		if err := Validate(w); err != nil {
			t.Fatal("vertical and horizontal trendlines must be valid", err)
		}
	}
	w := NewWorkspace()
	for len(w.Tabs[0].Charts[0].Drawings) < MaxDrawings {
		w.Tabs[0].Charts[0].Drawings = append(w.Tabs[0].Charts[0].Drawings, testDrawing("arrowUp"))
	}
	if err := Validate(w); err != nil {
		t.Fatal("100 drawings must be accepted", err)
	}
	w.Tabs[0].Charts[0].Drawings = append(w.Tabs[0].Charts[0].Drawings, testDrawing("arrowDown"))
	if err := Validate(w); err == nil {
		t.Fatal("accepted 101 drawings")
	}
}

func TestInvalidDrawingConfigurationsRejected(t *testing.T) {
	cases := map[string]func(*Drawing){
		"unknown type":            func(d *Drawing) { d.Type = "script" },
		"invalid UUID":            func(d *Drawing) { d.ID = "drawing-1" },
		"unknown exchange":        func(d *Drawing) { d.Exchange = "other" },
		"unknown market":          func(d *Drawing) { d.Market = "futures" },
		"Coinbase perpetual":      func(d *Drawing) { d.Exchange, d.Market = "coinbase", "perpetual" },
		"blank instrument":        func(d *Drawing) { d.Instrument = " \t" },
		"long instrument":         func(d *Drawing) { d.Instrument = strings.Repeat("a", 129) },
		"control instrument":      func(d *Drawing) { d.Instrument = "BTC\x00USDT" },
		"executable instrument":   func(d *Drawing) { d.Instrument = "<script>" },
		"URL instrument":          func(d *Drawing) { d.Instrument = "https://example.invalid" },
		"no points":               func(d *Drawing) { d.Points = nil },
		"one trendline point":     func(d *Drawing) { d.Points = d.Points[:1] },
		"three trendline points":  func(d *Drawing) { d.Points = append(d.Points, DrawingPoint{Time: 1700010000, Price: 43000}) },
		"identical points":        func(d *Drawing) { d.Points[1] = d.Points[0] },
		"arrow with two points":   func(d *Drawing) { d.Type = "arrowUp" },
		"down arrow with two":     func(d *Drawing) { d.Type = "arrowDown" },
		"horizontal with two":     func(d *Drawing) { d.Type = "horizontal" },
		"time zero":               func(d *Drawing) { d.Points[0].Time = 0 },
		"negative time":           func(d *Drawing) { d.Points[1].Time = -1 },
		"time beyond range":       func(d *Drawing) { d.Points[1].Time = 253402300800 },
		"price zero":              func(d *Drawing) { d.Points[0].Price = 0 },
		"price negative":          func(d *Drawing) { d.Points[1].Price = -1 },
		"price NaN":               func(d *Drawing) { d.Points[0].Price = math.NaN() },
		"price positive infinity": func(d *Drawing) { d.Points[1].Price = math.Inf(1) },
		"price negative infinity": func(d *Drawing) { d.Points[1].Price = math.Inf(-1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			w := NewWorkspace()
			d := testDrawing("trendline")
			mutate(&d)
			w.Tabs[0].Charts[0].Drawings = []Drawing{d}
			if err := Validate(w); err == nil {
				t.Fatal("accepted invalid drawing")
			}
		})
	}
}

func TestDrawingIDsAreGloballyUnique(t *testing.T) {
	for _, owner := range []string{"workspace", "tab", "chart", "indicator", "drawing", "other chart"} {
		t.Run(owner, func(t *testing.T) {
			w := NewWorkspace()
			chart := &w.Tabs[0].Charts[0]
			chart.Drawings = []Drawing{testDrawing("arrowUp")}
			var duplicate string
			switch owner {
			case "workspace":
				duplicate = w.ID
			case "tab":
				duplicate = w.Tabs[0].ID
			case "chart":
				duplicate = chart.ID
			case "indicator":
				chart.Indicators = []Indicator{{ID: ID(), Type: "sma", Parameters: map[string]int{"period": 20}}}
				duplicate = chart.Indicators[0].ID
			case "drawing":
				chart.Drawings = append(chart.Drawings, testDrawing("arrowDown"))
				duplicate = chart.Drawings[1].ID
			case "other chart":
				w.Tabs = append(w.Tabs, NewTab("Another chart"))
				w.Tabs[1].Charts[0].Drawings = []Drawing{testDrawing("horizontal")}
				duplicate = w.Tabs[1].Charts[0].Drawings[0].ID
			}
			chart.Drawings[0].ID = strings.ToUpper(duplicate)
			if err := Validate(w); err == nil {
				t.Fatal("accepted duplicated drawing UUID")
			}
		})
	}
}

func TestDrawingJSONStrictness(t *testing.T) {
	w := NewWorkspace()
	w.Tabs[0].Charts[0].Drawings = []Drawing{testDrawing("arrowUp")}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	drawingData, _ := json.Marshal(w.Tabs[0].Charts[0].Drawings)
	cases := map[string][2]string{
		"null drawings":       {`"drawings":` + string(drawingData), `"drawings":null`},
		"object drawings":     {`"drawings":` + string(drawingData), `"drawings":{}`},
		"null points":         {`"points":[{"time":1700000000,"price":42123.5}]`, `"points":null`},
		"null point":          {`"points":[{"time":1700000000,"price":42123.5}]`, `"points":[null]`},
		"empty points":        {`"points":[{"time":1700000000,"price":42123.5}]`, `"points":[]`},
		"missing points":      {`,"points":[{"time":1700000000,"price":42123.5}]`, ``},
		"missing scope":       {`"type":"arrowUp","exchange":"binance",`, `"type":"arrowUp",`},
		"missing time":        {`"time":1700000000,`, ``},
		"missing price":       {`,"price":42123.5`, ``},
		"null time":           {`"time":1700000000`, `"time":null`},
		"fractional time":     {`"time":1700000000`, `"time":1700000000.5`},
		"string time":         {`"time":1700000000`, `"time":"1700000000"`},
		"null price":          {`"price":42123.5`, `"price":null`},
		"overflow price":      {`"price":42123.5`, `"price":1e999`},
		"string price":        {`"price":42123.5`, `"price":"42123.5"`},
		"case alias":          {`"time":1700000000`, `"Time":1700000000`},
		"duplicate anchor":    {`"time":1700000000`, `"time":1700000000,"time":1700000001`},
		"unknown point field": {`"price":42123.5`, `"price":42123.5,"x":120`},
		"unknown color":       {`"type":"arrowUp"`, `"type":"arrowUp","color":"#ffffff"`},
		"unknown endpoint":    {`"type":"arrowUp"`, `"type":"arrowUp","endpoint":"https://example.invalid"`},
	}
	for name, replacement := range cases {
		t.Run(name, func(t *testing.T) {
			input := strings.Replace(string(data), replacement[0], replacement[1], 1)
			if input == string(data) {
				t.Fatal("test mutation did not alter the document")
			}
			if _, err := DecodeWorkspace([]byte(input)); err == nil {
				t.Fatal("accepted malformed drawing", input)
			}
		})
	}
}
