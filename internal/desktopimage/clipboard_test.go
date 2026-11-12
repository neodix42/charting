package desktopimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 37, 19))
	img.SetNRGBA(0, 0, color.NRGBA{R: 0x17, G: 0xa8, B: 0xc9, A: 255})
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestClipboardPreservesEncodedImageAndRejectsOtherContent(t *testing.T) {
	data := testPNG(t)
	actual, err := imageDataURL(data)
	if err != nil || actual != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("original image changed: %v %q", err, actual)
	}
	for _, invalid := range [][]byte{nil, []byte("plain text"), make([]byte, MaxBytes+1)} {
		if _, err = imageDataURL(invalid); err == nil {
			t.Fatal("accepted invalid clipboard content")
		}
	}
}

func TestClipboardCopiedFileURIsAreLocalRegularImages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "screen with spaces.png")
	original := testPNG(t)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	for _, list := range []string{uri + "\r\n", "copy\n" + uri, "cut\n" + uri, "# files\nhttps://example.com/a.png\n" + uri} {
		actual, err := clipboardFiles([]byte(list))
		if err != nil || actual != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(original) {
			t.Fatalf("URI copy: %v", err)
		}
	}
	for _, list := range []string{"https://example.com/screen.png", "file://remote-host" + path, "file:" + path + "?query=1", "file:" + path + "#fragment", "file:relative.png", "file:" + dir} {
		if _, err := clipboardFiles([]byte(list)); !errors.Is(err, ErrNoImage) {
			t.Fatalf("invalid URI accepted: %q %v", list, err)
		}
	}
}

func TestClipboardCancellationAndLateCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadClipboard(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
	id := sequence.Add(1)
	ch := make(chan clipboardResult, 1)
	requests.Lock()
	requests.items[id] = ch
	requests.Unlock()
	complete(id, clipboardResult{data: []byte("first")})
	complete(id, clipboardResult{data: []byte("late")})
	if string((<-ch).data) != "first" || pending(id) {
		t.Fatal("completion was repeated or leaked")
	}
	// The native callback may arrive after the caller has timed out/shut down.
	complete(sequence.Add(1), clipboardResult{data: []byte("abandoned")})
}

func TestClipboardPixelConversionHonorsPaddingAndCancellation(t *testing.T) {
	for _, result := range []clipboardResult{
		{width: 1, height: 2, stride: 4, channels: 3, pixels: []byte{17, 22, 33, 0, 44, 55, 66}},
		{width: 1, height: 2, stride: 6, channels: 4, pixels: []byte{17, 22, 33, 255, 0, 0, 44, 55, 66, 255}},
	} {
		value, err := encodeClipboardResult(context.Background(), result)
		if err != nil {
			t.Fatal(err)
		}
		data, err := base64.StdEncoding.DecodeString(value[len("data:image/png;base64,"):])
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, a := img.At(0, 1).RGBA()
		if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 2 || r != 44*257 || g != 55*257 || b != 66*257 || a != 65535 {
			t.Fatal("pixel stride or dimensions changed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writer := limitedBuffer{ctx: ctx}
	if _, err := writer.Write([]byte("pixels")); !errors.Is(err, context.Canceled) {
		t.Fatalf("encoding ignored cancellation: %v", err)
	}
	if _, err := clipboardFilesContext(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("file read ignored cancellation: %v", err)
	}
}
