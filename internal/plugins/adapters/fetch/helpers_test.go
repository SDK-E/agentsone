// SPDX-License-Identifier: MIT

package fetch_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/adapters/fetch"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// entry is one archive member.
type entry struct {
	name string
	body string
	mode fs.FileMode // zero means 0644
	// kind is a tar type flag; zero means a regular file.
	kind byte
	link string
}

func zipBytes(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		switch e.kind {
		case tar.TypeSymlink:
			mode |= fs.ModeSymlink
		case tar.TypeDir:
			mode |= fs.ModeDir
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		body := e.body
		if e.kind == tar.TypeSymlink {
			body = e.link
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tgzBytes(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		mode := int64(e.mode)
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Typeflag: kind, Mode: mode, Linkname: e.link}
		if kind == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// newFetcher returns a fetcher with its own temporary folder, so a test can
// check that nothing is left behind.
func newFetcher(t *testing.T, opts ...fetch.Option) (*fetch.Fetcher, string) {
	t.Helper()
	tmp := t.TempDir()
	return fetch.New(append([]fetch.Option{fetch.WithTempDir(tmp)}, opts...)...), tmp
}

func assertNothingLeft(t *testing.T, tmp string) {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temporary folder not empty: %d entries", len(entries))
	}
}

func readTree(t *testing.T, tree domain.Tree, name string) string {
	t.Helper()
	b, err := fs.ReadFile(tree.FS, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
