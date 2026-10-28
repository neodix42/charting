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
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

type Settings struct {
	APIToken string `json:"apiToken"`
}

type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore(directory string) *Store {
	return &Store{path: filepath.Join(directory, "telegram-bot.json")}
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
	return s.readLocked()
}

func (s *Store) readLocked() (Settings, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, errors.New("could not read Telegram Bot settings")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 32*1024+1))
	if err != nil || len(data) > 32*1024 {
		return Settings{}, errors.New("could not read Telegram Bot settings; saved file preserved")
	}
	var settings Settings
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return Settings{}, errors.New("could not read Telegram Bot settings; saved file preserved")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var trailing any
	if decoder.Decode(&settings) != nil || decoder.Decode(&trailing) != io.EOF {
		// Parse errors may include fragments of a secret. Keep diagnostics generic.
		return Settings{}, errors.New("could not read Telegram Bot settings; saved file preserved")
	}
	settings.APIToken, err = normalize(settings.APIToken)
	if err != nil {
		return Settings{}, errors.New("could not read Telegram Bot settings; saved file preserved")
	}
	return settings, nil
}

func (s *Store) Save(token string) (Settings, error) {
	normalized, err := normalize(token)
	if err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Preserve unreadable or future configuration instead of overwriting it.
	if _, err = s.readLocked(); err != nil {
		return Settings{}, err
	}
	settings := Settings{APIToken: normalized}
	data, _ := json.Marshal(settings)
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
