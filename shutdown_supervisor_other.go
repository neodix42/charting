//go:build !linux && !bindings

package main

import "os"

func superviseGUI(_ <-chan os.Signal) (bool, int, error) { return false, 0, nil }
