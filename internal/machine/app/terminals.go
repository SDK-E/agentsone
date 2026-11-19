// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// PtyProcess is a program running on a pseudo terminal.
type PtyProcess interface {
	io.ReadWriter
	Resize(cols, rows int) error
	// Wait blocks until the program ends and returns its exit code.
	Wait() int
	// Close hangs up the terminal and ends its process group.
	Close() error
}

// PtyStarter starts programs on pseudo terminals as a Linux user.
type PtyStarter interface {
	Start(ctx context.Context, spec domain.PtySpec, uid, gid int, home string) (PtyProcess, error)
}

// Terminals holds the machine's terminal sessions. They live in memory: a
// reboot ends every terminal.
type Terminals struct {
	start PtyStarter
	users UserDB
	clock core.Clock
	ids   core.IDs

	mu    sync.Mutex
	terms map[string]*terminal
	order []string
}

type terminal struct {
	s    domain.PtySession
	p    PtyProcess
	done chan struct{} // closed when the program ends
}

// NewTerminals returns an empty terminal table.
func NewTerminals(start PtyStarter, users UserDB, clock core.Clock, ids core.IDs) *Terminals {
	return &Terminals{start: start, users: users, clock: clock, ids: ids, terms: map[string]*terminal{}}
}

// Open starts a terminal running as the caller's Linux user: root for
// humans and root agents, the agent's own user otherwise.
func (t *Terminals) Open(ctx context.Context, a domain.Actor, command []string, cols, rows *int) (domain.PtySession, error) {
	u, err := t.userOf(ctx, a)
	if err != nil {
		return domain.PtySession{}, err
	}
	spec, err := domain.NewPtySpec(command, cols, rows, u.Shell)
	if err != nil {
		return domain.PtySession{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := domain.CanOpenPty(a, t.sessionsLocked()); err != nil {
		return domain.PtySession{}, err
	}
	p, err := t.start.Start(ctx, spec, u.UID, u.GID, u.Home)
	if err != nil {
		return domain.PtySession{}, fmt.Errorf("start terminal for %s: %w", a.Label(), err)
	}
	now := t.clock.Now()
	s := domain.PtySession{
		ID: t.ids.New("pty"), Owner: a.Label(), UID: u.UID, User: u.Name,
		Spec: spec, Created: now, LastActive: now,
	}
	term := &terminal{s: s, p: p, done: make(chan struct{})}
	t.terms[s.ID] = term
	t.order = append(t.order, s.ID)
	go t.wait(term)
	return s, nil
}

func (t *Terminals) userOf(ctx context.Context, a domain.Actor) (domain.MachineUser, error) {
	users, err := t.users.List(ctx)
	if err != nil {
		return domain.MachineUser{}, fmt.Errorf("list users: %w", err)
	}
	uid := a.RunAsUID()
	for _, u := range users {
		if u.UID == uid {
			return u, nil
		}
	}
	return domain.MachineUser{}, fmt.Errorf("%s has no Linux user: %w", a.Label(), core.ErrForbidden)
}

func (t *Terminals) wait(term *terminal) {
	code := term.p.Wait()
	t.mu.Lock()
	term.s.Exited, term.s.ExitCode = true, &code
	t.mu.Unlock()
	close(term.done)
}

func (t *Terminals) sessionsLocked() []domain.PtySession {
	out := make([]domain.PtySession, 0, len(t.order))
	for _, id := range t.order {
		out = append(out, t.terms[id].s)
	}
	return out
}

// List returns the terminals the actor may see, oldest first.
func (t *Terminals) List(_ context.Context, a domain.Actor, cursor string, limit int) ([]domain.PtySession, string, error) {
	t.mu.Lock()
	all := slices.DeleteFunc(t.sessionsLocked(), func(s domain.PtySession) bool { return !s.VisibleTo(a) })
	t.mu.Unlock()
	return domain.Page(all, cursor, limit)
}

// Get returns one terminal the actor may see.
func (t *Terminals) Get(_ context.Context, a domain.Actor, id string) (domain.PtySession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	term, err := t.visibleLocked(a, id)
	if err != nil {
		return domain.PtySession{}, err
	}
	return term.s, nil
}

// visibleLocked hides terminals the actor may not see behind not found, so
// ids of other agents' terminals are not confirmed.
func (t *Terminals) visibleLocked(a domain.Actor, id string) (*terminal, error) {
	term, ok := t.terms[id]
	if !ok || !term.s.VisibleTo(a) {
		return nil, fmt.Errorf("terminal %s: %w", id, core.ErrNotFound)
	}
	return term, nil
}

// Close hangs up a terminal and forgets it.
func (t *Terminals) Close(_ context.Context, a domain.Actor, id string) error {
	t.mu.Lock()
	term, err := t.visibleLocked(a, id)
	if err == nil {
		t.removeLocked(id)
	}
	t.mu.Unlock()
	if err != nil {
		return err
	}
	return term.p.Close()
}

func (t *Terminals) removeLocked(id string) {
	delete(t.terms, id)
	t.order = slices.DeleteFunc(t.order, func(o string) bool { return o == id })
}

// CloseIdle hangs up terminals that had no client for longer than the idle
// timeout.
func (t *Terminals) CloseIdle(context.Context) {
	now := t.clock.Now()
	t.mu.Lock()
	var idle []PtyProcess
	for _, id := range slices.Clone(t.order) {
		if term := t.terms[id]; term.s.Idle(now) {
			idle = append(idle, term.p)
			t.removeLocked(id)
		}
	}
	t.mu.Unlock()
	for _, p := range idle {
		_ = p.Close() // the session is already forgotten; nothing left to report to
	}
}

// CloseAll hangs up every terminal, at daemon stop.
func (t *Terminals) CloseAll() {
	t.mu.Lock()
	var all []PtyProcess
	for _, id := range t.order {
		all = append(all, t.terms[id].p)
	}
	t.terms, t.order = map[string]*terminal{}, nil
	t.mu.Unlock()
	for _, p := range all {
		_ = p.Close() // stopping; nothing left to report to
	}
}

// Attach connects the one client a terminal allows.
func (t *Terminals) Attach(_ context.Context, a domain.Actor, id string) (*PtyConn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	term, err := t.visibleLocked(a, id)
	if err != nil {
		return nil, err
	}
	if term.s.Connected {
		return nil, fmt.Errorf("terminal %s already has a client: %w", id, core.ErrConflict)
	}
	term.s.Connected = true
	return &PtyConn{t: t, id: id, term: term}, nil
}

// PtyConn is the attached client's side of a terminal.
type PtyConn struct {
	t    *Terminals
	id   string
	term *terminal
}

// Done is closed when the program ends.
func (c *PtyConn) Done() <-chan struct{} { return c.term.done }

// ExitCode is the program's exit code once Done is closed, -1 before.
func (c *PtyConn) ExitCode() int {
	c.t.mu.Lock()
	defer c.t.mu.Unlock()
	if c.term.s.ExitCode == nil {
		return -1
	}
	return *c.term.s.ExitCode
}

// Read reads terminal output.
func (c *PtyConn) Read(b []byte) (int, error) { return c.term.p.Read(b) }

// Write sends input to the terminal.
func (c *PtyConn) Write(b []byte) (int, error) {
	c.touch(nil)
	return c.term.p.Write(b)
}

// Resize changes the terminal size.
func (c *PtyConn) Resize(cols, rows int) error {
	c.touch(func(s *domain.PtySession) { s.Spec.Cols, s.Spec.Rows = cols, rows })
	return c.term.p.Resize(cols, rows)
}

// Release lets another client connect; the idle timeout starts now.
func (c *PtyConn) Release() {
	c.touch(func(s *domain.PtySession) { s.Connected = false })
}

func (c *PtyConn) touch(change func(*domain.PtySession)) {
	c.t.mu.Lock()
	defer c.t.mu.Unlock()
	if term, ok := c.t.terms[c.id]; ok {
		term.s.LastActive = c.t.clock.Now()
		if change != nil {
			change(&term.s)
		}
	}
}
