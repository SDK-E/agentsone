// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

func TestPanelFilePath(t *testing.T) {
	t.Parallel()
	panels := []domain.Panel{{Name: "disk", Entry: "./panel/index.html", Icon: "./panel/icon.svg"}}
	cases := []struct {
		in   string
		want string
		err  error
	}{
		{"./panel/index.html", "panel/index.html", nil},
		{"panel/icon.svg", "panel/icon.svg", nil},
		{"panel/parts/../index.html", "panel/index.html", nil},
		{"panel/../../etc/passwd", "", domain.ErrUnsafePath},
		{"../outside", "", domain.ErrUnsafePath},
		{"/etc/passwd", "", domain.ErrUnsafePath},
		{"panel/.secret", "", domain.ErrUnsafePath},
		{"panel\\index.html", "", domain.ErrUnsafePath},
		{"", "", domain.ErrUnsafePath},
		{".", "", domain.ErrUnsafePath},
		{"agents/founder.json", "", domain.ErrNotFound},
		{"panel", "", domain.ErrNotFound},
		{"panelx/index.html", "", domain.ErrNotFound},
	}
	for _, c := range cases {
		got, err := domain.PanelFilePath(panels, c.in)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("%q: err = %v, want %v", c.in, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// An entry at the plugin root opens only itself and its icon, never the rest of the plugin.
func TestPanelFilePath_RootEntryOpensOnlyItself(t *testing.T) {
	t.Parallel()
	panels := []domain.Panel{{Entry: "./index.html", Icon: "./icon.svg"}}
	for _, ok := range []string{"index.html", "./icon.svg"} {
		if _, err := domain.PanelFilePath(panels, ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	if _, err := domain.PanelFilePath(panels, "agents/founder.json"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("other file: %v", err)
	}
}

func TestPanelFilePath_NoPanelsMeansNothing(t *testing.T) {
	t.Parallel()
	if _, err := domain.PanelFilePath(nil, "panel/index.html"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPanelMediaType(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"panel/index.html": "text/html",
		"panel/icon.svg":   "image/svg+xml",
		"panel/data.json":  "application/json",
		"panel/blob":       "application/octet-stream",
	} {
		if got := domain.PanelMediaType(in); got != want {
			t.Errorf("%s = %s, want %s", in, got, want)
		}
	}
}
