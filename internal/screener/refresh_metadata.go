package screener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Metadata is committed ahead of the CSV rename. A crash between those steps
// leaves a safe recipe union but invalidates coverage, so the next refresh
// verifies history again rather than trusting a partially committed watermark.
type fileMetadata struct {
	Version        int                 `json:"version"`
	Filename       string              `json:"filename"`
	Recipes        []EnrichmentRequest `json:"recipes"`
	Verified       bool                `json:"verified"`
	Bytes          int64               `json:"bytes"`
	Modified       int64               `json:"modified"`
	FirstTimestamp int64               `json:"firstTimestamp,omitempty"`
	LastTimestamp  int64               `json:"lastTimestamp,omitempty"`
	Checksum       string              `json:"checksum,omitempty"`
	CheckedThrough int64               `json:"checkedThrough,omitempty"`
	Coverage       []dataRange         `json:"coverage,omitempty"`
}

type dataRange struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

func metadataDirectory(directory string) string {
	return filepath.Join(directory, ".cryptodesk-screener-metadata")
}

func readFileMetadata(directory, name string) (fileMetadata, error) {
	var metadata fileMetadata
	folder := metadataDirectory(directory)
	if st, err := os.Lstat(folder); err == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		return metadata, errors.New("Screener metadata folder must be a real directory")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return metadata, err
	}
	filename := filepath.Join(folder, dataFileID(directory, name)+".json")
	st, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return metadata, nil
	}
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1024*1024 {
		return metadata, errors.New("Screener file metadata is not a bounded regular file")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return metadata, err
	}
	if err = json.Unmarshal(data, &metadata); err != nil || metadata.Version != 1 || metadata.Filename != name || len(metadata.Recipes) > 2000 {
		return metadata, errors.New("Screener file metadata is invalid or uses an unsupported version; original files kept")
	}
	if len(metadata.Coverage) > 10000 {
		return metadata, errors.New("too many saved data coverage ranges")
	}
	for _, span := range metadata.Coverage {
		if span.From < -8640000000000000 || span.To <= span.From || span.To > 8640000000000000 {
			return metadata, errors.New("invalid saved data coverage range; original files kept")
		}
	}
	manager := &Manager{}
	for i, recipe := range metadata.Recipes {
		metadata.Recipes[i], err = manager.validateEnrichment(recipe)
		if err != nil {
			return metadata, fmt.Errorf("saved indicator recipe: %w", err)
		}
	}
	return metadata, nil
}

func writeFileMetadata(directory, name string, metadata fileMetadata) error {
	if len(metadata.Recipes) > 2000 {
		return errors.New("at most 2,000 distinct applied indicator recipes may be retained for one CSV")
	}
	folder := metadataDirectory(directory)
	if err := os.Mkdir(folder, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if st, err := os.Lstat(folder); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("Screener metadata folder must be a real directory")
	}
	metadata.Version, metadata.Filename = 1, name
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if len(data) > 1024*1024 {
		return errors.New("applied indicator recipe metadata exceeds 1 MiB; original kept")
	}
	temporary, err := os.CreateTemp(folder, ".metadata-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.Write(append(data, '\n'))
	err = errors.Join(writeErr, temporary.Sync(), temporary.Close())
	if err != nil {
		return err
	}
	filename := filepath.Join(folder, dataFileID(directory, name)+".json")
	if err = os.Rename(temporary.Name(), filename); err != nil {
		return err
	}
	if parent, err := os.Open(folder); err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return nil
}

func metadataMatches(metadata fileMetadata, original os.FileInfo) bool {
	return metadata.Version == 1 && metadata.Bytes == original.Size() && metadata.Modified == original.ModTime().UnixNano()
}

func addRecipe(recipes []EnrichmentRequest, recipe EnrichmentRequest) []EnrichmentRequest {
	encoded, _ := json.Marshal(recipe)
	for _, previous := range recipes {
		other, _ := json.Marshal(previous)
		if string(encoded) == string(other) {
			return recipes
		}
	}
	return append(recipes, recipe)
}

// Existing enrichment releases kept each request beside its exact original
// backup. Read these once per refresh batch, rather than scanning every backup
// again for every asset. The CSV headers remain the final authority on which
// recipes are currently applied.
func legacyRecipes(ctx context.Context, directory string) map[string][]EnrichmentRequest {
	result := map[string][]EnrichmentRequest{}
	folder := filepath.Join(directory, ".cryptodesk-enrichment-backups")
	if st, err := os.Lstat(folder); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return result
	}
	entries, _ := os.ReadDir(folder)
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if strings.HasPrefix(entry.Name(), ".") || filepath.Ext(entry.Name()) != ".json" || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		st, err := entry.Info()
		if err != nil || !st.Mode().IsRegular() || st.Size() > 65536 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(folder, entry.Name()))
		var metadata struct {
			Filename string            `json:"filename"`
			Request  EnrichmentRequest `json:"request"`
		}
		if err != nil || json.Unmarshal(data, &metadata) != nil || metadata.Filename == "" || filepath.Base(metadata.Filename) != metadata.Filename {
			continue
		}
		request, err := (&Manager{}).validateEnrichment(metadata.Request)
		if err == nil {
			result[metadata.Filename] = addRecipe(result[metadata.Filename], request)
		}
	}
	return result
}
