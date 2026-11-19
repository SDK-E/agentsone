// SPDX-License-Identifier: MIT

package fsinstall_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SDK-E/agentsone/internal/plugins/adapters/fsinstall"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

var manifestJSON = []byte(`{"name":"garden-kit"}`)

func request(tree fstest.MapFS) domain.InstallRequest {
	return domain.InstallRequest{Name: "garden-kit", Version: "1.0.0", Tree: tree, Manifest: manifestJSON}
}

func newInstaller(t *testing.T, limits domain.InstallLimits) (*fsinstall.Installer, string) {
	t.Helper()
	root := t.TempDir()
	return fsinstall.New(root, limits), root
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- path built by the test
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestInstall_WritesTreeManifestAndExtraFilesUnderNameAndVersion(t *testing.T) {
	t.Parallel()
	inst, root := newInstaller(t, domain.DefaultInstallLimits)
	req := request(fstest.MapFS{
		"skills/prune/SKILL.md": {Data: []byte("skill")},
		"bin/run":               {Data: []byte("#!/bin/sh\n"), Mode: 0o755},
	})
	req.Extra = []domain.File{{Path: "agentsone/templates/gardener.json", Data: []byte("{}")}}

	got, err := inst.Install(context.Background(), req)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	want := filepath.Join(root, "garden-kit", "1.0.0")
	if got.Dir != want {
		t.Errorf("Dir = %q, want %q", got.Dir, want)
	}
	if readFile(t, filepath.Join(want, "skills/prune/SKILL.md")) != "skill" {
		t.Error("tree file missing")
	}
	if readFile(t, filepath.Join(want, "agentsone.plugin.json")) != string(manifestJSON) {
		t.Error("manifest missing")
	}
	if readFile(t, filepath.Join(want, "agentsone/templates/gardener.json")) != "{}" {
		t.Error("extra file missing")
	}
	info, err := os.Stat(filepath.Join(want, "bin/run"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("exec bit lost: %v %v", info, err)
	}
	info, _ = os.Stat(filepath.Join(want, "skills/prune/SKILL.md"))
	if info.Mode().Perm() != 0o644 {
		t.Errorf("file mode = %v, want 0644", info.Mode().Perm())
	}
	digest, err := fsinstall.TreeDigest(want)
	if err != nil || digest != got.SHA256 || len(digest) != 64 {
		t.Errorf("digest %q (err %v), result %q", digest, err, got.SHA256)
	}
	if v, err := inst.Verify(want); err != nil || v != got.SHA256 {
		t.Errorf("Verify() = %q, %v", v, err)
	}
}

func TestInstall_PlacesTreeUnderPrefixAndKeepsOnlyOneManifest(t *testing.T) {
	t.Parallel()
	inst, root := newInstaller(t, domain.DefaultInstallLimits)
	req := request(fstest.MapFS{
		"SKILL.md":              {Data: []byte("skill")},
		"agentsone.plugin.json": {Data: []byte(`{"from":"source"}`)},
	})
	req.Prefix = "skills/garden-kit"

	if _, err := inst.Install(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "garden-kit", "1.0.0")
	if readFile(t, filepath.Join(dir, "skills/garden-kit/SKILL.md")) != "skill" {
		t.Error("prefixed tree missing")
	}
	if readFile(t, filepath.Join(dir, "agentsone.plugin.json")) != string(manifestJSON) {
		t.Error("manifest at the root is not the normalised one")
	}
}

func TestInstall_SourceManifestAtTheRootIsReplacedByTheNormalisedOne(t *testing.T) {
	t.Parallel()
	inst, root := newInstaller(t, domain.DefaultInstallLimits)
	req := request(fstest.MapFS{"agentsone.plugin.json": {Data: []byte(`{"from":"source"}`)}})

	if _, err := inst.Install(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(root, "garden-kit", "1.0.0", "agentsone.plugin.json")); got != string(manifestJSON) {
		t.Errorf("manifest = %s", got)
	}
}

func TestInstall_ExistingNameAndVersionIsRefused(t *testing.T) {
	t.Parallel()
	inst, root := newInstaller(t, domain.DefaultInstallLimits)
	req := request(fstest.MapFS{"a": {Data: []byte("1")}})
	if _, err := inst.Install(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	_, err := inst.Install(context.Background(), req)

	if !errors.Is(err, domain.ErrAlreadyInstalled) {
		t.Fatalf("error = %v, want ErrAlreadyInstalled", err)
	}
	assertNoTemp(t, root)
}

func TestInstall_RefusesUnsafeNamesVersionsAndPrefixes(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*domain.InstallRequest){
		"name with slash":    func(r *domain.InstallRequest) { r.Name = "a/b" },
		"name parent":        func(r *domain.InstallRequest) { r.Name = ".." },
		"empty name":         func(r *domain.InstallRequest) { r.Name = "" },
		"version parent":     func(r *domain.InstallRequest) { r.Version = ".." },
		"version with slash": func(r *domain.InstallRequest) { r.Version = "1.0.0/../x" },
		"not semantic":       func(r *domain.InstallRequest) { r.Version = "latest" },
		"prefix parent":      func(r *domain.InstallRequest) { r.Prefix = "../x" },
		"prefix absolute":    func(r *domain.InstallRequest) { r.Prefix = "/etc" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			inst, root := newInstaller(t, domain.DefaultInstallLimits)
			req := request(fstest.MapFS{"a": {Data: []byte("1")}})
			change(&req)

			_, err := inst.Install(context.Background(), req)

			if !errors.Is(err, domain.ErrUnsafePath) && !errors.Is(err, domain.ErrNotSemantic) {
				t.Fatalf("error = %v, want ErrUnsafePath or ErrNotSemantic", err)
			}
			assertEmpty(t, root)
		})
	}
}

func TestInstall_RefusesExtraFilesThatLeaveThePluginAndLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"../escape.json", "/abs.json", "a/../../b.json", "", "."} {
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			inst, root := newInstaller(t, domain.DefaultInstallLimits)
			req := request(fstest.MapFS{"a": {Data: []byte("1")}})
			req.Extra = []domain.File{{Path: "ok.json", Data: []byte("{}")}, {Path: bad, Data: []byte("x")}}

			_, err := inst.Install(context.Background(), req)

			if !errors.Is(err, domain.ErrUnsafePath) {
				t.Fatalf("error = %v, want ErrUnsafePath", err)
			}
			assertEmpty(t, root)
		})
	}
}

func TestInstall_ExtraFileThatCollidesWithATreeFileIsRefused(t *testing.T) {
	t.Parallel()
	inst, root := newInstaller(t, domain.DefaultInstallLimits)
	req := request(fstest.MapFS{"a/b.json": {Data: []byte("1")}})
	req.Extra = []domain.File{{Path: "a/b.json", Data: []byte("2")}}

	if _, err := inst.Install(context.Background(), req); !errors.Is(err, domain.ErrUnsafePath) {
		t.Fatalf("error = %v, want ErrUnsafePath", err)
	}
	assertEmpty(t, root)
}

func TestInstall_SizeLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		limits domain.InstallLimits
		tree   fstest.MapFS
	}{
		{"one file over the file limit", domain.InstallLimits{MaxFiles: 10, MaxFileBytes: 4, MaxTotalSize: 100},
			fstest.MapFS{"big": {Data: []byte("12345")}}},
		{"total over the total limit", domain.InstallLimits{MaxFiles: 10, MaxFileBytes: 10, MaxTotalSize: 8},
			fstest.MapFS{"a": {Data: []byte("12345")}, "b": {Data: []byte("12345")}}},
		{"too many entries", domain.InstallLimits{MaxFiles: 2, MaxFileBytes: 10, MaxTotalSize: 100},
			fstest.MapFS{"a": {Data: []byte("1")}, "b": {Data: []byte("1")}, "c": {Data: []byte("1")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			inst, root := newInstaller(t, tt.limits)

			_, err := inst.Install(context.Background(), request(tt.tree))

			if !errors.Is(err, domain.ErrTooLarge) {
				t.Fatalf("error = %v, want ErrTooLarge", err)
			}
			assertEmpty(t, root)
		})
	}
}

func TestInstall_SymlinkThatLeavesTheTreeIsRefused(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "ok.txt"), "ok")
	for name, target := range map[string]string{
		"up":       "../outside",
		"absolute": "/etc/passwd",
		"deep-up":  "a/../../outside",
	} {
		if err := os.Symlink(target, filepath.Join(src, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"up", "absolute", "deep-up"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			inst, root := newInstaller(t, domain.DefaultInstallLimits)
			only := t.TempDir()
			if err := os.Symlink(readLink(t, filepath.Join(src, name)), filepath.Join(only, name)); err != nil {
				t.Fatal(err)
			}
			req := domain.InstallRequest{Name: "garden-kit", Version: "1.0.0", Tree: os.DirFS(only), Manifest: manifestJSON}

			_, err := inst.Install(context.Background(), req)

			if !errors.Is(err, domain.ErrUnsafePath) {
				t.Fatalf("error = %v, want ErrUnsafePath", err)
			}
			assertEmpty(t, root)
		})
	}
}

func TestInstall_SymlinkInsideTheTreeIsCopiedAsASymlink(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "real/file.txt"), "data")
	if err := os.Symlink("real/file.txt", filepath.Join(src, "link.txt")); err != nil {
		t.Fatal(err)
	}
	inst, root := newInstaller(t, domain.DefaultInstallLimits)

	_, err := inst.Install(context.Background(), domain.InstallRequest{Name: "garden-kit", Version: "1.0.0", Tree: os.DirFS(src), Manifest: manifestJSON})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	link := filepath.Join(root, "garden-kit", "1.0.0", "link.txt")
	if got := readLink(t, link); got != "real/file.txt" {
		t.Errorf("link target = %q", got)
	}
	if readFile(t, link) != "data" {
		t.Error("link does not resolve to the copied file")
	}
}

func TestInstall_NonRegularFilesAreRefused(t *testing.T) {
	t.Parallel()
	inst, root := newInstaller(t, domain.DefaultInstallLimits)
	tree := fstest.MapFS{"pipe": {Mode: os.ModeNamedPipe}}

	_, err := inst.Install(context.Background(), request(tree))

	if !errors.Is(err, domain.ErrUnsafePath) {
		t.Fatalf("error = %v, want ErrUnsafePath", err)
	}
	assertEmpty(t, root)
}

func TestInstall_CancelledContextLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	inst, root := newInstaller(t, domain.DefaultInstallLimits)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := inst.Install(ctx, request(fstest.MapFS{"a": {Data: []byte("1")}}))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	assertEmpty(t, root)
}

func TestTreeDigest_ChangesWithContentPathAndExecBit(t *testing.T) {
	t.Parallel()
	digest := func(files map[string]string, mode os.FileMode) string {
		dir := t.TempDir()
		for p, c := range files {
			mustWrite(t, filepath.Join(dir, p), c)
			if err := os.Chmod(filepath.Join(dir, p), mode); err != nil {
				t.Fatal(err)
			}
		}
		d, err := fsinstall.TreeDigest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	base := digest(map[string]string{"a": "1", "b/c": "2"}, 0o644)

	if same := digest(map[string]string{"b/c": "2", "a": "1"}, 0o644); same != base {
		t.Error("digest depends on creation order")
	}
	for name, d := range map[string]string{
		"content": digest(map[string]string{"a": "1", "b/c": "3"}, 0o644),
		"path":    digest(map[string]string{"a": "1", "b/d": "2"}, 0o644),
		"exec":    digest(map[string]string{"a": "1", "b/c": "2"}, 0o755),
	} {
		if d == base {
			t.Errorf("digest does not change with %s", name)
		}
	}
}

func TestVerify_DetectsATamperedPlugin(t *testing.T) {
	t.Parallel()
	inst, _ := newInstaller(t, domain.DefaultInstallLimits)
	res, err := inst.Install(context.Background(), request(fstest.MapFS{"a": {Data: []byte("1")}}))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(res.Dir, "a"), "tampered")

	got, err := inst.Verify(res.Dir)

	if err != nil || got == res.SHA256 {
		t.Fatalf("Verify() = %q, %v; digest must differ from %q", got, err, res.SHA256)
	}
}

func TestRemove_DeletesTheVersionAndAnEmptyNameFolder(t *testing.T) {
	t.Parallel()
	inst, root := newInstaller(t, domain.DefaultInstallLimits)
	for _, v := range []string{"1.0.0", "1.1.0"} {
		req := request(fstest.MapFS{"a": {Data: []byte("1")}})
		req.Version = v
		if _, err := inst.Install(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}

	if err := inst.Remove("garden-kit", "1.0.0"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "garden-kit", "1.1.0")); err != nil {
		t.Error("other version removed")
	}
	if err := inst.Remove("garden-kit", "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "garden-kit")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("name folder still there: %v", err)
	}
}

func TestRemove_MissingPluginIsNotFound(t *testing.T) {
	t.Parallel()
	inst, _ := newInstaller(t, domain.DefaultInstallLimits)
	if err := inst.Remove("garden-kit", "1.0.0"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestRemove_RefusesPathsThatLeaveThePluginsFolder(t *testing.T) {
	t.Parallel()
	inst, root := newInstaller(t, domain.DefaultInstallLimits)
	outside := filepath.Join(filepath.Dir(root), "victim-"+filepath.Base(root))
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	tests := [][2]string{
		{"..", "1.0.0"},
		{"garden-kit", ".."},
		{"../" + filepath.Base(outside), "1.0.0"},
		{"a/b", "1.0.0"},
		{"", ""},
		{".", "."},
	}
	for _, tt := range tests {
		err := inst.Remove(tt[0], tt[1])
		if !errors.Is(err, domain.ErrUnsafePath) && !errors.Is(err, domain.ErrNotSemantic) {
			t.Errorf("Remove(%q, %q) error = %v, want a refusal", tt[0], tt[1], err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("outside folder was touched: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("plugins folder was touched: %v", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readLink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// assertEmpty fails when the plugins folder holds anything, including hidden
// temporary directories.
func assertEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) > 0 {
		t.Errorf("plugins folder is not empty: %s", strings.Join(names, ", "))
	}
}

// assertNoTemp fails when a temporary install directory is left behind.
func assertNoTemp(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("temporary directory left behind: %s", e.Name())
		}
	}
}
