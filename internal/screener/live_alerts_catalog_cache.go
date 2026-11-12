package screener

import (
	"cryptodesk/internal/domain"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type publicAlertCatalogSnapshot struct {
	Version   int                 `json:"version"`
	Source    string              `json:"source"`
	FetchedAt int64               `json:"fetchedAt"`
	Items     []domain.Instrument `json:"items"`
}

const publicAlertCatalogBytes = 16 * 1024 * 1024

func reusablePublicAlertCatalog(cached publicAlertCatalog) []domain.Instrument {
	if len(cached.items) > 0 && time.Since(cached.at) < 7*24*time.Hour {
		return cached.items
	}
	return nil
}

func validatePublicAlertCatalog(source string, snapshot publicAlertCatalogSnapshot) error {
	if snapshot.Version != 1 || snapshot.Source != source || snapshot.FetchedAt <= 0 || snapshot.FetchedAt > time.Now().Add(time.Minute).UnixMilli() || len(snapshot.Items) == 0 || len(snapshot.Items) > 20000 {
		return errors.New("Invalid saved market catalog; original preserved")
	}
	seen := map[string]bool{}
	for _, item := range snapshot.Items {
		key := domain.FeedKey{Exchange: source, Market: item.Market, Instrument: item.NativeID, Timeframe: "1h"}
		if key.Validate() != nil || seen[key.String()] || len(item.BaseAsset) > 128 || len(item.QuoteAsset) > 128 || len(item.PriceIncrement) > 128 || len(item.SizeIncrement) > 128 || len(item.Availability) > 128 || len(item.ProviderMetadata) > 64 || len(item.SupportedIntervals) > 32 || item.ProviderMetadata["catalogWarning"] != "" {
			return errors.New("Invalid or incomplete saved market catalog; original preserved")
		}
		seen[key.String()] = true
		for name, value := range item.ProviderMetadata {
			if len(name) > 128 || len(value) > 4096 {
				return errors.New("Oversized saved market metadata; original preserved")
			}
		}
	}
	return nil
}

func (l *LiveAlerts) publicAlertCatalogPath(source string) string {
	return filepath.Join(l.manager.profile, "screener-public-history", source, "catalog.json")
}

func (l *LiveAlerts) readPublicAlertCatalog(source string) (publicAlertCatalog, error) {
	var snapshot publicAlertCatalogSnapshot
	if err := readState(l.publicAlertCatalogPath(source), publicAlertCatalogBytes, &snapshot); errors.Is(err, os.ErrNotExist) {
		return publicAlertCatalog{}, nil
	} else if err != nil {
		return publicAlertCatalog{}, fmt.Errorf("Read saved %s market catalog (original preserved): %w", source, err)
	}
	if err := validatePublicAlertCatalog(source, snapshot); err != nil {
		return publicAlertCatalog{}, err
	}
	return publicAlertCatalog{items: snapshot.Items, at: time.UnixMilli(snapshot.FetchedAt)}, nil
}

func (l *LiveAlerts) savePublicAlertCatalog(source string, catalog publicAlertCatalog) error {
	snapshot := publicAlertCatalogSnapshot{Version: 1, Source: source, FetchedAt: catalog.at.UnixMilli(), Items: catalog.items}
	if err := validatePublicAlertCatalog(source, snapshot); err != nil {
		return err
	}
	path := l.publicAlertCatalogPath(source)
	for _, directory := range []string{filepath.Dir(filepath.Dir(path)), filepath.Dir(path)} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		if err := checkHistoryDirectory(directory, false); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(raw) > publicAlertCatalogBytes {
		return errors.New("Market catalog exceeds the 16 MiB cache limit")
	}
	return publicAlertAtomicWrite(path, append(raw, '\n'))
}
