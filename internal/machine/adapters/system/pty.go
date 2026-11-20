// SPDX-License-Identifier: MIT

package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/process"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

// PtyStarter starts programs on pseudo terminals, each in its own session,
// as the given Linux user, in the given cell.
type PtyStarter struct{}

var _ app.PtyStarter = PtyStarter{}

// Start runs spec as uid and gid in home. The environment is fresh, so no
// daemon setting or secret leaks into the terminal. A placed cell starts
// the program through the launch helper, which joins the cell's cgroup,
// lowers its limits and becomes uid and gid before it execs the program:
// the program never runs outside the cell, and nor does anything it starts.
func (PtyStarter) Start(_ context.Context, spec domain.PtySpec, uid, gid int, home string, cell core.ProcessCell) (app.PtyProcess, error) {
	cmd := exec.Command(spec.Command[0], spec.Command[1:]...) //nolint:gosec // G204: the caller's own command, run as the caller's own user.
	cmd.Dir = home
	cmd.Env = []string{
		"HOME=" + home, "TERM=xterm-256color", "LANG=C.UTF-8",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	same := uid == os.Getuid() && gid == os.Getgid()
	var placed *process.Placed
	switch {
	case cell.Placed():
		p := process.Placement{Cell: cell, UID: uid, GID: gid}
		if same {
			p.UID, p.GID = -1, -1
		}
		var err error
		if placed, err = process.Place(cmd, p); err != nil {
			return nil, fmt.Errorf("place %s: %w", spec.Command[0], err)
		}
	case !same:
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{uint32(gid)}} //nolint:gosec // G115: uids and gids come from passwd and are non-negative.
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(spec.Cols), Rows: uint16(spec.Rows)}) //nolint:gosec // G115: the domain caps the size at 1000x500.
	if err != nil {
		if placed != nil {
			placed.Close()
		}
		return nil, fmt.Errorf("start %s: %w", spec.Command[0], err)
	}
	proc := &ptyProcess{f: f, cmd: cmd}
	if placed != nil {
		if err := placed.Started(); err != nil {
			// The helper exits by itself once it reported; reap it.
			return nil, errors.Join(fmt.Errorf("start %s in its cell: %w", spec.Command[0], err), proc.Close(), waitErr(proc))
		}
	}
	return proc, nil
}

// waitErr reaps a helper that failed. Its exit code is known: LaunchFailed.
func waitErr(p *ptyProcess) error {
	if code := p.Wait(); code != process.LaunchFailed {
		return fmt.Errorf("launch helper exited %d, want %d", code, process.LaunchFailed)
	}
	return nil
}

type ptyProcess struct {
	f    *os.File
	cmd  *exec.Cmd
	once sync.Once
}

func (p *ptyProcess) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *ptyProcess) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *ptyProcess) Resize(cols, rows int) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}) //nolint:gosec // G115: the domain caps the size at 1000x500.
}

// Wait returns the exit code, or 128 plus the signal for a killed program,
// as shells report it.
func (p *ptyProcess) Wait() int {
	err := p.cmd.Wait()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return -1
	}
	if ws, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return p.cmd.ProcessState.ExitCode()
}

// Close hangs up the whole process group, then closes the terminal.
func (p *ptyProcess) Close() error {
	var err error
	p.once.Do(func() {
		if kerr := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGHUP); kerr != nil && !errors.Is(kerr, syscall.ESRCH) {
			err = fmt.Errorf("hang up terminal: %w", kerr)
		}
		if cerr := p.f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close terminal: %w", cerr)
		}
	})
	return err
}
