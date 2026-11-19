// SPDX-License-Identifier: MIT

// Package memory holds in-memory plugin repositories for tests and for
// running without a database.
package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/SDK-E/agentsone/internal/plugins/domain"
)

// Themes is an in-memory domain.ThemeRepository.
type Themes struct {
	mu         sync.Mutex
	themes     map[string]domain.InstalledTheme
	selections map[string]domain.Selection
}

var _ domain.ThemeRepository = (*Themes)(nil)

// NewThemes returns an empty repository.
func NewThemes() *Themes {
	return &Themes{themes: map[string]domain.InstalledTheme{}, selections: map[string]domain.Selection{}}
}

// Insert adds t.
func (r *Themes) Insert(_ context.Context, t domain.InstalledTheme) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, other := range r.themes {
		if other.Name == t.Name {
			return fmt.Errorf("theme %q: %w", t.Name, domain.ErrAlreadyInstalled)
		}
	}
	r.themes[t.ID] = clone(t)
	return nil
}

// Update replaces the version and document of t.
func (r *Themes) Update(_ context.Context, t domain.InstalledTheme) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.themes[t.ID]
	if !ok {
		return fmt.Errorf("theme %s: %w", t.ID, domain.ErrNotFound)
	}
	cur.Version, cur.Document = t.Version, append([]byte(nil), t.Document...)
	r.themes[t.ID] = cur
	return nil
}

// Get returns the theme with id.
func (r *Themes) Get(_ context.Context, id string) (domain.InstalledTheme, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.themes[id]
	if !ok {
		return domain.InstalledTheme{}, fmt.Errorf("theme %s: %w", id, domain.ErrNotFound)
	}
	return clone(t), nil
}

// FindByName returns the theme called name.
func (r *Themes) FindByName(_ context.Context, name string) (domain.InstalledTheme, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.themes {
		if t.Name == name {
			return clone(t), true, nil
		}
	}
	return domain.InstalledTheme{}, false, nil
}

// Builtin returns the built-in theme.
func (r *Themes) Builtin(context.Context) (domain.InstalledTheme, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.themes {
		if t.Builtin {
			return clone(t), true, nil
		}
	}
	return domain.InstalledTheme{}, false, nil
}

// ByPlugin returns the themes of one plugin.
func (r *Themes) ByPlugin(_ context.Context, pluginID string) ([]domain.InstalledTheme, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.InstalledTheme
	for _, t := range r.sorted() {
		if t.PluginID == pluginID {
			out = append(out, clone(t))
		}
	}
	return out, nil
}

// List returns up to limit themes after afterID.
func (r *Themes) List(_ context.Context, afterID string, limit int) ([]domain.InstalledTheme, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.InstalledTheme
	for _, t := range r.sorted() {
		if t.ID > afterID && len(out) < limit {
			out = append(out, clone(t))
		}
	}
	return out, nil
}

// Delete removes the theme unless somebody selected it.
func (r *Themes) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.themes[id]; !ok {
		return fmt.Errorf("theme %s: %w", id, domain.ErrNotFound)
	}
	for _, s := range r.selections {
		if s.ThemeID == id {
			return fmt.Errorf("theme %s is selected: %w", id, domain.ErrInUse)
		}
	}
	delete(r.themes, id)
	return nil
}

// Selection returns the human's choice.
func (r *Themes) Selection(_ context.Context, human string) (domain.Selection, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.selections[human]
	return s, ok, nil
}

// SetSelection stores the human's choice.
func (r *Themes) SetSelection(_ context.Context, human string, s domain.Selection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.themes[s.ThemeID]; !ok {
		return fmt.Errorf("theme %s: %w", s.ThemeID, domain.ErrNotFound)
	}
	r.selections[human] = s
	return nil
}

// ReassignSelections moves selections from one theme to another.
func (r *Themes) ReassignSelections(_ context.Context, from, to string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.themes[to]; !ok {
		return fmt.Errorf("theme %s: %w", to, domain.ErrNotFound)
	}
	for human, s := range r.selections {
		if s.ThemeID == from {
			s.ThemeID = to
			r.selections[human] = s
		}
	}
	return nil
}

func (r *Themes) sorted() []domain.InstalledTheme {
	out := make([]domain.InstalledTheme, 0, len(r.themes))
	for _, t := range r.themes {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func clone(t domain.InstalledTheme) domain.InstalledTheme {
	t.Document = append([]byte(nil), t.Document...)
	return t
}
