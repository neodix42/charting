//go:build bindings

package main

import (
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
)

// Wails executes the bindings build during compilation to reflect App's API.
// It must not start services or open the user's workspace while doing so.
func main() {
	if err := wails.Run(&options.App{Bind: []interface{}{&App{}}}); err != nil {
		fmt.Fprintln(os.Stderr, "CryptoDesk bindings:", err)
		os.Exit(1)
	}
}
