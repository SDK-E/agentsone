// SPDX-License-Identifier: MIT

// Package process starts the VSCodium server as a human's Linux user, in its
// own process group, listening on a Unix socket in a folder private to that
// user. Arguments go straight to the program; no shell is involved.
package process

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/SDK-E/agentsone/internal/ide/app"
)

// DefaultBin is the server baked into the machine image.
const DefaultBin = DefaultDir + "/bin/codium-server"

// DefaultGrace is how long a server may take to end after SIGTERM.
const DefaultGrace = 10 * time.Second

// readyPoll is how often Ready tries the socket.
const readyPoll = 100 * time.Millisecond

// Launcher implements app.Launcher.
type Launcher struct {
	// Bin is the server program; empty means DefaultBin.
	Bin string
	// Grace is the wait between SIGTERM and SIGKILL; zero means DefaultGrace.
	Grace time.Duration
}

var _ app.Launcher = Launcher{}

// Launch prepares the run folder (0700, owned by the user, no stale
// socket), opens the log (0600, owned by the user) and starts the server
// as the user, with a clean environment and its home as working folder.
func (l Launcher) Launch(_ context.Context, s app.Spec) (app.Process, error) {
	if filepath.Dir(s.Socket) != s.RunDir || filepath.Dir(s.LogFile) != s.RunDir {
		return nil, fmt.Errorf("editor of %s: socket and log must be in %s", s.User.Human, s.RunDir)
	}
	if err := l.prepare(s); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(s.LogFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- path built by the app from the run root
	if err != nil {
		return nil, fmt.Errorf("editor log: %w", err)
	}
	defer func() { _ = log.Close() }() // the child holds its own copy once started
	if err := os.Chown(s.LogFile, s.User.UID, s.User.GID); err != nil {
		return nil, fmt.Errorf("editor log: %w", err)
	}
	bin := l.Bin
	if bin == "" {
		bin = DefaultBin
	}
	cmd := exec.Command(bin, s.Args...) // #nosec G204 -- fixed program, arguments built by domain.Args
	cmd.Dir = s.User.Home
	cmd.Env = []string{
		"HOME=" + s.User.Home, "USER=" + s.User.Name, "LOGNAME=" + s.User.Name, "SHELL=" + s.User.Shell,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8",
	}
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if s.User.UID != os.Getuid() {
		// Drop to the user, with no supplementary groups. Only skipped when
		// the daemon already is that user (tests).
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(s.User.UID), Gid: uint32(s.User.GID), Groups: []uint32{}} // #nosec G115 -- uid and gid passed humandomain.UserFor
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the editor of %s: %w", s.User.Human, err)
	}
	p := &proc{cmd: cmd, socket: s.Socket, grace: l.grace(), done: make(chan struct{})}
	go p.wait()
	return p, nil
}

func (l Launcher) grace() time.Duration {
	if l.Grace > 0 {
		return l.Grace
	}
	return DefaultGrace
}

// prepare makes the run root traversable but not listable (0711), and the
// human's folder private to their user (0700).
func (Launcher) prepare(s app.Spec) error {
	root := filepath.Dir(s.RunDir)
	if err := os.MkdirAll(root, 0o711); err != nil { // #nosec G301 -- each user must pass through to their own 0700 folder; nobody may list it
		return fmt.Errorf("editor run root: %w", err)
	}
	if err := os.Mkdir(s.RunDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("editor run folder: %w", err)
	}
	if err := os.Chmod(s.RunDir, 0o700); err != nil { // #nosec G302 -- a directory needs the owner's execute bit; 0700 is private
		return fmt.Errorf("editor run folder: %w", err)
	}
	if err := os.Chown(s.RunDir, s.User.UID, s.User.GID); err != nil {
		return fmt.Errorf("editor run folder: %w", err)
	}
	if err := os.Remove(s.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stale editor socket: %w", err)
	}
	return nil
}

type proc struct {
	cmd    *exec.Cmd
	socket string
	grace  time.Duration
	done   chan struct{}
	err    error
}

func (p *proc) wait() {
	p.err = p.cmd.Wait()
	close(p.done)
}

// Ready tries the socket until it accepts a connection.
func (p *proc) Ready(ctx context.Context) error {
	t := time.NewTicker(readyPoll)
	defer t.Stop()
	var d net.Dialer
	for {
		if c, err := d.DialContext(ctx, "unix", p.socket); err == nil {
			_ = c.Close() // only the dial mattered
			return nil
		}
		select {
		case <-p.done:
			return fmt.Errorf("the server exited: %w", p.err)
		case <-ctx.Done():
			return fmt.Errorf("wait for the editor socket: %w", ctx.Err())
		case <-t.C:
		}
	}
}

func (p *proc) Done() <-chan struct{} { return p.done }

func (p *proc) Err() error {
	select {
	case <-p.done:
		return p.err
	default:
		return nil
	}
}

// Stop sends SIGTERM to the whole process group, SIGKILL after the grace
// time, and waits for the server to end.
func (p *proc) Stop() {
	select {
	case <-p.done:
		return
	default:
	}
	pgid := -p.cmd.Process.Pid
	_ = syscall.Kill(pgid, syscall.SIGTERM) // it may have just ended; the wait below tells
	t := time.NewTimer(p.grace)
	defer t.Stop()
	select {
	case <-p.done:
	case <-t.C:
		_ = syscall.Kill(pgid, syscall.SIGKILL) // as above
		<-p.done
	}
}
