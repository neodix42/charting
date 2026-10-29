package predictions

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type testTarMember struct {
	name, body, target string
	kind               byte
}

func makePythonArchive(t *testing.T, members []testTarMember) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "python.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		kind := m.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		h := &tar.Header{Name: m.name, Mode: 0755, Typeflag: kind, Linkname: m.target}
		if kind == tar.TypeReg {
			h.Size = int64(len(m.body))
		}
		if err = tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err = tw.Write([]byte(m.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestRuntimePythonExtractionConfinesMembersAndDefersLinks(t *testing.T) {
	archive := makePythonArchive(t, []testTarMember{{name: "python/bin/python", target: "python3.12", kind: tar.TypeSymlink}, {name: "python/bin/python3.12", body: "real interpreter"}, {name: "python/lib/version", body: "3.12"}})
	dest := t.TempDir()
	if err := extractPython(archive, dest); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation unavailable")
		}
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "python", "bin", "python"))
	if err != nil || string(b) != "real interpreter" {
		t.Fatalf("link=%s err=%v", b, err)
	}
	for _, m := range []testTarMember{{name: "../escaped", body: "bad"}, {name: "/tmp/escaped", body: "bad"}, {name: "python/../../escaped", body: "bad"}, {name: "python\\escaped", body: "bad"}, {name: "python/bin/python", target: "../../../escaped", kind: tar.TypeSymlink}, {name: "python/bin/python", target: "/usr/bin/python", kind: tar.TypeSymlink}, {name: "python/link", target: "../escaped", kind: tar.TypeLink}, {name: "python/device", kind: tar.TypeChar}} {
		t.Run(m.name+m.target, func(t *testing.T) {
			if err := extractPython(makePythonArchive(t, []testTarMember{m}), t.TempDir()); err == nil {
				t.Fatalf("unsafe archive member accepted: %+v", m)
			}
		})
	}
}

func TestRuntimePythonExtractionRejectsDeferredLinkParentAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows archive has no POSIX symlinks")
	}
	archive := makePythonArchive(t, []testTarMember{{name: "python/deep", target: ".", kind: tar.TypeSymlink}, {name: "python/deep/sub/x", target: "../../outside", kind: tar.TypeSymlink}})
	if err := extractPython(archive, t.TempDir()); err == nil {
		t.Fatal("deferred parent symlink alias accepted")
	}
}
