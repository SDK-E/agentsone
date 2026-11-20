// SPDX-License-Identifier: MIT

package daemontest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"sync"

	ideapp "github.com/SDK-E/agentsone/internal/ide/app"
)

// EditorRun is one editor server the daemon started: as whom, where it
// listens and with which arguments.
type EditorRun struct {
	// User, UID and Home are the human's Linux user the server runs as.
	User string
	UID  int
	Home string
	// Socket is the Unix socket it listens on.
	Socket string
	// Args are the server's command-line arguments.
	Args []string
}

// editors stands in for the VSCodium server launcher
// (internal/ide/adapters/process). It opens the socket the daemon asks
// for and answers every request with "editor of <user>", so a test sees
// which human's server the daemon's proxy reached. It never changes
// users: the harness runs without root (TESTING.md B5).
type editors struct {
	mu   sync.Mutex
	runs []EditorRun
	seen []http.Header
	errs []error
}

var _ ideapp.Launcher = (*editors)(nil)

// Launch opens spec's socket and serves on it until Stop.
func (e *editors) Launch(ctx context.Context, spec ideapp.Spec) (ideapp.Process, error) {
	if err := os.MkdirAll(spec.RunDir, 0o700); err != nil {
		return nil, fmt.Errorf("make the editor's folder: %w", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", spec.Socket)
	if err != nil {
		return nil, fmt.Errorf("open the editor's socket: %w", err)
	}
	user := spec.User.Name
	e.mu.Lock()
	e.runs = append(e.runs, EditorRun{
		User: user, UID: spec.User.UID, Home: spec.User.Home, Socket: spec.Socket, Args: slices.Clone(spec.Args),
	})
	e.mu.Unlock()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			e.mu.Lock()
			e.seen = append(e.seen, r.Header.Clone())
			e.mu.Unlock()
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "editor of "+user) // a failed write means the client left
		}),
		ReadHeaderTimeout: Timeout,
	}
	p := &editorProcess{owner: e, srv: srv, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.err = err
		}
	}()
	return p, nil
}

func (e *editors) started() []EditorRun {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.runs)
}

func (e *editors) requests() []http.Header {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.seen)
}

// editorProcess is one fake server.
type editorProcess struct {
	owner *editors
	srv   *http.Server
	done  chan struct{}
	err   error
}

// Ready returns at once: the socket listens before Launch returns.
func (*editorProcess) Ready(context.Context) error { return nil }

func (p *editorProcess) Done() <-chan struct{} { return p.done }

// Err is why the server ended; read it after Done.
func (p *editorProcess) Err() error { return p.err }

// Stop closes the server and waits for it. A failed close is kept for the
// harness, which fails the test with it when the daemon stops.
func (p *editorProcess) Stop() {
	if err := p.srv.Close(); err != nil {
		p.owner.fail(fmt.Errorf("stop the editor: %w", err))
	}
	<-p.done
}

func (e *editors) fail(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errs = append(e.errs, err)
}

// failures are the editor servers that did not stop cleanly.
func (e *editors) failures() []error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.errs)
}

// Editors are the editor servers the daemon started, in order.
func (d *Daemon) Editors() []EditorRun { return d.editors.started() }

// EditorRequests are the headers of every request an editor server got.
func (d *Daemon) EditorRequests() []http.Header { return d.editors.requests() }
