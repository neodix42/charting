package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const TestMessage = "CryptoDesk test notification: Telegram delivery is working."

type BotIdentity struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type TestResult struct {
	Sent            bool   `json:"sent"`
	Code            string `json:"code,omitempty"`
	BotUsername     string `json:"botUsername,omitempty"`
	MessageID       int64  `json:"messageId,omitempty"`
	SentCount       int    `json:"sentCount,omitempty"`
	TotalRecipients int    `json:"totalRecipients,omitempty"`
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
	if id <= 0 || id >= 1<<52 {
		return fail("response")
	}
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
	return s.sendMessage(ctx, TestMessage, false)
}

// SendAlert delivers one plain-text alert to each verified private recipient.
// It shares Test's identity checks, settings serialization and no-retry policy.
func (s *Store) SendAlert(ctx context.Context, message string) error {
	if !utf8.ValidString(message) || strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > 4096 {
		return fail("message")
	}
	result, err := s.sendMessage(ctx, message, true)
	if err != nil {
		return fail("storage")
	}
	if !result.Sent {
		if result.TotalRecipients > 0 {
			return fmt.Errorf("Telegram alert delivery: %d of %d recipients confirmed; no automatic resend: %w", result.SentCount, result.TotalRecipients, fail(result.Code))
		}
		return fail(result.Code)
	}
	return nil
}

// Multi-recipient batches retain bounded HTTP requests and cancellable pacing.
// The overall bound allows twenty private chats without an eight-second cutoff.
const deliveryTimeout = 3 * time.Minute

func operationCode(err error) string {
	var operation *operationError
	if errors.As(err, &operation) {
		return operation.code
	}
	return "response"
}

func individualFailure(code string) bool {
	return code == "recipientNotFound" || code == "blocked" || code == "webhook"
}

func (s *Store) sendMessage(ctx context.Context, message string, wait bool) (TestResult, error) {
	queueCtx, stopWaiting := context.WithTimeout(ctx, deliveryTimeout)
	defer stopWaiting()
	if wait {
		select {
		case s.sendGate <- struct{}{}:
		case <-queueCtx.Done():
			return TestResult{Code: "network"}, nil
		}
	} else {
		select {
		case s.sendGate <- struct{}{}:
		default:
			return TestResult{Code: "busy"}, nil
		}
	}
	defer func() { <-s.sendGate }()
	ctx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return TestResult{Code: "network"}, nil
	}
	s.mu.Lock()
	config, err := s.readLocked()
	s.mu.Unlock()
	if err != nil {
		return TestResult{}, err
	}
	recipients := recipientValues(config.Recipient)
	result := TestResult{}
	if len(recipients) == 0 {
		result.Code = "recipientNotFound"
		return result, nil
	}
	multi := len(recipients) > 1
	total, delivered := len(recipients), 0
	finish := func() (TestResult, error) {
		result.Sent = result.Code == "" && delivered == total
		if multi {
			result.SentCount, result.TotalRecipients = delivered, total
		}
		return result, nil
	}
	bot, err := s.BotIdentity(ctx, config.APIToken)
	if err != nil {
		result.Code = operationCode(err)
		return finish()
	}
	result.BotUsername = bot.Username
	seen := map[int64]bool{}
	for _, recipient := range recipients {
		if ctx.Err() != nil {
			result.Code = "network"
			break
		}
		// Resolution may take place outside the settings lock; every target is
		// checked against the full immutable configuration before cache/send.
		target := config
		target.Recipient = recipient
		target.ChatID, target.BotID = config.resolved(recipient)
		id, resolveErr := s.resolve(ctx, target, bot)
		if resolveErr != nil {
			code := operationCode(resolveErr)
			if !individualFailure(code) {
				result.Code = code
				break
			}
			if result.Code == "" {
				result.Code = code
			}
			continue
		}
		if seen[id] {
			s.mu.Lock()
			cacheErr := s.publishRecipientLocked(config, recipient, bot, id)
			s.mu.Unlock()
			if cacheErr != nil {
				result.Code = operationCode(cacheErr)
				break
			}
			total--
			continue
		}
		seen[id] = true
		if sendErr := s.sendRecipient(ctx, config, recipient, bot, id, message, &result.MessageID); sendErr != nil {
			code := operationCode(sendErr)
			if !individualFailure(code) {
				result.Code = code
				break
			}
			if result.Code == "" {
				result.Code = code
			}
			continue
		}
		delivered++
	}
	return finish()
}

func (s *Store) sendRecipient(ctx context.Context, config configuration, recipient string, bot BotIdentity, id int64, message string, messageID *int64) error {
	if delay := time.Until(s.nextSend); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return fail("network")
		case <-timer.C:
		}
	}
	if ctx.Err() != nil {
		return fail("network")
	}
	// Preserve the existing final settings check/send serialization. A settings
	// edit between recipients stops the remainder of this original batch.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.publishRecipientLocked(config, recipient, bot, id); err != nil {
		return err
	}
	var sent struct {
		MessageID int64 `json:"message_id"`
		Chat      chat  `json:"chat"`
	}
	s.nextSend = time.Now().Add(time.Second)
	if err := s.client.call(ctx, config.APIToken, "sendMessage", map[string]any{"chat_id": id, "text": message}, &sent); err != nil {
		return err
	}
	if sent.MessageID <= 0 || sent.Chat.ID != id || sent.Chat.Type != "private" {
		return fail("uncertain")
	}
	*messageID = sent.MessageID
	return nil
}

// The caller owns the settings mutex, including any following send.
func (s *Store) publishRecipientLocked(config configuration, recipient string, bot BotIdentity, id int64) error {
	current, err := s.readLocked()
	if err != nil {
		return fail("storage")
	}
	if current.Settings != config.Settings {
		return fail("changed")
	}
	cachedID, cachedBot := current.resolved(recipient)
	if cachedID != id || cachedBot != bot.ID {
		found := false
		for i := range current.Resolved {
			if strings.EqualFold(current.Resolved[i].Recipient, recipient) {
				current.Resolved[i] = resolvedRecipient{recipient, id, bot.ID}
				found = true
				break
			}
		}
		if !found {
			current.Resolved = append(current.Resolved, resolvedRecipient{recipient, id, bot.ID})
		}
		if len(recipientValues(current.Recipient)) == 1 {
			current.ChatID, current.BotID = id, bot.ID
		} else {
			current.ChatID, current.BotID = 0, 0
		}
		data, _ := json.Marshal(current)
		if s.writeLocked(data) != nil {
			return fail("storage")
		}
	}
	return nil
}
