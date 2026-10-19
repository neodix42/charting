//go:build !windows

package main

import "github.com/wailsapp/wails/v2/pkg/options/windows"

func preparePlatformStartup(_ startupPaths) (*windows.Options, error) { return nil, nil }
func platformFatal(_ error)                                           {}
