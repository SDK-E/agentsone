// SPDX-License-Identifier: MIT

package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// EnvSocket names the environment variable that holds the daemon's socket path.
const EnvSocket = "AGENTSONE_RUNNER_SOCKET"

// Options configure a runner connection.
type Options struct {
	// Runner is the name and version sent in hello.
	Runner Info
	// Capabilities are sent in hello.
	Capabilities Capabilities
	// Versions are the protocol versions the runner speaks, highest first.
	// The default is just ProtocolVersion.
	Versions []string
	// Socket is the daemon socket path. The default is $AGENTSONE_RUNNER_SOCKET.
	Socket string
	// Clock is the time source. The default is SystemClock.
	Clock Clock
}

func (o Options) withDefaults() Options {
	if len(o.Versions) == 0 {
		o.Versions = []string{ProtocolVersion}
	}
	if o.Clock == nil {
		o.Clock = SystemClock{}
	}
	return o
}

type (
	helloParams struct {
		ProtocolVersions []string         `json:"protocolVersions"`
		Runner           runnerInfo       `json:"runner"`
		Capabilities     capabilitiesWire `json:"capabilities"`
	}
	runnerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	capabilitiesWire struct {
		Streaming  bool `json:"streaming"`
		Cancel     bool `json:"cancel"`
		Compaction bool `json:"compaction"`
		Resume     bool `json:"resume"`
	}
)

// Run connects to the daemon socket and serves the protocol until shutdown
// (nil), the daemon closing the socket (ErrDaemonClosed) or ctx ending.
func Run(ctx context.Context, opts Options, h Handler) error {
	path := opts.Socket
	if path == "" {
		path = os.Getenv(EnvSocket)
	}
	if path == "" {
		return fmt.Errorf("sdk: %s is not set", EnvSocket)
	}
	var d net.Dialer
	nc, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("sdk: connect to daemon: %w", err)
	}
	return RunConn(ctx, nc, opts, h)
}

// Main runs a runner as a process: it serves until shutdown and returns the
// exit status. It is 0 after a shutdown request or a signal, and 1 when the
// daemon went away or the handshake failed (rule X2), so systemd restarts it.
func Main(opts Options, h Handler) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	err := Run(ctx, opts, h)
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return 0
	default:
		fmt.Fprintln(os.Stderr, "runner:", err)
		return 1
	}
}

// RunConn serves the protocol over an established connection.
func RunConn(ctx context.Context, rwc io.ReadWriteCloser, opts Options, h Handler) error {
	opts = opts.withDefaults()
	c := newConn(rwc, DefaultMaxLineBytes)
	defer c.close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s := &session{
		conn: c, handler: h, opts: opts, clock: opts.Clock, started: opts.Clock.Now(),
		wakeSignal: make(chan struct{}, 1), shutdown: make(chan shutdownRequest, 1), workerDone: make(chan struct{}),
		turnCtx: ctx,
	}
	readErr := make(chan error, 1)
	go func() { readErr <- s.readLoop() }()

	if err := s.handshake(ctx); err != nil {
		return err
	}
	if err := h.Start(ctx, s.welcome); err != nil {
		_ = c.notify(protocol.MethodError, errorParams{Code: CodeInternalError, Message: OneLine("start failed: " + err.Error()), Fatal: true})
		s.status(statusParams{State: "error"})
		return fmt.Errorf("sdk: start runner: %w", err)
	}
	go s.worker(ctx)
	s.mu.Lock()
	s.ready = true
	s.pausedID = deref(s.welcome.Session.PausedTurnID)
	s.oneLine = s.welcome.Agent.OneLine
	s.mu.Unlock()
	s.status(statusParams{State: "idle"}) // H7
	if interval := s.welcome.Config.HeartbeatIntervalMs; interval > 0 {
		go s.heartbeat(ctx, time.Duration(interval)*time.Millisecond)
	}

	select {
	case req := <-s.shutdown:
		cancel()
		select {
		case <-s.workerDone:
		case <-opts.Clock.After(req.grace):
		}
		return nil
	case err := <-readErr:
		cancel()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// handshake sends hello and reads the welcome (rules H1, H2).
func (s *session) handshake(ctx context.Context) error {
	versions := slices.Clone(s.opts.Versions)
	raw, err := s.conn.call(ctx, protocol.MethodHello, helloParams{
		ProtocolVersions: versions,
		Runner:           runnerInfo{Name: s.opts.Runner.Name, Version: s.opts.Runner.Version},
		Capabilities:     capabilitiesWire(s.opts.Capabilities),
	})
	if err != nil {
		var rpc *RPCError
		if errors.As(err, &rpc) && rpc.Code == CodeUnsupportedVersion {
			var data struct {
				Supported []string `json:"supported"`
			}
			_ = json.Unmarshal(rpc.Data, &data)
			return &UnsupportedVersionError{Supported: data.Supported}
		}
		return fmt.Errorf("sdk: hello: %w", err)
	}
	if err := json.Unmarshal(raw, &s.welcome); err != nil {
		return fmt.Errorf("sdk: read welcome: %w", err)
	}
	if !slices.Contains(versions, s.welcome.ProtocolVersion) {
		return fmt.Errorf("%w: daemon chose %q", ErrUnsupportedVersion, s.welcome.ProtocolVersion)
	}
	if limit := s.welcome.Config.MaxLineBytes; limit > 0 {
		s.conn.setMaxLine(limit)
	}
	return nil
}
