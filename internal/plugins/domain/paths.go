// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// pluginPathPattern is common.schema.json#/$defs/PluginPath.
var pluginPathPattern = regexp.MustCompile(`^(\./|\$\{PLUGIN_ROOT\}/)([A-Za-z0-9_@+-][A-Za-z0-9_.@+-]*/)*[A-Za-z0-9_@+-][A-Za-z0-9_.@+-]*/?$`)

const (
	claudeRoot = "${CLAUDE_PLUGIN_ROOT}"
	pluginRoot = "${PLUGIN_ROOT}"
	// PluginRoot is the variable that stands for the plugin's folder.
	PluginRoot = pluginRoot
	maxPathLen = 512
)

// ValidatePluginPath checks a path of a normalised manifest: it starts with
// ./ or ${PLUGIN_ROOT}/, has no segment that starts with a dot, and so
// cannot leave the plugin root.
func ValidatePluginPath(p string) error {
	if len(p) > maxPathLen || !pluginPathPattern.MatchString(p) {
		return fmt.Errorf("path %q: %w", p, ErrUnsafePath)
	}
	return nil
}

// RelPath turns a valid plugin path into the slash-separated path below the
// plugin root, for example "./skills/a/" gives "skills/a".
func RelPath(p string) (string, error) {
	if err := ValidatePluginPath(p); err != nil {
		return "", err
	}
	p = strings.TrimPrefix(strings.TrimPrefix(p, "./"), pluginRoot+"/")
	return strings.TrimSuffix(p, "/"), nil
}

// RewritePluginRoot maps Claude Code's variable to ours, everywhere in s.
func RewritePluginRoot(s string) string {
	return strings.ReplaceAll(s, claudeRoot, pluginRoot)
}

// NormalizePath turns a path from a foreign manifest into a valid plugin path
// starting with "./". It accepts "x", "./x", "${CLAUDE_PLUGIN_ROOT}/x" and
// "${PLUGIN_ROOT}/x". Absolute paths, parent segments, dot segments,
// backslashes and control characters are refused.
func NormalizePath(raw string) (string, error) {
	p := RewritePluginRoot(raw)
	p = strings.TrimPrefix(p, pluginRoot+"/")
	if strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || strings.Contains(p, "${") {
		return "", fmt.Errorf("path %q: %w", raw, ErrUnsafePath)
	}
	clean := path.Clean(strings.TrimPrefix(p, "./"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path %q: %w", raw, ErrUnsafePath)
	}
	for _, seg := range strings.Split(clean, "/") {
		if strings.HasPrefix(seg, ".") {
			return "", fmt.Errorf("path %q: %w", raw, ErrUnsafePath)
		}
	}
	out := "./" + clean
	if err := ValidatePluginPath(out); err != nil {
		return "", fmt.Errorf("path %q: %w", raw, ErrUnsafePath)
	}
	return out, nil
}
