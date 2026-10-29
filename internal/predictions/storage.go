package predictions

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const forecastLimit = 100
const forecastBytes = 2 * 1024 * 1024

var forecastID = regexp.MustCompile(`^[0-9]{15}-[a-f0-9]{32}$`)

type savedSettings struct {
	Version   int       `json:"version"`
	Settings  Settings  `json:"settings"`
	Selection Selection `json:"selection"`
}

func readJSON(path string, limit int64, target any) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return errors.New("saved prediction data must be a regular file within its size limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return errors.New("saved prediction data exceeds its size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("unexpected trailing saved prediction data")
	}
	return nil
}

func writeJSON(path string, value any, exclusive bool) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if exclusive {
		if _, err = os.Lstat(path); err == nil {
			return errors.New("forecast already exists; immutable snapshot preserved")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".predictions-save-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if exclusive {
		// Publish atomically without replacing an existing immutable forecast,
		// including a competing writer that appeared after the initial check.
		err = os.Link(f.Name(), path)
	} else {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return err
	}
	if directory, err := os.Open(filepath.Dir(path)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func newForecastID(timestamp int64) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%015d-%s", timestamp, hex.EncodeToString(random[:])), nil
}

func summary(forecast Forecast) ForecastSummary {
	return ForecastSummary{ID: forecast.ID, CreatedAt: forecast.CreatedAt, AsOf: forecast.AsOf, Model: forecast.Model, Symbol: forecast.Symbol, AssetName: forecast.AssetName, Period: forecast.Period, ContextLength: forecast.ContextLength, Horizon: forecast.Horizon}
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func validatePoints(points []Point, horizon int) error {
	if len(points) != horizon {
		return errors.New("model returned a different number of forecast bars than requested")
	}
	for i, point := range points {
		if point.Step != i+1 || !finite(point.Lower) || !finite(point.Median) || !finite(point.Upper) || point.Lower > point.Median || point.Median > point.Upper {
			return errors.New("model returned invalid, unordered or non-finite forecast quantiles")
		}
	}
	return nil
}

func validateForecast(forecast Forecast) error {
	if !forecastID.MatchString(forecast.ID) || !strings.HasPrefix(forecast.ID, fmt.Sprintf("%015d-", forecast.CreatedAt)) || forecast.CreatedAt < 0 || forecast.AsOf < 0 || forecast.AsOf > forecast.CreatedAt || forecast.CreatedAt > 253_402_300_799_999 {
		return errors.New("invalid saved forecast identity or timestamps")
	}
	if !ValidModel(forecast.Model) || forecast.ModelRevision == "" || len(forecast.ModelRevision) > 200 || strings.ContainsFunc(forecast.ModelRevision, unicode.IsControl) {
		return errors.New("saved forecast model provenance is invalid")
	}
	if err := validateSelection(Selection{Asset: forecast.Symbol, Period: forecast.Period, ContextLength: forecast.ContextLength, Horizon: forecast.Horizon}, true); err != nil {
		return err
	}
	if len(forecast.SourceHash) != 64 || len(forecast.History) != forecast.ContextLength || filepath.Base(forecast.SourceFile) != forecast.SourceFile || forecast.SourceFile == "." || forecast.SourceFile == "" {
		return errors.New("saved forecast source snapshot is invalid")
	}
	if _, err := hex.DecodeString(forecast.SourceHash); err != nil {
		return errors.New("saved forecast source checksum is invalid")
	}
	previous := int64(-1)
	for _, observation := range forecast.History {
		if observation.Time <= previous || !finite(observation.Close) || closedAt(observation.Time, forecast.Period) > forecast.AsOf {
			return errors.New("saved forecast history contains invalid or open candles")
		}
		previous = observation.Time
	}
	return validatePoints(forecast.Points, forecast.Horizon)
}

func (m *Manager) loadForecasts() error {
	forecasts, err := m.forecastInventory("")
	m.forecasts = forecasts
	return err
}

func (m *Manager) forecastInventory(excludeID string) ([]ForecastSummary, error) {
	forecasts := []ForecastSummary{}
	directory, err := os.Open(m.forecastDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return forecasts, nil
	}
	if err != nil {
		return forecasts, err
	}
	defer directory.Close()
	names := []string{}
	for {
		entries, readErr := directory.ReadDir(256)
		for _, entry := range entries {
			id := strings.TrimSuffix(entry.Name(), ".json")
			if id != excludeID && entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") && forecastID.MatchString(id) {
				names = append(names, entry.Name())
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		if len(names) > forecastLimit {
			names = names[:forecastLimit]
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return forecasts, readErr
		}
	}
	var failures []error
	for _, name := range names {
		var forecast Forecast
		if err = readJSON(filepath.Join(m.forecastDirectory, name), forecastBytes, &forecast); err == nil {
			err = validateForecast(forecast)
		}
		if err == nil && forecast.ID+".json" != name {
			err = errors.New("saved forecast filename and identity disagree")
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("restore forecast %s: %w", name, err))
			continue
		}
		forecasts = append(forecasts, summary(forecast))
	}
	return forecasts, errors.Join(failures...)
}
