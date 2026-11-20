// SPDX-License-Identifier: MIT

package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	humandomain "github.com/SDK-E/agentsone/internal/humanuser/domain"
	"github.com/SDK-E/agentsone/internal/ide/app"
	"github.com/SDK-E/agentsone/internal/ide/domain"
)

var (
	ada  = core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}
	bob  = core.Caller{Kind: core.CallerHuman, Name: "bob", UID: -1}
	root = core.Caller{Kind: core.CallerAgent, Name: "founder", UID: 0, IsRoot: true}
)

type users struct{ err error }

func (u users) Ensure(_ context.Context, human string) (humandomain.User, error) {
	if u.err != nil {
		return humandomain.User{}, u.err
	}
	uid := map[string]int{"ada": 1000, "bob": 1001}[human]
	return humandomain.UserFor(human, uid)
}

type proc struct {
	ready   chan error
	done    chan struct{}
	err     error
	once    sync.Once
	stopped atomic.Bool
}

func newProc() *proc { return &proc{ready: make(chan error, 1), done: make(chan struct{})} }

func (p *proc) Ready(ctx context.Context) error {
	select {
	case err := <-p.ready:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *proc) Done() <-chan struct{} { return p.done }
func (p *proc) Err() error            { return p.err }
func (p *proc) Stop()                 { p.stopped.Store(true); p.once.Do(func() { close(p.done) }) }
func (p *proc) exit(err error)        { p.err = err; p.once.Do(func() { close(p.done) }) }

type launcher struct {
	mu    sync.Mutex
	specs []app.Spec
	procs []*proc
	err   error
	// wait leaves new processes not ready until the test says so.
	wait bool
	// launched gets every new process, when set.
	launched chan *proc
}

func (l *launcher) Launch(_ context.Context, s app.Spec) (app.Process, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	p := newProc()
	if !l.wait {
		p.ready <- nil
	}
	l.specs = append(l.specs, s)
	l.procs = append(l.procs, p)
	if l.launched != nil {
		l.launched <- p
	}
	return p, nil
}

func (l *launcher) last(t *testing.T) *proc {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.procs) == 0 {
		t.Fatal("nothing launched")
	}
	return l.procs[len(l.procs)-1]
}

type env struct {
	servers *app.Servers
	launch  *launcher
	changes chan domain.Server
}

func newEnv(t *testing.T, u users, l *launcher) env {
	t.Helper()
	humans := coretest.NewHumans()
	humans.Add("ada", 1000)
	humans.Add("bob", 1001)
	s := app.New(humans, u, l, coretest.NewClock(coretest.Epoch), app.Options{RunRoot: "/run/agentsone/ide", ReadyTimeout: time.Minute})
	changes := make(chan domain.Server, 64)
	s.OnChange(func(srv domain.Server) { changes <- srv })
	t.Cleanup(s.Close)
	return env{servers: s, launch: l, changes: changes}
}

// waitFor reads changes until human's server reaches state.
func (e env) waitFor(t *testing.T, human string, state domain.State) domain.Server {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-e.changes:
			if s.Human == human && s.State == state {
				return s
			}
		case <-deadline:
			t.Fatalf("%s never reached %s", human, state)
		}
	}
}

func TestStartRunsTheHumansServerAsTheirUserOnTheirSocket(t *testing.T) {
	e := newEnv(t, users{}, &launcher{})
	ctx := context.Background()
	s, err := e.servers.Start(ctx, ada, "ada")
	if err != nil || s.State != domain.Starting || !s.StartedAt.Equal(coretest.Epoch) {
		t.Fatalf("start: %+v %v", s, err)
	}
	running := e.waitFor(t, "ada", domain.Running)
	if u, ok := running.URL(); !ok || u != "/ide/ada/" {
		t.Fatalf("url %q %v", u, ok)
	}
	spec := e.launch.specs[0]
	if spec.User.Name != "h_ada" || spec.Socket != "/run/agentsone/ide/ada/server.sock" ||
		spec.RunDir != "/run/agentsone/ide/ada" || spec.LogFile != "/run/agentsone/ide/ada/server.log" {
		t.Fatalf("spec %+v", spec)
	}
	if !strings.Contains(strings.Join(spec.Args, " "), "--server-base-path /ide/ada") {
		t.Fatalf("args %q", spec.Args)
	}
	sock, err := e.servers.Socket(ada, "ada")
	if err != nil || sock != spec.Socket {
		t.Fatalf("socket %q %v", sock, err)
	}
}

func TestOnlyTheOwnerStartsStopsAndReachesTheServer(t *testing.T) {
	e := newEnv(t, users{}, &launcher{})
	ctx := context.Background()
	for _, c := range []core.Caller{bob, root} {
		if _, err := e.servers.Start(ctx, c, "ada"); !errors.Is(err, core.ErrForbidden) {
			t.Errorf("start by %s: %v", c.Name, err)
		}
		if err := e.servers.Stop(ctx, c, "ada"); !errors.Is(err, core.ErrForbidden) {
			t.Errorf("stop by %s: %v", c.Name, err)
		}
	}
	if _, err := e.servers.Start(ctx, ada, "ada"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(t, "ada", domain.Running)
	if _, err := e.servers.Socket(bob, "ada"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("bob reached ada's editor: %v", err)
	}
	if _, err := e.servers.Socket(core.Anonymous, "ada"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("anonymous reached ada's editor: %v", err)
	}
}

func TestStartTwiceKeepsOneServer(t *testing.T) {
	e := newEnv(t, users{}, &launcher{})
	ctx := context.Background()
	if _, err := e.servers.Start(ctx, ada, "ada"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(t, "ada", domain.Running)
	s, err := e.servers.Start(ctx, ada, "ada")
	if err != nil || s.State != domain.Running {
		t.Fatalf("second start: %+v %v", s, err)
	}
	if len(e.launch.specs) != 1 {
		t.Fatalf("launched %d servers", len(e.launch.specs))
	}
}

func TestFailuresShowOnTheServerWithTheReason(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		users  users
		launch *launcher
		want   string
	}{
		"no linux user": {users{err: errors.New("useradd failed")}, &launcher{}, "useradd failed"},
		"launch fails":  {users{}, &launcher{err: errors.New("no such file")}, "no such file"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, c.users, c.launch)
			if _, err := e.servers.Start(ctx, ada, "ada"); err != nil {
				t.Fatal(err)
			}
			s := e.waitFor(t, "ada", domain.Failed)
			if !strings.Contains(s.Error, c.want) || !s.StartedAt.IsZero() {
				t.Fatalf("failed server %+v", s)
			}
			if _, ok := s.URL(); ok {
				t.Fatal("a failed server has a url")
			}
		})
	}
}

func TestAServerThatNeverAnswersIsStoppedAndFails(t *testing.T) {
	l := &launcher{wait: true, launched: make(chan *proc, 1)}
	e := newEnv(t, users{}, l)
	if _, err := e.servers.Start(context.Background(), ada, "ada"); err != nil {
		t.Fatal(err)
	}
	p := <-l.launched
	p.ready <- errors.New("socket never opened")
	s := e.waitFor(t, "ada", domain.Failed)
	if !strings.Contains(s.Error, "socket never opened") || !p.stopped.Load() {
		t.Fatalf("server %+v stopped %v", s, p.stopped.Load())
	}
}

func TestAServerThatExitsFailsAndCanStartAgain(t *testing.T) {
	e := newEnv(t, users{}, &launcher{})
	ctx := context.Background()
	if _, err := e.servers.Start(ctx, ada, "ada"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(t, "ada", domain.Running)
	e.launch.last(t).exit(errors.New("exit status 1"))
	s := e.waitFor(t, "ada", domain.Failed)
	if !strings.Contains(s.Error, "exit status 1") {
		t.Fatalf("server %+v", s)
	}
	if _, err := e.servers.Socket(ada, "ada"); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("socket of a failed server: %v", err)
	}
	if _, err := e.servers.Start(ctx, ada, "ada"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(t, "ada", domain.Running)
	if len(e.launch.specs) != 2 {
		t.Fatalf("launched %d", len(e.launch.specs))
	}
}

func TestStopEndsTheServer(t *testing.T) {
	e := newEnv(t, users{}, &launcher{})
	ctx := context.Background()
	if err := e.servers.Stop(ctx, ada, "ada"); err != nil {
		t.Fatalf("stop with nothing running: %v", err)
	}
	if _, err := e.servers.Start(ctx, ada, "ada"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(t, "ada", domain.Running)
	if err := e.servers.Stop(ctx, ada, "ada"); err != nil {
		t.Fatal(err)
	}
	if !e.launch.last(t).stopped.Load() {
		t.Fatal("process not stopped")
	}
	s, err := e.servers.Get(ctx, "ada")
	if err != nil || s.State != domain.Stopped || !s.StartedAt.IsZero() || s.Error != "" {
		t.Fatalf("after stop: %+v %v", s, err)
	}
	if _, err := e.servers.Socket(ada, "ada"); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("socket after stop: %v", err)
	}
}

func TestStopWhileStartingCancelsTheStart(t *testing.T) {
	e := newEnv(t, users{}, &launcher{wait: true})
	ctx := context.Background()
	if _, err := e.servers.Start(ctx, ada, "ada"); err != nil {
		t.Fatal(err)
	}
	if err := e.servers.Stop(ctx, ada, "ada"); err != nil {
		t.Fatal(err)
	}
	s, _ := e.servers.Get(ctx, "ada")
	if s.State != domain.Stopped {
		t.Fatalf("after stop while starting: %+v", s)
	}
}

func TestGetAndListShowEveryHuman(t *testing.T) {
	e := newEnv(t, users{}, &launcher{})
	ctx := context.Background()
	if _, err := e.servers.Get(ctx, "eve"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown human: %v", err)
	}
	if _, err := e.servers.Start(ctx, bob, "bob"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(t, "bob", domain.Running)
	all, err := e.servers.List(ctx)
	if err != nil || len(all) != 2 || all[0].Human != "ada" || all[0].State != domain.Stopped ||
		all[1].Human != "bob" || all[1].State != domain.Running {
		t.Fatalf("list %+v %v", all, err)
	}
}

func TestCloseStopsEveryServerAndRefusesNewOnes(t *testing.T) {
	e := newEnv(t, users{}, &launcher{})
	ctx := context.Background()
	if _, err := e.servers.Start(ctx, ada, "ada"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(t, "ada", domain.Running)
	e.servers.Close()
	if !e.launch.last(t).stopped.Load() {
		t.Fatal("close left the server running")
	}
	if _, err := e.servers.Start(ctx, ada, "ada"); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("start after close: %v", err)
	}
}
