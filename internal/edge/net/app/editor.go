// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

// ErrInvalidReason is returned for a reason that is not one line of text.
var ErrInvalidReason = errors.New("reason must be one line of text")

const maxReasonLength = 200

// Editor is the Go API the control page and the command line use to read and change the allow
// list. It edits the stored list; a running network picks the change up through its watcher.
type Editor struct {
	store AllowListStore
}

// NewEditor makes an editor over store.
func NewEditor(store AllowListStore) *Editor { return &Editor{store: store} }

// List returns the stored entries sorted by pattern. Before the first start this is the default
// list.
func (e *Editor) List(ctx context.Context) ([]domain.Entry, error) {
	l, err := e.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return l.Entries(), nil
}

// Add adds pattern with the reason why to the stored list and returns the new entry.
func (e *Editor) Add(ctx context.Context, pattern, why string) (domain.Entry, error) {
	p, err := domain.ParsePattern(strings.TrimSpace(pattern))
	if err != nil {
		return domain.Entry{}, err
	}
	why = strings.TrimSpace(why)
	if strings.ContainsAny(why, "\r\n") || len(why) > maxReasonLength {
		return domain.Entry{}, fmt.Errorf("%w (at most %d characters)", ErrInvalidReason, maxReasonLength)
	}
	entry := domain.Entry{Pattern: p, Why: why}
	if _, err := e.store.Update(ctx, func(l domain.AllowList) (domain.AllowList, error) { return l.With(entry) }); err != nil {
		return domain.Entry{}, err
	}
	return entry, nil
}

// Remove removes pattern from the stored list.
func (e *Editor) Remove(ctx context.Context, pattern string) error {
	p, err := domain.ParsePattern(strings.TrimSpace(pattern))
	if err != nil {
		return err
	}
	_, err = e.store.Update(ctx, func(l domain.AllowList) (domain.AllowList, error) { return l.Without(p) })
	return err
}
