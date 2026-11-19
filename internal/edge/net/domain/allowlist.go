// SPDX-License-Identifier: MIT

package domain

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Errors returned by allow list parsing and editing.
var (
	ErrInvalidName    = errors.New("invalid host name")
	ErrInvalidPattern = errors.New("invalid allow list pattern")
	ErrDuplicateEntry = errors.New("allow list entry already present")
	ErrEntryNotFound  = errors.New("allow list entry not found")
)

const (
	maxNameLength  = 253
	maxLabelLength = 63
	wildcardPrefix = "*."
)

// Name is a normalised DNS host name: lower case ASCII, no trailing dot. The zero Name is invalid.
type Name struct{ s string }

// ParseName parses a host name as the virtual machine sends it in a DNS query. It accepts letters,
// digits, hyphens inside a label and underscores (used by service records). Names that are IP
// literals, wildcards or non-ASCII are refused: the virtual machine must send punycode.
func ParseName(s string) (Name, error) {
	s = strings.ToLower(strings.TrimSuffix(s, "."))
	if s == "" || len(s) > maxNameLength {
		return Name{}, fmt.Errorf("%w: %q", ErrInvalidName, s)
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return Name{}, fmt.Errorf("%w: %q is an IP address", ErrInvalidName, s)
	}
	for label := range strings.SplitSeq(s, ".") {
		if !validLabel(label) {
			return Name{}, fmt.Errorf("%w: %q", ErrInvalidName, s)
		}
	}
	return Name{s: s}, nil
}

func validLabel(label string) bool {
	if label == "" || len(label) > maxLabelLength {
		return false
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := range len(label) {
		c := label[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func (n Name) String() string { return n.s }

// IsZero reports whether n is the zero Name.
func (n Name) IsZero() bool { return n.s == "" }

// Pattern is one allow list pattern: an exact name ("github.com") or a wildcard ("*.github.com")
// that matches every name below the base name at any depth, but not the base name itself.
type Pattern struct {
	base     Name
	wildcard bool
}

// ParsePattern parses an allow list pattern. A wildcard needs a base of at least two labels, so
// "*.com" is refused.
func ParsePattern(s string) (Pattern, error) {
	wildcard := strings.HasPrefix(s, wildcardPrefix)
	rest := strings.TrimPrefix(s, wildcardPrefix)
	if strings.Contains(rest, "*") {
		return Pattern{}, fmt.Errorf("%w: %q", ErrInvalidPattern, s)
	}
	base, err := ParseName(rest)
	if err != nil {
		return Pattern{}, fmt.Errorf("%w: %w", ErrInvalidPattern, err)
	}
	if wildcard && !strings.Contains(base.s, ".") {
		return Pattern{}, fmt.Errorf("%w: %q covers a whole top level domain", ErrInvalidPattern, s)
	}
	return Pattern{base: base, wildcard: wildcard}, nil
}

func (p Pattern) String() string {
	if p.wildcard {
		return wildcardPrefix + p.base.s
	}
	return p.base.s
}

// Matches reports whether n matches p.
func (p Pattern) Matches(n Name) bool {
	if p.base.IsZero() || n.IsZero() {
		return false
	}
	if !p.wildcard {
		return n.s == p.base.s
	}
	return strings.HasSuffix(n.s, "."+p.base.s)
}

// Entry is one allow list line: a pattern and why it is there.
type Entry struct {
	Pattern Pattern
	Why     string
}

// AllowList is the set of names the virtual machine may resolve. It is an immutable value: editing
// returns a new list. Entries are kept sorted by pattern. The zero AllowList allows nothing.
type AllowList struct {
	entries []Entry
}

// NewAllowList builds a list from entries. Two entries with the same pattern are refused.
func NewAllowList(entries []Entry) (AllowList, error) {
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b Entry) int { return strings.Compare(a.Pattern.String(), b.Pattern.String()) })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Pattern == sorted[i-1].Pattern {
			return AllowList{}, fmt.Errorf("%w: %s", ErrDuplicateEntry, sorted[i].Pattern)
		}
	}
	return AllowList{entries: sorted}, nil
}

// Allows reports whether n matches any entry.
func (l AllowList) Allows(n Name) bool {
	for _, e := range l.entries {
		if e.Pattern.Matches(n) {
			return true
		}
	}
	return false
}

// Entries returns a copy of the entries, sorted by pattern.
func (l AllowList) Entries() []Entry { return slices.Clone(l.entries) }

// With returns a new list that also holds e.
func (l AllowList) With(e Entry) (AllowList, error) {
	return NewAllowList(append(l.Entries(), e))
}

// Without returns a new list without the entry for p.
func (l AllowList) Without(p Pattern) (AllowList, error) {
	i := slices.IndexFunc(l.entries, func(e Entry) bool { return e.Pattern == p })
	if i < 0 {
		return AllowList{}, fmt.Errorf("%w: %s", ErrEntryNotFound, p)
	}
	return AllowList{entries: slices.Delete(l.Entries(), i, i+1)}, nil
}

// DefaultAllowList is the list written on first start. Model providers are deliberately absent:
// the machine reaches models only through the edge gateway, which holds the keys.
func DefaultAllowList() AllowList {
	defaults := []struct{ pattern, why string }{
		{"deb.debian.org", "Debian packages: apt updates and installs inside the machine."},
		{"security.debian.org", "Debian security updates."},
		{"registry.npmjs.org", "npm registry: packages for the machine and its tools."},
		{"downloads.claude.ai", "Claude engine downloads: the Claude runner fetches the engine from here, after the licence is accepted."},
		{"open-vsx.org", "Open VSX: the extension store of the built-in IDE."},
		{"openvsxorg.blob.core.windows.net", "Open VSX download host: extension files are served from here."},
		{"github.com", "GitHub: plugins and agent templates are fetched from repositories."},
		{"codeload.github.com", "GitHub archive downloads used when fetching plugins."},
		{"objects.githubusercontent.com", "GitHub release and large file downloads used by plugins."},
		{"raw.githubusercontent.com", "GitHub raw files: plugin manifests and single-file fetches."},
		{"release-assets.githubusercontent.com", "GitHub release downloads: release files now redirect here."},
		{"registry.modelcontextprotocol.io", "MCP registry: the Plugins page lists MCP servers from here."},
	}
	entries := make([]Entry, 0, len(defaults))
	for _, d := range defaults {
		p, err := ParsePattern(d.pattern)
		if err != nil {
			panic(err) // a fixed list; a bad entry is a programming error caught by the tests
		}
		entries = append(entries, Entry{Pattern: p, Why: d.why})
	}
	l, err := NewAllowList(entries)
	if err != nil {
		panic(err)
	}
	return l
}
