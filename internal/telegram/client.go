package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const TestMessage = "CryptoDesk test notification: Telegram delivery is working. Automatic alerts are not enabled yet."

type BotIdentity struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type TestResult struct {
	Sent        bool   `json:"sent"`
	Code        string `json:"code,omitempty"`
	BotUsername string `json:"botUsername,omitempty"`
	MessageID   int64  `json:"messageId,omitempty"`
}

type operationError struct{ code string }

func (e *operationError) Error() string { return "Telegram operation failed (" + e.code + ")" }
func fail(code string) error            { return &operationError{code} }

type client struct{ http *http.Client }

func newClient(transport http.RoundTripper) *client {
	return &client{http: &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *client) call(ctx context.Context, token, method string, input, output any) error {
	if token == "" {
		return fail("auth")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return fail("response")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+url.PathEscape(token)+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fail("auth")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		if method == "sendMessage" {
			return fail("uncertain")
		}
		return fail("network")
	}
	defer response.Body.Close()
	var envelope struct {
		OK        bool            `json:"ok"`
		Result    json.RawMessage `json:"result"`
		ErrorCode int             `json:"error_code"`
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 || json.Unmarshal(data, &envelope) != nil {
		if method == "sendMessage" {
			return fail("uncertain")
		}
		return fail("response")
	}
	if response.StatusCode != http.StatusOK || !envelope.OK {
		status := response.StatusCode
		if envelope.ErrorCode != 0 {
			status = envelope.ErrorCode
		}
		switch status {
		case 401:
			return fail("auth")
		case 403:
			return fail("blocked")
		case 429:
			return fail("rateLimit")
		case 409:
			return fail("webhook")
		case 400:
			if method == "getMe" {
				return fail("auth")
			}
			return fail("recipientNotFound")
		}
		if method == "sendMessage" {
			return fail("uncertain")
		}
		return fail("response")
	}
	if json.Unmarshal(envelope.Result, output) != nil {
		if method == "sendMessage" {
			return fail("uncertain")
		}
		return fail("response")
	}
	return nil
}

func (s *Store) BotIdentity(ctx context.Context, token string) (BotIdentity, error) {
	token, err := normalize(token)
	if err != nil {
		return BotIdentity{}, fail("auth")
	}
	var bot struct {
		BotIdentity
		IsBot bool `json:"is_bot"`
	}
	if err = s.client.call(ctx, token, "getMe", struct{}{}, &bot); err != nil {
		return BotIdentity{}, err
	}
	if !bot.IsBot || bot.ID <= 0 || !usernamePattern.MatchString(bot.Username) {
		return BotIdentity{}, fail("response")
	}
	return bot.BotIdentity, nil
}

type chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Username string `json:"username"`
}

func (s *Store) verifyChat(ctx context.Context, config configuration, id int64) error {
	var target chat
	if err := s.client.call(ctx, config.APIToken, "getChat", map[string]any{"chat_id": id}, &target); err != nil {
		return err
	}
	if target.ID != id || target.Type != "private" || strings.HasPrefix(config.Recipient, "@") && !strings.EqualFold(target.Username, config.Recipient[1:]) {
		return fail("recipientNotFound")
	}
	return nil
}

func (s *Store) resolve(ctx context.Context, config configuration, bot BotIdentity) (int64, error) {
	if !strings.HasPrefix(config.Recipient, "@") {
		var id int64
		if json.Unmarshal([]byte(config.Recipient), &id) != nil {
			return 0, fail("recipientNotFound")
		}
		return id, s.verifyChat(ctx, config, id)
	}
	if config.ChatID > 0 && config.BotID == bot.ID {
		if err := s.verifyChat(ctx, config, config.ChatID); err == nil {
			return config.ChatID, nil
		} else {
			var operation *operationError
			if !errors.As(err, &operation) || operation.code != "recipientNotFound" {
				return 0, err
			}
		}
	}
	var webhook struct {
		URL string `json:"url"`
	}
	if err := s.client.call(ctx, config.APIToken, "getWebhookInfo", struct{}{}, &webhook); err != nil {
		return 0, err
	}
	if webhook.URL != "" {
		return 0, fail("webhook")
	}
	// No offset or allowed_updates: do not acknowledge/drop other consumers'
	// pending messages or change their filters/webhooks to resolve a recipient.
	var updates []struct {
		Message *struct {
			Chat chat `json:"chat"`
		} `json:"message"`
		EditedMessage *struct {
			Chat chat `json:"chat"`
		} `json:"edited_message"`
		Callback *struct {
			Message *struct {
				Chat chat `json:"chat"`
			} `json:"message"`
		} `json:"callback_query"`
	}
	if err := s.client.call(ctx, config.APIToken, "getUpdates", map[string]any{"limit": 100, "timeout": 0}, &updates); err != nil {
		return 0, err
	}
	ids := map[int64]bool{}
	for _, update := range updates {
		candidates := []*struct {
			Chat chat `json:"chat"`
		}{update.Message, update.EditedMessage}
		if update.Callback != nil {
			candidates = append(candidates, update.Callback.Message)
		}
		for _, message := range candidates {
			if message != nil && message.Chat.Type == "private" && message.Chat.ID > 0 && strings.EqualFold(message.Chat.Username, config.Recipient[1:]) {
				ids[message.Chat.ID] = true
			}
		}
	}
	if len(ids) != 1 {
		return 0, fail("recipientNotFound")
	}
	for id := range ids {
		return id, s.verifyChat(ctx, config, id)
	}
	return 0, fail("recipientNotFound")
}

func (s *Store) Test(ctx context.Context) (TestResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if !s.testMu.TryLock() {
		return TestResult{Code: "busy"}, nil
	}
	defer s.testMu.Unlock()
	s.mu.Lock()
	config, err := s.readLocked()
	s.mu.Unlock()
	if err != nil {
		return TestResult{}, err
	}
	result := TestResult{}
	run := func() error {
		if config.Recipient == "" {
			return fail("recipientNotFound")
		}
		bot, err := s.BotIdentity(ctx, config.APIToken)
		if err != nil {
			return err
		}
		result.BotUsername = bot.Username
		id, err := s.resolve(ctx, config, bot)
		if err != nil {
			return err
		}
		// Serialize the final settings check/cache publication/send with edits.
		s.mu.Lock()
		defer s.mu.Unlock()
		current, err := s.readLocked()
		if err != nil {
			return fail("storage")
		}
		if current.Settings != config.Settings {
			return fail("changed")
		}
		if current.ChatID != id || current.BotID != bot.ID {
			current.ChatID, current.BotID = id, bot.ID
			data, _ := json.Marshal(current)
			if s.writeLocked(data) != nil {
				return fail("storage")
			}
		}
		var sent struct {
			MessageID int64 `json:"message_id"`
			Chat      chat  `json:"chat"`
		}
		if err = s.client.call(ctx, config.APIToken, "sendMessage", map[string]any{"chat_id": id, "text": TestMessage}, &sent); err != nil {
			return err
		}
		if sent.MessageID <= 0 || sent.Chat.ID != id || sent.Chat.Type != "private" {
			return fail("uncertain")
		}
		result.Sent, result.MessageID = true, sent.MessageID
		return nil
	}
	if err = run(); err != nil {
		var operation *operationError
		if errors.As(err, &operation) {
			result.Code = operation.code
		} else {
			result.Code = "response"
		}
	}
	return result, nil
}
