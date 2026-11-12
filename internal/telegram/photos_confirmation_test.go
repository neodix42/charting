package telegram

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestPhotoHTTP200WithoutVerifiedMessageIsNeverConfirmedOrResent(t *testing.T) {
	for _, name := range []string{"missing-message-id", "zero-message-id", "negative-message-id", "missing-chat", "wrong-chat-type", "empty-photo-id", "zero-photo-dimensions"} {
		t.Run(name, func(t *testing.T) {
			writes := 0
			store := setupPhotoClient(t, func(input photoRequest) (*http.Response, error) {
				if !isWrite(input.method) {
					return nil, nil
				}
				writes++
				if input.method != "sendPhoto" {
					t.Fatal("an unconfirmed photo retried as text", input.method)
				}
				result := photoResult(42)
				switch name {
				case "missing-message-id":
					delete(result, "message_id")
				case "zero-message-id":
					result["message_id"] = 0
				case "negative-message-id":
					result["message_id"] = -7
				case "missing-chat":
					delete(result, "chat")
				case "wrong-chat-type":
					result["chat"] = map[string]any{"id": 42, "type": "channel"}
				case "empty-photo-id":
					result["photo"] = []any{map[string]any{"file_id": "", "width": 4, "height": 3}}
				case "zero-photo-dimensions":
					result["photo"] = []any{map[string]any{"file_id": "photo", "width": 0, "height": 3}}
				}
				// The transport reports HTTP200 and Telegram ok=true. Neither is
				// sufficient evidence without a real acknowledged private message.
				return response(http.StatusOK, result)
			})
			err := store.SendAlertPhoto(context.Background(), "Signal which must not be counted", testPNG(t))
			var delivery *DeliveryError
			if !errors.As(err, &delivery) || delivery.Code != "uncertain" || delivery.ConfirmedRecipients() != 0 || delivery.TotalRecipients != 1 || writes != 1 {
				t.Fatalf("incomplete acknowledgement became success/retry: error=%v writes=%d", err, writes)
			}
		})
	}
}
