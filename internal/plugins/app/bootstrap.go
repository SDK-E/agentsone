// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// BootstrapOptions tune the first-start install of the bundled plugins.
type BootstrapOptions struct {
	// DefaultThemePlugin names the bundled plugin whose theme is the
	// built-in "Agents One Theme". It is installed but not enabled, and its
	// theme goes to EnsureBuiltinTheme instead of the theme hook.
	DefaultThemePlugin string
	EnsureBuiltinTheme func(ctx context.Context, raw []byte) error
}

// Bootstrap installs every plugin found under dir (one folder each, with an
// agentsone.plugin.json) that is not installed yet, as the system. It is
// safe to run on every start: installed name and version pairs are skipped.
// It returns the names it installed. A missing dir is not an error.
func (p *Plugins) Bootstrap(ctx context.Context, dir string, opt BootstrapOptions) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("bootstrap plugins: read %s: %w", dir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	sys := core.WithCaller(ctx, core.Caller{Kind: core.CallerSystem, Name: "plugins", UID: -1, IsRoot: true})
	var installed []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(filepath.Join(path, nativeManifest)) // #nosec G304 -- the image's own folder
		if err != nil {
			continue
		}
		m, err := domain.ParseManifest(raw)
		if err != nil {
			return installed, fmt.Errorf("bootstrap plugins: %s: %w", e.Name(), err)
		}
		if _, err := p.findVersion(sys, m.Name, m.Version); err == nil {
			continue
		} else if !errors.Is(err, domain.ErrNotFound) {
			return installed, pluginError("bootstrap plugins", err)
		}
		isTheme := opt.DefaultThemePlugin != "" && m.Name == opt.DefaultThemePlugin
		pl, err := p.Install(sys, InstallInput{Source: string(domain.SourceAgentsOne), Ref: path, Enable: !isTheme})
		if err != nil {
			return installed, fmt.Errorf("bootstrap plugins: install %s: %w", m.Name, err)
		}
		installed = append(installed, pl.Name)
		if isTheme && opt.EnsureBuiltinTheme != nil {
			for _, ref := range pl.Manifest.Contents.Themes {
				doc, err := readInTree(pl.Dir, ref.Path)
				if err != nil {
					return installed, pluginError("bootstrap plugins", err)
				}
				if err := opt.EnsureBuiltinTheme(sys, doc); err != nil {
					return installed, fmt.Errorf("bootstrap plugins: built-in theme: %w", err)
				}
			}
		}
	}
	return installed, nil
}
