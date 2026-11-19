// SPDX-License-Identifier: MIT

// Package memory holds in-memory identity repositories for tests.
package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/auth/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

// Store implements every identity repository in memory.
type Store struct {
	mu       sync.Mutex
	sessions map[string]domain.Session
	tokens   map[string]domain.Token
	humans   map[string]domain.Human
	licence  *domain.Licence
	fetch    *domain.EngineFetch
}

var (
	_ domain.Sessions = (*Store)(nil)
	_ domain.Tokens   = (*Store)(nil)
	_ domain.Humans   = (*Store)(nil)
	_ domain.Setup    = (*Store)(nil)
)

// New returns an empty store.
func New() *Store {
	return &Store{sessions: map[string]domain.Session{}, tokens: map[string]domain.Token{}, humans: map[string]domain.Human{}}
}

// SaveSession stores s.
func (m *Store) SaveSession(_ context.Context, s domain.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
	return nil
}

// Session returns the session with id.
func (m *Store) Session(_ context.Context, id string) (domain.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return domain.Session{}, fmt.Errorf("session: %w", core.ErrNotFound)
	}
	return s, nil
}

// DeleteSession removes a session; an unknown id is not an error.
func (m *Store) DeleteSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	return nil
}

// DeleteHumanSessions removes every session of human.
func (m *Store) DeleteHumanSessions(_ context.Context, human string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		if s.Human == human {
			delete(m.sessions, id)
		}
	}
	return nil
}

// SaveToken stores t.
func (m *Store) SaveToken(_ context.Context, t domain.Token) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t.Scopes = slices.Clone(t.Scopes)
	m.tokens[t.ID] = t
	return nil
}

// Token returns the token with id.
func (m *Store) Token(_ context.Context, id string) (domain.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok {
		return domain.Token{}, fmt.Errorf("token: %w", core.ErrNotFound)
	}
	t.Scopes = slices.Clone(t.Scopes)
	return t, nil
}

// RevokeToken marks the token revoked at at.
func (m *Store) RevokeToken(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok {
		return fmt.Errorf("token: %w", core.ErrNotFound)
	}
	t.Revoke(at)
	m.tokens[id] = t
	return nil
}

// AddHuman stores a new human.
func (m *Store) AddHuman(_ context.Context, h domain.Human) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.humans[h.Name]; ok {
		return fmt.Errorf("human %s: %w", h.Name, core.ErrConflict)
	}
	m.humans[h.Name] = h
	return nil
}

// Human returns the human called name.
func (m *Store) Human(_ context.Context, name string) (domain.Human, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.humans[name]
	if !ok {
		return domain.Human{}, fmt.Errorf("human %s: %w", name, core.ErrNotFound)
	}
	return h, nil
}

// HumanByUID returns the human with uid.
func (m *Store) HumanByUID(_ context.Context, uid int) (domain.Human, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.humans {
		if h.UID == uid {
			return h, nil
		}
	}
	return domain.Human{}, fmt.Errorf("human uid %d: %w", uid, core.ErrNotFound)
}

// ListHumans returns every human by name.
func (m *Store) ListHumans(context.Context) ([]domain.Human, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.Human, 0, len(m.humans))
	for _, h := range m.humans {
		out = append(out, h)
	}
	slices.SortFunc(out, func(a, b domain.Human) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// UpdateHuman replaces a stored human.
func (m *Store) UpdateHuman(_ context.Context, h domain.Human) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.humans[h.Name]; !ok {
		return fmt.Errorf("human %s: %w", h.Name, core.ErrNotFound)
	}
	m.humans[h.Name] = h
	return nil
}

// DeleteHuman removes a human.
func (m *Store) DeleteHuman(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.humans[name]; !ok {
		return fmt.Errorf("human %s: %w", name, core.ErrNotFound)
	}
	delete(m.humans, name)
	return nil
}

// Licence returns the recorded acceptance.
func (m *Store) Licence(context.Context) (domain.Licence, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.licence == nil {
		return domain.Licence{}, false, nil
	}
	return *m.licence, true, nil
}

// SaveLicence records an acceptance.
func (m *Store) SaveLicence(_ context.Context, l domain.Licence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.licence = &l
	return nil
}

// EngineFetch returns the stored fetch status.
func (m *Store) EngineFetch(context.Context) (domain.EngineFetch, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fetch == nil {
		return domain.EngineFetch{}, false, nil
	}
	return *m.fetch, true, nil
}

// SaveEngineFetch stores the fetch status.
func (m *Store) SaveEngineFetch(_ context.Context, f domain.EngineFetch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fetch = &f
	return nil
}
