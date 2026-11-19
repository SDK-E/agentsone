// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/memory/domain"
)

// Service holds the memory use cases.
type Service struct {
	repo  domain.Repository
	ports *core.Ports
	clock core.Clock
	ids   core.IDs
	// mu makes "find the record with this key, then save" one step, so two
	// writes with one key cannot both insert.
	mu sync.Mutex
}

// New returns a service over repo. ports is read when used, because the
// owners fill their ports after the modules are built.
func New(repo domain.Repository, ports *core.Ports, clock core.Clock, ids core.IDs) *Service {
	return &Service{repo: repo, ports: ports, clock: clock, ids: ids}
}

// WriteInput is a memory write as the caller sent it.
type WriteInput struct {
	Scope string
	Key   string
	Title string
	Body  string
	Tags  []string
}

// Write stores a record owned by the caller. A record the caller wrote with
// the same key in the same scope is replaced.
func (s *Service) Write(ctx context.Context, in WriteInput) (domain.Record, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return domain.Record{}, err
	}
	d, err := domain.NewDraft(domain.DraftInput(in))
	if err != nil {
		return domain.Record{}, translate(err)
	}
	if err := domain.CanWrite(p, d.Scope); err != nil {
		return domain.Record{}, translate(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.recordFor(ctx, p, d)
	if err != nil {
		return domain.Record{}, err
	}
	if err := s.repo.Save(ctx, rec); err != nil {
		return domain.Record{}, fmt.Errorf("write memory: %w", err)
	}
	return rec, nil
}

func (s *Service) recordFor(ctx context.Context, p domain.Principal, d domain.Draft) (domain.Record, error) {
	now := s.clock.Now()
	if d.Key != "" {
		existing, found, err := s.repo.FindByKey(ctx, p.Name, d.Scope, d.Key)
		if err != nil {
			return domain.Record{}, fmt.Errorf("write memory: %w", err)
		}
		if found {
			return existing.Replace(d, p.Team, now), nil
		}
	}
	return domain.NewRecord(s.ids.New("mem"), d, p.Name, p.Team, now), nil
}

// Get returns a record the caller may read. A record the caller may not read
// is reported as missing, so its existence does not leak.
func (s *Service) Get(ctx context.Context, id string) (domain.Record, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return domain.Record{}, err
	}
	return s.readable(ctx, p, id)
}

func (s *Service) readable(ctx context.Context, p domain.Principal, id string) (domain.Record, error) {
	rec, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.Record{}, translate(err)
	}
	if !domain.CanRead(p, rec) {
		return domain.Record{}, translate(fmt.Errorf("memory %s: %w", id, domain.ErrNotFound))
	}
	return rec, nil
}

// Delete removes a record. Its owner may, and so may a root agent.
func (s *Service) Delete(ctx context.Context, id string) error {
	p, err := s.principal(ctx)
	if err != nil {
		return err
	}
	rec, err := s.readable(ctx, p, id)
	if err != nil {
		return err
	}
	if err := domain.CanDelete(p, rec); err != nil {
		return translate(err)
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return translate(err)
	}
	return nil
}

// translate gives a domain error the core sentinel the API layer maps to a
// status. The domain error stays in the chain.
func translate(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotAllowed):
		return fmt.Errorf("%w: %w", core.ErrForbidden, err)
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("%w: %w", core.ErrNotFound, err)
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, domain.ErrTooLarge):
		return fmt.Errorf("%w: %w", core.ErrInvalid, err)
	default:
		return err
	}
}
