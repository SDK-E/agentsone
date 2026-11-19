// SPDX-License-Identifier: MIT

// Package domain holds the rules of the engine fetch: the pinned engine
// version and its checksums, where the engine is installed, and the status the
// daemon shows while it downloads.
//
// The Claude engine is licensed separately, so it is never baked into the
// image. It is downloaded at first start, after a human accepted its licence,
// and only the exact version and checksum pinned in this repository is ever
// installed.
package domain

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Errors of the pin rules.
var (
	// ErrInvalidPin means the pinned engine is not exact or not safe to use.
	ErrInvalidPin = errors.New("invalid engine pin")
	// ErrUnsupportedPlatform means no artifact is pinned for this machine.
	ErrUnsupportedPlatform = errors.New("no engine is pinned for this platform")
)

// Artifact is one pinned download: the engine binary for one platform.
type Artifact struct {
	// Platform is the download folder name, such as linux-x64.
	Platform string `json:"platform"`
	// Binary is the file name in that folder, such as claude.
	Binary string `json:"binary"`
	// SHA256 is the lower-case hex checksum of the binary.
	SHA256 string `json:"sha256"`
	// Size is the binary size in bytes.
	Size int64 `json:"size"`
}

// Pin is the exact engine version this repository installs.
type Pin struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	// BaseURL is the release folder, without a trailing slash. Its host must be
	// on the edge allow list.
	BaseURL string `json:"baseUrl"`
	// Artifacts are keyed by Go platform, such as linux/amd64.
	Artifacts map[string]Artifact `json:"artifacts"`
}

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	sumRe     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	segmentRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// Validate checks that the pin names one exact version over HTTPS, and that
// every artifact has a full checksum and a size.
func (p Pin) Validate() error {
	switch {
	case !nameRe.MatchString(p.Engine):
		return fmt.Errorf("%w: engine name %q", ErrInvalidPin, p.Engine)
	case !versionRe.MatchString(p.Version):
		return fmt.Errorf("%w: version %q is not an exact x.y.z version", ErrInvalidPin, p.Version)
	case !strings.HasPrefix(p.BaseURL, "https://") || strings.HasSuffix(p.BaseURL, "/"):
		return fmt.Errorf("%w: base url %q must be https without a trailing slash", ErrInvalidPin, p.BaseURL)
	case len(p.Artifacts) == 0:
		return fmt.Errorf("%w: no artifacts", ErrInvalidPin)
	}
	for key, a := range p.Artifacts {
		switch {
		case !segmentRe.MatchString(a.Platform) || !segmentRe.MatchString(a.Binary):
			return fmt.Errorf("%w: %s: platform %q or binary %q is not a plain name", ErrInvalidPin, key, a.Platform, a.Binary)
		case !sumRe.MatchString(a.SHA256):
			return fmt.Errorf("%w: %s: checksum must be 64 lower-case hex digits", ErrInvalidPin, key)
		case a.Size <= 0:
			return fmt.Errorf("%w: %s: size must be positive", ErrInvalidPin, key)
		}
	}
	return nil
}

// For returns the artifact for a Go platform (GOOS and GOARCH).
func (p Pin) For(goos, goarch string) (Artifact, error) {
	a, ok := p.Artifacts[goos+"/"+goarch]
	if !ok {
		return Artifact{}, fmt.Errorf("%w: %s/%s (engine %s %s)", ErrUnsupportedPlatform, goos, goarch, p.Engine, p.Version)
	}
	return a, nil
}

// URL is where the artifact is downloaded from.
func (p Pin) URL(a Artifact) string {
	return p.BaseURL + "/" + p.Version + "/" + a.Platform + "/" + a.Binary
}

// DefaultRoot is the engines folder in the machine container: the
// machine-engines volume, owned by root with folders and files 0755, so every
// agent user can run the engine and none can change it. It sits outside the
// state folder /var/lib/agentsone, which stays private (0700).
const DefaultRoot = "/var/lib/agentsone-engines"

// BinaryPath is where the verified engine is installed under root, for
// example /var/lib/agentsone-engines/claude-code/2.1.328/claude. The runner
// starts the engine from this path.
func BinaryPath(root string, p Pin) string {
	return filepath.Join(root, p.Engine, p.Version, "claude")
}

//go:embed claude-code.pin.json
var pinned []byte

// Pinned returns the engine pin shipped in this repository
// (claude-code.pin.json next to this file).
func Pinned() (Pin, error) {
	var p Pin
	if err := json.Unmarshal(pinned, &p); err != nil {
		return Pin{}, fmt.Errorf("%w: %w", ErrInvalidPin, err)
	}
	if err := p.Validate(); err != nil {
		return Pin{}, err
	}
	return p, nil
}
