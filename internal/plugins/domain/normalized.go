// SPDX-License-Identifier: MIT

package domain

import (
	"io/fs"
	"regexp"
	"strings"
	"unicode/utf8"
)

// File is a file an adapter writes into the installed plugin, for example a
// generated agent template. Path is relative to the plugin root, slash separated.
type File struct {
	Path string
	Data []byte
}

// Report lists what an install did not map, and other facts a human should
// see. Nothing is dropped silently.
type Report struct {
	NotMapped []string `json:"notMapped"`
	Notes     []string `json:"notes"`
}

func (r *Report) skip(format string, a ...any) {
	r.NotMapped = append(r.NotMapped, sprintf(format, a...))
}

func (r *Report) note(format string, a ...any) {
	r.Notes = append(r.Notes, sprintf(format, a...))
}

// Normalized is what a source adapter produces: the manifest, files to
// generate next to the fetched tree, and the report.
type Normalized struct {
	Manifest Manifest
	Files    []File
	Report   Report
	// TreePrefix, when set, is the folder inside the plugin root where the
	// fetched tree is placed (a lone skill at the root of a fetched folder
	// becomes skills/<name>).
	TreePrefix string
}

const (
	defaultVersion = "0.0.0"
	noAssertion    = "NOASSERTION"
	// DefaultCompat is compat.agentsone when the source does not say.
	DefaultCompat = ">=0.1.0"
)

var (
	nonNameChar = regexp.MustCompile(`[^a-z0-9-]+`)
	spdxLike    = regexp.MustCompile(`^[A-Za-z0-9.+-]+(( (AND|OR|WITH) )[A-Za-z0-9.+-]+)*$`)
)

// SanitizeName makes a plugin name from free text: lowercase, characters
// outside a-z, 0-9 and hyphen replaced by a hyphen, runs and edges trimmed,
// starting with a letter, at most 64 characters. Empty input gives "plugin".
func SanitizeName(s string) string {
	s = nonNameChar.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "plugin"
	}
	if s[0] < 'a' || s[0] > 'z' {
		s = "p-" + s
	}
	if len(s) > 64 {
		s = strings.Trim(s[:64], "-")
	}
	return s
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n]))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// readmeLine is the first line of text in the plugin's README, without
// Markdown heading marks, or "" when there is none.
func readmeLine(fsys fs.FS) string {
	for _, name := range []string{"README.md", "readme.md", "README"} {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
			if line != "" && !strings.HasPrefix(line, "<") && !strings.HasPrefix(line, "[!") {
				return oneLine(line)
			}
		}
	}
	return ""
}

func describe(description string, fsys fs.FS, name string) string {
	d := oneLine(description)
	if d == "" && fsys != nil {
		d = readmeLine(fsys)
	}
	if d == "" {
		d = name
	}
	return cut(d, 255)
}

func licenseOrNone(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 128 || !spdxLike.MatchString(s) {
		return noAssertion
	}
	return s
}

func versionOrDefault(v string) (string, error) {
	if v == "" {
		return defaultVersion, nil
	}
	if !IsSemver(v) {
		return "", errNotSemantic(v)
	}
	return v, nil
}
