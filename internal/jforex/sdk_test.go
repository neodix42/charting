package jforex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type sdkTransport func(*http.Request) (*http.Response, error)

func (transport sdkTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func fixtureArtifact(name string, data []byte) sdkArtifact {
	hash := sha256.Sum256(data)
	return sdkArtifact{Name: name, URL: "https://repo.maven.apache.org/maven2/cryptodesk-test/" + name, SHA256: hex.EncodeToString(hash[:]), Bytes: int64(len(data))}
}
func fixtureManifest(artifacts ...sdkArtifact) sdkManifest {
	return sdkManifest{Version: 1, SDKVersion: "3.6.51", APIVersion: "2.13.99", JavaMinimum: 17, MainClass: "cryptodesk.jforex.Bridge", Artifacts: artifacts}
}

func TestSDKSetupVerifiesArtifactsBeforeUseAndReusesVerifiedLocalFiles(t *testing.T) {
	data := []byte("a test artifact, explicitly not a real JAR")
	artifact := fixtureArtifact("fixture-1.jar", data)
	manifest := fixtureManifest(artifact)
	directory := t.TempDir()
	var requests atomic.Int32
	client := newSDKClient()
	client.Transport = sdkTransport(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}, Request: request}, nil
	})
	if err := prepareSDK(context.Background(), directory, manifest, client); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal(requests.Load())
	}
	if err := prepareSDK(context.Background(), directory, manifest, client); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("verified local JAR was downloaded again")
	}
	path := filepath.Join(directory, "lib", artifact.Name)
	if err := os.WriteFile(path, bytes.Repeat([]byte("!"), len(data)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareSDK(context.Background(), directory, manifest, client); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || !validArtifact(path, artifact) {
		t.Fatal("changed local artifact was trusted")
	}
}

func TestSDKSetupPreservesCompletedFilesAfterCancellationAndRejectsHashMismatch(t *testing.T) {
	first := []byte("first complete artifact")
	second := []byte("second complete artifact")
	a, b := fixtureArtifact("first.jar", first), fixtureArtifact("second.jar", second)
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	client := newSDKClient()
	client.Transport = sdkTransport(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "first.jar") {
			return &http.Response{StatusCode: 200, ContentLength: int64(len(first)), Body: io.NopCloser(bytes.NewReader(first)), Header: http.Header{}, Request: request}, nil
		}
		cancel()
		return nil, context.Canceled
	})
	if err := prepareSDK(ctx, directory, fixtureManifest(a, b), client); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !validArtifact(filepath.Join(directory, "lib", a.Name), a) {
		t.Fatal("cancellation removed completed artifact")
	}
	entries, _ := os.ReadDir(filepath.Join(directory, "lib"))
	if len(entries) != 1 {
		t.Fatal("incomplete JAR was published", entries)
	}
	client.Transport = sdkTransport(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: int64(len(second)), Body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), len(second)))), Header: http.Header{}, Request: request}, nil
	})
	if err := prepareSDK(context.Background(), directory, fixtureManifest(a, b), client); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatal("hash mismatch accepted", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "lib", b.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unverified code published")
	}
}

func TestSDKSetupRejectsForeignJARsTraversalAndUnapprovedRedirects(t *testing.T) {
	data := []byte("fixture")
	artifact := fixtureArtifact("fixture.jar", data)
	directory := t.TempDir()
	lib := filepath.Join(directory, "lib")
	if err := os.Mkdir(lib, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(lib, "untrusted.jar")
	if err := os.WriteFile(foreign, []byte("preserve me"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareSDK(context.Background(), directory, fixtureManifest(artifact), newSDKClient()); err == nil {
		t.Fatal("unrecognized code would enter wildcard classpath")
	}
	actual, _ := os.ReadFile(foreign)
	if string(actual) != "preserve me" {
		t.Fatal("foreign code deleted")
	}
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	bad := artifact
	bad.Name = "../escape.jar"
	if validateManifest(fixtureManifest(bad)) == nil {
		t.Fatal("artifact path traversal accepted")
	}
	for _, raw := range []string{"http://repo.maven.apache.org/maven2/x.jar", "https://evil.example/maven2/x.jar", "https://repo.maven.apache.org:443/maven2/x.jar", "https://repo.maven.apache.org/maven2/x.jar?token=secret", "https://www.dukascopy.com/unrelated/file.jar"} {
		parsed, _ := url.Parse(raw)
		if allowedSDKURL(parsed) {
			t.Fatal("unapproved source allowed", raw)
		}
	}
	var requests atomic.Int32
	client := newSDKClient()
	client.Transport = sdkTransport(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.example/payload.jar"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})
	if err := prepareSDK(context.Background(), directory, fixtureManifest(artifact), client); err == nil {
		t.Fatal("download followed unsafe redirect")
	}
	if requests.Load() != 1 {
		t.Fatal("unapproved redirect contacted external host")
	}
}

func TestSDKManifestPinMatchesReviewedSourceAndRejectsChanges(t *testing.T) {
	path := filepath.Join("..", "..", "jforex-bridge", "SDK-ARTIFACTS.json")
	manifest, err := readSDKManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Artifacts) != 51 {
		t.Fatal("SDK dependency pin changed unexpectedly", len(manifest.Artifacts))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := filepath.Join(t.TempDir(), "SDK-ARTIFACTS.json")
	if err := os.WriteFile(tampered, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSDKManifest(tampered); err == nil {
		t.Fatal("modified SDK manifest accepted")
	}
}

func TestCancelConnectionSetupRetainsOtherServiceStateAndStopsPendingPreparation(t *testing.T) {
	m, count := testManager(t, "")
	entered := make(chan struct{})
	m.prepare = func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }
	done := make(chan error, 1)
	go func() { _, err := m.TestConnection(context.Background()); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("SDK preparation never began")
	}
	if _, err := m.Challenge(context.Background()); err == nil {
		t.Fatal("overlapping explicit setup accepted")
	}
	m.CancelConnectionTest()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SDK setup could not be canceled")
	}
	if count.Load() != 0 {
		t.Fatal("canceled preparation started Java")
	}
	if !m.Info().PasswordConfigured {
		t.Fatal("cancellation discarded saved credentials")
	}
}
