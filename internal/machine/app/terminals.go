// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// PtyStarter starts programs on pseudo terminals as a Linux user, placed
// in cell before the program's first instruction.
type PtyStarter interface {
	Start(ctx context.Context, spec domain.PtySpec, uid, gid int, home string, cell core.ProcessCell) (PtyProcess, error)
}

// Audit actions of terminals.
const (
	ActionTerminalOpen  = "machine.terminal.open"
	ActionTerminalClose = "machine.terminal.close"
)

// TerminalDeps are the ports the terminal table needs.
type TerminalDeps struct {
	Start PtyStarter
	Users UserDB
	// Cells place each terminal in its opener's cgroup: an agent's under
	// the agent's limits, a human's in /humans/<name>.
	Cells core.Cells
	Audit core.AuditLog
	Log   *slog.Logger
	Clock core.Clock
	IDs   core.IDs
}

// Terminals holds the machine's terminal sessions. They live in memory: a
// reboot ends every terminal. Every open and close is audited.
type Terminals struct {
	d TerminalDeps

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
func NewTerminals(d TerminalDeps) *Terminals {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Terminals{d: d, terms: map[string]*terminal{}}
}

// Open starts a terminal running as the caller's Linux user: root for
// humans and root agents, the agent's own user otherwise. It runs in the
// caller's cell, never where the daemon runs: an agent's terminal in the
// agent's cgroup under the agent's limits, a human's in /humans/<name>.
// Without writable cgroups an agent's terminal gets its runner's process
// limits and a human's has no cell. A terminal that cannot be placed or
// audited does not open.
func (t *Terminals) Open(ctx context.Context, a domain.Actor, command []string, cols, rows *int) (domain.PtySession, error) {
	u, err := t.userOf(ctx, a)
	if err != nil {
		return domain.PtySession{}, err
	}
	spec, err := domain.NewPtySpec(command, cols, rows, u.Shell)
	if err != nil {
		return domain.PtySession{}, err
	}
	cell, err := t.cellOf(ctx, a)
	if err != nil {
		return domain.PtySession{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := domain.CanOpenPty(a, t.sessionsLocked()); err != nil {
		return domain.PtySession{}, err
	}
	p, err := t.d.Start.Start(ctx, spec, u.UID, u.GID, u.Home, cell)
	if err != nil {
		return domain.PtySession{}, fmt.Errorf("start terminal for %s: %w", a.Label(), err)
	}
	now := t.d.Clock.Now()
	s := domain.PtySession{
		ID: t.d.IDs.New("pty"), Owner: a.Label(), UID: u.UID, User: u.Name,
		Spec: spec, Created: now, LastActive: now,
	}
	detail := map[string]any{"user": u.Name, "uid": u.UID, "cgroup": cell.Cgroup}
	if err := t.audit(ctx, a.Label(), ActionTerminalOpen, s.ID, detail); err != nil {
		return domain.PtySession{}, errors.Join(err, p.Close())
	}
	term := &terminal{s: s, p: p, done: make(chan struct{})}
	t.terms[s.ID] = term
	t.order = append(t.order, s.ID)
	go t.wait(term)
	return s, nil
}

// cellOf asks where the actor's terminal runs. The daemon and the host
// open no terminals.
func (t *Terminals) cellOf(ctx context.Context, a domain.Actor) (core.ProcessCell, error) {
	var cell core.ProcessCell
	var err error
	switch a.Kind {
	case domain.ActorAgent:
		cell, err = t.d.Cells.AgentCell(ctx, a.Name)
	case domain.ActorHuman:
		cell, err = t.d.Cells.HumanCell(ctx, a.Name)
	default:
		return core.ProcessCell{}, fmt.Errorf("%s opens no terminals, only humans and agents do: %w", a.Label(), core.ErrForbidden)
	}
	if err != nil {
		return core.ProcessCell{}, fmt.Errorf("place terminal for %s: %w", a.Label(), err)
	}
	return cell, nil
}

// audit appends one terminal fact.
func (t *Terminals) audit(ctx context.Context, actor, action, id string, detail map[string]any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("audit %s detail: %w", action, err)
	}
	e := core.AuditEntry{Actor: actor, Action: action, Target: id, Detail: raw}
	if err := t.d.Audit.Append(ctx, e); err != nil {
		return fmt.Errorf("audit %s %s: %w", action, id, err)
	}
	return nil
}

// auditClose records that a terminal closed: who closed it, whose it was
// and why.
func (t *Terminals) auditClose(ctx context.Context, actor string, s domain.PtySession, reason string) error {
	detail := map[string]any{"owner": s.Owner, "user": s.User, "reason": reason}
	return t.audit(ctx, actor, ActionTerminalClose, s.ID, detail)
}

func (t *Terminals) userOf(ctx context.Context, a domain.Actor) (domain.MachineUser, error) {
	if !a.Privileged() && a.UID <= 0 {
		// Root is for humans and root agents. An agent that is not root and
		// has no real uid must not fall through to the root account.
		return domain.MachineUser{}, fmt.Errorf("%s has no Linux user: %w", a.Label(), core.ErrForbidden)
	}
	users, err := t.d.Users.List(ctx)
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
func (t *Terminals) Close(ctx context.Context, a domain.Actor, id string) error {
	t.mu.Lock()
	term, err := t.visibleLocked(a, id)
	var c closing
	if err == nil {
		c = closing{p: term.p, s: term.s}
		t.removeLocked(id)
	}
	t.mu.Unlock()
	if err != nil {
		return err
	}
	cerr := c.p.Close()
	return errors.Join(cerr, t.auditClose(ctx, a.Label(), c.s, "closed"))
}

// closing is a forgotten terminal to hang up, with its session as it was
// when it was forgotten (its wait goroutine may still write the original).
type closing struct {
	p PtyProcess
	s domain.PtySession
}

func (t *Terminals) removeLocked(id string) {
	delete(t.terms, id)
	t.order = slices.DeleteFunc(t.order, func(o string) bool { return o == id })
}

// CloseIdle hangs up terminals that had no client for longer than the idle
// timeout.
func (t *Terminals) CloseIdle(ctx context.Context) {
	now := t.d.Clock.Now()
	t.mu.Lock()
	var idle []closing
	for _, id := range slices.Clone(t.order) {
		if term := t.terms[id]; term.s.Idle(now) {
			idle = append(idle, closing{p: term.p, s: term.s})
			t.removeLocked(id)
		}
	}
	t.mu.Unlock()
	t.closeAll(ctx, idle, "idle")
}

// CloseAll hangs up every terminal, at daemon stop.
func (t *Terminals) CloseAll(ctx context.Context) {
	t.mu.Lock()
	var all []closing
	for _, id := range t.order {
		all = append(all, closing{p: t.terms[id].p, s: t.terms[id].s})
	}
	t.terms, t.order = map[string]*terminal{}, nil
	t.mu.Unlock()
	t.closeAll(ctx, all, "daemon stop")
}

// closeAll hangs up terminals already forgotten, as the daemon's own act.
// Nobody waits for the answer, so a failure is logged.
func (t *Terminals) closeAll(ctx context.Context, terms []closing, reason string) {
	for _, c := range terms {
		if err := c.p.Close(); err != nil {
			t.d.Log.Error("machine: terminal not hung up", "terminal", c.s.ID, "owner", c.s.Owner, "err", err)
		}
		if err := t.auditClose(ctx, daemon.Label(), c.s, reason); err != nil {
			t.d.Log.Error("machine: terminal close not audited", "terminal", c.s.ID, "owner", c.s.Owner, "err", err)
		}
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
		term.s.LastActive = c.t.d.Clock.Now()
		if change != nil {
			change(&term.s)
		}
	}
}
