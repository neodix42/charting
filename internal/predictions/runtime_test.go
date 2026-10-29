package predictions

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeManifestsPinOnlyOfficialCompleteArtifacts(t *testing.T) {
	if !ValidModel(DefaultModel) || len(Models()) != 5 {
		t.Fatal("default or official model selection missing")
	}
	models := Models()
	models[0].ID = "changed"
	if Models()[0].ID == "changed" {
		t.Fatal("model catalog is externally mutable")
	}
	for _, m := range runtimeModels {
		if len(m.Revision) != 40 || len(m.Files) != 2 {
			t.Fatalf("model pin incomplete: %+v", m)
		}
		for _, a := range m.Files {
			if !strings.Contains(a.URL, "/"+m.Revision+"/") {
				t.Fatalf("unpinned model URL %s", a.URL)
			}
			checkRuntimeArtifact(t, a)
		}
	}
	for _, platform := range []string{"linux", "windows"} {
		a, err := pythonArtifact(platform)
		if err != nil {
			t.Fatal(err)
		}
		checkRuntimeArtifact(t, a)
		wheels, err := wheelArtifacts(platform)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, w := range wheels {
			checkRuntimeArtifact(t, w)
			if w.Package == "" || w.Version == "" || seen[w.Package] {
				t.Fatalf("invalid wheel pin %+v", w)
			}
			seen[w.Package] = true
			if w.Package == "torch" && !strings.HasSuffix(w.Version, "+cpu") {
				t.Fatal("GPU dependency selected for default CPU runtime")
			}
		}
		for _, p := range []string{"torch", "toto-2", "dd-unit-scaling", "gluonts", "pytorch-lightning", "pip"} {
			if !seen[p] {
				t.Fatalf("missing runtime dependency %s/%s", platform, p)
			}
		}
		if runtimeDigest(platform) == "" {
			t.Fatal("runtime manifest missing")
		}
	}
}
func checkRuntimeArtifact(t *testing.T, a runtimeArtifact) {
	t.Helper()
	hash, err := hex.DecodeString(a.SHA256)
	if err != nil || len(hash) != 32 || a.Size < 1 || filepath.Base(a.Name) != a.Name || !officialRuntimeURL(a.URL) {
		t.Fatalf("invalid artifact pin: %+v", a)
	}
}

func prepareTestRuntimeMarkers(t *testing.T, r *localRuntime, settings Settings) (runtimeModel, string) {
	t.Helper()
	m := runtimeModel{ID: DefaultModel, Revision: strings.Repeat("a", 40), Files: []runtimeArtifact{testArtifact("config.json", "", []byte("{}")), testArtifact("model.safetensors", "", []byte("model weights"))}}
	r.models = []runtimeModel{m}
	if err := os.MkdirAll(r.root, 0700); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{"python": r.pythonPath(), "environment": r.environmentPython(), "worker": r.workerPath()}
	marker := runtimeCompletion{Version: 1, Digest: runtimeDigest(r.platform), Platform: r.platform, Files: map[string]string{}}
	for name, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		body := []byte("private python")
		if name == "worker" {
			body, _ = runtimeAssets.ReadFile("runtime_assets/worker.py")
		}
		if err := os.WriteFile(p, body, 0600); err != nil {
			t.Fatal(err)
		}
		signature, err := runtimeSizeSignature(p)
		if err != nil {
			t.Fatal(err)
		}
		marker.Files[name] = signature
	}
	if err := writeRuntimeJSON(filepath.Join(r.root, "ready.json"), marker); err != nil {
		t.Fatal(err)
	}
	dir := r.modelDirectory(settings, m)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	complete := modelCompletion{Version: 1, Revision: m.Revision, Files: map[string]string{}}
	for i, a := range m.Files {
		body := []byte("{}")
		if i == 1 {
			body = []byte("model weights")
		}
		p := filepath.Join(dir, a.Name)
		if err := os.WriteFile(p, body, 0600); err != nil {
			t.Fatal(err)
		}
		signature, err := runtimeSizeSignature(p)
		if err != nil {
			t.Fatal(err)
		}
		complete.Files[a.Name] = signature
	}
	if err := writeRuntimeJSON(filepath.Join(dir, "complete.json"), complete); err != nil {
		t.Fatal(err)
	}
	return m, dir
}

func TestRuntimeInspectIsPassiveAndDetectsChangedOrIncompletePublication(t *testing.T) {
	r := newTestLocalRuntime(t)
	r.platform = "linux"
	r.arch = "amd64"
	settings := Settings{Model: DefaultModel, ModelDirectory: filepath.Join(t.TempDir(), "models")}
	status := r.Inspect(settings)
	if status.Ready || status.RuntimeReady || status.ModelReady || status.Error != "" {
		t.Fatalf("normal first use=%+v", status)
	}
	if _, err := os.Stat(r.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Inspect created runtime folders")
	}
	_, dir := prepareTestRuntimeMarkers(t, r, settings)
	if status = r.Inspect(settings); !status.Ready || status.Error != "" {
		t.Fatalf("prepared=%+v", status)
	}
	p := filepath.Join(dir, "model.safetensors")
	info, _ := os.Stat(p)
	if err := os.Chtimes(p, time.Now(), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if status = r.Inspect(settings); status.Ready || status.ModelReady || !status.RuntimeReady || status.Error == "" {
		t.Fatalf("changed model=%+v", status)
	}
	if err := os.Remove(filepath.Join(dir, "complete.json")); err != nil {
		t.Fatal(err)
	}
	if status = r.Inspect(settings); status.Ready || status.Error != "" {
		t.Fatalf("incomplete publication=%+v", status)
	}
	if err := os.WriteFile(r.workerPath(), []byte("changed worker"), 0600); err != nil {
		t.Fatal(err)
	}
	if status = r.Inspect(settings); status.RuntimeReady || status.Error == "" {
		t.Fatalf("changed worker=%+v", status)
	}
}

func TestRuntimePredictionVerifiesActualModelAndInvalidatesCorruptCompletion(t *testing.T) {
	r := newTestLocalRuntime(t)
	r.platform = "linux"
	r.arch = "amd64"
	settings := Settings{Model: DefaultModel, ModelDirectory: filepath.Join(t.TempDir(), "models")}
	_, dir := prepareTestRuntimeMarkers(t, r, settings)
	p := filepath.Join(dir, "model.safetensors")
	info, _ := os.Stat(p)
	if err := os.WriteFile(p, []byte("model corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if !r.Inspect(settings).Ready {
		t.Fatal("metadata-only Inspect unexpectedly read model contents")
	}
	closes := make([]float64, 512)
	for i := range closes {
		closes[i] = float64(i) - 10
	}
	_, err := r.Predict(context.Background(), settings, closes, 20, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("corruption=%v", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "complete.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt model completion retained: %v", err)
	}
	if r.Inspect(settings).ModelReady {
		t.Fatal("model corruption cannot be repaired through Prepare")
	}
}

func TestRuntimeRejectsInvalidInputsWithoutStartingProcesses(t *testing.T) {
	r := newTestLocalRuntime(t)
	settings := Settings{Model: DefaultModel, ModelDirectory: t.TempDir()}
	for _, input := range []struct {
		closes []float64
		h      int
	}{{make([]float64, 511), 20}, {make([]float64, 512), 0}, {make([]float64, 512), 257}} {
		if _, err := r.Predict(context.Background(), settings, input.closes, input.h, nil); err == nil {
			t.Fatal("invalid prediction accepted")
		}
	}
	closes := make([]float64, 512)
	closes[20] = math.NaN()
	if _, err := r.Predict(context.Background(), settings, closes, 20, nil); err == nil {
		t.Fatal("nonfinite history accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	r.operations <- struct{}{}
	defer r.release()
	if err := r.Prepare(canceled, settings, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued preparation ignored cancellation: %v", err)
	}
}
