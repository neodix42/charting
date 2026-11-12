//go:build !linux || !cgo || bindings

package desktopimage

func requestClipboard(uint64) error { return ErrUnavailable }
