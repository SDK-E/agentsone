// SPDX-License-Identifier: MIT

package domain

import (
	"context"
	"io/fs"
)

// Fetch kinds: how a plugin source is read.
const (
	FetchLocal  = "local"  // a directory on this host
	FetchGit    = "git"    // any git URL
	FetchGitHub = "github" // owner/repo, resolved to https://github.com/owner/repo.git
	FetchURL    = "url"    // an https .zip, .tar.gz or .tgz archive
	FetchNPM    = "npm"    // an npm package, tarball only, no install scripts
)

// FetchSpec says what to fetch.
type FetchSpec struct {
	Kind   string // one of the Fetch* constants
	Ref    string // path, git URL, owner/repo, archive URL or npm package name
	Rev    string // git branch, tag or commit, or npm version; empty means default
	Subdir string // directory inside the fetched tree that is the plugin root
	SHA256 string // expected hex sha256 of an archive or npm tarball; empty skips the check
}

// Tree is a fetched, read-only plugin tree.
type Tree struct {
	FS      fs.FS        // rooted at the plugin root (Subdir already applied)
	Dir     string       // the same tree on disk
	Rev     string       // resolved git commit or npm version, if any
	SHA256  string       // sha256 of the downloaded archive or tarball, if any
	Cleanup func() error // removes the temporary copy; never nil
}

// Fetcher reads a plugin source into a temporary tree. Implementations must
// not run any code from the source, must not need the network for local
// sources and must refuse trees above the size limits.
type Fetcher interface {
	Fetch(ctx context.Context, spec FetchSpec) (Tree, error)
}

// InstallLimits bound what the installer accepts.
type InstallLimits struct {
	MaxFiles     int   // files and directories in total
	MaxFileBytes int64 // one file
	MaxTotalSize int64 // the whole tree
}

// DefaultInstallLimits are the limits used when none are configured.
var DefaultInstallLimits = InstallLimits{MaxFiles: 5000, MaxFileBytes: 50 << 20, MaxTotalSize: 200 << 20}

// InstallRequest is one install of a normalised plugin onto disk.
type InstallRequest struct {
	Name     string
	Version  string
	Tree     fs.FS  // the fetched tree
	Prefix   string // Normalized.TreePrefix: where the tree is placed inside the plugin dir
	Extra    []File // generated files, written after the tree
	Manifest []byte // encoded agentsone.plugin.json, written at the plugin root
}

// InstallResult says where the plugin landed.
type InstallResult struct {
	Dir    string // <PluginsDir>/<name>/<version>
	SHA256 string // hex sha256 over the installed tree, see TreeDigest
}

// Installer puts a plugin on disk atomically.
type Installer interface {
	// Install writes <PluginsDir>/<name>/<version> through a temporary
	// directory and one rename. It never leaves a half-written plugin.
	Install(ctx context.Context, req InstallRequest) (InstallResult, error)
	// Verify recomputes the digest of an installed plugin directory.
	Verify(dir string) (string, error)
	// Remove deletes one installed version and an empty parent.
	Remove(name, version string) error
}
