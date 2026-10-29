package predictions

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// extractPython never follows archive links while writing files. Links are
// validated and created only after all ordinary members have been extracted.
func extractPython(archive, destination string) error {
	return extractPythonContext(context.Background(), archive, destination)
}
func extractPythonContext(ctx context.Context, archive, destination string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	type link struct {
		name, target string
		hard         bool
	}
	var links []link
	var total int64
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, readErr := tr.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
		count++
		if count > 100000 {
			return errors.New("Python archive contains too many members")
		}
		name := path.Clean(h.Name)
		if name == "." {
			continue
		}
		if strings.ContainsAny(h.Name, "\\:") || path.IsAbs(h.Name) || name == ".." || strings.HasPrefix(name, "../") || !(name == "python" || strings.HasPrefix(name, "python/")) {
			return errors.New("Python archive contains an unsafe member path")
		}
		out := filepath.Join(destination, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(out, 0700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if h.Size < 0 || h.Size > 512*1024*1024 {
				return errors.New("Python archive member is unexpectedly large")
			}
			total += h.Size
			if total > 2*1024*1024*1024 {
				return errors.New("Python archive exceeds its extraction limit")
			}
			if err = os.MkdirAll(filepath.Dir(out), 0700); err != nil {
				return err
			}
			file, createErr := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|os.FileMode(h.Mode)&0111)
			if createErr != nil {
				return createErr
			}
			_, copyErr := io.CopyN(file, tr, h.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink, tar.TypeLink:
			if h.Linkname == "" || strings.ContainsAny(h.Linkname, "\\:") || path.IsAbs(h.Linkname) {
				return errors.New("Python archive contains an unsafe link")
			}
			target := path.Clean(h.Linkname)
			if h.Typeflag == tar.TypeSymlink {
				target = path.Clean(path.Join(path.Dir(name), h.Linkname))
			}
			if !(target == "python" || strings.HasPrefix(target, "python/")) {
				return errors.New("Python archive link leaves its installation folder")
			}
			links = append(links, link{name: name, target: target, hard: h.Typeflag == tar.TypeLink})
		default:
			return fmt.Errorf("Python archive contains unsupported member type %d", h.Typeflag)
		}
	}
	for _, l := range links {
		out := filepath.Join(destination, filepath.FromSlash(l.name))
		target := filepath.Join(destination, filepath.FromSlash(l.target))
		if err = runtimeSafeParents(destination, filepath.Dir(out)); err != nil {
			return err
		}
		if err = runtimeSafeParents(destination, filepath.Dir(target)); err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(out), 0700); err != nil {
			return err
		}
		if l.hard {
			if info, statErr := os.Lstat(target); statErr != nil || !info.Mode().IsRegular() {
				return errors.New("Python archive hard link must target a regular file")
			}
			if err = os.Link(target, out); err != nil {
				return err
			}
		} else {
			relative, relErr := filepath.Rel(filepath.Dir(out), target)
			if relErr != nil {
				return relErr
			}
			if err = os.Symlink(relative, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// A deferred link must never gain a different physical parent through another
// link created earlier in the archive. Lexically safe paths alone cannot prove
// confinement when symlinked directories are involved.
func runtimeSafeParents(root, dir string) error {
	relative, err := filepath.Rel(root, dir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("Python archive path leaves its extraction folder")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Python archive link has an unsafe parent directory")
		}
	}
	return nil
}
