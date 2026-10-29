package predictions

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type runtimeTail struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (w *runtimeTail) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if len(p) >= w.limit {
		w.data = append(w.data[:0], p[len(p)-w.limit:]...)
	} else {
		extra := len(w.data) + len(p) - w.limit
		if extra > 0 {
			w.data = append(w.data[:0], w.data[extra:]...)
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}
func (w *runtimeTail) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimSpace(string(w.data))
}

func (r *localRuntime) command(ctx context.Context, program string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = r.root
	cmd.Env = r.environment()
	cmd.WaitDelay = 2 * time.Second
	configureRuntimeProcess(cmd)
	return cmd
}
func (r *localRuntime) environment() []string {
	result := []string{}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "PYTHON") || strings.HasPrefix(upper, "PIP_") || strings.HasPrefix(upper, "HF_") || strings.HasPrefix(upper, "HUGGINGFACE_") || strings.HasPrefix(upper, "TORCH_") || upper == "VIRTUAL_ENV" || upper == "CONDA_PREFIX" || upper == "REQUESTS_CA_BUNDLE" || upper == "CURL_CA_BUNDLE" {
			continue
		}
		result = append(result, entry)
	}
	cache := filepath.Join(r.root, "cache")
	return append(result, "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1", "PIP_CONFIG_FILE="+os.DevNull, "HF_HUB_OFFLINE=1", "HF_DATASETS_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1", "HF_HUB_DISABLE_XET=1", "DO_NOT_TRACK=1", "HF_HOME="+filepath.Join(cache, "huggingface"), "MPLCONFIGDIR="+filepath.Join(cache, "matplotlib"), "TORCH_HOME="+filepath.Join(cache, "torch"), "OMP_NUM_THREADS=8", "MKL_NUM_THREADS=8", "TOKENIZERS_PARALLELISM=false")
}
func (r *localRuntime) runCommand(ctx context.Context, program string, args []string, emit func(Event), phase, message string) error {
	emitRuntime(emit, phase, 0, message)
	cmd := r.command(ctx, program, args...)
	output := &runtimeTail{limit: 16 * 1024}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s: %s", message, runtimeCommandError(err, output.String()))
	}
	return nil
}
func runtimeCommandError(err error, output string) string {
	if output == "" {
		return err.Error()
	}
	return output
}

type runtimeWorkerMessage struct {
	Type     string  `json:"type"`
	Phase    string  `json:"phase"`
	Progress float64 `json:"progress"`
	Message  string  `json:"message"`
	Points   []Point `json:"points"`
}

type runtimeWorkerError struct{ phase, message string }

func (e *runtimeWorkerError) Error() string { return "local prediction failed: " + e.message }

func (r *localRuntime) runWorker(ctx context.Context, request map[string]any, emit func(Event)) ([]Point, error) {
	return r.runProtocol(ctx, r.environmentPython(), []string{"-I", "-u", r.workerPath()}, request, emit)
}

// runProtocol owns its stdin pipe until the worker exits. EOF therefore also
// detects abrupt owner loss, while cancellation terminates the entire process
// tree. Both streams and every protocol record have explicit memory bounds.
func (r *localRuntime) runProtocol(ctx context.Context, program string, args []string, request map[string]any, emit func(Event)) ([]Point, error) {
	cmd := r.command(ctx, program, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &runtimeTail{limit: 16 * 1024}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start private prediction worker: %w", err)
	}
	fail := func(cause error) ([]Point, error) {
		_ = cmd.Cancel()
		_ = cmd.Wait()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, cause
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fail(err)
	}
	if len(payload) > 128*1024 {
		return fail(errors.New("prediction request exceeds its protocol limit"))
	}
	if _, err = stdin.Write(append(payload, '\n')); err != nil {
		return fail(fmt.Errorf("write prediction request: %w", err))
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var points []Point
	terminal := ""
	total := 0
	workerError := ""
	errorPhase := ""
	for scanner.Scan() {
		line := scanner.Bytes()
		total += len(line)
		if total > 4*1024*1024 {
			return fail(errors.New("prediction worker exceeded its output limit"))
		}
		var message runtimeWorkerMessage
		if err = json.Unmarshal(line, &message); err != nil {
			return fail(errors.New("prediction worker returned an invalid protocol record"))
		}
		if terminal != "" {
			return fail(errors.New("prediction worker returned data after its final response"))
		}
		switch message.Type {
		case "progress":
			if !finite(message.Progress) || message.Progress < 0 || message.Progress > 100 || len(message.Message) > 4000 || len(message.Phase) > 64 {
				return fail(errors.New("prediction worker returned invalid progress"))
			}
			emitRuntime(emit, message.Phase, message.Progress, message.Message)
		case "ready":
			terminal = "ready"
		case "result":
			terminal = "result"
			points = message.Points
		case "error":
			terminal = "error"
			workerError = message.Message
			errorPhase = message.Phase
			if len(workerError) > 4000 {
				workerError = workerError[:4000]
			}
		default:
			return fail(errors.New("prediction worker returned an unknown protocol record"))
		}
	}
	if err = scanner.Err(); err != nil {
		return fail(fmt.Errorf("read prediction worker output: %w", err))
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if workerError != "" {
		return nil, &runtimeWorkerError{phase: errorPhase, message: workerError}
	}
	if err != nil {
		return nil, fmt.Errorf("private prediction worker stopped: %s", runtimeCommandError(err, stderr.String()))
	}
	action, _ := request["action"].(string)
	if action == "probe" && terminal != "ready" {
		return nil, errors.New("private prediction worker did not confirm its dependencies")
	}
	if action != "probe" && terminal != "result" {
		return nil, errors.New("private prediction worker did not return a forecast")
	}
	return points, nil
}

// Keep io imported here as a compile-time assertion of the bounded log sink.
var _ io.Writer = (*runtimeTail)(nil)
