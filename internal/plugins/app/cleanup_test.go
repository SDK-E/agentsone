// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SDK-E/agentsone/internal/plugins/app"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// stuckCleanupFetcher answers a tree whose temporary copy cannot be removed.
type stuckCleanupFetcher struct{ files fstest.MapFS }

func (f stuckCleanupFetcher) Fetch(context.Context, domain.FetchSpec) (domain.Tree, error) {
	return domain.Tree{FS: f.files, Cleanup: func() error { return errors.New("folder busy") }}, nil
}

func TestPlugins_InstallReportsACopyItCannotRemove(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	svc, _, _ := depsWith(t, func(d *app.PluginsDeps) {
		d.Fetcher = stuckCleanupFetcher{files: nativeTree()}
		d.Log = slog.New(slog.NewTextHandler(&logs, nil))
	})

	if _, err := svc.Install(humanCtx(), app.InstallInput{Source: "agentsone", Ref: "acme/bright"}); err != nil {
		t.Fatalf("install failed over a leftover temporary copy: %v", err)
	}
	if got := logs.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, "folder busy") {
		t.Fatalf("leftover copy not reported: %q", got)
	}
}

// brokenInsertRepo refuses to store a plugin record.
type brokenInsertRepo struct{ domain.PluginRepository }

func (brokenInsertRepo) Insert(context.Context, domain.Plugin) error { return errors.New("database full") }

// stuckRemoveInstaller installs but cannot remove.
type stuckRemoveInstaller struct{ domain.Installer }

func (stuckRemoveInstaller) Remove(string, string) error { return errors.New("permission denied") }

func TestPlugins_InstallReportsFilesItCannotRollBack(t *testing.T) {
	t.Parallel()
	svc, _, _ := depsWith(t, func(d *app.PluginsDeps) {
		d.Fetcher = &specFetcher{files: nativeTree()}
		d.Repo = brokenInsertRepo{PluginRepository: d.Repo}
		d.Installer = stuckRemoveInstaller{Installer: d.Installer}
	})

	_, err := svc.Install(humanCtx(), app.InstallInput{Source: "agentsone", Ref: "acme/bright"})

	if err == nil || !strings.Contains(err.Error(), "database full") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v, want both the insert and the rollback failure", err)
	}
}
