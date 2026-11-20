// SPDX-License-Identifier: MIT

// Package fetch reads a plugin source into a temporary tree. It supports a
// local directory, a git repository, an archive URL and an npm package. It
// never runs code from the source: git hooks are off and npm is not used, only
// its registry protocol.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const (
	defaultRegistry = "https://registry.npmjs.org"
	defaultTimeout  = 2 * time.Minute
	tmpPrefix       = "fetch-"
	treeDir         = "tree"
)

// Fetcher implements domain.Fetcher.
type Fetcher struct {
	client        *http.Client
	tempDir       string
	limits        domain.InstallLimits
	registry      string
	insecureHTTP  bool
	allowLocalGit bool
	gitBin        string
}

var _ domain.Fetcher = (*Fetcher)(nil)

// Option configures a Fetcher.
type Option func(*Fetcher)

// WithHTTPClient sets the client used for archives and the npm registry.
func WithHTTPClient(c *http.Client) Option { return func(f *Fetcher) { f.client = c } }

// WithTempDir sets where temporary trees are created.
func WithTempDir(dir string) Option { return func(f *Fetcher) { f.tempDir = dir } }

// WithLimits sets the size limits for fetched trees.
func WithLimits(l domain.InstallLimits) Option { return func(f *Fetcher) { f.limits = l } }

// WithRegistryURL sets the npm registry base URL.
func WithRegistryURL(u string) Option {
	return func(f *Fetcher) { f.registry = strings.TrimRight(u, "/") }
}

// WithInsecureHTTP allows http URLs. Use it in tests only.
func WithInsecureHTTP() Option { return func(f *Fetcher) { f.insecureHTTP = true } }

// WithLocalGit allows git sources on this host (a path or a file URL). Use it
// in tests only.
func WithLocalGit() Option { return func(f *Fetcher) { f.allowLocalGit = true } }

// WithGitBinary sets the git executable.
func WithGitBinary(path string) Option { return func(f *Fetcher) { f.gitBin = path } }

// New returns a fetcher. Egress follows the environment's proxy settings.
func New(opts ...Option) *Fetcher {
	f := &Fetcher{
		tempDir:  os.TempDir(),
		limits:   domain.DefaultInstallLimits,
		registry: defaultRegistry,
		gitBin:   "git",
	}
	for _, o := range opts {
		o(f)
	}
	f.fillLimits()
	if f.client == nil {
		f.client = &http.Client{Timeout: defaultTimeout}
	}
	// Wrap the redirect policy so a redirect cannot downgrade to plain http.
	client := *f.client
	inner := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := f.checkScheme(req.URL.Scheme); err != nil {
			return err
		}
		if inner != nil {
			return inner(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	f.client = &client
	return f
}

func (f *Fetcher) fillLimits() {
	d := domain.DefaultInstallLimits
	if f.limits.MaxFiles <= 0 {
		f.limits.MaxFiles = d.MaxFiles
	}
	if f.limits.MaxFileBytes <= 0 {
		f.limits.MaxFileBytes = d.MaxFileBytes
	}
	if f.limits.MaxTotalSize <= 0 {
		f.limits.MaxTotalSize = d.MaxTotalSize
	}
}

func (f *Fetcher) checkScheme(scheme string) error {
	if scheme == "https" || (scheme == "http" && f.insecureHTTP) {
		return nil
	}
	return fmt.Errorf("url scheme %q is not https: %w", scheme, domain.ErrUnsupportedSource)
}

// Fetch reads the source into a new temporary tree.
func (f *Fetcher) Fetch(ctx context.Context, spec domain.FetchSpec) (domain.Tree, error) {
	if err := ctx.Err(); err != nil {
		return domain.Tree{}, err
	}
	if spec.Ref == "" {
		return domain.Tree{}, fmt.Errorf("source has no reference: %w", domain.ErrUnsupportedSource)
	}
	tmp, err := os.MkdirTemp(f.tempDir, tmpPrefix)
	if err != nil {
		return domain.Tree{}, fmt.Errorf("create temporary folder: %w", err)
	}
	cleanup := func() error {
		if err := os.RemoveAll(tmp); err != nil {
			return fmt.Errorf("remove temporary folder: %w", err)
		}
		return nil
	}
	dest := filepath.Join(tmp, treeDir)

	var res result
	switch spec.Kind {
	case domain.FetchLocal:
		res, err = f.fetchLocal(spec, dest)
	case domain.FetchGit, domain.FetchGitHub:
		res, err = f.fetchGit(ctx, spec, tmp, dest)
	case domain.FetchURL:
		res, err = f.fetchArchive(ctx, spec, tmp, dest)
	case domain.FetchNPM:
		res, err = f.fetchNPM(ctx, spec, tmp, dest)
	default:
		err = fmt.Errorf("fetch kind %q: %w", spec.Kind, domain.ErrUnsupportedSource)
	}
	if err != nil {
		return domain.Tree{}, errors.Join(err, cleanup())
	}
	root, err := resolveSubdir(res.root, spec.Subdir)
	if err != nil {
		return domain.Tree{}, errors.Join(err, cleanup())
	}
	return domain.Tree{FS: os.DirFS(root), Dir: root, Rev: res.rev, SHA256: res.sha256, Cleanup: cleanup}, nil
}

// result is what one kind-specific fetch returns.
type result struct {
	root   string // the fetched tree on disk
	rev    string
	sha256 string
}

// resolveSubdir returns the plugin root inside root. The subdirectory must be
// a plain relative path that stays inside root once symlinks are resolved.
func resolveSubdir(root, subdir string) (string, error) {
	if subdir == "" {
		return root, nil
	}
	if !fs.ValidPath(subdir) || subdir == "." || strings.Contains(subdir, "\\") {
		return "", fmt.Errorf("subdirectory %q: %w", subdir, domain.ErrUnsafePath)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(subdir)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("subdirectory %q does not exist: %w", subdir, domain.ErrNothingToInstall)
	}
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(realRoot, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("subdirectory %q leaves the source: %w", subdir, domain.ErrUnsafePath)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("subdirectory %q is not a directory: %w", subdir, domain.ErrNothingToInstall)
	}
	return target, nil
}

// checkLink applies the symlink policy shared by every kind: a link must be
// relative and must not contain "..", so it can only point deeper into the
// tree.
func checkLink(name, target string) error {
	if target == "" || strings.HasPrefix(target, "/") || filepath.IsAbs(target) || strings.Contains(target, "\\") {
		return fmt.Errorf("symlink %s points to %q: %w", name, target, domain.ErrUnsafePath)
	}
	for _, seg := range strings.Split(target, "/") {
		if seg == ".." {
			return fmt.Errorf("symlink %s points to %q: %w", name, target, domain.ErrUnsafePath)
		}
	}
	return nil
}

// budget counts files and bytes against the limits.
type budget struct {
	limits domain.InstallLimits
	files  int
	total  int64
}

func (b *budget) entry(name string) error {
	b.files++
	if b.files > b.limits.MaxFiles {
		return fmt.Errorf("more than %d files (at %s): %w", b.limits.MaxFiles, name, domain.ErrTooLarge)
	}
	return nil
}

func (b *budget) bytes(name string, n int64) error {
	if n > b.limits.MaxFileBytes {
		return fmt.Errorf("%s is larger than %d bytes: %w", name, b.limits.MaxFileBytes, domain.ErrTooLarge)
	}
	b.total += n
	if b.total > b.limits.MaxTotalSize {
		return fmt.Errorf("source is larger than %d bytes: %w", b.limits.MaxTotalSize, domain.ErrTooLarge)
	}
	return nil
}

// validateTree walks a tree on disk and applies the symlink, file type and
// size policy. Git sources use it after the clone.
func validateTree(root string, limits domain.InstallLimits) error {
	b := &budget{limits: limits}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if err := b.entry(rel); err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return nil
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return checkLink(rel, target)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return b.bytes(rel, info.Size())
		}
		return fmt.Errorf("%s is not a regular file: %w", rel, domain.ErrUnsafePath)
	})
}
