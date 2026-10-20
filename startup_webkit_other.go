//go:build (!linux || !cgo) && !bindings

package main

func configureWebKitSignal() int { return 0 }
