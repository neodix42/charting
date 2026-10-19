//go:build !windows

package storage

import (
	"os"
	"path/filepath"
)

func DefaultPath() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "cryptodesk", "workspace.sqlite"), nil
}
