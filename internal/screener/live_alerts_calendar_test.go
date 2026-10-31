package screener

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func calendarAlertCondition(field, operator string, value float64) AnalysisCondition {
	condition := analysisCondition(operator, analysisField(field), analysisNumber(value))
	condition.ID = fmt.Sprintf("%s-%s-%g", field, operator, value)
	return condition
}

func calendarAlertSaved(period string, conditions ...AnalysisCondition) SavedAnalysisCondition {
	return SavedAnalysisCondition{ID: strings.Repeat("a", 32), Name: "Calendar rule", Request: AnalysisRequest{Period: period, Conditions: conditions}}
}

func calendarAlertTime(value string) time.Time {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return at
}

func TestNextLiveAlertCalendarCloseUsesUTCSignalOpenAndStrictFutureClose(t *testing.T) {
	tuesday := calendarAlertCondition("weekday", "equal", 2)
	eight := calendarAlertCondition("hour", "equal", 8)
	hours := calendarAlertCondition("hour", "between", 8)
	hours.Upper = analysisNumber(10)
	weekdays := calendarAlertCondition("weekday", "between", 2)
	weekdays.Upper = analysisNumber(4)
	distance := calendarAlertCondition("weekday", "withinDistance", 3)
	distance.Distance, distance.Multiplier = analysisNumber(1), 1
	reverse := analysisCondition("below", analysisNumber(8), analysisField("hour"))
	for _, test := range []struct {
		name, after, period, expected string
		saved                         SavedAnalysisCondition
	}{
		{"Safe5 weekday Tuesday D1 closes Wednesday", "2024-01-01T12:00:00Z", "d1", "2024-01-03T00:00:00Z", calendarAlertSaved("d1", tuesday)},
		{"enabling during Tuesday waits for its close", "2024-01-02T12:30:00Z", "d1", "2024-01-03T00:00:00Z", calendarAlertSaved("d1", tuesday)},
		{"enabling exactly on eligible close waits one week", "2024-01-03T00:00:00Z", "d1", "2024-01-10T00:00:00Z", calendarAlertSaved("d1", tuesday)},
		{"hour eight waits for nine", "2024-01-02T07:30:00Z", "h1", "2024-01-02T09:00:00Z", calendarAlertSaved("h1", eight)},
		{"enabling inside signal hour", "2024-01-02T08:30:00Z", "h1", "2024-01-02T09:00:00Z", calendarAlertSaved("h1", eight)},
		{"hourly exact eligible boundary is strictly future", "2024-01-02T09:00:00Z", "h1", "2024-01-03T09:00:00Z", calendarAlertSaved("h1", eight)},
		{"first Tuesday hourly bar closes at one", "2024-01-01T23:59:59Z", "h1", "2024-01-02T01:00:00Z", calendarAlertSaved("h1", tuesday)},
		{"weekday and last hour closes next day", "2024-01-02T12:30:00Z", "h1", "2024-01-03T00:00:00Z", calendarAlertSaved("all", tuesday, calendarAlertCondition("hour", "equal", 23))},
		{"Sunday is ISO seven and closes Monday", "2024-01-06T12:30:00Z", "d1", "2024-01-08T00:00:00Z", calendarAlertSaved("d1", calendarAlertCondition("weekday", "equal", 7))},
		{"Friday open does not mean Friday close", "2024-01-05T00:00:00Z", "d1", "2024-01-06T00:00:00Z", calendarAlertSaved("d1", calendarAlertCondition("weekday", "equal", 5))},
		{"daily midnight hour is zero", "2024-01-02T12:30:00Z", "d1", "2024-01-03T00:00:00Z", calendarAlertSaved("d1", calendarAlertCondition("hour", "equal", 0))},
		{"hour range includes upper boundary", "2024-01-02T10:00:00Z", "h1", "2024-01-02T11:00:00Z", calendarAlertSaved("h1", hours)},
		{"weekday range includes upper boundary", "2024-01-04T12:00:00Z", "d1", "2024-01-05T00:00:00Z", calendarAlertSaved("d1", weekdays)},
		{"above is strict", "2024-01-02T08:30:00Z", "h1", "2024-01-02T10:00:00Z", calendarAlertSaved("h1", calendarAlertCondition("hour", "above", 8))},
		{"below is strict", "2024-01-02T00:00:00Z", "h1", "2024-01-02T01:00:00Z", calendarAlertSaved("h1", calendarAlertCondition("hour", "below", 1))},
		{"reversed number and calendar operand", "2024-01-02T08:30:00Z", "h1", "2024-01-02T10:00:00Z", calendarAlertSaved("h1", reverse)},
		{"distance has inclusive range", "2024-01-01T12:00:00Z", "d1", "2024-01-03T00:00:00Z", calendarAlertSaved("d1", distance)},
		{"local offset does not change UTC hour", "2024-01-02T10:30:00+04:00", "h1", "2024-01-02T09:00:00Z", calendarAlertSaved("h1", eight)},
		{"pre epoch UTC weekday", "1969-12-31T00:00:00Z", "d1", "1970-01-01T00:00:00Z", calendarAlertSaved("d1", calendarAlertCondition("weekday", "equal", 3))},
		{"unknown price condition keeps natural cadence", "2024-01-02T08:30:00Z", "h1", "2024-01-02T09:00:00Z", calendarAlertSaved("h1", calendarAlertCondition("close", "above", 100))},
		{"daily hour eight is impossible", "2024-01-02T12:30:00Z", "d1", "", calendarAlertSaved("d1", eight)},
		{"contradictory weekday clauses are impossible", "2024-01-02T12:30:00Z", "d1", "", calendarAlertSaved("d1", tuesday, calendarAlertCondition("weekday", "equal", 3))},
		{"fractional weekday equality impossible", "2024-01-02T12:30:00Z", "d1", "", calendarAlertSaved("d1", calendarAlertCondition("weekday", "equal", 1.5))},
		{"weekday out of range impossible", "2024-01-02T12:30:00Z", "d1", "", calendarAlertSaved("d1", calendarAlertCondition("weekday", "equal", 8))},
		{"hour out of range impossible", "2024-01-02T12:30:00Z", "h1", "", calendarAlertSaved("h1", calendarAlertCondition("hour", "below", 0))},
		{"saved period does not allow different period", "2024-01-02T12:30:00Z", "h1", "", calendarAlertSaved("d1", tuesday)},
		{"unsupported period", "2024-01-02T12:30:00Z", "h4", "", calendarAlertSaved("all", tuesday)},
	} {
		t.Run(test.name, func(t *testing.T) {
			after := calendarAlertTime(test.after)
			got, ok := nextLiveAlertConditionClose(after, test.saved, test.period)
			if test.expected == "" {
				if ok || !got.IsZero() {
					t.Fatal("impossible or unsupported schedule returned a close", got, ok)
				}
				return
			}
			if !ok || !got.Equal(calendarAlertTime(test.expected)) || !got.After(after) || got.Location() != time.UTC {
				t.Fatalf("close=%v ok=%v want=%s after=%v", got, ok, test.expected, after)
			}
		})
	}
}

func TestLiveAlertCalendarUnknownHistoryClausesCannotSuppressPossibleCloses(t *testing.T) {
	base := calendarAlertCondition("weekday", "equal", 2)
	for _, test := range []struct {
		name   string
		mutate func(*AnalysisCondition)
	}{
		{"previous bar offset", func(c *AnalysisCondition) { c.Left.Offset = 1 }},
		{"explicit same period timeframe", func(c *AnalysisCondition) { c.Left.Timeframe = "d1" }},
		{"companion timeframe", func(c *AnalysisCondition) { c.Left.Timeframe = "h1" }},
		{"highest window", func(c *AnalysisCondition) { c.Left.Aggregate, c.Left.Window = "highest", 3 }},
		{"lowest one bar window remains conservative", func(c *AnalysisCondition) { c.Left.Aggregate = "lowest" }},
		{"explicit window", func(c *AnalysisCondition) { c.Left.Window = 3 }},
		{"occurred within", func(c *AnalysisCondition) { c.Operator, c.Comparison, c.Bars = "occurredWithin", "equal", 3 }},
		{"remained for", func(c *AnalysisCondition) { c.Operator, c.Comparison, c.Bars = "remainedFor", "equal", 3 }},
		{"crosses above", func(c *AnalysisCondition) { c.Operator = "crossesAbove" }},
		{"previous extrema", func(c *AnalysisCondition) { c.Operator = "highest" }},
		{"calendar compared to unknown price", func(c *AnalysisCondition) { c.Right = analysisField("close") }},
		{"nonfinite numeric value", func(c *AnalysisCondition) { c.Right.Value = math.NaN() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			condition := base
			test.mutate(&condition)
			saved := calendarAlertSaved("d1", condition)
			got, ok := nextLiveAlertConditionClose(calendarAlertTime("2024-01-01T12:00:00Z"), saved, "d1")
			if !ok || !got.Equal(calendarAlertTime("2024-01-02T00:00:00Z")) || !liveAlertConditionDueAt(saved, "d1", got) {
				t.Fatal("unsupported predicate narrowed the ordinary daily cadence", got, ok)
			}
		})
	}
	// An ignored price/history clause must not disable an independent safe AND
	// constraint. The Tuesday bar remains a necessary part of every signal.
	price := calendarAlertCondition("close", "above", 100)
	saved := calendarAlertSaved("d1", base, price)
	if got, ok := nextLiveAlertConditionClose(calendarAlertTime("2024-01-01T12:00:00Z"), saved, "d1"); !ok || !got.Equal(calendarAlertTime("2024-01-03T00:00:00Z")) {
		t.Fatal("unknown clause erased independent necessary calendar constraint", got, ok)
	}
}

func TestLiveAlertCalendarDueAtUsesOriginalCutoffAndLatestClosedOpen(t *testing.T) {
	saved := calendarAlertSaved("d1", calendarAlertCondition("weekday", "equal", 2))
	for _, test := range []struct {
		cutoff string
		want   bool
	}{
		{"2024-01-02T00:00:00Z", false},
		{"2024-01-02T23:59:59.999Z", false},
		{"2024-01-03T00:00:00Z", true},
		{"2024-01-03T00:00:30Z", true},
		{"2024-01-03T23:59:59.999Z", true},
		{"2024-01-04T00:00:00Z", false},
	} {
		if got := liveAlertConditionDueAt(saved, "d1", calendarAlertTime(test.cutoff)); got != test.want {
			t.Fatalf("cutoff%s got%v want%v", test.cutoff, got, test.want)
		}
	}
	hourly := calendarAlertSaved("h1", calendarAlertCondition("hour", "equal", 8))
	if liveAlertConditionDueAt(hourly, "h1", calendarAlertTime("2024-01-02T08:59:59Z")) || !liveAlertConditionDueAt(hourly, "h1", calendarAlertTime("2024-01-02T09:00:00Z")) || liveAlertConditionDueAt(hourly, "h1", calendarAlertTime("2024-01-02T10:00:00Z")) {
		t.Fatal("hour eight did not become eligible exactly at its nine UTC close")
	}
}

func TestLiveAlertCalendarScheduleAgreesWithProductionConditionEvaluation(t *testing.T) {
	m := analysisTestManager(t)
	var text strings.Builder
	text.WriteString("timestamp,open,high,low,close,weekday,hour\n")
	start := calendarAlertTime("2024-01-01T00:00:00Z")
	for i := 0; i < 14*24; i++ {
		open := start.Add(time.Duration(i) * time.Hour)
		weekday := (int(open.Weekday())+6)%7 + 1
		fmt.Fprintf(&text, "%d,100,110,90,105,%d,%d\n", open.UnixMilli(), weekday, open.Hour())
	}
	name := "eurusd-h1-2024-01-01-2024-01-15.csv"
	writeDataFile(t, m, name, text.String())
	series, err := indexAnalysisCSV(context.Background(), m.directory, name, filepath.Join(t.TempDir(), "calendar.rows"), func(float64) {})
	if err != nil {
		t.Fatal(err)
	}
	tuesday := calendarAlertCondition("weekday", "equal", 2)
	eight := calendarAlertCondition("hour", "equal", 8)
	window := calendarAlertCondition("hour", "between", 8)
	window.Upper = analysisNumber(10)
	distance := calendarAlertCondition("weekday", "withinDistance", 3)
	distance.Distance, distance.Multiplier = analysisNumber(1), 1
	previous := tuesday
	previous.Left.Offset = 1
	recent := tuesday
	recent.Operator, recent.Comparison, recent.Bars = "occurredWithin", "equal", 30
	for _, test := range []struct {
		name       string
		conditions []AnalysisCondition
		exact      bool
	}{
		{"weekday direct", []AnalysisCondition{tuesday}, true},
		{"hour direct", []AnalysisCondition{eight}, true},
		{"weekday and hour", []AnalysisCondition{tuesday, eight}, true},
		{"inclusive hour range", []AnalysisCondition{window}, true},
		{"inclusive weekday distance", []AnalysisCondition{distance}, true},
		{"reverse numeric operand", []AnalysisCondition{analysisCondition("above", analysisNumber(3), analysisField("weekday"))}, true},
		{"calendar to calendar", []AnalysisCondition{analysisCondition("below", analysisField("weekday"), analysisField("hour"))}, true},
		{"previous weekday cannot narrow current weekday", []AnalysisCondition{previous}, false},
		{"recent weekday can remain true into following day", []AnalysisCondition{recent}, false},
		{"safe clause survives unknown price", []AnalysisCondition{tuesday, calendarAlertCondition("close", "above", 100)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			saved := calendarAlertSaved("h1", test.conditions...)
			a := &analysisState{request: saved.Request}
			compiled, err := compileAnalysisConditions(a, series)
			if err != nil {
				t.Fatal(err)
			}
			matches := 0
			for i := int64(0); i < series.count; i++ {
				open := start.Add(time.Duration(i) * time.Hour)
				close := open.Add(time.Hour)
				matched := true
				for _, condition := range compiled {
					value, err := condition.evaluate(i, close.UnixMilli())
					if err != nil {
						t.Fatal(err)
					}
					matched = matched && value
				}
				eligible := liveAlertConditionDueAt(saved, "h1", close)
				if matched {
					matches++
				}
				if matched && !eligible || test.exact && matched != eligible {
					t.Fatalf("production match%v schedule%v at UTC open%s", matched, eligible, open)
				}
			}
			if matches == 0 {
				t.Fatal("fixture did not exercise a production positive signal")
			}
		})
	}
}

func TestLiveAlertCalendarTimingHintAndConcurrentReaders(t *testing.T) {
	saved := calendarAlertSaved("d1", calendarAlertCondition("weekday", "equal", 2))
	next := calendarAlertTime("2024-01-03T00:00:00Z")
	if got := liveAlertConditionTimingHint(saved, "d1", next); got != "Next eligible signal bar: Tuesday 00:00 UTC → Wednesday 00:00 UTC." {
		t.Fatal(got)
	}
	if got := liveAlertConditionTimingHint(calendarAlertSaved("h1", calendarAlertCondition("close", "above", 1)), "h1", next); got != "" {
		t.Fatal("invented a calendar hint for a price-only condition", got)
	}
	if got := liveAlertConditionTimingHint(calendarAlertSaved("d1", calendarAlertCondition("hour", "equal", 8)), "d1", time.Time{}); !strings.Contains(got, "cannot match a D1 signal bar") {
		t.Fatal("impossible calendar lacked explanation", got)
	}
	if got := liveAlertConditionTimingHint(saved, "d1", time.Time{}); got != "" {
		t.Fatal("stopped monitoring confused with an impossible calendar", got)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				got, ok := nextLiveAlertConditionClose(calendarAlertTime("2024-01-02T12:30:00Z"), saved, "d1")
				if !ok || !got.Equal(next) || !liveAlertConditionDueAt(saved, "d1", next) {
					t.Error("concurrent calendar readers changed scheduling", got, ok)
				}
			}
		})
	}
	workers.Wait()
	if saved.Request.Conditions[0].Left.Field != "weekday" || saved.Request.Conditions[0].Right.Value != 2 {
		t.Fatal("scheduling mutated the saved condition")
	}
}
