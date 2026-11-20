// SPDX-License-Identifier: MIT

package domain

import (
	"fmt"
	"io/fs"
	"mime"
	"path"
	"strings"
)

// MaxPanelFile is the largest panel file the interface may read, in bytes.
const MaxPanelFile = 4 << 20

// PanelFile is one file of a panel plugin, read for the interface's panel host.
type PanelFile struct {
	Path      string // normalised, relative to the plugin root
	MediaType string
	Data      []byte
}

// PanelFilePath normalises rel and checks that it lies inside the folder of
// one of the panels' entries. An entry at the plugin root opens only itself
// and its icon. A path that is not clean and relative is ErrUnsafePath; a
// path outside every panel folder is ErrNotFound, so it says nothing about
// the plugin's other files.
func PanelFilePath(panels []Panel, rel string) (string, error) {
	clean := path.Clean(strings.TrimPrefix(rel, "./"))
	if rel == "" || strings.Contains(rel, `\`) || !fs.ValidPath(clean) || clean == "." {
		return "", fmt.Errorf("panel file %q: %w", rel, ErrUnsafePath)
	}
	for _, seg := range strings.Split(clean, "/") {
		if strings.HasPrefix(seg, ".") {
			return "", fmt.Errorf("panel file %q: %w", rel, ErrUnsafePath)
		}
	}
	for _, p := range panels {
		entry := path.Clean(strings.TrimPrefix(p.Entry, "./"))
		icon := path.Clean(strings.TrimPrefix(p.Icon, "./"))
		if clean == entry || clean == icon {
			return clean, nil
		}
		if dir := path.Dir(entry); dir != "." && strings.HasPrefix(clean, dir+"/") {
			return clean, nil
		}
	}
	return "", fmt.Errorf("panel file %q is not inside a panel folder: %w", rel, ErrNotFound)
}

// PanelMediaType is the media type of a panel file from its name, without
// parameters.
func PanelMediaType(p string) string {
	t := mime.TypeByExtension(path.Ext(p))
	if t == "" {
		return "application/octet-stream"
	}
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	return t
}
