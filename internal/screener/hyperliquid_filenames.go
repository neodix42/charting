package screener

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The public filename is independent of the stable source/market/native ID.
// Alias records keep readable names and legacy hashed histories in one family.
func parseDownloadFilename(directory, name string) []string {
	match := downloadFilename.FindStringSubmatch(strings.ToLower(name))
	if match == nil || !strings.HasPrefix(match[1], "hyperliquid_") {
		return match
	}
	var asset Instrument
	if readState(hyperliquidAssetPath(directory, match[1]), 4096, &asset) != nil || !validHyperliquidJob(Job{Symbol: asset.ID, Market: asset.Market, NativeID: asset.NativeID}) {
		return nil
	}
	match[1] = asset.ID
	return match
}

func hyperliquidSlug(value string) string {
	var out strings.Builder
	separator := false
	for _, c := range strings.ToLower(value) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			if separator && out.Len() > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(c)
			separator = false
		} else {
			separator = true
		}
	}
	return out.String()
}

func hyperliquidFileSymbol(directory, id string) (string, error) {
	asset, err := readHyperliquidAsset(directory, id)
	if errors.Is(err, os.ErrNotExist) {
		return id, nil
	} // old downloads without a name
	if err != nil {
		return "", err
	}
	label, _, _ := strings.Cut(asset.Name, " · ")
	if label == "" {
		label = asset.NativeID
	}
	slug := hyperliquidSlug(label)
	if slug == "" {
		slug = "asset"
	}
	if asset.Market != "spot" {
		slug += "-" + asset.Market
	}
	base := "hyperliquid_" + slug
	encoded, err := json.Marshal(asset)
	if err != nil {
		return "", err
	}
	// Preserve a friendly name once assigned; distinguish duplicate token names
	// or names that sanitize alike with the readable native ID, then a digest.
	for _, candidate := range []string{base, base + "-" + hyperliquidSlug(asset.NativeID), base + "-" + id[len(id)-16:]} {
		path := hyperliquidAssetPath(directory, candidate)
		var existing Instrument
		err := readState(path, 4096, &existing)
		if err == nil {
			if existing.ID == id {
				return candidate, nil
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := writeSyncedExclusive(path, encoded); err != nil {
			return "", err
		}
		return candidate, nil
	}
	return "", errors.New("cannot assign a unique Hyperliquid filename")
}

func downloadPathMatchesJob(directory string, job Job) bool {
	match := parseDownloadFilename(directory, filepath.Base(job.OutputPath))
	return match != nil && match[1] == job.Symbol && match[2] == job.Timeframe
}
