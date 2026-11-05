package screener

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

type confirmedDeliveryTestError struct{ count int }

func (e confirmedDeliveryTestError) Error() string {
	return "Telegram partial failure; no automatic resend"
}
func (e confirmedDeliveryTestError) ConfirmedRecipients() int { return e.count }

func TestLiveAlertsPartiallyConfirmedBatchCountsOneAlertAndKeepsFailure(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			m, l, config, _ := liveAlertsFixture(t)
			id := config.ConditionIDs[0]
			run, err := l.beginCheckHistory(config, false)
			if err != nil {
				t.Fatal(err)
			}
			l.send = func(context.Context, LiveAlert) error {
				return fmt.Errorf("wrapped: %w", confirmedDeliveryTestError{count})
			}
			l.state.Recent = []LiveAlertOutcome{{ConditionID: id, ConditionName: "Latest close positive", Symbol: "eurusd", Timeframe: "h1", Time: 1704070800000, Status: "claimed"}}
			l.state.Watermarks[id+"/eurusd/h1"] = 1704070800000
			err = l.deliver(context.Background(), LiveAlert{ConditionID: id, ConditionName: "Latest close positive", Symbol: "eurusd", Timeframe: "h1", Time: 1704070800000})
			if err == nil {
				t.Fatal("partial failure was hidden")
			}
			run.failed(id, err)
			if err = run.finish(err); err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if count > 0 {
				want = 1
			}
			if l.state.Sent != want || l.state.Checks[id].SentAlerts != want || l.state.Checks[id].Status != "failed" || l.state.Recent[0].Status != "failed" || l.state.Recent[0].ConfirmedRecipients != count {
				t.Fatalf("partial result counted recipients or lost failure %+v", l.state)
			}
			restored, err := NewLiveAlerts(m, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if restored.state.Sent != want || restored.state.Checks[id].SentAlerts != want || restored.state.Watermarks[id+"/eurusd/h1"] != 1704070800000 {
				t.Fatal("restart changed confirmation count or delivery claim")
			}
		})
	}
}

func TestLiveAlertsLegacyPartialRecoveryPreservesClaimsAndIsIdempotent(t *testing.T) {
	m, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	l.state.Sent = 1050
	l.state.Checks = map[string]liveAlertCheckState{id: {LastChecked: 1793752781528, SentAlerts: 0, Status: "failed", AssetsScanned: 53, Error: "Retain partial failure"}}
	l.state.Recent = []LiveAlertOutcome{{ConditionName: "Latest close positive", Symbol: "eurusd", Timeframe: "d1", Time: 1793664000000, Status: "failed", Message: "Telegram alert delivery: 1 of 2 recipients confirmed; no automatic resend: Telegram operation failed (recipientNotFound). This bar will not be resent automatically; check Telegram before any manual action."}}
	l.state.Watermarks = map[string]int64{id + "/eurusd/d1": 1793664000000}
	beforeConfig, beforeMarks, beforeCheck := copyLiveAlertsConfig(l.state.Config), copyWatermarksForDeliveryTest(l.state.Watermarks), l.state.Checks[id]
	if err := writeState(l.path, l.state); err != nil {
		t.Fatal(err)
	}
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if restored.state.Sent != 1051 || restored.state.Checks[id].SentAlerts != 1 || restored.state.Recent[0].ConfirmedRecipients != 1 || restored.state.Recent[0].Status != "failed" {
		t.Fatalf("explicit confirmation was not recovered %+v", restored.state)
	}
	check := restored.state.Checks[id]
	check.SentAlerts = beforeCheck.SentAlerts
	if !reflect.DeepEqual(restored.state.Config, beforeConfig) || !reflect.DeepEqual(restored.state.Watermarks, beforeMarks) || check != beforeCheck {
		t.Fatal("recovery changed active config, claims or check failure")
	}
	encodedBefore, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		again, err := NewLiveAlerts(m, nil)
		if err != nil {
			t.Fatal(err)
		}
		again.Close()
		if again.state.Sent != 1051 || again.state.Checks[id].SentAlerts != 1 {
			t.Fatal("recovery counted partial twice")
		}
	}
	encodedAfter, _ := os.ReadFile(l.path)
	if !reflect.DeepEqual(encodedBefore, encodedAfter) {
		t.Fatal("idempotent recovery rewrote saved state")
	}
}

func copyWatermarksForDeliveryTest(in map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func TestLiveAlertsLegacyPartialRecoveryRequiresExactEvidence(t *testing.T) {
	for _, variant := range []string{"unknown-name", "newer-watermark", "no-watermark", "no-confirmation", "zero-confirmed", "sent-status", "already-counted"} {
		t.Run(variant, func(t *testing.T) {
			_, l, config, _ := liveAlertsFixture(t)
			id := config.ConditionIDs[0]
			l.state.Sent = 5
			outcome := LiveAlertOutcome{ConditionName: "Latest close positive", Symbol: "eurusd", Timeframe: "d1", Time: 1793664000000, Status: "failed", Message: "Telegram alert delivery: 1 of 2 recipients confirmed; no automatic resend: Telegram operation failed (recipientNotFound). This bar will not be resent automatically; check Telegram before any manual action."}
			l.state.Watermarks = map[string]int64{id + "/eurusd/d1": outcome.Time}
			switch variant {
			case "unknown-name":
				outcome.ConditionName = "Renamed"
			case "newer-watermark":
				l.state.Watermarks[id+"/eurusd/d1"]++
			case "no-watermark":
				l.state.Watermarks = map[string]int64{}
			case "no-confirmation":
				outcome.Message = "Telegram delivery uncertain"
			case "zero-confirmed":
				outcome.Message = "Telegram alert delivery: 0 of 2 recipients confirmed; no automatic resend: Telegram operation failed (recipientNotFound). This bar will not be resent automatically; check Telegram before any manual action."
			case "sent-status":
				outcome.Status = "sent"
			case "already-counted":
				outcome.ConfirmedRecipients = 1
			}
			l.state.Recent = []LiveAlertOutcome{outcome}
			if err := l.recoverLegacyPartialDeliveries(); err != nil {
				t.Fatal(err)
			}
			if l.state.Sent != 5 {
				t.Fatal("guessed a confirmation from incomplete evidence", variant)
			}
		})
	}
}

func TestLiveAlertsLegacyPartialRecoveryRollsBackOnSaveFailure(t *testing.T) {
	_, l, config, _ := liveAlertsFixture(t)
	id := config.ConditionIDs[0]
	l.state.Recent = []LiveAlertOutcome{{ConditionName: "Latest close positive", Symbol: "eurusd", Timeframe: "h1", Time: time.Now().UnixMilli(), Status: "failed", Message: "Telegram alert delivery: 1 of 2 recipients confirmed; no automatic resend: Telegram operation failed (recipientNotFound). This bar will not be resent automatically; check Telegram before any manual action."}}
	l.state.Watermarks[id+"/eurusd/h1"] = l.state.Recent[0].Time
	l.path = t.TempDir()
	if err := l.recoverLegacyPartialDeliveries(); err == nil || l.state.Sent != 0 || l.state.Recent[0].ConfirmedRecipients != 0 {
		t.Fatal("failed recovery write changed in-memory totals", err)
	}
	if confirmedAlertDelivery(errors.New("1 of 2 recipients confirmed")) {
		t.Fatal("live counting trusted arbitrary error text")
	}
}
