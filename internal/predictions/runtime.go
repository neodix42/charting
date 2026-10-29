package predictions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type localRuntime struct {
	root       string
	platform   string
	arch       string
	client     *http.Client
	operations chan struct{}
	// Only isolated package tests replace artifact manifests or allow loopback HTTP.
	allowTestHTTP bool
	models        []runtimeModel
	wheels        []runtimeArtifact
	python        runtimeArtifact
}

type runtimeCompletion struct {
	Version  int               `json:"version"`
	Digest   string            `json:"digest"`
	Platform string            `json:"platform"`
	Files    map[string]string `json:"files"`
}
type modelCompletion struct {
	Version  int               `json:"version"`
	Revision string            `json:"revision"`
	Files    map[string]string `json:"files"`
}

func NewRuntime(profile string) ModelRuntime {
	absolute, err := filepath.Abs(profile)
	if err == nil {
		profile = absolute
	}
	return &localRuntime{root: filepath.Join(profile, "predictions", "runtime"), platform: runtime.GOOS, arch: runtime.GOARCH, client: runtimeHTTPClient(), operations: make(chan struct{}, 1), models: runtimeModels}
}
func (r *localRuntime) model(settings Settings) (runtimeModel, error) {
	for _, m := range r.models {
		if m.ID == settings.Model {
			return m, nil
		}
	}
	return runtimeModel{}, errors.New("choose one of the supported local prediction models")
}
func (r *localRuntime) supported() error {
	if r.arch != "amd64" || (r.platform != "linux" && r.platform != "windows") {
		return fmt.Errorf("local predictions support Linux and Windows on x86-64; this system is %s/%s", r.platform, r.arch)
	}
	return nil
}
func (r *localRuntime) modelDirectory(settings Settings, m runtimeModel) string {
	return filepath.Join(settings.ModelDirectory, strings.TrimPrefix(m.ID, "Datadog/"), m.Revision)
}
func (r *localRuntime) pythonPath() string {
	if r.platform == "windows" {
		return filepath.Join(r.root, "python", "python.exe")
	}
	return filepath.Join(r.root, "python", "bin", "python3.12")
}
func (r *localRuntime) environmentPython() string {
	if r.platform == "windows" {
		return filepath.Join(r.root, "env", "Scripts", "python.exe")
	}
	return filepath.Join(r.root, "env", "bin", "python")
}
func (r *localRuntime) workerPath() string { return filepath.Join(r.root, "worker.py") }
func (r *localRuntime) acquire(ctx context.Context) error {
	select {
	case r.operations <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *localRuntime) release() { <-r.operations }
func emitRuntime(emit func(Event), phase string, pct float64, message string) {
	if emit != nil {
		emit(Event{Phase: phase, Progress: pct, Message: message})
	}
}

// Inspect performs bounded metadata reads and file stats only. Full release
// hashes and real dependency imports are checked by background preparation and
// prediction, so application startup never reads GB of model weights.
func (r *localRuntime) Inspect(settings Settings) RuntimeStatus {
	status := RuntimeStatus{}
	if err := r.supported(); err != nil {
		status.Error = err.Error()
		return status
	}
	m, err := r.model(settings)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	if settings.ModelDirectory == "" || !filepath.IsAbs(settings.ModelDirectory) {
		status.Error = "Choose an absolute model storage folder"
		return status
	}
	runtimeErr := r.runtimePrepared()
	modelErr := r.modelPrepared(settings, m)
	status.RuntimeReady = runtimeErr == nil
	status.ModelReady = modelErr == nil
	status.Ready = status.RuntimeReady && status.ModelReady
	// Absent completion markers are normal on first use and after cancellation.
	// Existing but inconsistent records describe a repairable local problem.
	if runtimeErr != nil {
		if _, err := os.Lstat(filepath.Join(r.root, "ready.json")); err == nil {
			status.Error = "Private runtime requires repair: " + runtimeErr.Error()
		}
	}
	if modelErr != nil && status.Error == "" {
		if _, err := os.Lstat(filepath.Join(r.modelDirectory(settings, m), "complete.json")); err == nil {
			status.Error = "Local model requires repair: " + modelErr.Error()
		}
	}
	return status
}
func (r *localRuntime) runtimePrepared() error {
	var marker runtimeCompletion
	if err := readRuntimeJSON(filepath.Join(r.root, "ready.json"), &marker); err != nil {
		return err
	}
	if marker.Version != 1 || marker.Platform != r.platform || marker.Digest != runtimeDigest(r.platform) {
		return errors.New("private runtime preparation is incomplete or outdated")
	}
	for _, name := range []string{"python", "environment", "worker"} {
		expected, ok := marker.Files[name]
		if !ok {
			return errors.New("private runtime preparation is incomplete")
		}
		p := map[string]string{"python": r.pythonPath(), "environment": r.environmentPython(), "worker": r.workerPath()}[name]
		actual, err := runtimeSizeSignature(p)
		if err != nil || actual != expected {
			return errors.New("private runtime files have changed; prepare it again")
		}
	}
	worker, err := os.ReadFile(r.workerPath())
	if err != nil {
		return err
	}
	expected, _ := runtimeAssets.ReadFile("runtime_assets/worker.py")
	if sha256.Sum256(worker) != sha256.Sum256(expected) {
		return errors.New("private prediction worker has changed; prepare it again")
	}
	return nil
}
func (r *localRuntime) modelPrepared(settings Settings, m runtimeModel) error {
	dir := r.modelDirectory(settings, m)
	var marker modelCompletion
	if err := readRuntimeJSON(filepath.Join(dir, "complete.json"), &marker); err != nil {
		return err
	}
	if marker.Version != 1 || marker.Revision != m.Revision {
		return errors.New("model preparation is incomplete")
	}
	for _, a := range m.Files {
		actual, err := runtimeSizeSignature(filepath.Join(dir, a.Name))
		if err != nil || actual != marker.Files[a.Name] {
			return errors.New("model files have changed; prepare the model again")
		}
		info, err := os.Lstat(filepath.Join(dir, a.Name))
		if err != nil || info.Size() != a.Size {
			return errors.New("model files are incomplete")
		}
	}
	return nil
}
func (r *localRuntime) Prepare(ctx context.Context, settings Settings, emit func(Event)) error {
	if err := r.acquire(ctx); err != nil {
		return err
	}
	defer r.release()
	if err := r.supported(); err != nil {
		return err
	}
	m, err := r.model(settings)
	if err != nil {
		return err
	}
	if settings.ModelDirectory == "" || !filepath.IsAbs(settings.ModelDirectory) {
		return errors.New("choose an absolute model storage folder")
	}
	if err = os.MkdirAll(r.root, 0700); err != nil {
		return err
	}
	if err = r.prepareEnvironment(ctx, emit); err != nil {
		return err
	}
	dir := r.modelDirectory(settings, m)
	emitRuntime(emit, "model", 0, "Preparing "+m.ID+" from its pinned official release")
	for _, a := range m.Files {
		if err = r.downloadArtifact(ctx, dir, a, "model", emit); err != nil {
			return err
		}
	}
	marker := modelCompletion{Version: 1, Revision: m.Revision, Files: map[string]string{}}
	for _, a := range m.Files {
		signature, statErr := runtimeSizeSignature(filepath.Join(dir, a.Name))
		if statErr != nil {
			return statErr
		}
		marker.Files[a.Name] = signature
	}
	if err = writeRuntimeJSON(filepath.Join(dir, "complete.json"), marker); err != nil {
		return err
	}
	emitRuntime(emit, "ready", 100, "Local prediction runtime and model are ready")
	return nil
}
func (r *localRuntime) prepareEnvironment(ctx context.Context, emit func(Event)) error {
	if r.runtimePrepared() == nil {
		emitRuntime(emit, "verifying", 0, "Checking the prepared local runtime")
		_, err := r.runWorker(ctx, map[string]any{"action": "probe"}, emit)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	_ = os.Remove(filepath.Join(r.root, "ready.json"))
	a := r.python
	var err error
	if a.Name == "" {
		a, err = pythonArtifact(r.platform)
		if err != nil {
			return err
		}
	}
	cache := filepath.Join(r.root, "downloads")
	if err = r.downloadArtifact(ctx, cache, a, "runtime", emit); err != nil {
		return err
	}
	emitRuntime(emit, "installing", 0, "Installing private Python "+runtimePythonVersion)
	stage, err := os.MkdirTemp(r.root, "python-extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = extractPythonContext(ctx, filepath.Join(cache, a.Name), stage); err != nil {
		return fmt.Errorf("extract private Python: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// The runtime folder is entirely app-owned; model and market-data folders are
	// separate. A failed/canceled installation leaves no ready marker.
	if err = os.RemoveAll(filepath.Join(r.root, "env")); err != nil {
		return err
	}
	if err = os.RemoveAll(filepath.Join(r.root, "python")); err != nil {
		return err
	}
	if err = os.Rename(filepath.Join(stage, "python"), filepath.Join(r.root, "python")); err != nil {
		return err
	}
	if err = r.runCommand(ctx, r.pythonPath(), []string{"-I", "-m", "venv", "--copies", filepath.Join(r.root, "env")}, emit, "installing", "Creating an isolated Python environment"); err != nil {
		return err
	}
	wheels := r.wheels
	if len(wheels) == 0 {
		wheels, err = wheelArtifacts(r.platform)
		if err != nil {
			return err
		}
	}
	wheelCache := filepath.Join(cache, "wheels")
	args := []string{"-I", "-m", "pip", "--isolated", "--disable-pip-version-check", "install", "--no-index", "--no-deps", "--only-binary=:all:", "--no-compile"}
	for _, wheel := range wheels {
		if err = r.downloadArtifact(ctx, wheelCache, wheel, "dependencies", emit); err != nil {
			return err
		}
		args = append(args, filepath.Join(wheelCache, wheel.Name))
	}
	if err = r.runCommand(ctx, r.environmentPython(), args, emit, "installing", "Installing pinned CPU prediction dependencies"); err != nil {
		return err
	}
	if err = r.runCommand(ctx, r.environmentPython(), []string{"-I", "-m", "pip", "--isolated", "--disable-pip-version-check", "check"}, emit, "verifying", "Checking pinned dependency compatibility"); err != nil {
		return err
	}
	worker, err := runtimeAssets.ReadFile("runtime_assets/worker.py")
	if err != nil {
		return err
	}
	if err = os.WriteFile(r.workerPath(), worker, 0600); err != nil {
		return err
	}
	if _, err = r.runWorker(ctx, map[string]any{"action": "probe"}, emit); err != nil {
		return fmt.Errorf("check local model runtime: %w", err)
	}
	marker := runtimeCompletion{Version: 1, Digest: runtimeDigest(r.platform), Platform: r.platform, Files: map[string]string{}}
	for name, p := range map[string]string{"python": r.pythonPath(), "environment": r.environmentPython(), "worker": r.workerPath()} {
		signature, statErr := runtimeSizeSignature(p)
		if statErr != nil {
			return statErr
		}
		marker.Files[name] = signature
	}
	return writeRuntimeJSON(filepath.Join(r.root, "ready.json"), marker)
}
func (r *localRuntime) Predict(ctx context.Context, settings Settings, closes []float64, horizon int, emit func(Event)) (RuntimePrediction, error) {
	if err := r.acquire(ctx); err != nil {
		return RuntimePrediction{}, err
	}
	defer r.release()
	if err := r.supported(); err != nil {
		return RuntimePrediction{}, err
	}
	m, err := r.model(settings)
	if err != nil {
		return RuntimePrediction{}, err
	}
	if len(closes) != 512 && len(closes) != 1024 {
		return RuntimePrediction{}, errors.New("prediction context must contain 512 or 1024 closed prices")
	}
	if horizon < 1 || horizon > 256 {
		return RuntimePrediction{}, errors.New("prediction horizon must be between 1 and 256 bars")
	}
	for _, v := range closes {
		if !finite(v) {
			return RuntimePrediction{}, errors.New("prediction requires finite closed prices")
		}
	}
	if err = r.runtimePrepared(); err != nil {
		return RuntimePrediction{}, errors.New("prepare the private prediction runtime first")
	}
	if err = r.modelPrepared(settings, m); err != nil {
		return RuntimePrediction{}, errors.New("prepare the selected local model first")
	}
	emitRuntime(emit, "verifying", 0, "Verifying the local model release before forecasting")
	dir := r.modelDirectory(settings, m)
	for _, a := range m.Files {
		if err = verifyRuntimeFile(ctx, filepath.Join(dir, a.Name), a); err != nil {
			if ctx.Err() == nil {
				_ = os.Remove(filepath.Join(dir, "complete.json"))
			}
			return RuntimePrediction{}, fmt.Errorf("local model verification failed: %w", err)
		}
	}
	points, err := r.runWorker(ctx, map[string]any{"action": "predict", "modelDirectory": dir, "closes": closes, "horizon": horizon}, emit)
	if err != nil {
		var workerErr *runtimeWorkerError
		if ctx.Err() == nil && errors.As(err, &workerErr) && workerErr.phase == "runtime" {
			_ = os.Remove(filepath.Join(r.root, "ready.json"))
		}
		return RuntimePrediction{}, err
	}
	if len(points) != horizon {
		return RuntimePrediction{}, errors.New("prediction worker returned an incomplete forecast")
	}
	for i, p := range points {
		if p.Step != i+1 || !finite(p.Lower) || !finite(p.Median) || !finite(p.Upper) || p.Lower > p.Median || p.Median > p.Upper {
			return RuntimePrediction{}, errors.New("prediction worker returned invalid forecast quantiles")
		}
	}
	emitRuntime(emit, "complete", 100, "Local forecast is complete")
	return RuntimePrediction{Revision: m.Revision, Points: points}, nil
}
func readRuntimeJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return errors.New("invalid local runtime completion record")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}
func writeRuntimeJSON(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".completion-")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp, path)
}
func runtimeSHA(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
