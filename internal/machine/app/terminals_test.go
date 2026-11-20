// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// fakePty is a terminal whose program ends when exit is sent.
type fakePty struct {
	mu      sync.Mutex
	spec    domain.PtySpec
	uid     int
	cell    core.ProcessCell
	size    [2]int
	closed  bool
	exit    chan int
	written []byte
}

func (p *fakePty) Read([]byte) (int, error) { return 0, errors.New("not used") }
func (p *fakePty) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written = append(p.written, b...)
	return len(b), nil
}
func (p *fakePty) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.size = [2]int{cols, rows}
	return nil
}
func (p *fakePty) Wait() int { return <-p.exit }
func (p *fakePty) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.exit)
	}
	return nil
}

type fakeStarter struct {
	mu      sync.Mutex
	started []*fakePty
	fail    error
}

func (s *fakeStarter) Start(_ context.Context, spec domain.PtySpec, uid, _ int, _ string, cell core.ProcessCell) (app.PtyProcess, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &fakePty{spec: spec, uid: uid, cell: cell, exit: make(chan int, 1)}
	s.started = append(s.started, p)
	return p, nil
}

func (s *fakeStarter) last() *fakePty {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started[len(s.started)-1]
}

func newTerms(t *testing.T) (*app.Terminals, *fakeStarter, *coretest.Clock) {
	t.Helper()
	terms, st, clock, _ := newTermsWith(t, coretest.Cells{}, coretest.NewAudit())
	return terms, st, clock
}

// newTermsWith builds the table on cells and audit, and returns the log
// the terminals write to.
func newTermsWith(t *testing.T, cells core.Cells, audit core.AuditLog) (*app.Terminals, *fakeStarter, *coretest.Clock, core.AuditLog) {
	t.Helper()
	st := &fakeStarter{}
	clock := coretest.NewClock(coretest.Epoch)
	users := fakeUsers{
		{Name: "root", UID: 0, GID: 0, Home: "/root", Shell: "/bin/bash"},
		{Name: "writer", UID: 200001, GID: 200001, Home: "/home/agents/writer", Shell: "/usr/sbin/nologin"},
	}
	terms := app.NewTerminals(app.TerminalDeps{
		Start: st, Users: users, Cells: cells, Audit: audit, Clock: clock, IDs: coretest.NewIDs(),
	})
	return terms, st, clock, audit
}

func TestOpenPlacesEachTerminalInItsOpenersCell(t *testing.T) {
	cases := []struct {
		name  string
		actor domain.Actor
		want  string
	}{
		{"custom agent", custom, "/sys/fs/cgroup/agents/writer"},
		{"root agent", rootAg, "/sys/fs/cgroup/agents/founder"},
		{"human", human, "/sys/fs/cgroup/humans/alice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			terms, st, _ := newTerms(t)
			if _, err := terms.Open(ctx(), c.actor, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if got := st.last().cell.Cgroup; got != c.want {
				t.Fatalf("terminal of %s placed in %q, want %q", c.actor.Label(), got, c.want)
			}
		})
	}
}

func TestOpenRefusesATerminalItCannotPlace(t *testing.T) {
	down := errors.New("cgroup tree read only")
	terms, st, _, _ := newTermsWith(t, coretest.Cells{Err: down}, coretest.NewAudit())
	for _, a := range []domain.Actor{custom, human} {
		if _, err := terms.Open(ctx(), a, nil, nil, nil); !errors.Is(err, down) {
			t.Fatalf("%s: %v, want %v", a.Label(), err, down)
		}
	}
	if len(st.started) != 0 {
		t.Fatal("a terminal was started without its cell")
	}
}

func TestOpenRefusesTheDaemon(t *testing.T) {
	terms, st, _ := newTerms(t)
	system := domain.Actor{Kind: domain.ActorSystem, Name: "host", UID: 0}
	if _, err := terms.Open(ctx(), system, nil, nil, nil); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("%v, want forbidden", err)
	}
	if len(st.started) != 0 {
		t.Fatal("the daemon got a terminal")
	}
}

func TestOpenAndCloseAreAuditedWithTheHumansName(t *testing.T) {
	audit := coretest.NewAudit()
	terms, _, _, _ := newTermsWith(t, coretest.Cells{}, audit)
	s, err := terms.Open(ctx(), human, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := terms.Close(ctx(), human, s.ID); err != nil {
		t.Fatal(err)
	}
	got := audit.Entries()
	if len(got) != 2 {
		t.Fatalf("audit %+v, want open and close", got)
	}
	open, closed := got[0], got[1]
	if open.Actor != "human:alice" || open.Action != app.ActionTerminalOpen || open.Target != s.ID ||
		string(open.Detail) != `{"cgroup":"/sys/fs/cgroup/humans/alice","uid":0,"user":"root"}` {
		t.Errorf("open entry %+v detail %s", open, open.Detail)
	}
	if closed.Actor != "human:alice" || closed.Action != app.ActionTerminalClose || closed.Target != s.ID ||
		string(closed.Detail) != `{"owner":"human:alice","reason":"closed","user":"root"}` {
		t.Errorf("close entry %+v detail %s", closed, closed.Detail)
	}
}

func TestIdleCloseIsAuditedAsTheDaemonsWithTheOwner(t *testing.T) {
	audit := coretest.NewAudit()
	terms, _, clock, _ := newTermsWith(t, coretest.Cells{}, audit)
	s, err := terms.Open(ctx(), human, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(domain.PtyIdleTimeout + time.Second)
	terms.CloseIdle(ctx())
	got := audit.Entries()
	if len(got) != 2 {
		t.Fatalf("audit %+v, want open and close", got)
	}
	if c := got[1]; c.Actor != "system:agentsoned" || c.Action != app.ActionTerminalClose || c.Target != s.ID ||
		string(c.Detail) != `{"owner":"human:alice","reason":"idle","user":"root"}` {
		t.Errorf("close entry %+v detail %s", c, c.Detail)
	}
}

func TestOpenClosesTheTerminalWhenTheAuditFails(t *testing.T) {
	terms, st, _, _ := newTermsWith(t, coretest.Cells{}, failingAudit{})
	if _, err := terms.Open(ctx(), human, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "audit disk full") {
		t.Fatalf("%v, want the audit failure", err)
	}
	if p := st.last(); !p.closed {
		t.Fatal("an unaudited terminal was left running")
	}
	if all, _, _ := terms.List(ctx(), human, "", 10); len(all) != 0 {
		t.Fatalf("an unaudited terminal is listed: %+v", all)
	}
}

func waitExited(t *testing.T, terms *app.Terminals, a domain.Actor, id string) domain.PtySession {
	t.Helper()
	for {
		s, err := terms.Get(ctx(), a, id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Exited {
			return s
		}
	}
}

// A custom agent whose caller carries uid 0 or no uid must never get the
// root account: only humans and root agents run as root.
func TestOpenRefusesAnAgentThatIsNotRootButClaimsUID0(t *testing.T) {
	for _, uid := range []int{0, -1} {
		terms, st, _ := newTerms(t)
		a := domain.Actor{Kind: domain.ActorAgent, Name: "writer", UID: uid}
		_, err := terms.Open(ctx(), a, nil, nil, nil)
		if !errors.Is(err, core.ErrForbidden) {
			t.Fatalf("uid %d: %v, want forbidden", uid, err)
		}
		if len(st.started) != 0 {
			t.Fatalf("uid %d: a terminal was started", uid)
		}
	}
}

func TestOpenRunsAsTheCaller(t *testing.T) {
	terms, st, _ := newTerms(t)
	s, err := terms.Open(ctx(), custom, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := st.last()
	if p.uid != 200001 || p.spec.Command[0] != "/bin/bash" || s.User != "writer" || s.Owner != "agent:writer" {
		t.Fatalf("session %+v process uid %d %v", s, p.uid, p.spec.Command)
	}
	h, err := terms.Open(ctx(), human, []string{"htop"}, nil, nil)
	if err != nil || st.last().uid != 0 || h.User != "root" {
		t.Fatalf("human terminal %+v %v", h, err)
	}
}

func TestTerminalVisibilityAndClose(t *testing.T) {
	terms, st, _ := newTerms(t)
	h, _ := terms.Open(ctx(), human, nil, nil, nil)
	c, _ := terms.Open(ctx(), custom, nil, nil, nil)
	mine, _, err := terms.List(ctx(), custom, "", 10)
	if err != nil || len(mine) != 1 || mine[0].ID != c.ID {
		t.Fatalf("custom sees %+v %v", mine, err)
	}
	all, _, _ := terms.List(ctx(), human, "", 10)
	if len(all) != 2 {
		t.Fatalf("human sees %d", len(all))
	}
	if _, err := terms.Get(ctx(), custom, h.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("custom gets human's terminal: %v", err)
	}
	if err := terms.Close(ctx(), custom, h.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("custom closes human's terminal: %v", err)
	}
	if err := terms.Close(ctx(), human, c.ID); err != nil {
		t.Fatal(err)
	}
	if !st.started[1].closed {
		t.Fatal("process not hung up")
	}
	if _, err := terms.Get(ctx(), human, c.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("closed terminal still listed: %v", err)
	}
}

func TestExitIsRecorded(t *testing.T) {
	terms, st, _ := newTerms(t)
	s, _ := terms.Open(ctx(), human, nil, nil, nil)
	st.last().exit <- 3
	got := waitExited(t, terms, human, s.ID)
	if got.ExitCode == nil || *got.ExitCode != 3 {
		t.Fatalf("exit %+v", got)
	}
}

func TestTerminalCaps(t *testing.T) {
	terms, _, _ := newTerms(t)
	for range domain.MaxPtyPerActor {
		if _, err := terms.Open(ctx(), custom, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := terms.Open(ctx(), custom, nil, nil, nil); !errors.Is(err, domain.ErrTooMany) {
		t.Fatalf("over cap: %v", err)
	}
	bad := 3
	if _, err := terms.Open(ctx(), human, nil, &bad, nil); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad size: %v", err)
	}
}

func TestStartFailureIsWrapped(t *testing.T) {
	terms, st, _ := newTerms(t)
	st.fail = errors.New("no ptmx")
	if _, err := terms.Open(ctx(), human, nil, nil, nil); err == nil {
		t.Fatal("open without a pty")
	}
	unknown := domain.Actor{Kind: domain.ActorAgent, Name: "ghost", UID: 200099}
	st.fail = nil
	if _, err := terms.Open(ctx(), unknown, nil, nil, nil); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("agent without a Linux user: %v", err)
	}
}

func TestAttachOneClientAtATime(t *testing.T) {
	terms, st, clock := newTerms(t)
	s, _ := terms.Open(ctx(), custom, nil, nil, nil)
	conn, err := terms.Attach(ctx(), custom, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := terms.Attach(ctx(), human, s.ID); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("second client: %v", err)
	}
	if err := conn.Resize(100, 40); err != nil || st.last().size != [2]int{100, 40} {
		t.Fatalf("resize %v %v", st.last().size, err)
	}
	if _, err := conn.Write([]byte("ls\n")); err != nil || string(st.last().written) != "ls\n" {
		t.Fatalf("input %q %v", st.last().written, err)
	}
	clock.Advance(time.Hour)
	terms.CloseIdle(ctx())
	if _, err := terms.Get(ctx(), custom, s.ID); err != nil {
		t.Fatalf("connected terminal closed as idle: %v", err)
	}
	conn.Release()
	clock.Advance(domain.PtyIdleTimeout + time.Second)
	terms.CloseIdle(ctx())
	if _, err := terms.Get(ctx(), custom, s.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("idle terminal kept: %v", err)
	}
	if _, err := terms.Attach(ctx(), custom, "pty_missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("attach missing: %v", err)
	}
}

func TestAttachedClientLearnsTheExitCode(t *testing.T) {
	terms, st, _ := newTerms(t)
	s, _ := terms.Open(ctx(), human, nil, nil, nil)
	conn, err := terms.Attach(ctx(), human, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	st.last().exit <- 7
	<-conn.Done()
	if code := conn.ExitCode(); code != 7 {
		t.Fatalf("exit code %d", code)
	}
}

func TestCloseAll(t *testing.T) {
	terms, st, _ := newTerms(t)
	_, _ = terms.Open(ctx(), human, nil, nil, nil)
	_, _ = terms.Open(ctx(), custom, nil, nil, nil)
	terms.CloseAll(ctx())
	for _, p := range st.started {
		if !p.closed {
			t.Fatal("terminal left running at stop")
		}
	}
}
