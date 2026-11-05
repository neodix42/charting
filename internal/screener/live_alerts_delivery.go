package screener

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Keep the screener independent of a notification provider's concrete types.
// Full success counts one alert; an incomplete batch counts once only when a
// recipient's delivery was confirmed. Failure and no-resend handling remain.
func confirmedAlertRecipientCount(err error) int {
	var result interface{ ConfirmedRecipients() int }
	if errors.As(err, &result) {
		if count := result.ConfirmedRecipients(); count > 0 && count <= 20 {
			return count
		}
	}
	return 0
}

func confirmedAlertDelivery(err error) bool { return confirmedAlertRecipientCount(err) > 0 }

// Prior versions persisted the provider's confirmed count only in this exact
// generated message, while treating the incomplete batch as zero sent alerts.
// Recover only retained, uncounted outcomes that can be tied to one real saved
// condition and its exact delivery watermark. Renamed/ambiguous history remains.
var legacyPartialDelivery = regexp.MustCompile(`^Telegram alert delivery: ([1-9][0-9]?) of ([1-9][0-9]?) recipients confirmed; no automatic resend: Telegram operation failed \([a-zA-Z]+\)\. This bar will not be resent automatically; check Telegram before any manual action\.$`)

func (l *LiveAlerts) recoverLegacyPartialDeliveries() error {
	candidate := false
	for _, outcome := range l.state.Recent {
		if (outcome.DataSource == "" || outcome.DataSource == "dukascopy") && outcome.ConfirmedRecipients == 0 && (outcome.Status == "failed" || outcome.Status == "uncertain") && legacyPartialDelivery.MatchString(outcome.Message) {
			candidate = true
			break
		}
	}
	if !candidate {
		return nil
	}
	l.manager.mu.Lock()
	conditions, err := l.manager.readSavedConditions()
	l.manager.mu.Unlock()
	if err != nil {
		return nil // Unavailable saved conditions cannot justify guessing or changing active alerts.
	}
	byName := map[string][]string{}
	for _, condition := range conditions {
		byName[condition.Name] = append(byName[condition.Name], condition.ID)
	}
	seen := map[string]bool{}
	for _, outcome := range l.state.Recent {
		if outcome.ConfirmedRecipients <= 0 || (outcome.DataSource != "" && outcome.DataSource != "dukascopy") {
			continue
		}
		if ids := byName[outcome.ConditionName]; len(ids) == 1 {
			seen[ids[0]+"/"+outcome.Symbol+"/"+outcome.Timeframe+"/"+strconv.FormatInt(outcome.Time, 10)] = true
		}
	}
	previous := l.state
	l.state.Recent = append([]LiveAlertOutcome(nil), l.state.Recent...)
	l.state.Checks = copyLiveCheckStates(l.state.Checks)
	changed := false
	for i := range l.state.Recent {
		outcome := &l.state.Recent[i]
		if (outcome.DataSource != "" && outcome.DataSource != "dukascopy") || outcome.ConfirmedRecipients > 0 || (outcome.Status != "failed" && outcome.Status != "uncertain") {
			continue
		}
		match := legacyPartialDelivery.FindStringSubmatch(outcome.Message)
		if len(match) != 3 {
			continue
		}
		confirmed, _ := strconv.Atoi(match[1])
		total, _ := strconv.Atoi(match[2])
		if confirmed >= total || total > 20 {
			continue
		}
		ids := byName[outcome.ConditionName]
		if len(ids) != 1 {
			continue
		}
		id := ids[0]
		key := id + "/" + outcome.Symbol + "/" + outcome.Timeframe
		identity := key + "/" + strconv.FormatInt(outcome.Time, 10)
		if l.state.Watermarks[key] != outcome.Time || outcome.Time <= 0 || seen[identity] {
			continue
		}
		if outcome.ConditionID != "" && outcome.ConditionID != id {
			continue
		}
		if l.state.Sent == math.MaxInt64 {
			continue
		}
		seen[identity] = true
		outcome.ConfirmedRecipients = confirmed
		outcome.ConditionID = id
		l.state.Sent++
		if _, exists := l.state.Checks[id]; exists {
			l.recordConfirmedAlertLocked(id)
		}
		changed = true
	}
	if changed {
		if err := writeState(l.path, l.state); err != nil {
			l.state = previous
			return fmt.Errorf("recover confirmed partial Live Alerts deliveries: %w", err)
		}
	}
	return nil
}
