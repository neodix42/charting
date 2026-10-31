package screener

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const savedConditionsLimit = 100
const savedConditionsBytes = 128 * 1024 * 1024

// SavedAnalysisCondition is a named snapshot of all visual AND rows, separate
// from the editable draft, downloaded data and chart workspace exports.
type SavedAnalysisCondition struct {
	ID       string                  `json:"id"`
	Name     string                  `json:"name"`
	Request  AnalysisRequest         `json:"request"`
	Analysis *SavedConditionAnalysis `json:"analysis,omitempty"`
}

type savedAnalysisConditions struct {
	Version    int                      `json:"version"`
	Conditions []SavedAnalysisCondition `json:"conditions"`
}

func conditionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", errors.New("condition name must contain 1–100 characters without control characters")
	}
	return name, nil
}

func (m *Manager) readSavedConditions() ([]SavedAnalysisCondition, error) {
	path := filepath.Join(m.profile, "screener-saved-conditions.json")
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []SavedAnalysisCondition{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > savedConditionsBytes {
		return nil, errors.New("saved conditions must be a regular file no larger than 128 MiB; original state preserved")
	}
	var saved savedAnalysisConditions
	if err = readState(path, savedConditionsBytes, &saved); err != nil {
		return nil, fmt.Errorf("read saved conditions (original state preserved): %w", err)
	}
	if saved.Version != 1 || len(saved.Conditions) > savedConditionsLimit {
		return nil, errors.New("unsupported saved conditions version or count; original state preserved")
	}
	ids := map[string]bool{}
	for i, condition := range saved.Conditions {
		name, nameErr := conditionName(condition.Name)
		if nameErr != nil || name != condition.Name || len(condition.ID) != 32 || ids[condition.ID] {
			return nil, errors.New("invalid saved condition name or ID; original state preserved")
		}
		if _, err = hex.DecodeString(condition.ID); err != nil {
			return nil, errors.New("invalid saved condition ID; original state preserved")
		}
		ids[condition.ID] = true
		for _, previous := range saved.Conditions[:i] {
			if strings.EqualFold(previous.Name, name) {
				return nil, errors.New("duplicate saved condition names; original state preserved")
			}
		}
		if err = validateSavedConditionAnalysis(condition.Analysis); err != nil {
			return nil, fmt.Errorf("invalid saved condition profitability (original state preserved): %w", err)
		}
		if err = validateAnalysisRequest(condition.Request, false); err != nil {
			return nil, fmt.Errorf("invalid saved condition %q (original state preserved): %w", name, err)
		}
	}
	return copySavedAnalysisConditions(saved.Conditions), nil
}

func (m *Manager) SavedAnalysisConditions() ([]SavedAnalysisCondition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readSavedConditions()
}

func (m *Manager) SaveAnalysisCondition(name string, request AnalysisRequest) (SavedAnalysisCondition, error) {
	name, err := conditionName(name)
	if err != nil {
		return SavedAnalysisCondition{}, err
	}
	if err = validateAnalysisRequest(request, false); err != nil {
		return SavedAnalysisCondition{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return SavedAnalysisCondition{}, errors.New("screener is closed")
	}
	conditions, err := m.readSavedConditions()
	if err != nil {
		return SavedAnalysisCondition{}, err
	}
	for _, condition := range conditions {
		if strings.EqualFold(condition.Name, name) {
			return SavedAnalysisCondition{}, errors.New("a condition with this name already exists; choose another name")
		}
	}
	if len(conditions) >= savedConditionsLimit {
		return SavedAnalysisCondition{}, errors.New("at most 100 named conditions can be saved")
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return SavedAnalysisCondition{}, err
	}
	condition := SavedAnalysisCondition{ID: hex.EncodeToString(id[:]), Name: name, Request: copyAnalysisRequest(request)}
	if a := m.analysis; a != nil && sameAnalysisProfitabilityRequest(a.request, request) {
		condition.Analysis, err = completedAnalysisProfitability(a)
		if err != nil {
			return SavedAnalysisCondition{}, fmt.Errorf("record saved condition profitability: %w", err)
		}
	}
	saved := savedAnalysisConditions{Version: 1, Conditions: append(conditions, condition)}
	encoded, err := json.Marshal(saved)
	if err != nil {
		return SavedAnalysisCondition{}, err
	}
	if len(encoded)+1 > savedConditionsBytes {
		return SavedAnalysisCondition{}, errors.New("saved conditions exceed 128 MiB; existing conditions preserved")
	}
	if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), saved); err != nil {
		return SavedAnalysisCondition{}, fmt.Errorf("save named condition: %w", err)
	}
	return condition, nil
}

func (m *Manager) DeleteAnalysisCondition(id string) error {
	if len(id) != 32 {
		return errors.New("invalid saved condition ID")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return errors.New("invalid saved condition ID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("screener is closed")
	}
	conditions, err := m.readSavedConditions()
	if err != nil {
		return err
	}
	for i, condition := range conditions {
		if condition.ID != id {
			continue
		}
		saved := savedAnalysisConditions{Version: 1, Conditions: append(conditions[:i], conditions[i+1:]...)}
		if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), saved); err != nil {
			return fmt.Errorf("delete named condition: %w", err)
		}
		return nil
	}
	return errors.New("saved condition was not found")
}

// RenameAnalysisCondition changes only the saved snapshot's name. It never
// replaces its request with the editable draft or changes its ID or position.
func (m *Manager) RenameAnalysisCondition(id, name string) (SavedAnalysisCondition, error) {
	if len(id) != 32 {
		return SavedAnalysisCondition{}, errors.New("invalid saved condition ID")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return SavedAnalysisCondition{}, errors.New("invalid saved condition ID")
	}
	name, err := conditionName(name)
	if err != nil {
		return SavedAnalysisCondition{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return SavedAnalysisCondition{}, errors.New("screener is closed")
	}
	conditions, err := m.readSavedConditions()
	if err != nil {
		return SavedAnalysisCondition{}, err
	}
	for i, condition := range conditions {
		if condition.ID != id {
			continue
		}
		for _, other := range conditions {
			if other.ID != id && strings.EqualFold(other.Name, name) {
				return SavedAnalysisCondition{}, errors.New("a condition with this name already exists; choose another name")
			}
		}
		if condition.Name == name {
			return condition, nil
		}
		condition.Name = name
		conditions[i] = condition
		saved := savedAnalysisConditions{Version: 1, Conditions: conditions}
		encoded, err := json.Marshal(saved)
		if err != nil {
			return SavedAnalysisCondition{}, err
		}
		if len(encoded)+1 > savedConditionsBytes {
			return SavedAnalysisCondition{}, errors.New("saved conditions exceed 128 MiB; existing conditions preserved")
		}
		if err = writeState(filepath.Join(m.profile, "screener-saved-conditions.json"), saved); err != nil {
			return SavedAnalysisCondition{}, fmt.Errorf("rename named condition: %w", err)
		}
		return condition, nil
	}
	return SavedAnalysisCondition{}, errors.New("saved condition was not found")
}
