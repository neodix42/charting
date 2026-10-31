package screener

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Calendar fields describe the signal bar's UTC OPEN, while notifications can
// only inspect that bar after its CLOSE. Unknown price/history predicates do
// not narrow this schedule: calendar clauses are necessary, not sufficient,
// conditions for a signal.
type liveAlertCalendarSchedule struct {
	allowed     [7 * 24]bool
	constrained bool
}

// nextLiveAlertConditionClose returns the first eligible natural close strictly
// after after. A complete UTC week bounds the search, including impossible AND
// clauses. False means that the period is unsupported/out of scope or that no
// bar opening in the week can satisfy the safely inferred calendar clauses.
func nextLiveAlertConditionClose(after time.Time, saved SavedAnalysisCondition, period string) (time.Time, bool) {
	interval, count, supported := liveAlertCalendarPeriod(saved, period)
	if !supported {
		return time.Time{}, false
	}
	schedule := liveAlertCalendar(saved.Request)
	close := after.UTC().Truncate(interval).Add(interval)
	for range count {
		if schedule.allows(close.Add(-interval)) {
			return close, true
		}
		close = close.Add(interval)
	}
	return time.Time{}, false
}

// liveAlertConditionDueAt checks the latest naturally closed bar at cutoff.
// Keeping the original cutoff across publication retries keeps its eligibility
// stable. Explicit Check now must bypass this scheduling gate and evaluate the
// latest actual closed data normally.
func liveAlertConditionDueAt(saved SavedAnalysisCondition, period string, cutoff time.Time) bool {
	interval, _, supported := liveAlertCalendarPeriod(saved, period)
	if !supported {
		return false
	}
	return liveAlertCalendar(saved.Request).allows(cutoff.UTC().Truncate(interval).Add(-interval))
}

func liveAlertCalendarPeriod(saved SavedAnalysisCondition, period string) (time.Duration, int, bool) {
	if !includesAnalysisPeriod(saved.Request.Period, period) {
		return 0, 0, false
	}
	switch period {
	case "h1":
		return time.Hour, 7 * 24, true
	case "d1":
		return 24 * time.Hour, 7, true
	default:
		return 0, 0, false
	}
}

func (schedule liveAlertCalendarSchedule) allows(open time.Time) bool {
	open = open.UTC()
	// Enrichment uses ISO weekday numbering: Monday=1, Sunday=7.
	weekday := (int(open.Weekday()) + 6) % 7
	return schedule.allowed[weekday*24+open.Hour()]
}

func liveAlertCalendar(request AnalysisRequest) liveAlertCalendarSchedule {
	schedule := liveAlertCalendarSchedule{}
	for i := range schedule.allowed {
		schedule.allowed[i] = true
	}
	for _, condition := range request.Conditions {
		// Inspect support separately from the truth value. An unsupported clause
		// cannot exclude any bar, including an offset/window/temporal calendar
		// clause that may match on days other than its apparent current value.
		_, supported := liveAlertCalendarComparison(condition, 1, 0)
		if !supported {
			continue
		}
		schedule.constrained = true
		for slot := range schedule.allowed {
			if schedule.allowed[slot] {
				matched, _ := liveAlertCalendarComparison(condition, slot/24+1, slot%24)
				schedule.allowed[slot] = matched
			}
		}
	}
	return schedule
}

func liveAlertCalendarOperand(operand AnalysisOperand, weekday, hour int) (value float64, known, calendar bool) {
	if operand.Kind == "number" {
		return operand.Value, analysisFinite(operand.Value), false
	}
	if operand.Kind != "field" || operand.Offset != 0 || operand.Timeframe != "" && operand.Timeframe != "same" || operand.Aggregate != "" && operand.Aggregate != "value" || operand.Window > 1 {
		return 0, false, false
	}
	switch strings.ToLower(strings.TrimSpace(operand.Field)) {
	case "weekday":
		return float64(weekday), true, true
	case "hour":
		return float64(hour), true, true
	default:
		return 0, false, false
	}
}

func liveAlertCalendarComparison(condition AnalysisCondition, weekday, hour int) (matched, supported bool) {
	switch condition.Operator {
	case "above", "below", "equal", "between", "withinDistance":
	default:
		return false, false
	}
	left, leftKnown, leftCalendar := liveAlertCalendarOperand(condition.Left, weekday, hour)
	right, rightKnown, rightCalendar := liveAlertCalendarOperand(condition.Right, weekday, hour)
	if !leftKnown || !rightKnown {
		return false, false
	}
	hasCalendar := leftCalendar || rightCalendar
	switch condition.Operator {
	case "above":
		return left > right, hasCalendar
	case "below":
		return left < right, hasCalendar
	case "equal":
		return left == right, hasCalendar
	case "between":
		upper, known, calendar := liveAlertCalendarOperand(condition.Upper, weekday, hour)
		return known && left >= right && left <= upper, known && (hasCalendar || calendar)
	case "withinDistance":
		distance, known, calendar := liveAlertCalendarOperand(condition.Distance, weekday, hour)
		if !known || !analysisFinite(condition.Multiplier) || condition.Multiplier < 0 {
			return false, false
		}
		difference, threshold := math.Abs(left-right), condition.Multiplier*math.Abs(distance)
		// The production comparator reports overflow as an error, so an
		// overflowing deterministic clause cannot produce a positive signal.
		return analysisFinite(difference) && analysisFinite(threshold) && difference <= threshold, hasCalendar || calendar
	}
	return false, false
}

// The UI renders this authoritative hint without duplicating calendar logic.
func liveAlertConditionTimingHint(saved SavedAnalysisCondition, period string, nextClose time.Time) string {
	interval, _, supported := liveAlertCalendarPeriod(saved, period)
	schedule := liveAlertCalendar(saved.Request)
	if !supported || !schedule.constrained {
		return ""
	}
	if nextClose.IsZero() {
		// Disabled monitoring also has no next check. Describe impossibility
		// only when the calendar itself admits no opening for this period.
		for slot, allowed := range schedule.allowed {
			if allowed && (period == "h1" || slot%24 == 0) {
				return ""
			}
		}
		return fmt.Sprintf("The saved UTC weekday/hour conditions cannot match a %s signal bar.", strings.ToUpper(period))
	}
	close := nextClose.UTC()
	return fmt.Sprintf("Next eligible signal bar: %s → %s.", close.Add(-interval).Format("Monday 15:04 UTC"), close.Format("Monday 15:04 UTC"))
}
