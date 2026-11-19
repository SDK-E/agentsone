// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// Deps are the ports Start needs. Filter carries the edge address and the tuning knobs; its
// ports, allow list and frozen fields are filled in by Start.
type Deps struct {
	Store       AllowListStore
	Watcher     Watcher
	FreezeStore FreezeStore
	Clock       Clock
	Resolver    Resolver
	Observer    AuditObserver
	Log         *slog.Logger
	Filter      FilterConfig
}

// Running is a started filter with its allow list kept current.
type Running struct {
	filter *Filter
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// Start is the "start the filter" use case. It writes the default allow list on first start,
// loads the list and the freeze switch, and watches the list for changes. An unreadable list
// stops the start: the wall never comes up with a list the human did not write. An unreadable
// freeze switch starts frozen.
func Start(ctx context.Context, deps Deps) (*Running, error) {
	if !deps.Filter.Edge.IsValid() || deps.Filter.Edge.IsUnspecified() {
		return nil, errors.New("start filter: the edge inner address is not set")
	}
	if err := deps.Store.Init(ctx); err != nil {
		return nil, fmt.Errorf("write default allow list: %w", err)
	}
	list, err := deps.Store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load allow list: %w", err)
	}
	frozen, err := deps.FreezeStore.Load(ctx)
	if err != nil {
		deps.Log.Error("freeze switch unreadable, starting with all egress frozen", "error", err)
		frozen = true
	}
	cfg := deps.Filter
	cfg.Clock, cfg.Resolver, cfg.Observer, cfg.FreezeStore = deps.Clock, deps.Resolver, deps.Observer, deps.FreezeStore
	cfg.AllowList, cfg.Frozen = list, frozen
	filter := NewFilter(cfg)

	watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r := &Running{filter: filter, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		if err := deps.Watcher.Watch(watchCtx, func() { reload(watchCtx, deps, filter) }); err != nil {
			deps.Log.Error("allow list watcher stopped", "error", err)
		}
	}()
	return r, nil
}

// reload is the "reload allow list" use case. A list that cannot be read leaves the last good
// list in force, so a half-written file never opens or closes the wall by accident.
func reload(ctx context.Context, deps Deps, filter *Filter) {
	list, err := deps.Store.Load(ctx)
	if err != nil {
		deps.Log.Warn("allow list not reloaded, keeping the last good list", "error", err)
		return
	}
	filter.SetAllowList(list)
	deps.Log.Info("allow list reloaded", "entries", len(list.Entries()))
}

// Filter returns the running filter.
func (r *Running) Filter() *Filter { return r.filter }

// Stop stops watching the allow list. It is safe to call more than once.
func (r *Running) Stop() {
	r.once.Do(func() {
		r.cancel()
		<-r.done
	})
}
