// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

func panelPlugin(t *testing.T, env pluginEnv, enabled bool) (domain.Plugin, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "panel", "index.html"), "<h1>Disk watch</h1>")
	writeFile(t, filepath.Join(dir, "panel", "icon.svg"), "<svg/>")
	writeFile(t, filepath.Join(dir, "agents", "secret.json"), `{"token":"x"}`)
	name, id := "disk-watch", "plg_01JD7ZK3M00000000000000PA1"
	if !enabled {
		name, id = "disk-watch-off", "plg_01JD7ZK3M00000000000000PA2"
	}
	p := plugin(name, "1.0.0", func(m *domain.Manifest) {
		m.Contents.Panels = []domain.Panel{{Name: "disk", Title: "Disk", Icon: "./panel/icon.svg", Entry: "./panel/index.html", Scopes: []string{"machine:read"}}}
	})
	p.ID, p.Dir, p.Enabled = id, dir, enabled
	if err := env.repo.Insert(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p, dir
}

func TestReadPanelFile_ReadsTheEntry(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)
	p, _ := panelPlugin(t, env, true)

	f, err := env.svc.ReadPanelFile(context.Background(), p.ID, "./panel/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != "panel/index.html" || f.MediaType != "text/html" || string(f.Data) != "<h1>Disk watch</h1>" {
		t.Fatalf("file %+v", f)
	}
}

func TestReadPanelFile_Refusals(t *testing.T) {
	t.Parallel()
	env := newPluginEnv(t)
	p, dir := panelPlugin(t, env, true)
	off, _ := panelPlugin(t, env, false)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "shadow"), "secret")
	if err := os.Symlink(filepath.Join(outside, "shadow"), filepath.Join(dir, "panel", "escape.html")); err != nil {
		t.Fatal(err)
	}
	// A link inside the panel folder that points at the plugin's other files.
	if err := os.Symlink(filepath.Join("..", "agents", "secret.json"), filepath.Join(dir, "panel", "inside.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "agents"), filepath.Join(dir, "panel", "more")); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "panel", "big.html")
	writeFile(t, big, strings.Repeat("x", domain.MaxPanelFile+1))
	ctx := context.Background()

	cases := []struct {
		name, id, path string
		want           error
	}{
		{"turned off", off.ID, "panel/index.html", core.ErrConflict},
		{"unknown plugin", "plg_01JD7ZK3M0000000000000ZZZZ", "panel/index.html", core.ErrNotFound},
		{"dot dot", p.ID, "panel/../../etc/passwd", domain.ErrUnsafePath},
		{"absolute", p.ID, "/etc/passwd", domain.ErrUnsafePath},
		{"other plugin file", p.ID, "agents/secret.json", domain.ErrNotFound},
		{"missing", p.ID, "panel/nope.html", domain.ErrNotFound},
		{"symlink out of the plugin", p.ID, "panel/escape.html", domain.ErrUnsafePath},
		{"symlink to another plugin file", p.ID, "panel/inside.json", domain.ErrUnsafePath},
		{"symlinked folder", p.ID, "panel/more/secret.json", domain.ErrUnsafePath},
		{"too large", p.ID, "panel/big.html", domain.ErrTooLarge},
	}
	for _, c := range cases {
		if _, err := env.svc.ReadPanelFile(ctx, c.id, c.path); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}
