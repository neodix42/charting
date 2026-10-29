package predictions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func officialRuntimeURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range []string{"huggingface.co", "hf.co", "download.pytorch.org", "download-r2.pytorch.org", "files.pythonhosted.org", "github.com", "githubusercontent.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}
func runtimeHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 45 * time.Second
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many official artifact redirects")
		}
		if !officialRuntimeURL(req.URL.String()) {
			return errors.New("artifact redirect is outside the official download hosts")
		}
		return nil
	}}
}
func verifyRuntimeFile(ctx context.Context, path string, a runtimeArtifact) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != a.Size {
		return errors.New("artifact size or file type does not match the pinned release")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	buffer := make([]byte, 256*1024)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, readErr := f.Read(buffer)
		if n > 0 {
			hash.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return errors.New("artifact checksum does not match the pinned release")
	}
	return nil
}

// downloadArtifact resumes only CryptoDesk's own .part file; a complete file is
// published atomically after its immutable release size and SHA-256 both match.
func (r *localRuntime) downloadArtifact(ctx context.Context, dir string, a runtimeArtifact, phase string, emit func(Event)) error {
	if a.Size <= 0 || len(a.SHA256) != 64 || filepath.Base(a.Name) != a.Name {
		return errors.New("invalid pinned artifact manifest")
	}
	if !r.allowTestHTTP && !officialRuntimeURL(a.URL) {
		return errors.New("artifact is outside the official download hosts")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, a.Name)
	if err := verifyRuntimeFile(ctx, path, a); err == nil {
		r.downloadProgress(emit, phase, a.Name, a.Size, a.Size)
		return nil
	} else if ctx.Err() != nil {
		return ctx.Err()
	}
	partial := path + ".part"
	var offset int64
	if info, err := os.Lstat(partial); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("download partial is not a regular file")
		}
		offset = info.Size()
		if offset > a.Size {
			if err = os.Remove(partial); err != nil {
				return err
			}
			offset = 0
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if offset == a.Size {
		if err := verifyRuntimeFile(ctx, partial, a); err == nil {
			return os.Rename(partial, path)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := os.Remove(partial); err != nil {
			return err
		}
		offset = 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "CryptoDesk-local-predictions/1")
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("download %s: network request failed", a.Name)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		offset = 0
	case http.StatusPartialContent:
		expected := fmt.Sprintf("bytes %d-%d/%d", offset, a.Size-1, a.Size)
		if response.Header.Get("Content-Range") != expected {
			return errors.New("artifact server returned an inconsistent download range")
		}
	default:
		return fmt.Errorf("download %s: official server returned HTTP %d", a.Name, response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != a.Size-offset {
		return errors.New("artifact server returned an unexpected download size")
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(partial, flags, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return err
	}
	r.downloadProgress(emit, phase, a.Name, offset, a.Size)
	writer := &runtimeProgressWriter{writer: f, r: r, emit: emit, phase: phase, name: a.Name, done: offset, total: a.Size, last: time.Now()}
	_, copyErr := io.Copy(writer, io.LimitReader(response.Body, a.Size-offset+1))
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("download %s: transfer interrupted", a.Name)
	}
	if closeErr != nil {
		return closeErr
	}
	if err = verifyRuntimeFile(ctx, partial, a); err != nil {
		if ctx.Err() == nil {
			_ = os.Remove(partial)
		}
		return fmt.Errorf("verify %s: %w", a.Name, err)
	}
	if err = os.Rename(partial, path); err != nil {
		return err
	}
	r.downloadProgress(emit, phase, a.Name, a.Size, a.Size)
	return nil
}

type runtimeProgressWriter struct {
	writer      io.Writer
	r           *localRuntime
	emit        func(Event)
	phase, name string
	done, total int64
	last        time.Time
}

func (w *runtimeProgressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.done += int64(n)
	if time.Since(w.last) >= 150*time.Millisecond {
		w.r.downloadProgress(w.emit, w.phase, w.name, w.done, w.total)
		w.last = time.Now()
	}
	return n, err
}
func (r *localRuntime) downloadProgress(emit func(Event), phase, name string, done, total int64) {
	if emit != nil {
		pct := float64(0)
		if total > 0 {
			pct = 100 * float64(done) / float64(total)
		}
		emit(Event{Phase: phase, Progress: pct, Message: "Downloading " + name, Downloaded: done, Total: total})
	}
}

func runtimeSizeSignature(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("expected a regular runtime file")
	}
	return strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), 10), nil
}
