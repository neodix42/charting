package main

import (
	_ "embed"
	"encoding/json"
	"io/fs"
	"strconv"
	"strings"
)

//go:embed VERSION
var sourceVersion string

// Bindings generation and development builds without compiled frontend metadata
// use VERSION. Desktop builds read the same release metadata as the UI.
var applicationVersion = strings.TrimSpace(sourceVersion)

func bundledAppVersion(assets fs.FS) string {
	fallback := strings.TrimSpace(sourceVersion)
	data, err := fs.ReadFile(assets, "frontend/dist/app-version.json")
	if err != nil {
		return fallback
	}
	var metadata struct {
		Version string `json:"version"`
		Tag     string `json:"tag"`
	}
	if json.Unmarshal(data, &metadata) != nil || metadata.Tag != "v"+metadata.Version {
		return fallback
	}
	parts := strings.Split(metadata.Version, ".")
	if len(parts) != 3 {
		return fallback
	}
	for _, part := range parts {
		number, err := strconv.ParseUint(part, 10, 16)
		if err != nil || strconv.FormatUint(number, 10) != part {
			return fallback
		}
	}
	return metadata.Version
}
