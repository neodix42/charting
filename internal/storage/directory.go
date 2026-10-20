package storage

import "path/filepath"

// Directory is this profile's directory, including explicit --data-dir profiles.
func (s *Store) Directory() string { return filepath.Dir(s.path) }
