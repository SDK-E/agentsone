// SPDX-License-Identifier: MIT

package system

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SDK-E/agentsone/internal/desktop/app"
)

//go:embed labwc
var labwcConfig embed.FS

const (
	waylandSocket = "wayland-0"
	vncSocket     = "vnc.sock"
	pollEvery     = 50 * time.Millisecond
)

// Launcher starts a human's desktop: labwc on a headless output (pixman,
// no GPU) and wayvnc on a Unix socket in the human's private runtime
// folder. The socket, not a loopback port, is the wall: agents on the
// machine reach loopback, but only the human's user and root reach a
// 0700 folder, so the daemon's bridge is the only way in.
type Launcher struct {
	// Compositor and VNCServer are the programs, labwc and wayvnc in the image.
	Compositor string
	VNCServer  string
	// RuntimeRoot holds one runtime folder per desktop, named by uid
	// (XDG_RUNTIME_DIR of the desktop).
	RuntimeRoot string
	// StartTimeout bounds the wait for each program's socket.
	StartTimeout time.Duration
	// StopGrace is how long programs get to end after SIGTERM.
	StopGrace time.Duration
	// Procs finds programs the desktop started, to end them at stop.
	Procs Procs
}

var _ app.Launcher = Launcher{}

// Launch starts a desktop as u and returns when its VNC server answers.
func (l Launcher) Launch(ctx context.Context, u app.User) (app.Display, error) {
	rt := filepath.Join(l.RuntimeRoot, strconv.Itoa(u.UID))
	if err := l.prepare(rt, u); err != nil {
		_ = os.RemoveAll(rt) // already failing; the prepare error is the one to report
		return nil, err
	}
	d := &display{l: l, uid: u.UID, rt: rt, done: make(chan struct{})}
	env := desktopEnv(u, rt)
	ctx, cancel := context.WithTimeout(ctx, l.StartTimeout)
	defer cancel()
	var err error
	if d.comp, err = start(l.Compositor, []string{"-C", filepath.Join(rt, "labwc")}, env, u); err == nil {
		err = d.comp.waitFor(ctx, func() bool { return exists(filepath.Join(rt, waylandSocket)) })
	}
	if err == nil {
		d.vnc, err = start(l.VNCServer, []string{"-u", filepath.Join(rt, vncSocket)}, append(env, "WAYLAND_DISPLAY="+waylandSocket), u)
	}
	if err == nil {
		err = d.vnc.waitFor(ctx, func() bool { return d.answers(ctx) })
	}
	if err != nil {
		d.Stop()
		return nil, fmt.Errorf("desktop of %s: %w", u.Name, err)
	}
	go func() {
		select {
		case <-d.comp.exited:
		case <-d.vnc.exited:
		}
		d.Stop()
	}()
	return d, nil
}

// prepare makes the runtime folder afresh, owned by u with mode 0700, and
// writes labwc's configuration into it.
func (l Launcher) prepare(rt string, u app.User) error {
	if err := os.MkdirAll(l.RuntimeRoot, 0o755); err != nil { // #nosec G301 -- holds one 0700 folder per desktop
		return fmt.Errorf("desktop runtime root: %w", err)
	}
	if err := os.RemoveAll(rt); err != nil { // a stale folder from a crashed daemon
		return fmt.Errorf("clear desktop runtime folder: %w", err)
	}
	if err := os.Mkdir(rt, 0o700); err != nil {
		return fmt.Errorf("desktop runtime folder: %w", err)
	}
	cfg := filepath.Join(rt, "labwc")
	if err := os.Mkdir(cfg, 0o700); err != nil {
		return fmt.Errorf("desktop config folder: %w", err)
	}
	files, err := fs.ReadDir(labwcConfig, "labwc")
	if err != nil {
		return fmt.Errorf("desktop config: %w", err)
	}
	for _, f := range files {
		b, err := labwcConfig.ReadFile("labwc/" + f.Name())
		if err != nil {
			return fmt.Errorf("desktop config %s: %w", f.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(cfg, f.Name()), b, 0o600); err != nil {
			return fmt.Errorf("desktop config %s: %w", f.Name(), err)
		}
	}
	for _, p := range []string{rt, cfg, filepath.Join(cfg, "autostart"), filepath.Join(cfg, "menu.xml"), filepath.Join(cfg, "rc.xml")} {
		if err := os.Lchown(p, u.UID, u.GID); err != nil {
			return fmt.Errorf("give the desktop runtime folder to %s: %w", u.Name, err)
		}
	}
	return nil
}

// desktopEnv is the whole environment of the desktop's programs: nothing
// of the daemon's own environment passes.
func desktopEnv(u app.User, rt string) []string {
	return []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + u.Home, "USER=" + u.Name, "LOGNAME=" + u.Name, "LANG=C.UTF-8",
		"XDG_RUNTIME_DIR=" + rt,
		"WLR_BACKENDS=headless", "WLR_RENDERER=pixman", "WLR_LIBINPUT_NO_DEVICES=1", "WLR_HEADLESS_OUTPUTS=1",
		MarkerVar + "=" + rt,
	}
}

// proc is one started program.
type proc struct {
	cmd    *exec.Cmd
	stderr *tail
	exited chan struct{}
}

// start runs name as u in its own process group.
func start(name string, args, env []string, u app.User) (*proc, error) {
	cmd := exec.Command(name, args...) // #nosec G204 -- fixed programs from the daemon's configuration
	cmd.Env = env
	cmd.Dir = u.Home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if u.UID != os.Getuid() {
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(u.UID), Gid: uint32(u.GID), Groups: []uint32{}} // #nosec G115 -- uids come from the human account range
	}
	p := &proc{cmd: cmd, stderr: &tail{max: 2048}, exited: make(chan struct{})}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", filepath.Base(name), err)
	}
	go func() {
		_ = cmd.Wait() // the exit shows on exited; why it ended is in stderr
		close(p.exited)
	}()
	return p, nil
}

// waitFor polls ready until it holds, the program ends or ctx ends.
func (p *proc) waitFor(ctx context.Context, ready func() bool) error {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for !ready() {
		select {
		case <-p.exited:
			return fmt.Errorf("%s ended at start: %s", filepath.Base(p.cmd.Path), p.stderr.String())
		case <-ctx.Done():
			return fmt.Errorf("%s did not open its socket in time: %w", filepath.Base(p.cmd.Path), ctx.Err())
		case <-t.C:
		}
	}
	return nil
}

func (p *proc) signal(sig syscall.Signal) {
	if p != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, sig) // the group may be gone already
	}
}

// wait reports whether the program ended by deadline; the zero deadline
// waits for as long as it takes.
func (p *proc) wait(deadline time.Time) bool {
	if p == nil {
		return true
	}
	if deadline.IsZero() {
		<-p.exited
		return true
	}
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	select {
	case <-p.exited:
		return true
	case <-t.C:
		return false
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// display is one running desktop.
type display struct {
	l         Launcher
	uid       int
	rt        string
	comp, vnc *proc
	once      sync.Once
	done      chan struct{}
}

func (d *display) answers(ctx context.Context) bool {
	c, err := d.Dial(ctx)
	if err != nil {
		return false
	}
	_ = c.Close() // only the dial mattered
	return true
}

// Dial opens a connection to the desktop's VNC server.
func (d *display) Dial(ctx context.Context) (net.Conn, error) {
	var dialer net.Dialer
	c, err := dialer.DialContext(ctx, "unix", filepath.Join(d.rt, vncSocket))
	if err != nil {
		return nil, fmt.Errorf("desktop VNC server: %w", err)
	}
	return c, nil
}

// Done is closed once the desktop is stopped.
func (d *display) Done() <-chan struct{} { return d.done }

// Stop ends the VNC server, the compositor and every program started in
// the desktop: SIGTERM first, SIGKILL after the grace time. It removes the
// runtime folder and returns when all of that is done.
func (d *display) Stop() {
	d.once.Do(func() {
		d.vnc.signal(syscall.SIGTERM)
		d.comp.signal(syscall.SIGTERM)
		d.l.Procs.Sweep(d.uid, d.rt, syscall.SIGTERM)
		grace := time.Now().Add(d.l.StopGrace)
		if !d.vnc.wait(grace) || !d.comp.wait(grace) {
			d.vnc.signal(syscall.SIGKILL)
			d.comp.signal(syscall.SIGKILL)
		}
		d.l.Procs.Sweep(d.uid, d.rt, syscall.SIGKILL)
		d.vnc.wait(time.Time{})
		d.comp.wait(time.Time{})
		_ = os.RemoveAll(d.rt) // best effort: the next launch clears it too
		close(d.done)
	})
	<-d.done
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.TrimSpace(string(t.b))
	if s == "" {
		return "no output"
	}
	return s
}
