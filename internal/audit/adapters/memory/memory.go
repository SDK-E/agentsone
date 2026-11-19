// SPDX-License-Identifier: MIT

// Package memory is an in-memory audit repository for tests and for running
// the app layer without a database.
package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/SDK-E/agentsone/internal/audit/domain"
)

// Repository keeps the chain in a slice.
type Repository struct {
	mu      sync.Mutex
	records []domain.Record
}

var _ domain.Repository = (*Repository)(nil)

// New returns an empty repository.
func New() *Repository { return &Repository{} }

// Append adds r when it follows the last record.
func (m *Repository) Append(_ context.Context, r domain.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	last, hasLast := m.last()
	if !r.Follows(last, hasLast) {
		return fmt.Errorf("append seq %d: %w", r.Seq, domain.ErrOutOfOrder)
	}
	m.records = append(m.records, r)
	return nil
}

// Last returns the newest record.
func (m *Repository) Last(context.Context) (domain.Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	last, ok := m.last()
	return last, ok, nil
}

// List returns up to limit records after afterSeq.
func (m *Repository) List(_ context.Context, afterSeq int64, limit int) ([]domain.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Record
	for _, r := range m.records {
		if len(out) == limit {
			break
		}
		if r.Seq > afterSeq {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *Repository) last() (domain.Record, bool) {
	if len(m.records) == 0 {
		return domain.Record{}, false
	}
	return m.records[len(m.records)-1], true
}
