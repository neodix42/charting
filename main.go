//go:build !bindings

package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"

	"cryptodesk/internal/storage"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func main() {
	dataDir := flag.String("data-dir", "", "workspace directory (portable Windows default: executable directory/data)")
	flag.Parse()
	var executable string
	if runtime.GOOS == "windows" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			fatal(fmt.Errorf("locate executable: %w", err))
		}
	}
	paths, err := resolveStartupPaths(runtime.GOOS, executable, *dataDir, storage.DefaultPath)
	if err != nil {
		fatal(err)
	}
	windowsOptions, err := preparePlatformStartup(paths)
	if err != nil {
		fatal(err)
	}
	store, err := storage.Open(paths.workspacePath)
	if err != nil {
		fatal(err)
	}
	defer store.Close()
	if err := desktopSmokePrepare(store); err != nil {
		fatal(err)
	}
	loaded, err := store.Load()
	var recovery *storage.RecoveryError
	if err != nil && !errors.As(err, &recovery) {
		fatal(err)
	}
	a := newApp(store, loaded)
	width, height := a.window.Width, a.window.Height
	err = wails.Run(&options.App{Title: "CryptoDesk", Width: width, Height: height, MinWidth: 1100, MinHeight: 720, BackgroundColour: &options.RGBA{R: 14, G: 18, B: 25, A: 255}, AssetServer: &assetserver.Options{Assets: assets}, OnStartup: a.startup, OnDomReady: a.domready, OnBeforeClose: a.beforeClose, OnShutdown: a.shutdown, Bind: []interface{}{a}, Linux: &linux.Options{Icon: appIcon, ProgramName: "cryptodesk", WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand}, Windows: windowsOptions})
	if err != nil {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "CryptoDesk:", err); platformFatal(err); os.Exit(1) }
