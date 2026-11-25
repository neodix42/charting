package screener

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"cryptodesk/internal/exchange"
)

func TestPublicMissingCandleStatusKeepsRetryPolicyAndDistinctSourceFailures(t *testing.T) {
	end := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC).Unix()
	missing := publicAlertMissingCandleError(end, 86400)
	var typed *publicAlertMissingCandleIssue
	if !errors.As(missing, &typed) || typed.From != end-86400 || typed.To != end || !errors.Is(missing, errLiveAlertsPendingBar) || liveAlertRetryWindow(missing, "d1") != 24*time.Hour {
		t.Fatal("missing close changed its requested interval or retry classification", missing)
	}
	for _, item := range []struct {
		name string
		err  error
		want bool
	}{
		{"explicit missing", missing, true},
		{"wrapped missing", fmt.Errorf("@698 d1: %w", missing), true},
		{"joined missing", errors.Join(missing, fmt.Errorf("@300: %w", missing)), true},
		{"generic pending or open bar", errLiveAlertsPendingBar, false},
		{"provisional", fmt.Errorf("%w; latest public candle is provisional", errLiveAlertsPendingBar), false},
		{"warm-up", &publicAlertInsufficientHistory{Required: 27, Available: 22, Period: "d1"}, false},
		{"transport", &exchange.Error{Kind: "timeout", Message: "provider timed out"}, false},
		{"mixed transport", errors.Join(missing, &exchange.Error{Kind: "timeout", Message: "provider timed out"}), false},
		{"mixed warm-up", errors.Join(missing, &publicAlertInsufficientHistory{Required: 27, Available: 22, Period: "d1"}), false},
		{"nil", nil, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			if got := publicAlertMissingClosedCandle(item.err); got != item.want {
				t.Fatalf("wrong missing-candle status: got=%v want=%v error=%v", got, item.want, item.err)
			}
		})
	}
}
