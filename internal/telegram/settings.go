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
	"unicode"
	"unicode/utf8"
)

type Settings struct {
	APIToken  string `json:"apiToken"`
	Recipient string `json:"recipient"`
}

type configuration struct {
	Settings
	ChatID int64 `json:"resolvedChatId,omitempty"`
	BotID  int64 `json:"resolvedBotId,omitempty"`
}

type Store struct {
	mu     sync.Mutex
	path   string
	client *client
	testMu sync.Mutex
}

func NewStore(directory string) *Store {
	return &Store{path: filepath.Join(directory, "telegram-bot.json"), client: newClient(nil)}
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

func normalizeRecipient(recipient string) (string, error) {
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return "", nil
	}
	if strings.HasPrefix(recipient, "@") && usernamePattern.MatchString(recipient[1:]) {
		return recipient, nil
	}
	if id, err := strconv.ParseInt(recipient, 10, 64); err == nil && id > 0 && id < 1<<52 {
		return strconv.FormatInt(id, 10), nil
	}
	return "", errors.New("Recipient must be @username or a positive private chat ID")
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
	if config.APIToken != settings.APIToken || !strings.EqualFold(config.Recipient, settings.Recipient) {
		config.ChatID, config.BotID = 0, 0
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
