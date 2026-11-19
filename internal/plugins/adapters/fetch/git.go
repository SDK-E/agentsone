// SPDX-License-Identifier: MIT

package fetch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

var (
	githubRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)
	revRe    = regexp.MustCompile(`^[A-Za-z0-9._/@+-]{1,200}$`)
	shaRe    = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

// proxyEnv names the environment variables passed on to git so that egress
// follows the proxy and certificate settings of the host.
var proxyEnv = []string{
	"PATH", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
}

// fetchGit clones a repository without running anything from it. The tree is
// the working copy at the requested revision, without the .git directory.
func (f *Fetcher) fetchGit(ctx context.Context, spec domain.FetchSpec, tmp, dest string) (result, error) {
	url, err := f.gitURL(spec)
	if err != nil {
		return result{}, err
	}
	if spec.Rev != "" && (strings.HasPrefix(spec.Rev, "-") || !revRe.MatchString(spec.Rev)) {
		return result{}, fmt.Errorf("revision %q: %w", spec.Rev, domain.ErrUnsupportedSource)
	}
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, dirMode); err != nil {
		return result{}, err
	}
	env := f.gitEnv(home)
	byCommit := spec.Rev != "" && shaRe.MatchString(spec.Rev)

	args := []string{"clone", "--quiet", "--no-tags"}
	if !byCommit {
		args = append(args, "--depth", "1", "--single-branch")
		if spec.Rev != "" {
			args = append(args, "--branch", spec.Rev)
		}
	}
	args = append(args, "--", url, dest)
	if err := f.git(ctx, env, "", args...); err != nil {
		return result{}, err
	}
	if byCommit {
		if err := f.git(ctx, env, dest, "checkout", "--quiet", "--detach", spec.Rev, "--"); err != nil {
			return result{}, err
		}
	}
	rev, err := f.gitOutput(ctx, env, dest, "rev-parse", "HEAD")
	if err != nil {
		return result{}, err
	}
	if err := os.RemoveAll(filepath.Join(dest, ".git")); err != nil {
		return result{}, err
	}
	if err := validateTree(dest, f.limits); err != nil {
		return result{}, err
	}
	return result{root: dest, rev: strings.TrimSpace(rev)}, nil
}

// gitURL turns the spec into the URL git clones, and refuses what the fetcher
// is not allowed to reach.
func (f *Fetcher) gitURL(spec domain.FetchSpec) (string, error) {
	ref := spec.Ref
	if strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("git source %q: %w", ref, domain.ErrUnsupportedSource)
	}
	if spec.Kind == domain.FetchGitHub {
		if !githubRe.MatchString(ref) {
			return "", fmt.Errorf("github source %q is not owner/repo: %w", ref, domain.ErrUnsupportedSource)
		}
		return "https://github.com/" + ref + ".git", nil
	}
	switch {
	case strings.HasPrefix(ref, "https://"), strings.HasPrefix(ref, "ssh://"):
		return ref, nil
	case strings.HasPrefix(ref, "http://"):
		if f.insecureHTTP {
			return ref, nil
		}
	case strings.HasPrefix(ref, "git@") && strings.Contains(ref, ":"):
		return ref, nil
	case strings.HasPrefix(ref, "file://"), filepath.IsAbs(ref):
		if f.allowLocalGit {
			return ref, nil
		}
	}
	return "", fmt.Errorf("git source %q is not an https or ssh URL: %w", ref, domain.ErrUnsupportedSource)
}

func (f *Fetcher) allowedProtocols() string {
	p := []string{"https", "ssh"}
	if f.insecureHTTP {
		p = append(p, "http")
	}
	if f.allowLocalGit {
		p = append(p, "file")
	}
	return strings.Join(p, ":")
}

// gitEnv is a minimal environment: no user or system git config, no prompts,
// only the allowed transports.
func (f *Fetcher) gitEnv(home string) []string {
	env := []string{
		"HOME=" + home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=" + f.allowedProtocols(),
		"GIT_ASKPASS=true",
		"GIT_LFS_SKIP_SMUDGE=1",
	}
	for _, k := range proxyEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func (f *Fetcher) gitArgs(args []string) []string {
	return append([]string{
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "transfer.fsckObjects=true",
		"-c", "advice.detachedHead=false",
	}, args...)
}

func (f *Fetcher) git(ctx context.Context, env []string, dir string, args ...string) error {
	_, err := f.gitOutput(ctx, env, dir, args...)
	return err
}

func (f *Fetcher) gitOutput(ctx context.Context, env []string, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, f.gitBin, f.gitArgs(args)...) // #nosec G204 -- fixed binary, validated arguments
	cmd.Env = env
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], domain.ErrNothingToInstall, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
