package jforex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type configuration struct {
	Version     int    `json:"version"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Environment string `json:"environment"`
}

func defaultConfiguration() configuration { return configuration{Version: 1, Environment: "demo"} }

func normalizeSettings(request Settings, current configuration) (configuration, error) {
	request.Username = strings.TrimSpace(request.Username)
	request.Environment = strings.ToLower(strings.TrimSpace(request.Environment))
	if request.Environment == "" {
		request.Environment = "demo"
	}
	if request.Environment != "demo" && request.Environment != "live" {
		return configuration{}, errors.New("Dukascopy environment must be Demo or Live")
	}
	if len(request.Username) > 256 || !utf8.ValidString(request.Username) || strings.ContainsFunc(request.Username, unicode.IsControl) {
		return configuration{}, errors.New("Dukascopy username must contain at most 256 bytes without control characters")
	}
	if len(request.Password) > 4096 || !utf8.ValidString(request.Password) || strings.ContainsFunc(request.Password, unicode.IsControl) {
		return configuration{}, errors.New("Dukascopy password must contain at most 4096 bytes without control characters")
	}
	password := request.Password
	if request.ClearPassword {
		if password != "" {
			return configuration{}, errors.New("Enter a password or clear the saved password, not both")
		}
	} else if password == "" {
		if request.Username != current.Username || request.Environment != current.Environment {
			if current.Password != "" || request.Username != "" {
				return configuration{}, errors.New("Enter a new password when changing the Dukascopy username or environment")
			}
		} else {
			password = current.Password
		}
	}
	if request.Username == "" && password != "" {
		return configuration{}, errors.New("Enter the Dukascopy username for this password")
	}
	return configuration{Version: 1, Username: request.Username, Password: password, Environment: request.Environment}, nil
}

func readConfiguration(path string) (configuration, error) {
	defaults := defaultConfiguration()
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 16*1024 {
		return defaults, errors.New("Could not read Dukascopy settings; saved file preserved")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 16*1024 {
		return defaults, errors.New("Could not read Dukascopy settings; saved file preserved")
	}
	var config configuration
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var trailing any
	if decoder.Decode(&config) != nil || decoder.Decode(&trailing) != io.EOF || config.Version != 1 {
		return defaults, errors.New("Could not read Dukascopy settings; saved file preserved")
	}
	normalized, err := normalizeSettings(Settings{Username: config.Username, Password: config.Password, Environment: config.Environment, ClearPassword: config.Password == ""}, defaults)
	if err != nil || normalized != config {
		return defaults, errors.New("Could not read Dukascopy settings; saved file preserved")
	}
	return config, nil
}

func saveConfiguration(path string, config configuration) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("Could not create the private Dukascopy settings folder")
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("Dukascopy settings is not a regular file; saved file preserved")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("Could not inspect Dukascopy settings; saved file preserved")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".dukascopy-settings-*")
	if err != nil {
		return errors.New("Could not save Dukascopy settings")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	data, _ := json.Marshal(config)
	if file.Chmod(0600) != nil {
		return errors.New("Could not protect Dukascopy settings")
	}
	if _, err = file.Write(data); err != nil {
		return errors.New("Could not save Dukascopy settings")
	}
	if file.Sync() != nil || file.Close() != nil {
		return errors.New("Could not save Dukascopy settings")
	}
	if os.Rename(file.Name(), path) != nil {
		return errors.New("Could not publish Dukascopy settings")
	}
	if directory, err := os.Open(filepath.Dir(path)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
