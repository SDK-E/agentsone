// SPDX-License-Identifier: MIT

// Package socket serves one Unix socket per agent and runs the runner
// protocol on every connection to it. It also wraps a Launcher so a runner
// never starts before its socket listens.
package socket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/rpc"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// HelloTimeout is how long a runner has to send hello (rule H1).
const HelloTimeout = 10 * time.Second

// Server owns the agents' runner sockets.
type Server struct {
	// Dir holds the sockets, normally /run/agentsone/runners.
	Dir string
	// Supervisor builds a handler for each connection.
	Supervisor *app.Supervisor
	// MaxLineBytes is the line limit; zero means the protocol default.
	MaxLineBytes int
	// HelloDeadline returns a channel closed when hello is late. Nil means
	// HelloTimeout on the real clock.
	HelloDeadline func() <-chan struct{}
	// Chown sets the socket owner. Nil means os.Chown when running as root,
	// and nothing otherwise (development and tests).
	Chown func(path string, uid int) error

	mu        sync.Mutex
	listeners map[string]net.Listener
	wg        sync.WaitGroup
}

// Path is the socket path of agent name.
func (s *Server) Path(name string) string { return filepath.Join(s.Dir, name+".sock") }

// Listen opens agent name's socket, mode 0600 and owned by uid, unless it
// is already open. ctx bounds the life of the accept loop.
func (s *Server) Listen(ctx context.Context, name string, uid int) error {
	if err := domain.ValidateName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listeners == nil {
		s.listeners = map[string]net.Listener{}
	}
	if _, ok := s.listeners[name]; ok {
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil { // #nosec G301 -- runners must reach their socket
		return fmt.Errorf("socket dir: %w", err)
	}
	path := s.Path(name)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	if err := s.chown(path, uid); err != nil {
		_ = ln.Close()
		return fmt.Errorf("chown socket: %w", err)
	}
	s.listeners[name] = ln
	s.wg.Add(1)
	go s.accept(ctx, name, ln)
	return nil
}

func (s *Server) chown(path string, uid int) error {
	if s.Chown != nil {
		return s.Chown(path, uid)
	}
	if os.Geteuid() != 0 {
		return nil
	}
	return os.Chown(path, uid, 0)
}

func (s *Server) accept(ctx context.Context, name string, ln net.Listener) {
	defer s.wg.Done()
	for {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		h := s.Supervisor.Connect(name)
		c := rpc.Serve(ctx, nc, h, rpc.Options{
			Versions: domain.DaemonVersions, MaxLineBytes: s.MaxLineBytes, HelloDeadline: s.deadline(),
		})
		h.Attach(c)
	}
}

func (s *Server) deadline() <-chan struct{} {
	if s.HelloDeadline != nil {
		return s.HelloDeadline()
	}
	ch := make(chan struct{})
	time.AfterFunc(HelloTimeout, func() { close(ch) })
	return ch
}

// Close stops listening on agent name's socket and removes it.
func (s *Server) Close(name string) error {
	s.mu.Lock()
	ln, ok := s.listeners[name]
	delete(s.listeners, name)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	err := ln.Close()
	if rmErr := os.Remove(s.Path(name)); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		err = errors.Join(err, rmErr)
	}
	return err
}

// Shutdown closes every socket and waits for the accept loops to end.
func (s *Server) Shutdown() error {
	s.mu.Lock()
	names := make([]string, 0, len(s.listeners))
	for n := range s.listeners {
		names = append(names, n)
	}
	s.mu.Unlock()
	var errs []error
	for _, n := range names {
		errs = append(errs, s.Close(n))
	}
	s.wg.Wait()
	return errors.Join(errs...)
}

// Launcher wraps another launcher: the agent's socket listens before its
// runner starts, and is removed with its unit.
type Launcher struct {
	Inner  app.Launcher
	Server *Server
	// Ctx bounds the accept loops; normally the module's run context.
	Ctx context.Context //nolint:containedctx // accept loops outlive the Start call

	mu   sync.Mutex
	uids map[string]int
}

var _ app.Launcher = (*Launcher)(nil)

// Install remembers the runner's uid and installs the unit.
func (l *Launcher) Install(ctx context.Context, spec domain.UnitSpec) error {
	uid, err := lookupUID(spec.Account.User)
	if err != nil {
		return err
	}
	l.mu.Lock()
	if l.uids == nil {
		l.uids = map[string]int{}
	}
	l.uids[spec.Agent] = uid
	l.mu.Unlock()
	return l.Inner.Install(ctx, spec)
}

func lookupUID(name string) (int, error) {
	if name == "root" {
		return 0, nil
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, fmt.Errorf("look up user %s: %w", name, err)
	}
	return strconv.Atoi(u.Uid)
}

func (l *Launcher) listen(name string) error {
	l.mu.Lock()
	uid := l.uids[name]
	l.mu.Unlock()
	return l.Server.Listen(l.Ctx, name, uid)
}

// Start opens the socket, then starts the runner.
func (l *Launcher) Start(ctx context.Context, name string) error {
	if err := l.listen(name); err != nil {
		return err
	}
	return l.Inner.Start(ctx, name)
}

// Restart opens the socket if needed, then restarts the runner.
func (l *Launcher) Restart(ctx context.Context, name string) error {
	if err := l.listen(name); err != nil {
		return err
	}
	return l.Inner.Restart(ctx, name)
}

// Stop stops the runner; the socket stays for the next start.
func (l *Launcher) Stop(ctx context.Context, name string) error { return l.Inner.Stop(ctx, name) }

// Mode is the inner launcher's limit mode.
func (l *Launcher) Mode(name string) domain.LimitMode { return l.Inner.Mode(name) }

// Remove removes the unit and the socket.
func (l *Launcher) Remove(ctx context.Context, name string) error {
	return errors.Join(l.Inner.Remove(ctx, name), l.Server.Close(name))
}
