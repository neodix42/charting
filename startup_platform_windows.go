package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/wailsapp/wails/v2/pkg/options/windows"
	win "golang.org/x/sys/windows"
)

func preparePlatformStartup(paths startupPaths) (*windows.Options, error) {
	version := win.RtlGetVersion()
	commands := webviewACLArguments(version.MajorVersion, version.BuildNumber, paths.webviewRuntime)
	if len(commands) != 0 {
		systemDir, err := win.GetSystemDirectory()
		if err != nil {
			return nil, fmt.Errorf("locate Windows runtime permission tool: %w", err)
		}
		for _, args := range commands {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			command := exec.CommandContext(ctx, filepath.Join(systemDir, "icacls.exe"), args...)
			command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: win.CREATE_NO_WINDOW}
			output, err := command.CombinedOutput()
			cancel()
			if err != nil {
				return nil, fmt.Errorf("enable packaged WebView2 permissions on Windows 10: %w (%s). Extract the full app to a local folder you own and restart CryptoDesk", err, strings.TrimSpace(string(output)))
			}
		}
	}
	if err := os.MkdirAll(paths.webviewProfile, 0700); err != nil {
		return nil, fmt.Errorf("create WebView2 profile at %s: %w; use a writable local folder or --data-dir", paths.webviewProfile, err)
	}
	return &windows.Options{WebviewBrowserPath: paths.webviewRuntime, WebviewUserDataPath: paths.webviewProfile}, nil
}

func platformFatal(err error) {
	message, _ := win.UTF16PtrFromString("CryptoDesk could not start.\n\n" + strings.ReplaceAll(err.Error(), "\x00", ""))
	title, _ := win.UTF16PtrFromString("CryptoDesk startup error")
	_, _ = win.MessageBox(0, message, title, win.MB_OK|win.MB_ICONERROR|win.MB_TASKMODAL)
}
