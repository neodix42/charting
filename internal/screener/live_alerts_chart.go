package screener

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	liveAlertChartPreviousBars = 120
	liveAlertChartMaxFields    = 64
)

// Build media before claiming a signal, while the evaluated private index is
// still available. A media error preserves the text signal; cancellation must
// stop the check without consuming its durable claim.
func attachLiveAlertChart(ctx context.Context, a *analysisState, s *analysisSeries, index int64, alert *LiveAlert) error {
	alert.Chart, alert.ChartError = nil, ""
	chart, err := prepareLiveAlertChart(ctx, a, s, index, *alert)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		alert.ChartError = fmt.Sprintf("Signal chart could not be prepared: %v", err)
		return nil
	}
	alert.Chart = &chart
	return nil
}

// The chart is read from exactly the index used by the evaluator, independent
// of an Analysis UI run. It has no future bars or simulated future execution.
func prepareLiveAlertChart(ctx context.Context, a *analysisState, s *analysisSeries, index int64, alert LiveAlert) (AnalysisChart, error) {
	if err := ctx.Err(); err != nil {
		return AnalysisChart{}, err
	}
	if a == nil || s == nil || index < 0 || index >= s.count {
		return AnalysisChart{}, errors.New("checked candle index is unavailable")
	}
	compiled, err := compileAnalysisConditions(a, s)
	if err != nil {
		return AnalysisChart{}, err
	}
	dependencies := map[*analysisSeries]bool{s: true}
	required := map[int]bool{}
	for _, field := range []string{"timestamp", "open", "high", "low", "close", "volume", "bidprice", "askprice"} {
		if i, ok := s.fieldIndex(field); ok {
			required[i] = true
		}
	}
	qualified := map[string]*analysisOperandReader{}
	for _, condition := range compiled {
		for _, dependency := range condition.dependencies() {
			dependencies[dependency] = true
		}
		for _, reader := range []*analysisOperandReader{condition.left, condition.right, condition.upper, condition.distance} {
			if reader == nil || reader.operand.Kind != "field" {
				continue
			}
			operand := reader.operand
			if operand.Timeframe == "" || operand.Timeframe == "same" || operand.Timeframe == s.metadata.Timeframe {
				if i, ok := s.fieldIndex(operand.Field); ok {
					required[i] = true
				}
				continue
			}
			// Keep the Analysis chart's companion naming and closed-bar alignment.
			name := operand.Timeframe + ":" + strings.ToLower(operand.Field)
			if operand.Offset > 0 {
				name += fmt.Sprintf("[offset %d]", operand.Offset)
			}
			if operand.Aggregate == "highest" || operand.Aggregate == "lowest" {
				name += fmt.Sprintf("[%s %d]", operand.Aggregate, operand.Window)
			}
			qualified[name] = reader
		}
	}
	if config := a.request.TradeSimulation; config != nil {
		for _, field := range []string{config.StopLoss, config.TakeProfit} {
			if i, ok := s.fieldIndex(field); ok {
				required[i] = true
			}
		}
	}
	if len(required)+len(qualified) > liveAlertChartMaxFields {
		return AnalysisChart{}, errors.New("condition requires more than 64 chart fields")
	}
	for dependency := range dependencies {
		if err = analysisUnchanged(dependency); err != nil {
			return AnalysisChart{}, err
		}
	}
	// Preserve source field order, prioritizing all actual rule/level operands
	// over unrelated enrichment columns when a file has hundreds of fields.
	selected := required
	for i := range s.headers {
		if len(selected)+len(qualified) >= liveAlertChartMaxFields {
			break
		}
		selected[i] = true
	}
	fields, positions := []string{}, []int{}
	for i, field := range s.headers {
		if selected[i] {
			fields, positions = append(fields, field), append(positions, i)
		}
	}
	qualifiedNames := make([]string, 0, len(qualified))
	for name := range qualified {
		qualifiedNames = append(qualifiedNames, name)
	}
	sort.Strings(qualifiedNames)
	fields = append(fields, qualifiedNames...)
	start := max(int64(0), index-liveAlertChartPreviousBars)
	chart := AnalysisChart{ResultID: alert.ConditionID, Name: alert.AssetName, Symbol: alert.Symbol, Timeframe: alert.Timeframe,
		Direction:    analysisChartDirection(a.request.TradeSimulation),
		TotalMatches: 1, MatchTime: alert.Time, MatchIndex: index, StartIndex: start,
		Fields: fields, Bars: make([]AnalysisBar, 0, index-start+1),
		Signal: &AnalysisSignalLevels{Entry: alert.EntryPrice, StopLoss: copyLiveAlertChartLevel(alert.StopLoss), TakeProfit: copyLiveAlertChartLevel(alert.TakeProfit)}}
	for i := start; i <= index; i++ {
		if err = ctx.Err(); err != nil {
			return AnalysisChart{}, err
		}
		row, e := s.row(i)
		if e != nil {
			return AnalysisChart{}, e
		}
		at := analysisClosedAt(row.timestamp, s.metadata.Timeframe)
		if at > a.asOf {
			return AnalysisChart{}, errors.New("checked candle is not fully closed")
		}
		if i == index && row.timestamp != alert.Time {
			return AnalysisChart{}, errors.New("checked candle does not match signal time")
		}
		values := make(map[string]float64, len(fields))
		for _, position := range positions {
			if value := row.values[position]; analysisFinite(value) {
				values[s.headers[position]] = value
			}
		}
		for _, name := range qualifiedNames {
			value, ok, e := qualified[name].resolve(i, at, 0, 0)
			if e != nil {
				return AnalysisChart{}, e
			}
			if ok && analysisFinite(value) {
				values[name] = value
			}
		}
		chart.Bars = append(chart.Bars, AnalysisBar{Index: i, Time: row.timestamp, Values: values})
	}
	for dependency := range dependencies {
		if err = analysisUnchanged(dependency); err != nil {
			return AnalysisChart{}, err
		}
	}
	if err = ctx.Err(); err != nil {
		return AnalysisChart{}, err
	}
	return chart, nil
}

func copyLiveAlertChartLevel(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
