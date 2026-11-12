//go:build linux && cgo && !bindings && clipboardintegration

package desktopimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGTKClipboardRoundTrip(t *testing.T) {
	// This target must only run in its own xvfb-run process. It deliberately
	// owns that isolated server's clipboard, never the user's desktop clipboard.
	if os.Getenv("CRYPTODESK_CLIPBOARD_TEST") != "isolated-xvfb" || os.Getenv("GDK_BACKEND") != "x11" || !strings.HasPrefix(os.Getenv("DISPLAY"), ":") {
		t.Fatal("run only under isolated xvfb-run with CRYPTODESK_CLIPBOARD_TEST=isolated-xvfb GDK_BACKEND=x11")
	}
	done := startTestGTK()
	ready := func() {
		t.Helper()
		select {
		case <-testReady:
		case ok := <-done:
			t.Fatalf("GTK stopped: %v", ok)
		case <-time.After(5 * time.Second):
			t.Fatal("GTK test timed out")
		}
	}
	ready()
	defer func() {
		stopTestGTK()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("GTK loop did not stop")
		}
	}()
	data := testPNG(t)
	expected := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	setTestClipboard(data, "image/png")
	ready()
	actual, err := ReadClipboard(context.Background())
	if err != nil || actual != expected {
		t.Fatalf("native PNG changed: %v", err)
	}
	// A non-byte-oriented selection cannot be preserved directly. GTK converts
	// it to pixels, which the Go worker encodes as PNG without resizing.
	setTestPixelClipboard(data)
	ready()
	actual, err = ReadClipboard(context.Background())
	if err != nil {
		t.Fatalf("native pixel fallback: %v", err)
	}
	encoded := strings.TrimPrefix(actual, "data:image/png;base64,")
	decoded, e := base64.StdEncoding.DecodeString(encoded)
	if e != nil {
		t.Fatal(e)
	}
	img, e := png.Decode(bytes.NewReader(decoded))
	if e != nil || img.Bounds().Dx() != 37 || img.Bounds().Dy() != 19 {
		t.Fatalf("pixel fallback dimensions: %v", e)
	}
	r, g, b, a := img.At(0, 0).RGBA()
	if r != 0x1717 || g != 0xa8a8 || b != 0xc9c9 || a != 0xffff {
		t.Fatal("pixel fallback colors changed")
	}
	path := filepath.Join(t.TempDir(), "original screenshot.png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, mime := range []string{"text/uri-list", "x-special/gnome-copied-files"} {
		value := (&url.URL{Scheme: "file", Path: path}).String() + "\r\n"
		if strings.HasPrefix(mime, "x-special") {
			value = "copy\n" + value
		}
		setTestClipboard([]byte(value), mime)
		ready()
		actual, err = ReadClipboard(context.Background())
		if err != nil || actual != expected {
			t.Fatalf("native copied file %s: %v", mime, err)
		}
	}
	setTestClipboard(nil, "image/png")
	ready()
	if _, err = ReadClipboard(context.Background()); !errors.Is(err, ErrNoImage) {
		t.Fatalf("empty clipboard: %v", err)
	}
	// An owner which responds slowly must not block cancellation or retain its
	// Go callback token after returning. A later GUI callback is harmless.
	setTestSlowClipboard(data)
	ready()
	ctxSlow, cancelSlow := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if _, err = ReadClipboard(ctxSlow); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow clipboard cancellation: %v", err)
	}
	cancelSlow()
	setTestClipboard(data, "image/png")
	ready()
	actual, err = ReadClipboard(context.Background())
	if err != nil || actual != expected {
		t.Fatalf("recovery after cancelled native callback: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = ReadClipboard(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled native read: %v", err)
	}
}
