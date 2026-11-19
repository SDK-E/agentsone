// SPDX-License-Identifier: MIT

package pluginstest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

var pluginEpoch = time.Date(2026, 3, 1, 9, 30, 0, 123456789, time.UTC)

// Plugin returns an installed plugin with a stable id built from n. It
// provides the kinds skill and theme, and is enabled and healthy.
func Plugin(n int) domain.Plugin {
	raw := fmt.Sprintf(`{
  "name": "plugin-%d",
  "version": "1.%d.0",
  "description": "Fixture plugin %d.",
  "author": {"name": "Fixture"},
  "license": "MIT",
  "source": {"kind": "agentsone", "ref": "./plugin-%d"},
  "compat": {"agentsone": ">=0.1.0"},
  "contents": {
    "mcpServers": [],
    "skills": [{"name": "skill-%d", "path": "./skills/skill-%d", "description": "A skill."}],
    "agentTemplates": [],
    "runners": [],
    "panels": [],
    "themes": [{"path": "./themes/theme.json"}],
    "emitters": []
  }
}`, n, n, n, n, n, n)
	m, err := domain.ParseManifest([]byte(raw))
	if err != nil {
		panic(fmt.Sprintf("pluginstest.Plugin(%d): %v", n, err))
	}
	return domain.Plugin{
		ID:          fmt.Sprintf("plg_%026d", n),
		Name:        m.Name,
		Version:     m.Version,
		Kinds:       []string{"skill", "theme"},
		Source:      string(domain.SourceAgentsOne),
		Ref:         fmt.Sprintf("./plugin-%d", n),
		Enabled:     true,
		Health:      domain.HealthOK,
		InstalledBy: domain.Actor{Kind: "human", Name: "alice"},
		InstalledAt: pluginEpoch.Add(time.Duration(n) * time.Minute),
		Manifest:    m,
		Dir:         fmt.Sprintf("/var/lib/agentsone/plugins/plugin-%d/1.%d.0", n, n),
		SHA256:      fmt.Sprintf("%064d", n),
	}
}

// RunPluginRepositoryContract runs the contract on a fresh repository from
// newRepo for every case.
func RunPluginRepositoryContract(t *testing.T, newRepo func(t *testing.T) domain.PluginRepository) {
	t.Helper()
	cases := map[string]func(t *testing.T, repo domain.PluginRepository){
		"insert then get round-trips every field":      pluginRoundTrip,
		"get of a missing plugin is not found":         pluginGetMissing,
		"insert refuses a duplicate name and version":  pluginDuplicateNameVersion,
		"insert refuses a duplicate id":                pluginDuplicateID,
		"by name returns newest install first":         pluginByName,
		"by name of an unknown name is empty":          pluginByNameNone,
		"list pages by id":                             pluginListPages,
		"list filters by kind":                         pluginListKind,
		"list filters by enabled":                      pluginListEnabled,
		"set enabled changes the flag and health":      pluginSetEnabled,
		"set enabled of a missing plugin is not found": pluginSetEnabledMissing,
		"delete removes the plugin":                    pluginDelete,
		"delete of a missing plugin is not found":      pluginDeleteMissing,
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			run(t, newRepo(t))
		})
	}
}

func insertPlugin(t *testing.T, repo domain.PluginRepository, p domain.Plugin) {
	t.Helper()
	must(t, repo.Insert(context.Background(), p))
}

func pluginRoundTrip(t *testing.T, repo domain.PluginRepository) {
	want := Plugin(1)
	want.Enabled = false
	want.Health = domain.HealthDegraded
	insertPlugin(t, repo, want)

	got, err := repo.Get(context.Background(), want.ID)
	must(t, err)

	wantManifest, err := want.Manifest.Encode()
	must(t, err)
	gotManifest, err := got.Manifest.Encode()
	must(t, err)
	if got.ID != want.ID || got.Name != want.Name || got.Version != want.Version ||
		!slices.Equal(got.Kinds, want.Kinds) || got.Source != want.Source || got.Ref != want.Ref ||
		got.Enabled != want.Enabled || got.Health != want.Health || got.InstalledBy != want.InstalledBy ||
		got.Dir != want.Dir || got.SHA256 != want.SHA256 || string(gotManifest) != string(wantManifest) {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
	if !got.InstalledAt.Equal(want.InstalledAt) || got.InstalledAt.Location() != time.UTC {
		t.Fatalf("InstalledAt = %v (%v), want %v in UTC", got.InstalledAt, got.InstalledAt.Location(), want.InstalledAt)
	}
}

func pluginGetMissing(t *testing.T, repo domain.PluginRepository) {
	if _, err := repo.Get(context.Background(), "plg_none"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func pluginDuplicateNameVersion(t *testing.T, repo domain.PluginRepository) {
	insertPlugin(t, repo, Plugin(1))
	dup := Plugin(1)
	dup.ID = Plugin(2).ID
	if err := repo.Insert(context.Background(), dup); !errors.Is(err, domain.ErrAlreadyInstalled) {
		t.Fatalf("Insert() error = %v, want ErrAlreadyInstalled", err)
	}
}

func pluginDuplicateID(t *testing.T, repo domain.PluginRepository) {
	insertPlugin(t, repo, Plugin(1))
	dup := Plugin(2)
	dup.ID = Plugin(1).ID
	if err := repo.Insert(context.Background(), dup); !errors.Is(err, domain.ErrAlreadyInstalled) {
		t.Fatalf("Insert() error = %v, want ErrAlreadyInstalled", err)
	}
}

func pluginByName(t *testing.T, repo domain.PluginRepository) {
	older, newer := Plugin(1), Plugin(2)
	newer.Name, newer.Version = older.Name, "2.0.0"
	newer.Manifest.Name, newer.Manifest.Version = older.Name, "2.0.0"
	other := Plugin(3)
	insertPlugin(t, repo, older)
	insertPlugin(t, repo, newer)
	insertPlugin(t, repo, other)

	got, err := repo.ByName(context.Background(), older.Name)
	must(t, err)

	if len(got) != 2 || got[0].ID != newer.ID || got[1].ID != older.ID {
		t.Fatalf("ByName() = %v, want newest install first: %s, %s", ids(got), newer.ID, older.ID)
	}
}

func pluginByNameNone(t *testing.T, repo domain.PluginRepository) {
	got, err := repo.ByName(context.Background(), "nobody")
	must(t, err)
	if len(got) != 0 {
		t.Fatalf("ByName() = %v, want none", ids(got))
	}
}

func pluginListPages(t *testing.T, repo domain.PluginRepository) {
	for i := 1; i <= 5; i++ {
		insertPlugin(t, repo, Plugin(i))
	}
	ctx := context.Background()

	first, err := repo.List(ctx, domain.PluginFilter{}, "", 2)
	must(t, err)
	second, err := repo.List(ctx, domain.PluginFilter{}, first[1].ID, 2)
	must(t, err)
	rest, err := repo.List(ctx, domain.PluginFilter{}, second[1].ID, 10)
	must(t, err)

	got := append(append(ids(first), ids(second)...), ids(rest)...)
	want := []string{Plugin(1).ID, Plugin(2).ID, Plugin(3).ID, Plugin(4).ID, Plugin(5).ID}
	if !slices.Equal(got, want) || len(first) != 2 || len(second) != 2 || len(rest) != 1 {
		t.Fatalf("pages = %v, want %v in pages of 2, 2, 1", got, want)
	}
}

func pluginListKind(t *testing.T, repo domain.PluginRepository) {
	themeOnly := Plugin(1)
	themeOnly.Kinds = []string{"theme"}
	runner := Plugin(2)
	runner.Kinds = []string{"runner", "skill"}
	insertPlugin(t, repo, themeOnly)
	insertPlugin(t, repo, runner)

	got, err := repo.List(context.Background(), domain.PluginFilter{Kind: "skill"}, "", 10)
	must(t, err)

	if want := []string{runner.ID}; !slices.Equal(ids(got), want) {
		t.Fatalf("List(kind=skill) = %v, want %v", ids(got), want)
	}
}

func pluginListEnabled(t *testing.T, repo domain.PluginRepository) {
	on, off := Plugin(1), Plugin(2)
	off.Enabled = false
	insertPlugin(t, repo, on)
	insertPlugin(t, repo, off)
	yes, no := true, false

	gotOn, err := repo.List(context.Background(), domain.PluginFilter{Enabled: &yes}, "", 10)
	must(t, err)
	gotOff, err := repo.List(context.Background(), domain.PluginFilter{Enabled: &no}, "", 10)
	must(t, err)

	if !slices.Equal(ids(gotOn), []string{on.ID}) || !slices.Equal(ids(gotOff), []string{off.ID}) {
		t.Fatalf("enabled = %v, disabled = %v", ids(gotOn), ids(gotOff))
	}
}

func pluginSetEnabled(t *testing.T, repo domain.PluginRepository) {
	p := Plugin(1)
	insertPlugin(t, repo, p)

	must(t, repo.SetEnabled(context.Background(), p.ID, false, domain.HealthFailed))

	got, err := repo.Get(context.Background(), p.ID)
	must(t, err)
	if got.Enabled || got.Health != domain.HealthFailed {
		t.Fatalf("after SetEnabled: enabled = %v, health = %q", got.Enabled, got.Health)
	}
}

func pluginSetEnabledMissing(t *testing.T, repo domain.PluginRepository) {
	err := repo.SetEnabled(context.Background(), "plg_none", true, domain.HealthOK)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("SetEnabled() error = %v, want ErrNotFound", err)
	}
}

func pluginDelete(t *testing.T, repo domain.PluginRepository) {
	p := Plugin(1)
	insertPlugin(t, repo, p)

	must(t, repo.Delete(context.Background(), p.ID))

	if _, err := repo.Get(context.Background(), p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get() after Delete error = %v, want ErrNotFound", err)
	}
	// The name and version are free again.
	insertPlugin(t, repo, p)
}

func pluginDeleteMissing(t *testing.T, repo domain.PluginRepository) {
	if err := repo.Delete(context.Background(), "plg_none"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete() error = %v, want ErrNotFound", err)
	}
}

func ids(ps []domain.Plugin) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}
