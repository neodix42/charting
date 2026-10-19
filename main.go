package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"cryptodesk/internal/storage"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	dataDir := flag.String("data-dir", "", "isolated workspace directory (default: XDG data home/cryptodesk)")
	flag.Parse()
	path, err := storage.DefaultPath()
	if *dataDir != "" {
		path = filepath.Join(*dataDir, "workspace.sqlite")
	}
	if err != nil {
		fatal(err)
	}
	store, err := storage.Open(path)
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
	err = wails.Run(&options.App{Title: "CryptoDesk", Width: width, Height: height, MinWidth: 1100, MinHeight: 720, BackgroundColour: &options.RGBA{R: 14, G: 18, B: 25, A: 255}, AssetServer: &assetserver.Options{Assets: assets}, OnStartup: a.startup, OnDomReady: a.domready, OnBeforeClose: a.beforeClose, OnShutdown: a.shutdown, Bind: []interface{}{a}, Linux: &linux.Options{ProgramName: "cryptodesk", WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand}})
	if err != nil {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "CryptoDesk:", err); os.Exit(1) }
