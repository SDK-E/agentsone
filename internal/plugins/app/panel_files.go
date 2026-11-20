// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// ReadPanelFile reads one file of an enabled panel plugin for the
// interface's panel host. The path must lie inside the folder of a declared
// panel entry, and the file is read through the plugin's own root, so
// neither dot segments nor symlinks can leave it. The content is never
// logged.
func (p *Plugins) ReadPanelFile(ctx context.Context, id, rel string) (domain.PanelFile, error) {
	pl, err := p.d.Repo.Get(ctx, id)
	if err != nil {
		return domain.PanelFile{}, pluginError("read panel file", err)
	}
	if !pl.Enabled {
		return domain.PanelFile{}, fmt.Errorf("read panel file: %s is turned off: %w", pl.Name, core.ErrConflict)
	}
	clean, err := domain.PanelFilePath(pl.Manifest.Contents.Panels, rel)
	if err != nil {
		return domain.PanelFile{}, err
	}
	root, err := os.OpenRoot(pl.Dir)
	if err != nil {
		return domain.PanelFile{}, fmt.Errorf("read panel file: open %s: %w", pl.Name, err)
	}
	defer func() { _ = root.Close() }()
	// The root keeps links inside the plugin, but a link inside the panel
	// folder could still reach the plugin's other files, so no part of the
	// path may be a link.
	for i := range strings.Count(clean, "/") + 1 {
		part := strings.Join(strings.Split(clean, "/")[:i+1], "/")
		info, err := root.Lstat(part)
		if err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return domain.PanelFile{}, fmt.Errorf("read panel file: %s is a link: %w", part, domain.ErrUnsafePath)
		}
	}
	f, err := root.Open(clean)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return domain.PanelFile{}, fmt.Errorf("read panel file: %s has no %s: %w", pl.Name, clean, domain.ErrNotFound)
		}
		// Includes a symlink that points outside the plugin.
		return domain.PanelFile{}, fmt.Errorf("read panel file %s: %w: %w", clean, domain.ErrUnsafePath, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return domain.PanelFile{}, fmt.Errorf("read panel file %s: %w", clean, err)
	}
	if !info.Mode().IsRegular() {
		return domain.PanelFile{}, fmt.Errorf("read panel file: %s is not a file: %w", clean, domain.ErrNotFound)
	}
	if info.Size() > domain.MaxPanelFile {
		return domain.PanelFile{}, fmt.Errorf("read panel file: %s is over 4 MiB: %w", clean, domain.ErrTooLarge)
	}
	data, err := io.ReadAll(io.LimitReader(f, domain.MaxPanelFile+1))
	if err != nil {
		return domain.PanelFile{}, fmt.Errorf("read panel file %s: %w", clean, err)
	}
	if len(data) > domain.MaxPanelFile {
		return domain.PanelFile{}, fmt.Errorf("read panel file: %s is over 4 MiB: %w", clean, domain.ErrTooLarge)
	}
	return domain.PanelFile{Path: clean, MediaType: domain.PanelMediaType(clean), Data: data}, nil
}
