// Package telegram stores local bot configuration independently of workspaces
// and Wails. Reading or saving settings never contacts Telegram.
package telegram

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type Settings struct {
	APIToken  string `json:"apiToken"`
	Recipient string `json:"recipient"`
}

type configuration struct {
	Settings
	ChatID   int64               `json:"resolvedChatId,omitempty"`
	BotID    int64               `json:"resolvedBotId,omitempty"`
	Resolved []resolvedRecipient `json:"resolvedRecipients,omitempty"`
}

type resolvedRecipient struct {
	Recipient string `json:"recipient"`
	ChatID    int64  `json:"chatId"`
	BotID     int64  `json:"botId"`
}

const maxRecipients = 20
const maxRecipientBytes = 4096

type Store struct {
	mu       sync.Mutex
	path     string
	client   *client
	sendGate chan struct{}
	// Accessed only while sendGate serializes Test and alert deliveries.
	nextSend time.Time
}

func NewStore(directory string) *Store {
	return &Store{path: filepath.Join(directory, "telegram-bot.json"), client: newClient(nil), sendGate: make(chan struct{}, 1)}
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

func normalizeRecipient(recipient string) (string, error) {
	if len(recipient) > maxRecipientBytes || !utf8.ValidString(recipient) {
		return "", errors.New("Recipient list must contain at most 4096 bytes")
	}
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return "", nil
	}
	values := []string{}
	seen := map[string]bool{}
	for _, value := range strings.Split(recipient, ",") {
		normalized, err := normalizeRecipientValue(value)
		if err != nil {
			return "", err
		}
		key := strings.ToLower(normalized)
		if !seen[key] {
			seen[key] = true
			values = append(values, normalized)
		}
	}
	if len(values) > maxRecipients {
		return "", errors.New("Specify at most 20 Telegram recipients")
	}
	return strings.Join(values, ", "), nil
}

func normalizeRecipientValue(recipient string) (string, error) {
	recipient = strings.TrimSpace(recipient)
	if strings.HasPrefix(recipient, "@") && usernamePattern.MatchString(recipient[1:]) {
		return recipient, nil
	}
	if id, err := strconv.ParseInt(recipient, 10, 64); err == nil && id > 0 && id < 1<<52 {
		return strconv.FormatInt(id, 10), nil
	}
	return "", errors.New("Recipients must be comma-separated @usernames or positive private chat IDs, without empty entries")
}

func recipientValues(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ", ")
}

func (c configuration) resolved(recipient string) (int64, int64) {
	for _, target := range c.Resolved {
		if strings.EqualFold(target.Recipient, recipient) {
			return target.ChatID, target.BotID
		}
	}
	if strings.EqualFold(c.Recipient, recipient) {
		return c.ChatID, c.BotID
	}
	return 0, 0
}

func normalize(token string) (string, error) {
	token = strings.TrimSpace(token)
	if len(token) > 4096 || !utf8.ValidString(token) || strings.ContainsFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", errors.New("Telegram Bot API Token must contain at most 4096 bytes without internal whitespace or control characters")
	}
	return token, nil
}

func (s *Store) Read() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	config, err := s.readLocked()
	return config.Settings, err
}

func (s *Store) readLocked() (configuration, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return configuration{}, nil
	}
	if err != nil {
		return configuration{}, errors.New("could not read Telegram Bot settings")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 32*1024+1))
	if err != nil || len(data) > 32*1024 {
		return configuration{}, errors.New("could not read Telegram Bot settings; saved file preserved")
	}
	var settings configuration
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return configuration{}, errors.New("could not read Telegram Bot settings; saved file preserved")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var trailing any
	if decoder.Decode(&settings) != nil || decoder.Decode(&trailing) != io.EOF {
		// Parse errors may include fragments of a secret. Keep diagnostics generic.
		return configuration{}, errors.New("could not read Telegram Bot settings; saved file preserved")
	}
	settings.APIToken, err = normalize(settings.APIToken)
	if err != nil {
		return configuration{}, errors.New("could not read Telegram Bot settings; saved file preserved")
	}
	settings.Recipient, err = normalizeRecipient(settings.Recipient)
	if err != nil {
		return configuration{}, errors.New("could not read Telegram Bot settings; saved file preserved")
	}
	if len(settings.Resolved) > maxRecipients {
		return configuration{}, errors.New("could not read Telegram Bot settings; saved file preserved")
	}
	seen := map[string]bool{}
	for _, target := range settings.Resolved {
		value, targetErr := normalizeRecipientValue(target.Recipient)
		key := strings.ToLower(value)
		selected := false
		for _, recipient := range recipientValues(settings.Recipient) {
			selected = selected || strings.EqualFold(value, recipient)
		}
		if targetErr != nil || value != target.Recipient || !selected || seen[key] || target.ChatID <= 0 || target.ChatID >= 1<<52 || target.BotID <= 0 {
			return configuration{}, errors.New("could not read Telegram Bot settings; saved file preserved")
		}
		seen[key] = true
	}
	return settings, nil
}

func (s *Store) Save(token string) (Settings, error) {
	return s.configure(token, nil)
}

func (s *Store) Configure(token, recipient string) (Settings, error) {
	normalized, err := normalizeRecipient(recipient)
	if err != nil {
		return Settings{}, err
	}
	return s.configure(token, &normalized)
}

func (s *Store) configure(token string, recipient *string) (Settings, error) {
	normalized, err := normalize(token)
	if err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Preserve unreadable or future configuration instead of overwriting it.
	config, err := s.readLocked()
	if err != nil {
		return Settings{}, err
	}
	settings := Settings{APIToken: normalized, Recipient: config.Recipient}
	if recipient != nil {
		settings.Recipient = *recipient
	}
	resolved := []resolvedRecipient{}
	if config.APIToken == settings.APIToken {
		for _, target := range recipientValues(settings.Recipient) {
			if id, bot := config.resolved(target); id > 0 && bot > 0 {
				resolved = append(resolved, resolvedRecipient{target, id, bot})
			}
		}
	}
	config.Resolved = resolved
	config.ChatID, config.BotID = 0, 0
	if len(recipientValues(settings.Recipient)) == 1 && len(resolved) == 1 {
		config.ChatID, config.BotID = resolved[0].ChatID, resolved[0].BotID
	}
	config.Settings = settings
	data, _ := json.Marshal(config)
	if err = s.writeLocked(data); err != nil {
		return Settings{}, errors.New("could not save Telegram Bot settings; retry saving")
	}
	return settings, nil
}

func (s *Store) writeLocked(data []byte) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".telegram-bot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}
