package screener

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSignalChartRuntimeUsesDiscoveredPinnedBrowserAndRejectsPartialInstall(t *testing.T) {
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("chart runtime is packaged for Linux and Windows x64")
	}
	directory := t.TempDir()
	executable := filepath.Join("chrome-linux64", "chrome")
	if runtime.GOOS == "windows" {
		executable = filepath.Join("chrome-win64", "chrome.exe")
	}
	browser := filepath.Join(directory, "browsers", "chromium-1248", executable)
	packagePath := filepath.Join(directory, "node_modules", "playwright-core", "package.json")
	write := func(path, contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0700); err != nil {
			t.Fatal(err)
		}
	}
	node := filepath.Join(directory, "private-node")
	write(node, "local test executable")
	write(browser, "local test browser")
	write(packagePath, `{"version":"1.64.1"}`)
	m := &Manager{runtime: workerRuntime{node: node, worker: filepath.Join(directory, "worker.cjs")}}
	actualNode, actualDirectory, actualBrowser, err := m.SignalChartRuntime()
	if err != nil || actualNode != node || actualDirectory != directory || actualBrowser != browser {
		t.Fatal("chart runtime escaped its discovered directory", actualNode, actualDirectory, actualBrowser, err)
	}
	for _, invalid := range []string{`{"version":"1.0.0"}`, `{`, strings.Repeat(" ", 4097)} {
		write(packagePath, invalid)
		if _, _, _, err = m.SignalChartRuntime(); err == nil {
			t.Fatal("unverified Playwright version accepted")
		}
	}
	write(packagePath, `{"version":"1.64.1"}`)
	if err = os.Remove(browser); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = m.SignalChartRuntime(); err == nil {
		t.Fatal("missing browser was silently replaced with a system browser")
	}
	write(browser, "local test browser")
	m.closed = true
	if _, _, _, err = m.SignalChartRuntime(); err == nil {
		t.Fatal("closed manager exposed runtime")
	}
}
