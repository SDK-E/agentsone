// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"testing"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

func TestNormalizePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
		bad  bool
	}{
		{name: "bare relative", in: "skills/a", want: "./skills/a"},
		{name: "dot slash", in: "./skills/a", want: "./skills/a"},
		{name: "trailing slash", in: "./skills/a/", want: "./skills/a"},
		{name: "claude variable", in: "${CLAUDE_PLUGIN_ROOT}/bin/x", want: "./bin/x"},
		{name: "our variable", in: "${PLUGIN_ROOT}/bin/x", want: "./bin/x"},
		{name: "parent segment", in: "../x", bad: true},
		{name: "parent in the middle", in: "a/../../x", bad: true},
		{name: "hidden parent after variable", in: "${CLAUDE_PLUGIN_ROOT}/../x", bad: true},
		{name: "absolute", in: "/etc/passwd", bad: true},
		{name: "dot segment", in: "./.git/config", bad: true},
		{name: "backslash", in: `a\b`, bad: true},
		{name: "nul byte", in: "a\x00b", bad: true},
		{name: "unknown variable", in: "${HOME}/x", bad: true},
		{name: "the root itself", in: ".", bad: true},
		{name: "empty", in: "", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NormalizePath(tt.in)
			if tt.bad {
				if !errors.Is(err, domain.ErrUnsafePath) {
					t.Fatalf("NormalizePath(%q) = %q, %v; want ErrUnsafePath", tt.in, got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("NormalizePath(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestRelPath_StripsThePluginRootPrefixes(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"./a/b": "a/b", "${PLUGIN_ROOT}/a/b/": "a/b"} {
		got, err := domain.RelPath(in)
		if err != nil || got != want {
			t.Errorf("RelPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := domain.RelPath("./../x"); !errors.Is(err, domain.ErrUnsafePath) {
		t.Errorf("RelPath(./../x) error = %v, want ErrUnsafePath", err)
	}
}

func TestRewritePluginRoot_ReplacesEveryOccurrence(t *testing.T) {
	t.Parallel()
	got := domain.RewritePluginRoot("${CLAUDE_PLUGIN_ROOT}/a:${CLAUDE_PLUGIN_ROOT}/b")
	if want := "${PLUGIN_ROOT}/a:${PLUGIN_ROOT}/b"; got != want {
		t.Fatalf("RewritePluginRoot() = %q, want %q", got, want)
	}
}
