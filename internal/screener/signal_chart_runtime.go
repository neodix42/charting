package screener

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// SignalChartRuntime exposes only the already-discovered private runtime to Go
// callers. Chart requests and imported workspaces cannot choose executable paths.
// These pins match the existing release runtime in scripts/screener_bundle.py.
func (m *Manager) SignalChartRuntime() (node, directory, browser string, err error) {
	m.mu.Lock()
	r, problem, closed := m.runtime, m.runtimeError, m.closed
	m.mu.Unlock()
	unavailable := errors.New("the matching Screener chart runtime is unavailable; install the complete CryptoDesk package")
	if closed || problem != "" || r.node == "" || r.worker == "" {
		return "", "", "", unavailable
	}
	directory = filepath.Dir(r.worker)
	var pkg struct{ Version string }
	path := filepath.Join(directory, "node_modules", "playwright-core", "package.json")
	info, statErr := os.Stat(path)
	if statErr != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", "", "", unavailable
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || json.Unmarshal(data, &pkg) != nil || pkg.Version != "1.64.1" {
		return "", "", "", unavailable
	}
	if runtime.GOARCH != "amd64" {
		return "", "", "", unavailable
	}
	var executable string
	switch runtime.GOOS {
	case "linux":
		executable = filepath.Join("chrome-linux64", "chrome")
	case "windows":
		executable = filepath.Join("chrome-win64", "chrome.exe")
	default:
		return "", "", "", unavailable
	}
	browser = filepath.Join(directory, "browsers", "chromium-1248", executable)
	for _, path := range []string{r.node, browser} {
		info, e := os.Stat(path)
		if e != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
			return "", "", "", unavailable
		}
	}
	return r.node, directory, browser, nil
}
