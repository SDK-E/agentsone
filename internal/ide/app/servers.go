// SPDX-License-Identifier: MIT

// Package app runs the humans' editor servers: start one as its human's
// Linux user, watch it until it answers on its socket, and stop it on
// request, when it dies, or when the daemon stops. Servers live in memory:
// a restart stops every editor, and the human starts theirs again.
package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	humandomain "github.com/SDK-E/agentsone/internal/humanuser/domain"
	"github.com/SDK-E/agentsone/internal/ide/domain"
)

// Users makes the human's Linux user (internal/humanuser).
type Users interface {
	Ensure(ctx context.Context, human string) (humandomain.User, error)
}

// Spec is one server to start.
type Spec struct {
	// User is the human's Linux user the server runs as.
	User humandomain.User
	// RunDir holds the socket and the log: 0700, owned by User.
	RunDir  string
	Socket  string
	LogFile string
	// Args are the server's arguments (domain.Args).
	Args []string
}

// Launcher starts editor server processes (adapters/process).
type Launcher interface {
	Launch(ctx context.Context, s Spec) (Process, error)
}

// Process is a started editor server.
type Process interface {
	// Ready blocks until the server answers on its socket, the process
	// ends, or ctx ends.
	Ready(ctx context.Context) error
	// Done is closed when the process has ended; Err then says why.
	Done() <-chan struct{}
	Err() error
	// Stop ends the process and waits for it.
	Stop()
}

// Options are the servers' settings.
type Options struct {
	// RunRoot holds one folder per human for the socket and log.
	RunRoot string
	// ReadyTimeout bounds how long a new server may take to answer.
	ReadyTimeout time.Duration
}

// Servers are the humans' editor servers.
type Servers struct {
	humans core.HumanDirectory
	users  Users
	launch Launcher
	clock  core.Clock
	opt    Options

	mu       sync.Mutex
	byHuman  map[string]*entry
	closed   bool
	onChange func(domain.Server)
	wg       sync.WaitGroup
}

type entry struct {
	srv      domain.Server
	cancel   context.CancelFunc
	stopping bool
	done     chan struct{} // closed when its run goroutine ends
}

// New returns Servers with none running.
func New(humans core.HumanDirectory, users Users, launch Launcher, clock core.Clock, opt Options) *Servers {
	return &Servers{humans: humans, users: users, launch: launch, clock: clock, opt: opt, byHuman: map[string]*entry{}}
}

// OnChange calls f with every state a server takes, for tests and logs.
func (s *Servers) OnChange(f func(domain.Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = f
}

func (s *Servers) changedLocked(e *entry) {
	if s.onChange != nil {
		s.onChange(e.srv)
	}
}

// Start starts the human's server in the background and returns it as
// starting, or as it is when it already starts or runs. Only the human
// themselves may start it.
func (s *Servers) Start(_ context.Context, c core.Caller, human string) (domain.Server, error) {
	if err := domain.CanControl(c, human); err != nil {
		return domain.Server{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.Server{}, fmt.Errorf("the daemon is stopping: %w", core.ErrUnavailable)
	}
	if e := s.byHuman[human]; e != nil && (e.srv.State == domain.Starting || e.srv.State == domain.Running) {
		return e.srv, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &entry{
		srv:    domain.Server{Human: human, State: domain.Starting, StartedAt: s.clock.Now()},
		cancel: cancel, done: make(chan struct{}),
	}
	s.byHuman[human] = e
	s.changedLocked(e)
	s.wg.Add(1)
	go s.run(ctx, e)
	return e.srv, nil
}

func (s *Servers) run(ctx context.Context, e *entry) {
	defer s.wg.Done()
	defer close(e.done)
	defer e.cancel()
	human := e.srv.Human
	user, err := s.users.Ensure(ctx, human)
	if err != nil {
		s.finish(e, fmt.Errorf("make your Linux user: %w", err))
		return
	}
	socket := domain.SocketPath(s.opt.RunRoot, human)
	dir := filepath.Dir(socket)
	p, err := s.launch.Launch(ctx, Spec{
		User: user, RunDir: dir, Socket: socket, LogFile: filepath.Join(dir, "server.log"),
		Args: domain.Args(socket, human, user.Home),
	})
	if err != nil {
		s.finish(e, fmt.Errorf("start the editor: %w", err))
		return
	}
	ready, cancel := context.WithTimeout(ctx, s.opt.ReadyTimeout)
	err = p.Ready(ready)
	cancel()
	if err != nil {
		p.Stop()
		s.finish(e, fmt.Errorf("the editor did not answer: %w", err))
		return
	}
	s.mu.Lock()
	if !e.stopping {
		e.srv.State = domain.Running
		s.changedLocked(e)
	}
	s.mu.Unlock()
	select {
	case <-p.Done():
		err := p.Err()
		if err == nil {
			err = errors.New("it exited")
		}
		s.finish(e, fmt.Errorf("the editor stopped: %w", err))
	case <-ctx.Done():
		p.Stop()
		s.finish(e, nil)
	}
}

// finish records how a server ended: stopped when asked to stop, failed
// with err otherwise.
func (s *Servers) finish(e *entry, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.srv.StartedAt = time.Time{}
	if e.stopping || err == nil {
		e.srv.State, e.srv.Error = domain.Stopped, ""
	} else {
		e.srv.State, e.srv.Error = domain.Failed, err.Error()
	}
	s.changedLocked(e)
}

// Stop stops the human's server and waits for it. Stopping a server that
// does not run succeeds. Only the human themselves may stop it.
func (s *Servers) Stop(ctx context.Context, c core.Caller, human string) error {
	if err := domain.CanControl(c, human); err != nil {
		return err
	}
	s.mu.Lock()
	e := s.byHuman[human]
	if e == nil {
		s.mu.Unlock()
		return nil
	}
	e.stopping = true
	e.cancel()
	s.mu.Unlock()
	select {
	case <-e.done:
	case <-ctx.Done():
		return fmt.Errorf("stop the editor of %s: %w", human, ctx.Err())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e.srv.State, e.srv.Error, e.srv.StartedAt = domain.Stopped, "", time.Time{}
	return nil
}

// Get returns the human's server; a human who never started one has a
// stopped server. An unknown human is ErrNotFound.
func (s *Servers) Get(ctx context.Context, human string) (domain.Server, error) {
	s.mu.Lock()
	e := s.byHuman[human]
	var srv domain.Server
	if e != nil {
		srv = e.srv
	}
	s.mu.Unlock()
	if e != nil {
		return srv, nil
	}
	if _, err := s.humans.HumanAccount(ctx, human); err != nil {
		return domain.Server{}, fmt.Errorf("editor of %s: %w", human, err)
	}
	return domain.Server{Human: human, State: domain.Stopped}, nil
}

// List returns every human's server, in the human directory's order.
func (s *Servers) List(ctx context.Context) ([]domain.Server, error) {
	all, err := s.humans.HumanAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list humans: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Server, 0, len(all))
	for _, h := range all {
		if e := s.byHuman[h.Name]; e != nil {
			out = append(out, e.srv)
			continue
		}
		out = append(out, domain.Server{Human: h.Name, State: domain.Stopped})
	}
	return out, nil
}

// Socket returns the socket of the human's running server, for the proxy.
// Only the human themselves may reach it; a server that does not run is
// ErrUnavailable.
func (s *Servers) Socket(c core.Caller, human string) (string, error) {
	if err := domain.CanControl(c, human); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.byHuman[human]; e == nil || e.srv.State != domain.Running {
		return "", fmt.Errorf("your editor is not running; start it from the IDE panel: %w", core.ErrUnavailable)
	}
	return domain.SocketPath(s.opt.RunRoot, human), nil
}

// Close stops every server and waits for them, and refuses new ones.
func (s *Servers) Close() {
	s.mu.Lock()
	s.closed = true
	for _, e := range s.byHuman {
		e.stopping = true
		e.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}
