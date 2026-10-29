package jforex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// An installed manifest cannot redirect execution to newly published artifacts.
// Updating the SDK requires reviewing and changing both the manifest and pin.
const sdkManifestSHA256 = "dfad6867b4afbc235e5d0f4b39502852aca54a9fffdcd9e53f6578c38b0b75a3"

type sdkArtifact struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type sdkManifest struct {
	Version     int           `json:"version"`
	SDKVersion  string        `json:"sdkVersion"`
	APIVersion  string        `json:"apiVersion"`
	JavaMinimum int           `json:"javaMinimum"`
	MainClass   string        `json:"mainClass"`
	Artifacts   []sdkArtifact `json:"artifacts"`
}

func readSDKManifest(path string) (sdkManifest, error) {
	failure := errors.New("The pinned JForex SDK manifest is missing or changed; restore the complete jforex-runtime folder supplied with CryptoDesk")
	file, err := os.Open(path)
	if err != nil {
		return sdkManifest{}, failure
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 128*1024+1))
	if err != nil || len(data) > 128*1024 {
		return sdkManifest{}, failure
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != sdkManifestSHA256 {
		return sdkManifest{}, failure
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result sdkManifest
	if decoder.Decode(&result) != nil || validateManifest(result) != nil {
		return sdkManifest{}, failure
	}
	return result, nil
}

func validateManifest(manifest sdkManifest) error {
	if manifest.Version != 1 || manifest.SDKVersion != "3.6.51" || manifest.APIVersion != "2.13.99" || manifest.JavaMinimum != 17 || manifest.MainClass != "cryptodesk.jforex.Bridge" || len(manifest.Artifacts) == 0 || len(manifest.Artifacts) > 100 {
		return errors.New("Unsupported JForex SDK manifest")
	}
	seen := map[string]bool{}
	namePattern := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.jar$`)
	total := int64(0)
	for _, artifact := range manifest.Artifacts {
		decoded, err := hex.DecodeString(artifact.SHA256)
		parsed, urlErr := url.Parse(artifact.URL)
		if !namePattern.MatchString(artifact.Name) || seen[artifact.Name] || artifact.Bytes <= 0 || artifact.Bytes > 64*1024*1024 || err != nil || len(decoded) != sha256.Size || urlErr != nil || !allowedSDKURL(parsed) {
			return errors.New("Invalid pinned JForex SDK artifact")
		}
		seen[artifact.Name] = true
		total += artifact.Bytes
	}
	if total > 128*1024*1024 {
		return errors.New("JForex SDK artifacts exceed their total size limit")
	}
	return nil
}

func allowedSDKURL(value *url.URL) bool {
	if value == nil || value.Scheme != "https" || value.User != nil || value.Port() != "" || value.RawQuery != "" || value.Fragment != "" {
		return false
	}
	switch value.Hostname() {
	case "www.dukascopy.com":
		return strings.HasPrefix(value.Path, "/client/jforexlib/publicrepo/")
	case "repo.maven.apache.org":
		return strings.HasPrefix(value.Path, "/maven2/")
	default:
		return false
	}
}

func newSDKClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !allowedSDKURL(request.URL) {
			return errors.New("JForex SDK download redirected outside its approved HTTPS sources")
		}
		return nil
	}}
}

func prepareSDK(ctx context.Context, directory string, manifest sdkManifest, client *http.Client) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	lib := filepath.Join(directory, "lib")
	if err := os.MkdirAll(lib, 0700); err != nil {
		return errors.New("Could not create the private JForex SDK folder")
	}
	// Never add unexpected local JARs to a wildcard classpath. Versioned folders
	// contain exactly the reviewed artifacts; preserve and reject foreign files.
	entries, err := os.ReadDir(lib)
	if err != nil {
		return errors.New("Could not inspect the private JForex SDK folder")
	}
	names := map[string]bool{}
	for _, artifact := range manifest.Artifacts {
		names[artifact.Name] = true
	}
	for _, entry := range entries {
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") && !names[entry.Name()] {
			return errors.New("An unrecognized JAR exists in the private JForex SDK folder; move it aside before connecting")
		}
	}
	for _, artifact := range manifest.Artifacts {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(lib, artifact.Name)
		if validArtifact(path, artifact) {
			continue
		}
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return errors.New("A JForex SDK artifact is not a regular file; existing data preserved")
		}
		if err := downloadArtifact(ctx, client, path, artifact); err != nil {
			return err
		}
	}
	return nil
}

func validArtifact(path string, artifact sdkArtifact) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Bytes {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, artifact.Bytes+1))
	return err == nil && n == artifact.Bytes && hex.EncodeToString(hash.Sum(nil)) == artifact.SHA256
}

func downloadArtifact(ctx context.Context, client *http.Client, path string, artifact sdkArtifact) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return errors.New("Could not prepare the pinned JForex SDK download")
	}
	request.Header.Set("User-Agent", "CryptoDesk JForex SDK setup")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("Could not download JForex SDK artifact %s; check the network and retry", artifact.Name)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("JForex SDK download failed for %s (HTTP %d); retry later", artifact.Name, response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != artifact.Bytes {
		return fmt.Errorf("JForex SDK artifact %s failed its pinned size check", artifact.Name)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".jforex-sdk-*")
	if err != nil {
		return errors.New("Could not save a JForex SDK download")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if file.Chmod(0600) != nil {
		return errors.New("Could not protect a JForex SDK artifact")
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, artifact.Bytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("The JForex SDK download was interrupted; retry to retain completed artifacts")
	}
	if n != artifact.Bytes || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return fmt.Errorf("JForex SDK artifact %s failed SHA-256 verification; no downloaded code was executed", artifact.Name)
	}
	if file.Sync() != nil || file.Close() != nil {
		return errors.New("Could not finish saving a JForex SDK artifact")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("Could not publish a verified JForex SDK artifact")
	}
	return nil
}
