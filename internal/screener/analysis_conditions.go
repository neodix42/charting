package screener

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

type analysisOperandReader struct {
	operand AnalysisOperand
	base    *analysisSeries
	series  []*analysisSeries
	window  *analysisOperandWindow
}
type analysisWindowPoint struct {
	index int64
	value float64
}
type analysisOperandWindow struct {
	series        *analysisSeries
	field, window int
	lowest        bool
	end           int64
	values        []analysisWindowPoint
	invalid       []int64
}
type compiledAnalysisCondition struct {
	condition                                AnalysisCondition
	left, right, upper, distance             *analysisOperandReader
	previousLeft, previousRight              float64
	previousValid                            bool
	history                                  []bool
	historyPosition, historyCount, trueCount int
}

func compileAnalysisConditions(a *analysisState, base *analysisSeries) ([]*compiledAnalysisCondition, error) {
	compiled := []*compiledAnalysisCondition{}
	for _, condition := range a.request.Conditions {
		c := &compiledAnalysisCondition{condition: condition}
		var err error
		if c.left, err = compileAnalysisOperand(a, base, condition.Left); err != nil {
			return nil, err
		}
		if c.right, err = compileAnalysisOperand(a, base, condition.Right); err != nil {
			return nil, err
		}
		op := condition.Operator
		if op == "occurredWithin" || op == "remainedFor" {
			op = condition.Comparison
			c.history = make([]bool, condition.Bars)
		}
		if op == "between" {
			if c.upper, err = compileAnalysisOperand(a, base, condition.Upper); err != nil {
				return nil, err
			}
		}
		if op == "withinDistance" {
			if c.distance, err = compileAnalysisOperand(a, base, condition.Distance); err != nil {
				return nil, err
			}
		}
		compiled = append(compiled, c)
	}
	return compiled, nil
}
func compileAnalysisOperand(a *analysisState, base *analysisSeries, operand AnalysisOperand) (*analysisOperandReader, error) {
	r := &analysisOperandReader{operand: operand, base: base}
	if operand.Kind == "number" {
		return r, nil
	}
	if operand.Timeframe == "" || operand.Timeframe == "same" || operand.Timeframe == base.metadata.Timeframe {
		if _, exists := base.fieldIndex(operand.Field); !exists {
			return nil, fmt.Errorf("%s does not contain field %q", base.metadata.Name, operand.Field)
		}
		r.series = []*analysisSeries{base}
		return r, nil
	}
	matchingFiles := 0
	for _, s := range a.series {
		if s.metadata.Symbol == base.metadata.Symbol && s.metadata.Timeframe == operand.Timeframe {
			matchingFiles++
			if _, exists := s.fieldIndex(operand.Field); exists {
				r.series = append(r.series, s)
			}
		}
	}
	if matchingFiles == 0 {
		return nil, fmt.Errorf("no usable %s data for %s; download and enrich that timeframe first", operand.Timeframe, base.metadata.Symbol)
	}
	sort.Slice(r.series, func(i, j int) bool { return r.series[i].metadata.Name < r.series[j].metadata.Name })
	if len(r.series) == 0 {
		return nil, fmt.Errorf("%s %s data does not contain field %q", base.metadata.Symbol, operand.Timeframe, operand.Field)
	}
	return r, nil
}
func (r *analysisOperandReader) resolve(baseIndex, at int64, extraOffset, overrideWindow int) (float64, bool, error) {
	return r.resolveAggregate(baseIndex, at, extraOffset, overrideWindow, r.operand.Aggregate == "lowest")
}
func (r *analysisOperandReader) resolveAggregate(baseIndex, at int64, extraOffset, overrideWindow int, lowest bool) (float64, bool, error) {
	if r.operand.Kind == "number" {
		return r.operand.Value, true, nil
	}
	var selected *analysisSeries
	selectedIndex := int64(-1)
	selectedTime := int64(-1)
	for _, s := range r.series {
		var i int64
		var err error
		if s == r.base {
			i = baseIndex
		} else {
			i, err = s.closedIndex(at)
			if err != nil {
				return 0, false, err
			}
		}
		if i < 0 {
			continue
		}
		row, e := s.row(i)
		if e != nil {
			return 0, false, e
		}
		// Newest closed bar wins overlapping datasets; lexical filename order
		// resolves identical timestamps deterministically, without future data.
		if selected == nil || row.timestamp > selectedTime {
			selected, selectedIndex, selectedTime = s, i, row.timestamp
		}
	}
	if selected == nil {
		return 0, false, nil
	}
	field, ok := selected.fieldIndex(r.operand.Field)
	if !ok {
		return 0, false, fmt.Errorf("selected %s does not contain field %q", selected.metadata.Name, r.operand.Field)
	}
	end := selectedIndex - int64(r.operand.Offset+extraOffset)
	window := 1
	if r.operand.Aggregate == "highest" || r.operand.Aggregate == "lowest" {
		window = r.operand.Window
	}
	if overrideWindow > 0 {
		window = overrideWindow
	}
	if end < 0 || end-int64(window)+1 < 0 {
		return 0, false, nil
	}
	if window == 1 {
		row, e := selected.row(end)
		if e != nil {
			return 0, false, e
		}
		v := row.values[field]
		return v, !math.IsNaN(v) && !math.IsInf(v, 0), nil
	}
	start := end - int64(window) + 1
	w := r.window
	if w == nil || w.series != selected || w.field != field || w.window != window || w.lowest != lowest || end < w.end || end-w.end > int64(window) {
		w = &analysisOperandWindow{series: selected, field: field, window: window, lowest: lowest, end: start - 1}
		r.window = w
	}
	// Each source value enters and leaves this monotonic deque once. Large
	// lookback windows therefore remain linear in the number of scanned bars.
	for len(w.values) > 0 && w.values[0].index < start {
		w.values = w.values[1:]
	}
	for len(w.invalid) > 0 && w.invalid[0] < start {
		w.invalid = w.invalid[1:]
	}
	for i := w.end + 1; i <= end; i++ {
		row, e := selected.row(i)
		if e != nil {
			return 0, false, e
		}
		v := row.values[field]
		if math.IsNaN(v) || math.IsInf(v, 0) {
			w.invalid = append(w.invalid, i)
			continue
		}
		for len(w.values) > 0 && ((lowest && w.values[len(w.values)-1].value >= v) || (!lowest && w.values[len(w.values)-1].value <= v)) {
			w.values = w.values[:len(w.values)-1]
		}
		w.values = append(w.values, analysisWindowPoint{i, v})
	}
	w.end = end
	if len(w.invalid) > 0 || len(w.values) == 0 {
		return 0, false, nil
	}
	return w.values[0].value, true, nil
}
func (r *analysisOperandReader) extremum(baseIndex, at int64, window int, lowest bool) (float64, bool, error) {
	// The comparator's prior window excludes the currently aligned right bar.
	return r.resolveAggregate(baseIndex, at, 1, window, lowest)
}
func (c *compiledAnalysisCondition) dependencies() []*analysisSeries {
	set := map[*analysisSeries]bool{}
	values := []*analysisSeries{}
	for _, r := range []*analysisOperandReader{c.left, c.right, c.upper, c.distance} {
		if r != nil {
			for _, s := range r.series {
				if !set[s] {
					set[s] = true
					values = append(values, s)
				}
			}
		}
	}
	return values
}
func (c *compiledAnalysisCondition) evaluate(index, at int64) (bool, error) {
	condition := c.condition
	op := condition.Operator
	if op == "occurredWithin" || op == "remainedFor" {
		op = condition.Comparison
	}
	left, leftOK, err := c.left.resolve(index, at, 0, 0)
	if err != nil {
		return false, err
	}
	right, rightOK, err := c.right.resolve(index, at, 0, 0)
	if err != nil {
		return false, err
	}
	valid := leftOK && rightOK
	if op == "highest" || op == "lowest" {
		valid = leftOK && (c.right.operand.Kind != "number" || index >= int64(condition.Bars))
	}
	matched := false
	if valid {
		switch op {
		case "above":
			matched = left > right
		case "below":
			matched = left < right
		case "equal":
			matched = left == right
		case "crossesAbove":
			matched = c.previousValid && c.previousLeft <= c.previousRight && left > right
		case "crossesBelow":
			matched = c.previousValid && c.previousLeft >= c.previousRight && left < right
		case "between":
			upper, ok, e := c.upper.resolve(index, at, 0, 0)
			if e != nil {
				return false, e
			}
			if ok {
				if upper < right {
					return false, errors.New("between lower bound exceeds its upper bound")
				}
				matched = left >= right && left <= upper
			}
		case "withinDistance":
			distance, ok, e := c.distance.resolve(index, at, 0, 0)
			if e != nil {
				return false, e
			}
			difference, threshold := math.Abs(left-right), condition.Multiplier*math.Abs(distance)
			if ok && (math.IsInf(difference, 0) || math.IsInf(threshold, 0)) {
				return false, errors.New("distance calculation exceeds the numeric range")
			}
			matched = ok && difference <= threshold
		case "highest", "lowest":
			level, ok, e := c.right.extremum(index, at, condition.Bars, op == "lowest")
			if e != nil {
				return false, e
			}
			matched = ok && ((op == "highest" && left > level) || (op == "lowest" && left < level))
		}
	}
	c.previousLeft, c.previousRight, c.previousValid = left, right, valid
	if condition.Operator != "occurredWithin" && condition.Operator != "remainedFor" {
		return matched, nil
	}
	if len(c.history) == 0 {
		return false, errors.New("condition has no bar history window")
	}
	if c.history[c.historyPosition] {
		c.trueCount--
	}
	c.history[c.historyPosition] = matched
	if matched {
		c.trueCount++
	}
	c.historyPosition = (c.historyPosition + 1) % len(c.history)
	c.historyCount = min(c.historyCount+1, len(c.history))
	if condition.Operator == "occurredWithin" {
		return c.trueCount > 0, nil
	}
	return c.historyCount == len(c.history) && c.trueCount == len(c.history), nil
}
