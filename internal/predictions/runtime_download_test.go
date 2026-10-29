package predictions

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestLocalRuntime(t *testing.T) *localRuntime {
	t.Helper()
	r := NewRuntime(t.TempDir()).(*localRuntime)
	r.allowTestHTTP = true
	return r
}
func testArtifact(name, url string, body []byte) runtimeArtifact {
	return runtimeArtifact{Name: name, URL: url, SHA256: runtimeSHA(body), Size: int64(len(body))}
}

func TestRuntimeArtifactInterruptedDownloadResumesAndVerifiesBeforePublication(t *testing.T) {
	body := []byte(strings.Repeat("official pinned artifact\n", 2000))
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			if req.Header.Get("Range") != "" {
				t.Error("initial request had a range")
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.WriteHeader(200)
			_, _ = w.Write(body[:17000])
			return
		}
		if req.Header.Get("Range") != "bytes=17000-" {
			t.Errorf("resume Range=%q", req.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 17000-%d/%d", len(body)-1, len(body)))
		w.WriteHeader(206)
		_, _ = w.Write(body[17000:])
	}))
	defer server.Close()
	r := newTestLocalRuntime(t)
	r.client = server.Client()
	dir := t.TempDir()
	a := testArtifact("model.safetensors", server.URL, body)
	if err := r.downloadArtifact(context.Background(), dir, a, "model", nil); err == nil {
		t.Fatal("truncated transfer succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, a.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published interrupted model: %v", err)
	}
	partial, err := os.ReadFile(filepath.Join(dir, a.Name+".part"))
	if err != nil || len(partial) != 17000 {
		t.Fatalf("checkpoint=%d err=%v", len(partial), err)
	}
	var last Event
	if err = r.downloadArtifact(context.Background(), dir, a, "model", func(e Event) { last = e }); err != nil {
		t.Fatal(err)
	}
	if err = verifyRuntimeFile(context.Background(), filepath.Join(dir, a.Name), a); err != nil {
		t.Fatal(err)
	}
	if last.Downloaded != a.Size || last.Total != a.Size || last.Progress != 100 {
		t.Fatalf("completion progress=%+v", last)
	}
	if err = r.downloadArtifact(context.Background(), dir, a, "model", nil); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("verified cache redownloaded: %d requests", attempts.Load())
	}
}

func TestRuntimeArtifactRefusesChecksumAndMalformedRangeWithoutReplacingExisting(t *testing.T) {
	for _, mode := range []string{"checksum", "range", "status"} {
		t.Run(mode, func(t *testing.T) {
			good := []byte("the correct bytes")
			old := []byte("keep previous model until verified")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch mode {
				case "checksum":
					_, _ = w.Write([]byte(strings.Repeat("x", len(good))))
				case "range":
					w.Header().Set("Content-Range", "bytes 0-2/3")
					w.WriteHeader(206)
					_, _ = w.Write([]byte("bad"))
				case "status":
					w.WriteHeader(503)
				}
			}))
			defer server.Close()
			r := newTestLocalRuntime(t)
			r.client = server.Client()
			dir := t.TempDir()
			a := testArtifact("model.safetensors", server.URL, good)
			path := filepath.Join(dir, a.Name)
			if err := os.WriteFile(path, old, 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "range" {
				if err := os.WriteFile(path+".part", []byte("the "), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.downloadArtifact(context.Background(), dir, a, "model", nil); err == nil {
				t.Fatal("invalid artifact accepted")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(old) {
				t.Fatal("unverified bytes replaced the existing model")
			}
		})
	}
}

func TestRuntimeArtifactRangeIgnoredSafelyRestartsAndCanceledCheckpointRetained(t *testing.T) {
	body := []byte("complete immutable file")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	r := newTestLocalRuntime(t)
	r.client = server.Client()
	dir := t.TempDir()
	a := testArtifact("file.whl", server.URL, body)
	if err := os.WriteFile(filepath.Join(dir, a.Name+".part"), body[:5], 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.downloadArtifact(context.Background(), dir, a, "dependencies", nil); err != nil {
		t.Fatal(err)
	}
	if err := verifyRuntimeFile(context.Background(), filepath.Join(dir, a.Name), a); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	other := t.TempDir()
	partial := filepath.Join(other, a.Name+".part")
	_ = os.WriteFile(partial, body, 0600)
	if err := r.downloadArtifact(canceled, other, a, "dependencies", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if _, err := os.Stat(partial); err != nil {
		t.Fatalf("cancellation removed resumable artifact: %v", err)
	}
}

func TestRuntimeOfficialDownloadHosts(t *testing.T) {
	for _, raw := range []string{"https://github.com/astral-sh/python-build-standalone/releases/download/a", "https://release-assets.githubusercontent.com/asset", "https://cas-bridge.xethub.hf.co/weights", "https://files.pythonhosted.org/packages/file.whl", "https://download-r2.pytorch.org/whl/cpu/torch.whl"} {
		if !officialRuntimeURL(raw) {
			t.Errorf("official host rejected: %s", raw)
		}
	}
	for _, raw := range []string{"http://huggingface.co/model", "https://huggingface.co.evil.example/a", "https://evilgithubusercontent.com/a", "https://token@huggingface.co/a", "https://huggingface.co:8443/a", "file:///tmp/model"} {
		if officialRuntimeURL(raw) {
			t.Errorf("untrusted host accepted: %s", raw)
		}
	}
}
