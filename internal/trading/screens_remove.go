package trading

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type TradeScreenRemoval struct {
	Removed bool   `json:"removed"`
	Warning string `json:"warning,omitempty"`
}

func (s *EntryConditions) RemoveScreen(tradeID, screenID string) (TradeScreenRemoval, error) {
	return s.removeScreen(tradeID, screenID, func(data tradeAnnotations) error { return WriteJournal(s.path, data, false) }, os.Remove)
}

// Commit removal of the association before unlinking its original. A crash may
// leave an unreferenced original, but cannot leave a live attachment whose file
// was deleted before a failed metadata commit.
func (s *EntryConditions) removeScreen(tradeID, screenID string, persist func(tradeAnnotations) error, removeFile func(string) error) (TradeScreenRemoval, error) {
	if !validAnnotatedTrade(tradeID) || !ValidIntent(screenID) {
		return TradeScreenRemoval{}, errors.New("Invalid trade screen identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	metadata, err := s.read()
	if err != nil {
		return TradeScreenRemoval{}, err
	}
	note := metadata.Trades[tradeID]
	index := -1
	var selected TradeScreen
	for i, screen := range note.Screens {
		if screen.ID == screenID {
			index, selected = i, screen
			break
		}
	}
	if index < 0 {
		return TradeScreenRemoval{}, errors.New("This screen does not belong to the selected trade")
	}
	directory, err := s.screenDirectory(tradeID, false)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return TradeScreenRemoval{}, err
	}
	filePath := ""
	if err == nil {
		filePath = filepath.Join(directory, screenID+screenExtension(selected.MIMEType))
		info, err := os.Lstat(filePath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return TradeScreenRemoval{}, err
		}
		if err == nil && !info.Mode().IsRegular() {
			return TradeScreenRemoval{}, errors.New("The saved image must be a regular file; original preserved")
		}
	}
	note.Screens = append(note.Screens[:index:index], note.Screens[index+1:]...)
	if len(note.Screens) == 0 && note.EntryCondition == "" {
		delete(metadata.Trades, tradeID)
	} else {
		metadata.Trades[tradeID] = note
	}
	metadata.Version = 2
	if err := persist(metadata); err != nil {
		return TradeScreenRemoval{}, fmt.Errorf("Could not remove image attachment: %w", err)
	}
	if filePath != "" {
		if err := removeFile(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return TradeScreenRemoval{Removed: true, Warning: fmt.Sprintf("Image attachment removed, but its original file could not be removed: %v", err)}, nil
		}
		if directoryFile, err := os.Open(directory); err == nil {
			_ = directoryFile.Sync()
			_ = directoryFile.Close()
		}
	}
	return TradeScreenRemoval{Removed: true}, nil
}
