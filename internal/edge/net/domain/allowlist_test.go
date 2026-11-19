// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

func TestParseName_NormalisesCaseAndTrailingDot(t *testing.T) {
	t.Parallel()
	got, err := domain.ParseName("Deb.Debian.ORG.")
	if err != nil {
		t.Fatalf("ParseName() error = %v", err)
	}
	if got.String() != "deb.debian.org" {
		t.Fatalf("ParseName() = %q, want %q", got, "deb.debian.org")
	}
}

func TestParseName_RejectsInvalidNames(t *testing.T) {
	t.Parallel()
	label63 := strings.Repeat("a", 63)
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"only a dot", "."},
		{"empty label", "a..b"},
		{"label of 64 characters", strings.Repeat("a", 64) + ".com"},
		{"label starts with a hyphen", "-a.com"},
		{"label ends with a hyphen", "a-.com"},
		{"space", "a b.com"},
		{"wildcard is not a name", "*.github.com"},
		{"ip literal", "10.0.0.1"},
		{"name of 254 characters", strings.Join([]string{label63, label63, label63, strings.Repeat("a", 62)}, ".")},
		{"non ascii", "bücher.de"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.ParseName(tt.input); !errors.Is(err, domain.ErrInvalidName) {
				t.Fatalf("ParseName(%q) error = %v, want %v", tt.input, err, domain.ErrInvalidName)
			}
		})
	}
}

func TestParseName_AcceptsEdgeLengths(t *testing.T) {
	t.Parallel()
	label63 := strings.Repeat("a", 63)
	tests := []struct {
		name  string
		input string
	}{
		{"label of 63 characters", label63 + ".com"},
		{"name of 253 characters", strings.Join([]string{label63, label63, label63, strings.Repeat("a", 61)}, ".")},
		{"single label", "localhost"},
		{"digits and hyphen inside", "a-1.b2.com"},
		{"underscore label for service records", "_dmarc.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.ParseName(tt.input); err != nil {
				t.Fatalf("ParseName(%q) error = %v", tt.input, err)
			}
		})
	}
}

func TestParsePattern_RejectsInvalidPatterns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"bare star", "*"},
		{"star without dot", "*github.com"},
		{"star on a top level domain", "*.com"},
		{"star in the middle", "a.*.com"},
		{"double star", "*.*.github.com"},
		{"bad name", "git hub.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := domain.ParsePattern(tt.input); !errors.Is(err, domain.ErrInvalidPattern) {
				t.Fatalf("ParsePattern(%q) error = %v, want %v", tt.input, err, domain.ErrInvalidPattern)
			}
		})
	}
}

func TestPattern_StringRoundTrips(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"github.com", "*.githubusercontent.com"} {
		p, err := domain.ParsePattern(in)
		if err != nil {
			t.Fatalf("ParsePattern(%q) error = %v", in, err)
		}
		if p.String() != in {
			t.Fatalf("String() = %q, want %q", p.String(), in)
		}
	}
}

func TestPattern_Matches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		host    string
		want    bool
	}{
		{"exact name matches itself", "github.com", "github.com", true},
		{"exact name ignores case", "github.com", "GitHub.com", true},
		{"exact name does not match a subdomain", "github.com", "api.github.com", false},
		{"exact name does not match a longer suffix", "github.com", "evilgithub.com", false},
		{"wildcard matches one label below", "*.github.com", "api.github.com", true},
		{"wildcard matches two labels below", "*.github.com", "a.b.github.com", true},
		{"wildcard does not match the apex", "*.github.com", "github.com", false},
		{"wildcard does not match a lookalike suffix", "*.github.com", "evilgithub.com", false},
		{"wildcard does not match a parent", "*.api.github.com", "github.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := mustPattern(t, tt.pattern)
			n := mustName(t, tt.host)
			if got := p.Matches(n); got != tt.want {
				t.Fatalf("%q.Matches(%q) = %v, want %v", tt.pattern, tt.host, got, tt.want)
			}
		})
	}
}

func TestAllowList_AllowsOnlyMatchingNames(t *testing.T) {
	t.Parallel()
	list := mustList(t, "deb.debian.org", "*.githubusercontent.com")
	tests := []struct {
		host string
		want bool
	}{
		{"deb.debian.org", true},
		{"raw.githubusercontent.com", true},
		{"debian.org", false},
		{"example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			if got := list.Allows(mustName(t, tt.host)); got != tt.want {
				t.Fatalf("Allows(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestAllowList_EmptyListAllowsNothing(t *testing.T) {
	t.Parallel()
	var list domain.AllowList
	if list.Allows(mustName(t, "github.com")) {
		t.Fatal("empty list allowed github.com")
	}
}

func TestNewAllowList_RejectsDuplicatePatterns(t *testing.T) {
	t.Parallel()
	_, err := domain.NewAllowList([]domain.Entry{
		{Pattern: mustPattern(t, "github.com")},
		{Pattern: mustPattern(t, "GitHub.com")},
	})
	if !errors.Is(err, domain.ErrDuplicateEntry) {
		t.Fatalf("NewAllowList() error = %v, want %v", err, domain.ErrDuplicateEntry)
	}
}

func TestAllowList_WithAddsEntryInSortedOrder(t *testing.T) {
	t.Parallel()
	list := mustList(t, "github.com", "deb.debian.org")

	got, err := list.With(domain.Entry{Pattern: mustPattern(t, "example.org"), Why: "test"})
	if err != nil {
		t.Fatalf("With() error = %v", err)
	}

	want := []string{"deb.debian.org", "example.org", "github.com"}
	if diff := cmp.Diff(want, patterns(got)); diff != "" {
		t.Fatalf("entries mismatch (-want +got):\n%s", diff)
	}
}

func TestAllowList_WithLeavesOriginalUnchanged(t *testing.T) {
	t.Parallel()
	list := mustList(t, "github.com")

	if _, err := list.With(domain.Entry{Pattern: mustPattern(t, "example.org")}); err != nil {
		t.Fatalf("With() error = %v", err)
	}

	if diff := cmp.Diff([]string{"github.com"}, patterns(list)); diff != "" {
		t.Fatalf("original changed (-want +got):\n%s", diff)
	}
}

func TestAllowList_WithRejectsDuplicate(t *testing.T) {
	t.Parallel()
	list := mustList(t, "github.com")
	if _, err := list.With(domain.Entry{Pattern: mustPattern(t, "github.com")}); !errors.Is(err, domain.ErrDuplicateEntry) {
		t.Fatalf("With() error = %v, want %v", err, domain.ErrDuplicateEntry)
	}
}

func TestAllowList_WithoutRemovesEntry(t *testing.T) {
	t.Parallel()
	list := mustList(t, "github.com", "deb.debian.org")

	got, err := list.Without(mustPattern(t, "github.com"))
	if err != nil {
		t.Fatalf("Without() error = %v", err)
	}

	if diff := cmp.Diff([]string{"deb.debian.org"}, patterns(got)); diff != "" {
		t.Fatalf("entries mismatch (-want +got):\n%s", diff)
	}
}

func TestAllowList_WithoutRejectsUnknownPattern(t *testing.T) {
	t.Parallel()
	list := mustList(t, "github.com")
	if _, err := list.Without(mustPattern(t, "*.github.com")); !errors.Is(err, domain.ErrEntryNotFound) {
		t.Fatalf("Without() error = %v, want %v", err, domain.ErrEntryNotFound)
	}
}

func TestDefaultAllowList_HoldsTheDocumentedHosts(t *testing.T) {
	t.Parallel()
	want := []string{
		"codeload.github.com",
		"deb.debian.org",
		"downloads.claude.ai",
		"github.com",
		"objects.githubusercontent.com",
		"open-vsx.org",
		"openvsxorg.blob.core.windows.net",
		"raw.githubusercontent.com",
		"registry.modelcontextprotocol.io",
		"registry.npmjs.org",
		"release-assets.githubusercontent.com",
		"security.debian.org",
	}
	if diff := cmp.Diff(want, patterns(domain.DefaultAllowList())); diff != "" {
		t.Fatalf("default list mismatch (-want +got):\n%s", diff)
	}
}

func TestDefaultAllowList_ExplainsEveryEntry(t *testing.T) {
	t.Parallel()
	for _, e := range domain.DefaultAllowList().Entries() {
		if strings.TrimSpace(e.Why) == "" {
			t.Errorf("entry %q has no reason", e.Pattern)
		}
	}
}

func TestDefaultAllowList_LeavesModelProvidersOut(t *testing.T) {
	t.Parallel()
	list := domain.DefaultAllowList()
	for _, host := range []string{"api.anthropic.com", "api.openai.com", "generativelanguage.googleapis.com"} {
		if list.Allows(mustName(t, host)) {
			t.Errorf("default list allows model provider %q", host)
		}
	}
}

func mustName(t *testing.T, s string) domain.Name {
	t.Helper()
	n, err := domain.ParseName(s)
	if err != nil {
		t.Fatalf("ParseName(%q) error = %v", s, err)
	}
	return n
}

func mustPattern(t *testing.T, s string) domain.Pattern {
	t.Helper()
	p, err := domain.ParsePattern(s)
	if err != nil {
		t.Fatalf("ParsePattern(%q) error = %v", s, err)
	}
	return p
}

func mustList(t *testing.T, ps ...string) domain.AllowList {
	t.Helper()
	entries := make([]domain.Entry, 0, len(ps))
	for _, p := range ps {
		entries = append(entries, domain.Entry{Pattern: mustPattern(t, p), Why: "test"})
	}
	l, err := domain.NewAllowList(entries)
	if err != nil {
		t.Fatalf("NewAllowList() error = %v", err)
	}
	return l
}

func patterns(l domain.AllowList) []string {
	out := []string{}
	for _, e := range l.Entries() {
		out = append(out, e.Pattern.String())
	}
	return out
}
