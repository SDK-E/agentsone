// SPDX-License-Identifier: MIT

package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// Plugins is an in-memory domain.PluginRepository.
type Plugins struct {
	mu      sync.Mutex
	plugins map[string]domain.Plugin
}

var _ domain.PluginRepository = (*Plugins)(nil)

// NewPlugins returns an empty repository.
func NewPlugins() *Plugins { return &Plugins{plugins: map[string]domain.Plugin{}} }

// Insert adds p.
func (r *Plugins) Insert(_ context.Context, p domain.Plugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.plugins[p.ID]; taken {
		return fmt.Errorf("plugin id %s: %w", p.ID, domain.ErrAlreadyInstalled)
	}
	for _, other := range r.plugins {
		if other.Name == p.Name && other.Version == p.Version {
			return fmt.Errorf("plugin %s %s: %w", p.Name, p.Version, domain.ErrAlreadyInstalled)
		}
	}
	r.plugins[p.ID] = clonePlugin(p)
	return nil
}

// Get returns the plugin with id.
func (r *Plugins) Get(_ context.Context, id string) (domain.Plugin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plugins[id]
	if !ok {
		return domain.Plugin{}, fmt.Errorf("plugin %s: %w", id, domain.ErrNotFound)
	}
	return clonePlugin(p), nil
}

// ByName returns every installed version of name, newest install first.
func (r *Plugins) ByName(_ context.Context, name string) ([]domain.Plugin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Plugin
	for _, p := range r.plugins {
		if p.Name == name {
			out = append(out, clonePlugin(p))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].InstalledAt.Equal(out[j].InstalledAt) {
			return out[i].InstalledAt.After(out[j].InstalledAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// List returns up to limit plugins after id after that match f, by id.
func (r *Plugins) List(_ context.Context, f domain.PluginFilter, after string, limit int) ([]domain.Plugin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Plugin
	for _, p := range r.plugins {
		if p.ID <= after || !matches(p, f) {
			continue
		}
		out = append(out, clonePlugin(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func matches(p domain.Plugin, f domain.PluginFilter) bool {
	if f.Kind != "" && !slices.Contains(p.Kinds, f.Kind) {
		return false
	}
	return f.Enabled == nil || *f.Enabled == p.Enabled
}

// SetEnabled flips the enabled flag and health of id.
func (r *Plugins) SetEnabled(_ context.Context, id string, enabled bool, health domain.Health) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.plugins[id]
	if !ok {
		return fmt.Errorf("plugin %s: %w", id, domain.ErrNotFound)
	}
	p.Enabled, p.Health = enabled, health
	r.plugins[id] = p
	return nil
}

// Delete removes the plugin with id.
func (r *Plugins) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plugins[id]; !ok {
		return fmt.Errorf("plugin %s: %w", id, domain.ErrNotFound)
	}
	delete(r.plugins, id)
	return nil
}

func clonePlugin(p domain.Plugin) domain.Plugin {
	p.Kinds = slices.Clone(p.Kinds)
	return p
}
