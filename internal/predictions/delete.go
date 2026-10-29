package predictions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DeleteForecast removes one saved snapshot. Source CSVs and model files live
// outside this directory and are never part of the operation.
func (m *Manager) DeleteForecast(id string) (Info, error) {
	if !forecastID.MatchString(id) {
		return m.Info(), errors.New("invalid stored forecast ID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.mutableLocked(); err != nil {
		return m.infoLocked(), err
	}
	for _, path := range []string{filepath.Dir(m.forecastDirectory), m.forecastDirectory} {
		info, err := os.Lstat(path)
		if err != nil {
			return m.infoLocked(), fmt.Errorf("open saved forecasts: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return m.infoLocked(), errors.New("saved forecasts must be stored in a real directory")
		}
	}
	// Anchor removal to the opened forecast folder, even if another process
	// changes the directory path while the selection is being saved.
	directory, err := os.OpenRoot(m.forecastDirectory)
	if err != nil {
		return m.infoLocked(), fmt.Errorf("open saved forecasts: %w", err)
	}
	defer directory.Close()
	name := id + ".json"
	info, err := directory.Lstat(name)
	if err != nil {
		return m.infoLocked(), fmt.Errorf("find saved forecast: %w", err)
	}
	if !info.Mode().IsRegular() {
		return m.infoLocked(), errors.New("saved forecast must be a regular file")
	}
	remaining, inventoryError := m.forecastInventory(id)
	previous := m.selection
	selection := previous
	if previous.ForecastID == id || (previous.ForecastID == "" && len(m.forecasts) > 0 && m.forecasts[0].ID == id) {
		selection.ForecastID = ""
		if len(remaining) > 0 {
			selection.ForecastID = remaining[0].ID
		}
	}
	selectionChanged := selection != previous
	if selectionChanged {
		// Commit the next reference first. A settings failure leaves the original
		// snapshot intact; a crash before removal leaves it merely unselected.
		if err = writeJSON(m.settingsPath, savedSettings{Version: 1, Settings: m.settings, Selection: selection}, false); err != nil {
			return m.infoLocked(), fmt.Errorf("save selection before removing forecast: %w", err)
		}
	}
	if err = directory.Remove(name); err != nil {
		if selectionChanged {
			if rollback := writeJSON(m.settingsPath, savedSettings{Version: 1, Settings: m.settings, Selection: previous}, false); rollback != nil {
				m.selection = selection
				return m.infoLocked(), fmt.Errorf("remove forecast: %w; restore previous selection: %v", err, rollback)
			}
		}
		return m.infoLocked(), fmt.Errorf("remove forecast: %w", err)
	}
	if folder, err := directory.Open("."); err == nil {
		_ = folder.Sync()
		_ = folder.Close()
	}
	m.selection, m.forecasts = selection, remaining
	m.restoreError = ""
	if inventoryError != nil {
		m.restoreError = cleanError(inventoryError)
	}
	return m.infoLocked(), nil
}
