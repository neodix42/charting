//go:build windows

package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

func DefaultPath() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(base) {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(base) {
			return "", fmt.Errorf("Windows application data directory must be absolute")
		}
	}
	return filepath.Join(base, "cryptodesk", "workspace.sqlite"), nil
}
