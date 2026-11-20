// SPDX-License-Identifier: MIT

package fetch

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" // #nosec G505 -- only to check npm's legacy shasum field
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// download is an archive fetched to disk with its digests.
type download struct {
	path   string
	sha256 string
	sha512 []byte
	sha1   string
}

// fetchArchive downloads an https .zip, .tar.gz or .tgz and extracts it.
func (f *Fetcher) fetchArchive(ctx context.Context, spec domain.FetchSpec, tmp, dest string) (result, error) {
	u, err := url.Parse(spec.Ref)
	if err != nil || u.Host == "" {
		return result{}, fmt.Errorf("archive url %q: %w", spec.Ref, domain.ErrUnsupportedSource)
	}
	if err := f.checkScheme(u.Scheme); err != nil {
		return result{}, err
	}
	dl, err := f.download(ctx, spec.Ref, filepath.Join(tmp, "archive"), f.limits.MaxTotalSize)
	if err != nil {
		return result{}, err
	}
	if spec.SHA256 != "" && !strings.EqualFold(spec.SHA256, dl.sha256) {
		return result{}, fmt.Errorf("archive sha256 is %s, expected %s: %w", dl.sha256, spec.SHA256, domain.ErrIntegrity)
	}
	if err := f.extract(dl.path, dest); err != nil {
		return result{}, err
	}
	root, err := soleDir(dest)
	if err != nil {
		return result{}, err
	}
	return result{root: root, sha256: dl.sha256}, nil
}

// download saves the body of a GET request to file. The body may not exceed
// limit bytes.
func (f *Fetcher) download(ctx context.Context, rawURL, file string, limit int64) (download, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return download{}, fmt.Errorf("request %q: %w", rawURL, domain.ErrUnsupportedSource)
	}
	resp, err := f.client.Do(req) // #nosec G107 G704 -- scheme checked by the caller, redirects checked by the client
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return download{}, ctxErr
		}
		return download{}, fmt.Errorf("download %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // read-only body; the copy below reports read failures
	if resp.StatusCode != http.StatusOK {
		return download{}, fmt.Errorf("download %s: status %d: %w", rawURL, resp.StatusCode, domain.ErrNothingToInstall)
	}
	out, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) // #nosec G304 -- inside the temporary folder
	if err != nil {
		return download{}, err
	}
	h256, h512, h1 := sha256.New(), sha512.New(), sha1.New() // #nosec G401
	n, copyErr := io.Copy(io.MultiWriter(out, h256, h512, h1), io.LimitReader(resp.Body, limit+1))
	closeErr := out.Close()
	if copyErr != nil {
		return download{}, fmt.Errorf("download %s: %w", rawURL, copyErr)
	}
	if closeErr != nil {
		return download{}, closeErr
	}
	if n > limit {
		return download{}, fmt.Errorf("download %s is larger than %d bytes: %w", rawURL, limit, domain.ErrTooLarge)
	}
	return download{path: file, sha256: hex.EncodeToString(h256.Sum(nil)), sha512: h512.Sum(nil), sha1: hex.EncodeToString(h1.Sum(nil))}, nil
}

// extract unpacks a zip or a gzip-compressed tar into dest.
func (f *Fetcher) extract(file, dest string) error {
	if err := os.MkdirAll(dest, dirMode); err != nil {
		return err
	}
	head, err := readHead(file)
	if err != nil {
		return err
	}
	x := &extractor{dest: dest, b: &budget{limits: f.limits}}
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		return x.zip(file)
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		return x.tgz(file)
	}
	return fmt.Errorf("archive is not a zip or a tar.gz: %w", domain.ErrUnsupportedSource)
}

func readHead(file string) ([]byte, error) {
	in, err := os.Open(file) // #nosec G304 -- inside the temporary folder
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }() //nolint:errcheck // read-only file; the reads report any failure
	head := make([]byte, 4)
	n, err := io.ReadFull(in, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return head[:n], nil
}

type extractor struct {
	dest string
	b    *budget
}

// target validates an entry name and returns where it goes on disk. Leading
// "./" is accepted, anything that is not a clean relative path is refused.
func (x *extractor) target(name string) (string, string, error) {
	n := name
	for strings.HasPrefix(n, "./") {
		n = strings.TrimPrefix(n, "./")
	}
	n = strings.TrimSuffix(n, "/")
	if n == "" || n == "." {
		return "", "", nil
	}
	if strings.ContainsAny(n, "\\\x00") || !fs.ValidPath(n) {
		return "", "", fmt.Errorf("archive entry %q: %w", name, domain.ErrUnsafePath)
	}
	return n, filepath.Join(x.dest, filepath.FromSlash(n)), nil
}

func (x *extractor) dir(name string) error {
	rel, abs, err := x.target(name)
	if err != nil || rel == "" {
		return err
	}
	if err := x.b.entry(rel); err != nil {
		return err
	}
	return os.MkdirAll(abs, dirMode)
}

func (x *extractor) file(name string, r io.Reader, exec bool) error {
	rel, abs, err := x.target(name)
	if err != nil {
		return err
	}
	if rel == "" {
		return fmt.Errorf("archive entry %q: %w", name, domain.ErrUnsafePath)
	}
	if err := x.b.entry(rel); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), dirMode); err != nil {
		return err
	}
	mode := os.FileMode(fileMode)
	if exec {
		mode = execMode
	}
	out, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) // #nosec G302 G304 -- inside the temporary folder
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("archive entry %q appears twice: %w", name, domain.ErrUnsafePath)
		}
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(r, x.b.limits.MaxFileBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := x.b.bytes(rel, n); err != nil {
		return err
	}
	return os.Chmod(abs, mode) // #nosec G302 -- mode is 0644 or 0755 by design
}

func (x *extractor) link(name, linkTarget string) error {
	rel, abs, err := x.target(name)
	if err != nil {
		return err
	}
	if rel == "" {
		return fmt.Errorf("archive entry %q: %w", name, domain.ErrUnsafePath)
	}
	if err := checkLink(rel, linkTarget); err != nil {
		return err
	}
	if err := x.b.entry(rel); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), dirMode); err != nil {
		return err
	}
	return os.Symlink(linkTarget, abs)
}

func (x *extractor) zip(file string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return fmt.Errorf("read zip: %w", domain.ErrUnsupportedSource)
	}
	defer func() { _ = zr.Close() }() //nolint:errcheck // read-only archive; entry reads report any failure
	for _, zf := range zr.File {
		if err := x.zipEntry(zf); err != nil {
			return err
		}
	}
	return nil
}

func (x *extractor) zipEntry(zf *zip.File) error {
	mode := zf.Mode()
	switch {
	case zf.FileInfo().IsDir():
		return x.dir(zf.Name)
	case mode&fs.ModeSymlink != 0:
		r, err := zf.Open()
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }() //nolint:errcheck // read-only entry; reads report failures and the zip checksum
		target, err := io.ReadAll(io.LimitReader(r, 4096))
		if err != nil {
			return err
		}
		return x.link(zf.Name, string(target))
	case mode.IsRegular():
		r, err := zf.Open()
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }() //nolint:errcheck // read-only entry; reads report failures and the zip checksum
		return x.file(zf.Name, r, mode.Perm()&0o111 != 0)
	}
	return fmt.Errorf("archive entry %q is not a regular file: %w", zf.Name, domain.ErrUnsafePath)
}

func (x *extractor) tgz(file string) error {
	in, err := os.Open(file) // #nosec G304 -- inside the temporary folder
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() //nolint:errcheck // read-only file; the reads report any failure
	gz, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("read gzip: %w", domain.ErrUnsupportedSource)
	}
	defer func() { _ = gz.Close() }() //nolint:errcheck // read-only stream; tar reads report data and checksum failures
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", domain.ErrUnsupportedSource)
		}
		if err := x.tarEntry(tr, hdr); err != nil {
			return err
		}
	}
}

func (x *extractor) tarEntry(tr *tar.Reader, hdr *tar.Header) error {
	switch hdr.Typeflag {
	case tar.TypeXGlobalHeader:
		return nil
	case tar.TypeDir:
		return x.dir(hdr.Name)
	case tar.TypeReg:
		if hdr.Size > x.b.limits.MaxFileBytes {
			return fmt.Errorf("archive entry %q is larger than %d bytes: %w", hdr.Name, x.b.limits.MaxFileBytes, domain.ErrTooLarge)
		}
		return x.file(hdr.Name, tr, hdr.FileInfo().Mode().Perm()&0o111 != 0)
	case tar.TypeSymlink:
		return x.link(hdr.Name, hdr.Linkname)
	}
	return fmt.Errorf("archive entry %q has type %q: %w", hdr.Name, string(hdr.Typeflag), domain.ErrUnsafePath)
}

// soleDir returns the only directory inside root when the archive has one top
// level entry and it is a directory, which is how GitHub and npm archives
// are laid out. Otherwise it returns root.
func soleDir(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(root, entries[0].Name()), nil
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("archive is empty: %w", domain.ErrNothingToInstall)
	}
	return root, nil
}

// matchesIntegrity checks a download against an npm integrity string
// ("sha512-<base64>") or a legacy sha1 hex shasum. It reports false when
// neither value is given.
func (d download) matchesIntegrity(integrity, shasum string) (checked, ok bool) {
	if integrity != "" {
		algo, b64, found := strings.Cut(integrity, "-")
		if found && algo == "sha512" {
			return true, sameBase64(b64, d.sha512)
		}
	}
	if shasum != "" {
		return true, strings.EqualFold(shasum, d.sha1)
	}
	return false, false
}

func sameBase64(b64 string, sum []byte) bool {
	got, err := base64.StdEncoding.DecodeString(b64)
	return err == nil && bytes.Equal(got, sum)
}
