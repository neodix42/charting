package screener

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type workerRuntime struct{ node, worker string }

func inspectRuntime(directory string, allowSystemNode bool) (workerRuntime, error) {
	worker := filepath.Join(directory, "worker.cjs")
	if st, err := os.Stat(worker); err != nil || !st.Mode().IsRegular() {
		return workerRuntime{}, fmt.Errorf("downloader worker is missing from %s", directory)
	}
	if st, err := os.Stat(filepath.Join(directory, "incremental-worker.cjs")); err != nil || !st.Mode().IsRegular() {
		return workerRuntime{}, fmt.Errorf("incremental downloader worker is missing from %s; rebuild or restore the complete Screener runtime", directory)
	}
	if st, err := os.Stat(filepath.Join(directory, "provider-session.cjs")); err != nil || !st.Mode().IsRegular() {
		return workerRuntime{}, fmt.Errorf("provider session worker is missing from %s; rebuild or restore the complete Screener runtime", directory)
	}
	if st, err := os.Stat(filepath.Join(directory, "provider-transport.cjs")); err != nil || !st.Mode().IsRegular() {
		return workerRuntime{}, fmt.Errorf("provider pacing worker is missing from %s; rebuild or restore the complete Screener runtime", directory)
	}
	data, err := os.ReadFile(filepath.Join(directory, "node_modules", "dukascopy-node", "package.json"))
	if err != nil {
		return workerRuntime{}, fmt.Errorf("pinned dukascopy-node is missing; run npm ci --prefix screener-runtime for development")
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err = json.Unmarshal(data, &pkg); err != nil || pkg.Version != loadCatalog().Version {
		return workerRuntime{}, fmt.Errorf("downloader requires dukascopy-node %s", loadCatalog().Version)
	}
	node := filepath.Join(directory, "node")
	if runtime.GOOS == "windows" {
		node += ".exe"
	}
	if st, e := os.Stat(node); e != nil || !st.Mode().IsRegular() {
		if !allowSystemNode {
			return workerRuntime{}, fmt.Errorf("bundled Node runtime is missing from %s", directory)
		}
		node, err = exec.LookPath("node")
		if err != nil {
			return workerRuntime{}, fmt.Errorf("Node.js 20 or newer is required for development; release packages bundle it")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--version")
	configureProcess(cmd)
	version, err := cmd.Output()
	major, parseErr := strconv.Atoi(strings.Split(strings.TrimPrefix(strings.TrimSpace(string(version)), "v"), ".")[0])
	if err != nil || parseErr != nil || major < 20 {
		return workerRuntime{}, fmt.Errorf("downloader requires working Node.js 20 or newer at %s", node)
	}
	return workerRuntime{node: node, worker: worker}, nil
}

func sourceRuntime(root string) (workerRuntime, error) {
	staged := filepath.Join(root, ".tools", "screener-runtime-"+runtime.GOOS+"-"+runtime.GOARCH)
	if _, err := os.Stat(staged); err == nil {
		return inspectRuntime(staged, false)
	}
	return inspectRuntime(filepath.Join(root, "screener-runtime"), true)
}

func discoverRuntime() (workerRuntime, error) {
	if explicit := strings.TrimSpace(os.Getenv("CRYPTODESK_SCREENER_RUNTIME")); explicit != "" {
		absolute, err := filepath.Abs(explicit)
		if err != nil {
			return workerRuntime{}, err
		}
		return inspectRuntime(absolute, false)
	}
	if executable, err := os.Executable(); err == nil {
		if resolved, e := filepath.EvalSymlinks(executable); e == nil {
			executable = resolved
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(executable), "go.mod")); err == nil {
			return sourceRuntime(filepath.Dir(executable))
		}
		directory := filepath.Join(filepath.Dir(executable), "screener-runtime")
		if _, err := os.Stat(directory); err == nil {
			return inspectRuntime(directory, false)
		}
	}
	// Source checkouts may use their existing local Node installation. Installed
	// packages always resolve the bundled sibling or the launcher's explicit path.
	if cwd, err := os.Getwd(); err == nil {
		for directory := cwd; ; directory = filepath.Dir(directory) {
			if _, e := os.Stat(filepath.Join(directory, "go.mod")); e == nil {
				return sourceRuntime(directory)
			}
			if filepath.Dir(directory) == directory {
				break
			}
		}
	}
	return workerRuntime{}, fmt.Errorf("bundled downloader runtime is unavailable; reinstall the complete CryptoDesk package")
}
