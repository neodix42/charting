package trading

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

type tradeAnnotation struct {
	EntryCondition string        `json:"entryCondition"`
	Screens        []TradeScreen `json:"screens,omitempty"`
}

type tradeAnnotations struct {
	Version int                        `json:"version"`
	Trades  map[string]tradeAnnotation `json:"trades"`
}

// EntryConditions keeps local annotations separate from exchange-owned history.
// Construction does no I/O; the file is created only on the first edit.
type EntryConditions struct {
	mu   sync.Mutex
	path string
}

func NewEntryConditions(profile string) *EntryConditions {
	return &EntryConditions{path: filepath.Join(profile, "trades.json")}
}

func validAnnotatedTrade(id string) bool {
	provider, intent, ok := strings.Cut(id, ":")
	return ok && (provider == "dukascopy" || provider == "hyperliquid") && ValidIntent(intent)
}

func ValidateEntryCondition(value string) error {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 100 || strings.ContainsAny(value, "\r\n") {
		return errors.New("Entry condition must be a single line of at most 100 characters")
	}
	return nil
}

func (s *EntryConditions) read() (tradeAnnotations, error) {
	data := tradeAnnotations{Version: 2, Trades: map[string]tradeAnnotation{}}
	var raw json.RawMessage
	if err := ReadJournal(s.path, &raw); errors.Is(err, os.ErrNotExist) {
		return data, nil
	} else if err != nil {
		return data, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if !utf8.Valid(raw) || decoder.Decode(&data) != nil || (data.Version != 1 && data.Version != 2) || data.Trades == nil || len(data.Trades) > 20000 {
		return data, errors.New("Invalid or unsupported trades.json; original file preserved")
	}
	for id, note := range data.Trades {
		if !validAnnotatedTrade(id) || ValidateEntryCondition(note.EntryCondition) != nil {
			return data, errors.New("Invalid entry condition in trades.json; original file preserved")
		}
		seen := map[string]bool{}
		for _, screen := range note.Screens {
			if !validTradeScreen(screen, id) || seen[screen.ID] {
				return data, errors.New("Invalid screen metadata in trades.json; original file preserved")
			}
			seen[screen.ID] = true
		}
	}
	return data, nil
}

func (s *EntryConditions) Snapshot() (map[string]string, error) {
	data, err := s.MetadataSnapshot()
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(data))
	for id, note := range data {
		result[id] = note.EntryCondition
	}
	return result, nil
}

func (s *EntryConditions) MetadataSnapshot() (map[string]tradeAnnotation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.read()
	if err != nil {
		return nil, err
	}
	return data.Trades, nil
}

func (s *EntryConditions) Set(id, value string) (string, error) {
	if !validAnnotatedTrade(id) {
		return "", errors.New("Select a locally recorded Dukascopy or Hyperliquid trade")
	}
	if err := ValidateEntryCondition(value); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.read()
	if err != nil {
		return "", err
	}
	note := data.Trades[id]
	note.EntryCondition = value
	if value == "" && len(note.Screens) == 0 {
		delete(data.Trades, id)
	} else {
		data.Trades[id] = note
	}
	data.Version = 2
	if len(data.Trades) > 20000 {
		return "", errors.New("Trade annotations exceed 20,000 records; original file preserved")
	}
	if err := WriteJournal(s.path, data, false); err != nil {
		return "", err
	}
	return value, nil
}
