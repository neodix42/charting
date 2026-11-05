package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAlertDeliveryUsesVerifiedPrivateRecipientAndPlainText(t *testing.T) {
	message := "CryptoDesk Live Alert\nAsset: EURUSD — Euro vs US Dollar\nCondition: Close < SMA & RSI > 40\nEntry: 1.1\nSL: 1.0\nTP: 1.3"
	sends := 0
	store := setupClient(t, func(method string, input map[string]any) (*http.Response, error) {
		switch method {
		case "getMe":
			return response(200, botResponse())
		case "getChat":
			return response(200, privateChat(42, "neodix"))
		case "sendMessage":
			sends++
			if len(input) != 2 || input["chat_id"] != float64(42) || input["text"] != message {
				t.Fatalf("alert changed recipient, text or enabled formatting: %v", input)
			}
			return response(200, map[string]any{"message_id": 9, "chat": privateChat(42, "neodix")})
		default:
			t.Fatalf("unexpected method %s", method)
			return nil, nil
		}
	})
	if _, err := store.Configure("123:test", "42"); err != nil {
		t.Fatal(err)
	}
	if err := store.SendAlert(context.Background(), message); err != nil || sends != 1 {
		t.Fatalf("alert delivery failed: %v (sends %d)", err, sends)
	}
}

func TestAlertSendFailuresAreSanitizedAndNeverRetried(t *testing.T) {
	for _, status := range []int{403, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			sends := 0
			store := setupClient(t, func(method string, _ map[string]any) (*http.Response, error) {
				switch method {
				case "getMe":
					return response(200, botResponse())
				case "getChat":
					return response(200, privateChat(42, "neodix"))
				case "sendMessage":
					sends++
					return response(status, nil)
				}
				t.Fatalf("unexpected method %s", method)
				return nil, nil
			})
			_, _ = store.Configure("123:test", "42")
			err := store.SendAlert(context.Background(), "One alert")
			if err == nil || sends != 1 || strings.Contains(err.Error(), "123:test") || strings.Contains(err.Error(), "secret-provider") {
				t.Fatalf("unsafe delivery error: %v (sends %d)", err, sends)
			}
		})
	}
}

func TestAlertRejectsInvalidTextBeforeAnyTelegramCall(t *testing.T) {
	store := setupClient(t, func(string, map[string]any) (*http.Response, error) {
		t.Fatal("invalid message contacted Telegram")
		return nil, nil
	})
	for _, message := range []string{"", " \n\t", strings.Repeat("x", 4097), string([]byte{0xff})} {
		if store.SendAlert(context.Background(), message) == nil {
			t.Fatal("accepted invalid message")
		}
	}
}

func TestAlertPacingIsCancellableBeforeSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sends := 0
	store := setupClient(t, func(method string, _ map[string]any) (*http.Response, error) {
		switch method {
		case "getMe":
			return response(200, botResponse())
		case "getChat":
			cancel()
			return response(200, privateChat(42, "neodix"))
		case "sendMessage":
			sends++
		}
		return nil, errors.New("unexpected Telegram call")
	})
	_, _ = store.Configure("123:test", "42")
	store.nextSend = time.Now().Add(time.Hour)
	if err := store.SendAlert(ctx, "Cancelled alert"); err == nil || sends != 0 {
		t.Fatalf("cancelled pacing delivered a message: %v, sends=%d", err, sends)
	}
}

func TestAlertWaitsForExplicitTestInsteadOfDroppingItsDelivery(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	var messages []string
	store := setupClient(t, func(method string, input map[string]any) (*http.Response, error) {
		switch method {
		case "getMe":
			first.Do(func() { close(entered); <-release })
			return response(200, botResponse())
		case "getChat":
			return response(200, privateChat(42, "neodix"))
		case "sendMessage":
			messages = append(messages, input["text"].(string))
			return response(200, map[string]any{"message_id": len(messages), "chat": privateChat(42, "neodix")})
		}
		t.Errorf("unexpected method %s", method)
		return response(500, nil)
	})
	_, _ = store.Configure("123:test", "42")
	testDone := make(chan TestResult, 1)
	go func() { result, _ := store.Test(context.Background()); testDone <- result }()
	<-entered
	alertDone := make(chan error, 1)
	go func() { alertDone <- store.SendAlert(context.Background(), "Queued signal") }()
	close(release)
	if result := <-testDone; !result.Sent {
		t.Fatal("explicit Test failed", result)
	}
	if err := <-alertDone; err != nil {
		t.Fatal("queued alert was dropped", err)
	}
	if len(messages) != 2 || messages[0] != TestMessage || messages[1] != "Queued signal" {
		t.Fatal("lost or duplicated a serialized message", messages)
	}
}

func TestAlertWaitingForAnotherSendHonorsCancellation(t *testing.T) {
	store := setupClient(t, func(string, map[string]any) (*http.Response, error) {
		t.Error("cancelled queued alert contacted Telegram")
		return response(500, nil)
	})
	store.sendGate <- struct{}{}
	defer func() { <-store.sendGate }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.SendAlert(ctx, "Cancelled queued signal"); err == nil {
		t.Fatal("cancelled alert acquired an occupied send slot")
	}
}

func TestAlertPartialDeliveryRetainsConfirmedCountAndSanitizedFailure(t *testing.T) {
	for _, failedFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("failedFirst=%v", failedFirst), func(t *testing.T) {
			sends := 0
			var store *Store
			store = setupClient(t, func(method string, input map[string]any) (*http.Response, error) {
				switch method {
				case "getMe":
					return response(200, botResponse())
				case "getChat":
					id := int64(input["chat_id"].(float64))
					if id == 43 {
						return response(400, nil)
					}
					return response(200, privateChat(id, "neodix"))
				case "sendMessage":
					sends++
					store.nextSend = time.Time{}
					return response(200, map[string]any{"message_id": 9, "chat": privateChat(42, "neodix")})
				}
				t.Fatalf("unexpected method %s", method)
				return nil, nil
			})
			recipients := "42, 43"
			if failedFirst {
				recipients = "43, 42"
			}
			_, _ = store.Configure("123:test", recipients)
			err := store.SendAlert(context.Background(), "One alert")
			var result *DeliveryError
			if !errors.As(err, &result) || result.ConfirmedRecipients() != 1 || result.TotalRecipients != 2 || result.Code != "recipientNotFound" || sends != 1 {
				t.Fatalf("partial confirmation lost: %v sends%d", err, sends)
			}
			if strings.Contains(err.Error(), "123:test") || !strings.Contains(err.Error(), "1 of 2 recipients confirmed") || !strings.Contains(err.Error(), "no automatic resend") {
				t.Fatalf("unsafe or misleading error %v", err)
			}
			wrapped := fmt.Errorf("outer context: %w", err)
			var counted interface{ ConfirmedRecipients() int }
			if !errors.As(wrapped, &counted) || counted.ConfirmedRecipients() != 1 {
				t.Fatal("wrapping discarded confirmation")
			}
		})
	}
}

func TestAlertUnconfirmedRecipientBatchDoesNotClaimDelivery(t *testing.T) {
	store := setupClient(t, func(method string, _ map[string]any) (*http.Response, error) {
		if method == "getMe" {
			return response(200, botResponse())
		}
		if method == "getChat" {
			return response(403, nil)
		}
		t.Fatalf("failed recipients sent a notification: %s", method)
		return nil, nil
	})
	_, _ = store.Configure("123:test", "42, 43")
	err := store.SendAlert(context.Background(), "One alert")
	var result *DeliveryError
	if !errors.As(err, &result) || result.ConfirmedRecipients() != 0 || result.TotalRecipients != 2 {
		t.Fatalf("unconfirmed batch counted: %v", err)
	}
}
