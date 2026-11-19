// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// errDuplicateTemplate is a template id two templates share.
var errDuplicateTemplate = errors.New("duplicate agent template")

// TemplateCatalog returns the agent templates of the enabled plugins, as
// the daemon's core.TemplateCatalog. It reads the installed files on every
// call, so it always matches what is enabled.
func (p *Plugins) TemplateCatalog() core.TemplateCatalog { return templateCatalog{p} }

type templateCatalog struct{ p *Plugins }

// List returns every enabled template, sorted by id. A template file that
// cannot be read is an error, not a gap: the plugin's tree is broken.
func (c templateCatalog) List(ctx context.Context) ([]core.AgentTemplate, error) {
	enabled := true
	plugins, err := c.p.d.Repo.List(ctx, domain.PluginFilter{Enabled: &enabled}, "", pluginMax)
	if err != nil {
		return nil, fmt.Errorf("list agent templates: %w", err)
	}
	var all []core.AgentTemplate
	for _, pl := range plugins {
		tpls, err := pluginTemplates(pl)
		if err != nil {
			return nil, fmt.Errorf("list agent templates: %w", err)
		}
		all = append(all, tpls...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all, nil
}

// Get returns one enabled template, or core.ErrNotFound.
func (c templateCatalog) Get(ctx context.Context, id string) (core.AgentTemplate, error) {
	all, err := c.List(ctx)
	if err != nil {
		return core.AgentTemplate{}, err
	}
	for _, t := range all {
		if t.ID == id {
			return t, nil
		}
	}
	return core.AgentTemplate{}, fmt.Errorf("agent template %s: %w", id, core.ErrNotFound)
}

// pluginTemplates reads and parses pl's agent templates. A file that is
// missing is a plain error; a bad document is ErrInvalidManifest.
func pluginTemplates(pl domain.Plugin) ([]core.AgentTemplate, error) {
	out := make([]core.AgentTemplate, 0, len(pl.Manifest.Contents.AgentTemplates))
	for _, ref := range pl.Manifest.Contents.AgentTemplates {
		raw, err := readInTree(pl.Dir, ref.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: read %s: %w", pl.Name, ref.Path, err)
		}
		t, err := domain.ParseAgentTemplate(pl.Name, raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", pl.Name, ref.Path, err)
		}
		out = append(out, t)
	}
	return out, nil
}

// checkTemplates refuses to enable pl when one of its templates is bad or
// has the id of another of its templates or of an enabled plugin's.
func (p *Plugins) checkTemplates(ctx context.Context, pl domain.Plugin) error {
	mine, err := pluginTemplates(pl)
	if err != nil || len(mine) == 0 {
		return err
	}
	others, err := p.TemplateCatalog().List(ctx)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(others)+len(mine))
	for _, t := range others {
		seen[t.ID] = true
	}
	for _, t := range mine {
		if seen[t.ID] {
			return fmt.Errorf("%s: %w %s: %w", pl.Name, errDuplicateTemplate, t.ID, domain.ErrInUse)
		}
		seen[t.ID] = true
	}
	return nil
}
