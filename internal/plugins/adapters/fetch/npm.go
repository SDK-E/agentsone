// SPDX-License-Identifier: MIT

package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

const maxPackument = 20 << 20

var npmNameRe = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]{0,213}/)?[a-z0-9][a-z0-9._-]{0,213}$`)

// packument is the part of the registry's package document we read.
type packument struct {
	DistTags map[string]string `json:"dist-tags"`
	Versions map[string]struct {
		Dist struct {
			Tarball   string `json:"tarball"`
			Integrity string `json:"integrity"`
			Shasum    string `json:"shasum"`
		} `json:"dist"`
	} `json:"versions"`
}

// fetchNPM reads a package through the registry protocol: it downloads the
// tarball and unpacks it. npm itself never runs, so no install script runs.
func (f *Fetcher) fetchNPM(ctx context.Context, spec domain.FetchSpec, tmp, dest string) (result, error) {
	if !npmNameRe.MatchString(spec.Ref) {
		return result{}, fmt.Errorf("npm package %q: %w", spec.Ref, domain.ErrUnsupportedSource)
	}
	if err := f.checkRegistry(); err != nil {
		return result{}, err
	}
	doc, err := f.packument(ctx, spec.Ref)
	if err != nil {
		return result{}, err
	}
	version := spec.Rev
	if version == "" {
		version = doc.DistTags["latest"]
	}
	v, ok := doc.Versions[version]
	if !ok || v.Dist.Tarball == "" {
		return result{}, fmt.Errorf("npm package %s has no version %q: %w", spec.Ref, version, domain.ErrNothingToInstall)
	}
	tarURL, err := url.Parse(v.Dist.Tarball)
	if err != nil || tarURL.Host == "" {
		return result{}, fmt.Errorf("npm tarball url %q: %w", v.Dist.Tarball, domain.ErrUnsupportedSource)
	}
	if err := f.checkScheme(tarURL.Scheme); err != nil {
		return result{}, err
	}
	dl, err := f.download(ctx, v.Dist.Tarball, filepath.Join(tmp, "package.tgz"), f.limits.MaxTotalSize)
	if err != nil {
		return result{}, err
	}
	if checked, matches := dl.matchesIntegrity(v.Dist.Integrity, v.Dist.Shasum); checked && !matches {
		return result{}, fmt.Errorf("npm tarball of %s@%s does not match the registry digest: %w", spec.Ref, version, domain.ErrIntegrity)
	}
	if spec.SHA256 != "" && !strings.EqualFold(spec.SHA256, dl.sha256) {
		return result{}, fmt.Errorf("npm tarball sha256 is %s, expected %s: %w", dl.sha256, spec.SHA256, domain.ErrIntegrity)
	}
	if err := f.extract(dl.path, dest); err != nil {
		return result{}, err
	}
	root := filepath.Join(dest, "package")
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		if root, err = soleDir(dest); err != nil {
			return result{}, err
		}
	}
	return result{root: root, rev: version, sha256: dl.sha256}, nil
}

func (f *Fetcher) checkRegistry() error {
	u, err := url.Parse(f.registry)
	if err != nil || u.Host == "" {
		return fmt.Errorf("npm registry %q: %w", f.registry, domain.ErrUnsupportedSource)
	}
	return f.checkScheme(u.Scheme)
}

func (f *Fetcher) packument(ctx context.Context, name string) (packument, error) {
	endpoint := f.registry + "/" + strings.ReplaceAll(name, "/", "%2f")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return packument{}, fmt.Errorf("npm request %q: %w", endpoint, domain.ErrUnsupportedSource)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req) // #nosec G107 G704 -- registry scheme checked above
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return packument{}, ctxErr
		}
		return packument{}, fmt.Errorf("npm registry: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // read-only body; ReadAll below reports read failures
	if resp.StatusCode == http.StatusNotFound {
		return packument{}, fmt.Errorf("npm package %s: %w", name, domain.ErrNothingToInstall)
	}
	if resp.StatusCode != http.StatusOK {
		return packument{}, fmt.Errorf("npm registry answered %d: %w", resp.StatusCode, domain.ErrNothingToInstall)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPackument+1))
	if err != nil {
		return packument{}, fmt.Errorf("npm registry: %w", err)
	}
	if len(body) > maxPackument {
		return packument{}, fmt.Errorf("npm package document is larger than %d bytes: %w", maxPackument, domain.ErrTooLarge)
	}
	var doc packument
	if err := json.Unmarshal(body, &doc); err != nil {
		return packument{}, fmt.Errorf("npm package document: %w", errors.Join(domain.ErrNothingToInstall, err))
	}
	return doc, nil
}
