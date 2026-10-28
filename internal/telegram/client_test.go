package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, result any) (*http.Response, error) {
	data, _ := json.Marshal(map[string]any{"ok": status == 200, "error_code": status, "description": "secret-provider-payload", "result": result})
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data)))}, nil
}
func botResponse() map[string]any {
	return map[string]any{"id": 10, "is_bot": true, "username": "TestCryptoDeskBot"}
}
func privateChat(id int64, name string) map[string]any {
	return map[string]any{"id": id, "type": "private", "username": name}
}
func setupClient(t *testing.T, handle func(string, map[string]any) (*http.Response, error)) *Store {
	t.Helper()
	store := NewStore(t.TempDir())
	store.client = newClient(fakeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.telegram.org" || r.Method != "POST" {
			t.Error("unexpected Telegram destination or method")
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		return handle(method, input)
	}))
	if _, err := store.Configure("123:test", "@neodix"); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestRecipientResolutionCachesPrivateIdentityAndChecksItAfterRestart(t *testing.T) {
	var methods []string
	sends := 0
	handle := func(method string, input map[string]any) (*http.Response, error) {
		methods = append(methods, method)
		switch method {
		case "getMe":
			return response(200, botResponse())
		case "getWebhookInfo":
			return response(200, map[string]any{"url": ""})
		case "getUpdates":
			if len(input) != 2 || input["limit"] != float64(100) || input["timeout"] != float64(0) {
				t.Error("recipient lookup acknowledged updates or changed filters")
			}
			return response(200, []any{map[string]any{"message": map[string]any{"chat": map[string]any{"id": 99, "type": "group", "username": "neodix"}}}, map[string]any{"message": map[string]any{"chat": privateChat(51, "someone_else")}}, map[string]any{"message": map[string]any{"chat": privateChat(42, "NeoDix"), "text": "/start"}}})
		case "getChat":
			if input["chat_id"] != float64(42) {
				t.Error("verified wrong recipient")
			}
			return response(200, privateChat(42, "neodix"))
		case "sendMessage":
			sends++
			if input["chat_id"] != float64(42) || input["text"] != TestMessage {
				t.Error("wrong test notification or recipient")
			}
			return response(200, map[string]any{"message_id": 7, "chat": privateChat(42, "neodix")})
		}
		t.Errorf("unexpected method %s", method)
		return response(500, nil)
	}
	store := setupClient(t, handle)
	result, err := store.Test(context.Background())
	if err != nil || !result.Sent || result.MessageID != 7 || result.BotUsername != "TestCryptoDeskBot" || sends != 1 {
		t.Fatalf("test delivery failed: %+v %v", result, err)
	}
	reopened := NewStore(filepath.Dir(store.path))
	reopened.client = store.client
	methods = nil
	result, err = reopened.Test(context.Background())
	if err != nil || !result.Sent || sends != 2 || strings.Join(methods, ",") != "getMe,getChat,sendMessage" {
		t.Fatalf("restart failed to reuse and verify private recipient: %v %+v %v", methods, result, err)
	}
	data, _ := os.ReadFile(store.path)
	if strings.Contains(string(data), "someone_else") || strings.Contains(string(data), "/start") {
		t.Fatal("stored unrelated Telegram updates")
	}
}

func TestTelegramTestFailuresNeverSendOrExposeProviderPayloads(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		webhook bool
		updates any
		code    string
	}{
		{"recipient has not started", 200, false, []any{}, "recipientNotFound"},
		{"group is not a private user", 200, false, []any{map[string]any{"message": map[string]any{"chat": map[string]any{"id": 42, "type": "group", "username": "neodix"}}}}, "recipientNotFound"},
		{"existing webhook", 200, true, nil, "webhook"},
		{"invalid token", 401, false, nil, "auth"},
		{"rate limit", 429, false, nil, "rateLimit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sends := 0
			updates := 0
			store := setupClient(t, func(method string, _ map[string]any) (*http.Response, error) {
				switch method {
				case "getMe":
					return response(test.status, botResponse())
				case "getWebhookInfo":
					url := ""
					if test.webhook {
						url = "https://existing.invalid/private-webhook"
					}
					return response(200, map[string]any{"url": url})
				case "getUpdates":
					updates++
					return response(200, test.updates)
				case "sendMessage":
					sends++
				}
				return response(500, nil)
			})
			result, err := store.Test(context.Background())
			if err != nil || result.Sent || result.Code != test.code || sends != 0 || test.webhook && updates != 0 {
				t.Fatalf("unsafe test result: %+v %v sends=%d", result, err, sends)
			}
		})
	}
	store := setupClient(t, func(string, map[string]any) (*http.Response, error) {
		return nil, errors.New("private token 123:test transport failure")
	})
	if _, err := store.BotIdentity(context.Background(), "123:test"); err == nil || strings.Contains(err.Error(), "123:test") {
		t.Fatal("transport error exposed credentials")
	}
}

func TestChangedUsernameCannotSendToCachedFormerOwner(t *testing.T) {
	store := setupClient(t, func(string, map[string]any) (*http.Response, error) { return response(500, nil) })
	store.mu.Lock()
	config, _ := store.readLocked()
	config.BotID, config.ChatID = 10, 42
	data, _ := json.Marshal(config)
	_ = store.writeLocked(data)
	store.mu.Unlock()
	sentTo := float64(0)
	store.client = newClient(fakeTransport(func(r *http.Request) (*http.Response, error) {
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		switch r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:] {
		case "getMe":
			return response(200, botResponse())
		case "getChat":
			id := int64(input["chat_id"].(float64))
			name := "neodix"
			if id == 42 {
				name = "former_owner"
			}
			return response(200, privateChat(id, name))
		case "getWebhookInfo":
			return response(200, map[string]any{"url": ""})
		case "getUpdates":
			return response(200, []any{map[string]any{"edited_message": map[string]any{"chat": privateChat(84, "neodix")}}})
		case "sendMessage":
			sentTo = input["chat_id"].(float64)
			return response(200, map[string]any{"message_id": 8, "chat": privateChat(84, "neodix")})
		}
		return response(500, nil)
	}))
	result, err := store.Test(context.Background())
	if err != nil || !result.Sent || sentTo != 84 {
		t.Fatalf("sent to former username owner: %+v %v", result, err)
	}
}

func TestTelegramTestSerializesDeliveryAndStopsIfSettingsChanged(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var sends atomic.Int32
	store := setupClient(t, func(method string, input map[string]any) (*http.Response, error) {
		switch method {
		case "getMe":
			close(started)
			<-release
			return response(200, botResponse())
		case "getChat":
			return response(200, privateChat(42, "neodix"))
		case "sendMessage":
			sends.Add(1)
			return response(200, map[string]any{"message_id": 7, "chat": privateChat(42, "neodix")})
		}
		return response(500, nil)
	})
	if _, err := store.Configure("123:test", "42"); err != nil {
		t.Fatal(err)
	}
	done := make(chan TestResult, 1)
	go func() { result, _ := store.Test(context.Background()); done <- result }()
	<-started
	if result, err := store.Test(context.Background()); err != nil || result.Code != "busy" {
		t.Fatal("overlapping test was not blocked", err)
	}
	if _, err := store.Configure("123:test", "43"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if result := <-done; result.Code != "changed" || result.Sent || sends.Load() != 0 {
		t.Fatal("changed settings still sent a message")
	}
}

func TestTelegramSendFailureDoesNotAutomaticallyRetry(t *testing.T) {
	for _, status := range []int{403, 429, 500} {
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
			return response(500, nil)
		})
		_, _ = store.Configure("123:test", "42")
		result, err := store.Test(context.Background())
		want := map[int]string{403: "blocked", 429: "rateLimit", 500: "uncertain"}[status]
		if err != nil || result.Sent || result.Code != want || sends != 1 {
			t.Fatalf("send failure retried or misreported: %+v %v sends=%d", result, err, sends)
		}
	}
}

func TestTelegramClientDoesNotFollowRedirectsOrExposeMalformedBodies(t *testing.T) {
	calls := 0
	c := newClient(fakeTransport(func(*http.Request) (*http.Response, error) {
		calls++
		r, _ := response(302, nil)
		r.Header.Set("Location", "https://elsewhere.invalid/secret")
		return r, nil
	}))
	var result any
	if err := c.call(context.Background(), "123:secret", "sendMessage", struct{}{}, &result); err == nil || strings.Contains(err.Error(), "secret") || calls != 1 {
		t.Fatal("redirect followed or secret exposed")
	}
	c = newClient(fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 2*1024*1024+2)))}, nil
	}))
	if err := c.call(context.Background(), "123:secret", "getMe", struct{}{}, &result); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("oversized provider response accepted or exposed")
	}
	c = newClient(fakeTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, errors.New("secret timeout")
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.call(ctx, "123:secret", "sendMessage", struct{}{}, &result)
	var failure *operationError
	if !errors.As(err, &failure) || failure.code != "uncertain" || strings.Contains(err.Error(), "secret") {
		t.Fatal("uncertain send was exposed or misreported")
	}
}
