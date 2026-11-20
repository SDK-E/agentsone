// SPDX-License-Identifier: MIT

package fetch_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/plugins/adapters/fetch"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// gitRepo is a local repository used as a fixture.
type gitRepo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *gitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	r := &gitRepo{t: t, dir: t.TempDir()}
	r.git("init", "--quiet", "--initial-branch=main")
	return r
}

func (r *gitRepo) git(args ...string) string {
	r.t.Helper()
	full := append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...) // #nosec G204 -- fixed test arguments
	cmd.Dir = r.dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + r.dir, "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *gitRepo) commit(path, content, message string) string {
	r.t.Helper()
	writeFile(r.t, filepath.Join(r.dir, path), content, 0o644)
	r.git("add", "-A")
	r.git("commit", "--quiet", "-m", message)
	return r.git("rev-parse", "HEAD")
}

func localGit(t *testing.T) (*fetch.Fetcher, string) {
	t.Helper()
	return newFetcher(t, fetch.WithLocalGit())
}

func TestFetch_GitClonesTheDefaultBranchWithoutTheGitFolder(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	head := repo.commit("skills/a/SKILL.md", "skill", "first")
	f, tmp := localGit(t)

	tree, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchGit, Ref: repo.dir})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if readTree(t, tree, "skills/a/SKILL.md") != "skill" {
		t.Error("file missing")
	}
	if tree.Rev != head {
		t.Errorf("Rev = %q, want %q", tree.Rev, head)
	}
	if _, err := os.Stat(filepath.Join(tree.Dir, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Error(".git was kept")
	}
	coretest.Check(t, tree.Cleanup)
	assertNothingLeft(t, tmp)
}

func TestFetch_GitRevisionCanBeABranchATagOrACommit(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	first := repo.commit("v.txt", "one", "first")
	repo.git("tag", "v1")
	repo.git("checkout", "--quiet", "-b", "feature")
	repo.commit("v.txt", "feature", "feature")
	repo.git("checkout", "--quiet", "main")
	second := repo.commit("v.txt", "two", "second")

	tests := map[string]struct{ rev, content, wantRev string }{
		"default":      {"", "two", second},
		"branch":       {"feature", "feature", ""},
		"tag":          {"v1", "one", first},
		"full commit":  {first, "one", first},
		"short commit": {first[:8], "one", first},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, _ := localGit(t)
			tree, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchGit, Ref: repo.dir, Rev: tt.rev})
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			defer coretest.Check(t, tree.Cleanup)
			if got := readTree(t, tree, "v.txt"); got != tt.content {
				t.Errorf("content = %q, want %q", got, tt.content)
			}
			if tt.wantRev != "" && tree.Rev != tt.wantRev {
				t.Errorf("Rev = %q, want %q", tree.Rev, tt.wantRev)
			}
			if len(tree.Rev) != 40 {
				t.Errorf("Rev = %q, want a full commit", tree.Rev)
			}
		})
	}
}

func TestFetch_GitSubdir(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	repo.commit("plugins/one/x.txt", "1", "first")
	f, _ := localGit(t)

	tree, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchGit, Ref: repo.dir, Subdir: "plugins/one"})
	if err != nil {
		t.Fatal(err)
	}
	defer coretest.Check(t, tree.Cleanup)

	if readTree(t, tree, "x.txt") != "1" {
		t.Error("subdir not applied")
	}
}

func TestFetch_GitSymlinkThatLeavesTheRepositoryIsRefused(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	repo.commit("ok.txt", "1", "first")
	if err := os.Symlink("../../../etc", filepath.Join(repo.dir, "link")); err != nil {
		t.Fatal(err)
	}
	repo.git("add", "-A")
	repo.git("commit", "--quiet", "-m", "link")
	f, tmp := localGit(t)

	_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchGit, Ref: repo.dir})

	if !errors.Is(err, domain.ErrUnsafePath) {
		t.Fatalf("error = %v, want ErrUnsafePath", err)
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_GitRepositoryOverTheSizeLimitIsRefused(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	repo.commit("big.txt", "12345678", "first")
	f, tmp := newFetcher(t, fetch.WithLocalGit(), fetch.WithLimits(domain.InstallLimits{MaxFiles: 10, MaxFileBytes: 4, MaxTotalSize: 100}))

	_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchGit, Ref: repo.dir})

	if !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge", err)
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_GitRefusesWhatItMustNotReach(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	repo.commit("a", "1", "first")
	tests := map[string]domain.FetchSpec{
		"local path without the test option": {Kind: domain.FetchGit, Ref: repo.dir},
		"file url without the test option":   {Kind: domain.FetchGit, Ref: "file://" + repo.dir},
		"relative path":                      {Kind: domain.FetchGit, Ref: "../repo"},
		"plain http":                         {Kind: domain.FetchGit, Ref: "http://example.com/r.git"},
		"ext transport":                      {Kind: domain.FetchGit, Ref: "ext::sh -c touch% /tmp/pwned"},
		"option as repository":               {Kind: domain.FetchGit, Ref: "--upload-pack=touch /tmp/pwned"},
		"github not owner/repo":              {Kind: domain.FetchGitHub, Ref: "owner"},
		"github url":                         {Kind: domain.FetchGitHub, Ref: "https://github.com/o/r"},
		"github traversal":                   {Kind: domain.FetchGitHub, Ref: "../x/y"},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, tmp := newFetcher(t) // default: remote only
			_, err := f.Fetch(context.Background(), spec)
			if !errors.Is(err, domain.ErrUnsupportedSource) {
				t.Fatalf("error = %v, want ErrUnsupportedSource", err)
			}
			assertNothingLeft(t, tmp)
		})
	}
}

func TestFetch_GitRefusesRevisionsThatLookLikeOptions(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	repo.commit("a", "1", "first")
	for _, rev := range []string{"--upload-pack=x", "-b", "a b", "a;b", "a\nb"} {
		f, _ := localGit(t)
		_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchGit, Ref: repo.dir, Rev: rev})
		if !errors.Is(err, domain.ErrUnsupportedSource) {
			t.Errorf("Rev %q: error = %v, want ErrUnsupportedSource", rev, err)
		}
	}
}

func TestFetch_GitUnknownRevisionOrRepositoryIsNothingToInstall(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	repo.commit("a", "1", "first")
	f, tmp := localGit(t)

	_, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchGit, Ref: repo.dir, Rev: "no-such-branch"})
	if !errors.Is(err, domain.ErrNothingToInstall) {
		t.Errorf("unknown rev: error = %v, want ErrNothingToInstall", err)
	}
	_, err = f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchGit, Ref: filepath.Join(repo.dir, "missing")})
	if !errors.Is(err, domain.ErrNothingToInstall) {
		t.Errorf("unknown repo: error = %v, want ErrNothingToInstall", err)
	}
	_, err = f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchGit, Ref: repo.dir, Rev: "deadbeef"})
	if !errors.Is(err, domain.ErrNothingToInstall) {
		t.Errorf("unknown commit: error = %v, want ErrNothingToInstall", err)
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_GitCancelledContext(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	repo.commit("a", "1", "first")
	f, tmp := localGit(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := f.Fetch(ctx, domain.FetchSpec{Kind: domain.FetchGit, Ref: repo.dir}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	assertNothingLeft(t, tmp)
}

func TestFetch_GitMissingBinaryIsAnError(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	repo.commit("a", "1", "first")
	f, tmp := newFetcher(t, fetch.WithLocalGit(), fetch.WithGitBinary(filepath.Join(t.TempDir(), "no-git")))

	if _, err := f.Fetch(context.Background(), domain.FetchSpec{Kind: domain.FetchGit, Ref: repo.dir}); err == nil {
		t.Fatal("want an error")
	}
	assertNothingLeft(t, tmp)
}
