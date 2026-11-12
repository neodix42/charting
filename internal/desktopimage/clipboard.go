// Package desktopimage bridges native desktop image clipboard formats which
// embedded webviews do not consistently expose as ClipboardEvent files.
package desktopimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const MaxBytes = 32 * 1024 * 1024

var ErrNoImage = errors.New("The clipboard has no supported image. Copy an image or paste a screenshot.")
var ErrUnavailable = errors.New("Native image clipboard access is unavailable; use Paste in the image panel or Choose images.")

type clipboardResult struct {
	data                            []byte
	pixels                          []byte
	width, height, stride, channels int
	err                             error
	fileList                        []byte
}

var requests = struct {
	sync.Mutex
	items map[uint64]chan clipboardResult
}{items: make(map[uint64]chan clipboardResult)}
var sequence atomic.Uint64

// ReadClipboard reads only after an explicit paste action. It never acquires
// clipboard ownership, clears it, or starts a competing native event loop.
func ReadClipboard(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id := sequence.Add(1)
	ch := make(chan clipboardResult, 1)
	requests.Lock()
	requests.items[id] = ch
	requests.Unlock()
	defer func() { requests.Lock(); delete(requests.items, id); requests.Unlock() }()
	if err := requestClipboard(id); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-ch:
		return encodeClipboardResult(ctx, result)
	}
}

func encodeClipboardResult(ctx context.Context, result clipboardResult) (string, error) {
	if result.err != nil {
		return "", result.err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(result.fileList) > 0 {
		return clipboardFilesContext(ctx, result.fileList)
	}
	if len(result.pixels) > 0 {
		var img image.Image
		if result.channels == 4 {
			img = &image.NRGBA{Pix: result.pixels, Stride: result.stride, Rect: image.Rect(0, 0, result.width, result.height)}
		} else {
			rgba := image.NewNRGBA(image.Rect(0, 0, result.width, result.height))
			for y := 0; y < result.height; y++ {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				for x := 0; x < result.width; x++ {
					src := y*result.stride + x*3
					dst := y*rgba.Stride + x*4
					copy(rgba.Pix[dst:dst+3], result.pixels[src:src+3])
					rgba.Pix[dst+3] = 255
				}
			}
			img = rgba
		}
		output := limitedBuffer{ctx: ctx}
		if err := png.Encode(&output, img); err != nil {
			return "", err
		}
		result.data = output.Bytes()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return imageDataURL(result.data)
}

// Desktop file managers publish copied image files as URI lists instead of
// encoded image targets. Read local regular image files only, never URLs.
func clipboardFiles(list []byte) (string, error) {
	return clipboardFilesContext(context.Background(), list)
}

func clipboardFilesContext(ctx context.Context, list []byte) (string, error) {
	if len(list) > 1024*1024 {
		return "", ErrNoImage
	}
	for _, line := range strings.Split(string(list), "\n") {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || line == "copy" || line == "cut" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(u.Path) {
			continue
		}
		info, err := os.Stat(u.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxBytes {
			continue
		}
		file, err := os.Open(u.Path)
		if err != nil {
			continue
		}
		info, err = file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > MaxBytes {
			file.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
		file.Close()
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err == nil {
			if result, e := imageDataURL(data); e == nil {
				return result, nil
			}
		}
	}
	return "", ErrNoImage
}

type limitedBuffer struct {
	bytes.Buffer
	ctx context.Context
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.ctx != nil {
		if err := b.ctx.Err(); err != nil {
			return 0, err
		}
	}
	if b.Len()+len(p) > MaxBytes {
		return 0, errors.New("Choose an image up to 32 MiB.")
	}
	return b.Buffer.Write(p)
}

func imageDataURL(data []byte) (string, error) {
	if len(data) == 0 {
		return "", ErrNoImage
	}
	if len(data) > MaxBytes {
		return "", errors.New("Choose an image up to 32 MiB.")
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
	default:
		return "", ErrNoImage
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func pending(id uint64) bool {
	requests.Lock()
	defer requests.Unlock()
	return requests.items[id] != nil
}
func complete(id uint64, result clipboardResult) {
	requests.Lock()
	ch := requests.items[id]
	delete(requests.items, id)
	requests.Unlock()
	if ch != nil {
		ch <- result
	}
}
