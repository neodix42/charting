package screener

import (
	"context"
	"cryptodesk/internal/telegram"
	"errors"
	"testing"
)

func TestLiveAlertsUnconfirmedDeliveryCannotBecomeCurrentCheckSentCount(t *testing.T) {
	for _, code := range []string{"auth", "recipientNotFound", "blocked", "rateLimit", "uncertain", "chartUnavailable", "nil-sender", "cancelled"} {
		t.Run(code, func(t *testing.T) {
			m, l, config, _ := liveAlertsFixture(t)
			id := config.ConditionIDs[0]
			// Historical confirmations are real retained accounting, not the
			// result of this fresh check or permission to resend their candles.
			l.state.Sent = 37
			l.state.Checks = map[string]liveAlertCheckState{id: {SentAlerts: 37, LastChecked: l.now().UnixMilli(), Status: "completed"}}
			run, err := l.beginCheckHistory(config, true)
			if err != nil {
				t.Fatal(err)
			}
			const timestamp = int64(1704070800000)
			l.state.Recent = []LiveAlertOutcome{{ConditionID: id, ConditionName: "Latest close positive", Symbol: "eurusd", Timeframe: "h1", Time: timestamp, Status: "claimed"}}
			claim := id + "/eurusd/h1"
			l.state.Watermarks[claim] = timestamp
			calls := 0
			l.send = func(context.Context, LiveAlert) error {
				calls++
				// This is the notifier's production error type: no private-chat
				// message was acknowledged, regardless of the sanitized reason.
				return &telegram.DeliveryError{ConfirmedCount: 0, TotalRecipients: 1, Code: code}
			}
			ctx := context.Background()
			wantCalls := 1
			if code == "nil-sender" {
				l.send, wantCalls = nil, 0
			} else if code == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx, wantCalls = cancelled, 0
			}
			err = l.deliver(ctx, LiveAlert{ConditionID: id, ConditionName: "Latest close positive", Symbol: "eurusd", Timeframe: "h1", Time: timestamp})
			if err == nil || calls != wantCalls {
				t.Fatal("unconfirmed send disappeared or was retried", err, calls)
			}
			run.failed(id, err)
			if finishErr := run.finish(err); finishErr != nil {
				t.Fatal(finishErr)
			}
			check := func(l *LiveAlerts) {
				t.Helper()
				info := l.Info()
				if len(info.Checks) != 1 || !info.Checks[0].SentAlertsKnown || info.Checks[0].SentAlerts != 0 || info.Checks[0].LifetimeSentAlerts != 37 || info.Sent != 37 || l.state.Checks[id].SentAlerts != 37 {
					t.Fatalf("historical or unconfirmed deliveries became current sent alerts: %+v", info.Checks)
				}
				if l.state.Recent[0].Status == "sent" || l.state.Recent[0].ConfirmedRecipients != 0 || l.state.Watermarks[claim] != timestamp {
					t.Fatal("unconfirmed result claimed success or released its no-resend watermark", l.state.Recent)
				}
			}
			check(l)
			restored, err := NewLiveAlerts(m, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			check(restored)
			if code == "cancelled" && !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("cancellation fixture did not cancel")
			}
		})
	}
}
