package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type startupPaths struct {
	workspacePath  string
	webviewRuntime string
	webviewProfile string
	portable       bool
}

// resolveStartupPaths only inspects the package. It never creates user data, and
// --data-dir takes precedence without asking the OS for a default home directory.
func resolveStartupPaths(goos, executable, override string, defaultPath func() (string, error)) (startupPaths, error) {
	var result startupPaths
	if goos == "windows" {
		if executable == "" {
			return result, errors.New("cannot locate the CryptoDesk executable")
		}
		if isNetworkPath(executable) {
			return result, errors.New("WebView2 cannot run from a network or UNC path; extract CryptoDesk to a local folder you own")
		}
		absolute, err := filepath.Abs(executable)
		if err != nil {
			return result, fmt.Errorf("locate executable directory: %w", err)
		}
		root := filepath.Dir(absolute)
		marker := filepath.Join(root, "portable.marker")
		info, err := os.Stat(marker)
		if err == nil {
			if !info.Mode().IsRegular() {
				return result, fmt.Errorf("portable marker is not a regular file: %s", marker)
			}
			result.portable = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("inspect portable marker: %w", err)
		}
		runtimeDir := filepath.Join(root, "WebView2")
		info, err = os.Stat(runtimeDir)
		if err == nil || result.portable {
			if err != nil {
				return result, fmt.Errorf("packaged WebView2 runtime is missing at %s; extract the complete portable ZIP, including WebView2", runtimeDir)
			}
			if !info.IsDir() {
				return result, fmt.Errorf("packaged WebView2 path is not a directory: %s", runtimeDir)
			}
			runtimeExe := filepath.Join(runtimeDir, "msedgewebview2.exe")
			browser, err := os.Stat(runtimeExe)
			if err != nil || !browser.Mode().IsRegular() {
				return result, fmt.Errorf("packaged WebView2 executable is missing at %s; extract the complete portable ZIP", runtimeExe)
			}
			result.webviewRuntime = runtimeDir
		} else if !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("inspect packaged WebView2 runtime: %w", err)
		}
		if override == "" && result.portable {
			result.workspacePath = filepath.Join(root, "data", "workspace.sqlite")
		}
	}
	if override != "" {
		result.workspacePath = filepath.Join(override, "workspace.sqlite")
	}
	if result.workspacePath == "" {
		path, err := defaultPath()
		if err != nil {
			return result, err
		}
		result.workspacePath = path
	}
	if goos == "windows" {
		absolute, err := filepath.Abs(result.workspacePath)
		if err != nil {
			return result, fmt.Errorf("resolve workspace directory: %w", err)
		}
		result.workspacePath = absolute
		result.webviewProfile = filepath.Join(filepath.Dir(absolute), "WebView2Profile")
	}
	return result, nil
}

func isNetworkPath(path string) bool {
	normalized := strings.ReplaceAll(path, `\`, "/")
	// A local extended-length Windows path (\\?\C:\...) is supported.
	if strings.HasPrefix(strings.ToLower(normalized), "//?/unc/") {
		return true
	}
	return strings.HasPrefix(normalized, "//") && !strings.HasPrefix(normalized, "//?/")
}

func webviewACLArguments(major, build uint32, runtimeDir string) [][]string {
	// Fixed Version 120+ needs these AppContainer permissions on Windows 10.
	// Windows 11 (build 22000+) is explicitly exempt in Microsoft's guidance.
	if runtimeDir == "" || major != 10 || build >= 22000 {
		return nil
	}
	return [][]string{
		{runtimeDir, "/grant", "*S-1-15-2-2:(OI)(CI)(RX)"},
		{runtimeDir, "/grant", "*S-1-15-2-1:(OI)(CI)(RX)"},
	}
}
