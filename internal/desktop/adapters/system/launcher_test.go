// SPDX-License-Identifier: MIT

package system_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/desktop/adapters/system"
	"github.com/SDK-E/agentsone/internal/desktop/app"
)

// The test binary stands in for labwc and wayvnc: the launcher runs it
// with their arguments, and TestMain plays the part the arguments name.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-C": // labwc -C <config> -s <startup>
			fakeCompositor(os.Args[2])
			return
		case "-u": // wayvnc -u <socket>
			fakeVNC(os.Args[2])
			return
		}
	}
	os.Exit(m.Run())
}

// fakeCompositor writes its environment and pid to the runtime folder,
// then opens the Wayland socket and waits for a signal, as labwc does.
// With FAKE_CRASH in its home it exits at once instead.
func fakeCompositor(config string) {
	// The fake trusts its environment: the launcher under test sets it.
	rt := os.Getenv("XDG_RUNTIME_DIR")
	_ = os.WriteFile(filepath.Join(rt, "env"), []byte(strings.Join(os.Environ(), "\n")), 0o600)   // #nosec G703 -- test fake
	_ = os.WriteFile(filepath.Join(rt, "compositor.pid"), []byte(fmt.Sprint(os.Getpid())), 0o600) // #nosec G703 -- test fake
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "FAKE_CRASH")); err == nil {            // #nosec G703 -- test fake
		fmt.Fprintln(os.Stderr, "failed to create backend")
		os.Exit(1)
	}
	if _, err := os.Stat(filepath.Join(config, "menu.xml")); err != nil { // #nosec G703 -- test fake
		os.Exit(2)
	}
	if err := os.WriteFile(filepath.Join(rt, "wayland-0"), nil, 0o600); err != nil { // #nosec G703 -- test fake
		os.Exit(3)
	}
	waitForSignal()
}

// fakeVNC answers each connection with the RFB banner.
func fakeVNC(sock string) {
	if os.Getenv("WAYLAND_DISPLAY") != "wayland-0" {
		os.Exit(4)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.Exit(5)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("RFB 003.008\n"))
			_ = c.Close()
		}
	}()
	waitForSignal()
}

func waitForSignal() {
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGTERM, syscall.SIGINT)
	<-s
	os.Exit(0)
}

func launcher(t *testing.T) system.Launcher {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Unix socket paths are short (104 bytes on macOS), so not t.TempDir.
	root, err := os.MkdirTemp("/tmp", "desk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return system.Launcher{
		Compositor: self, VNCServer: self, RuntimeRoot: root,
		StartTimeout: 10 * time.Second, StopGrace: 5 * time.Second,
		Procs: system.Procs{Root: t.TempDir()}, // an empty process table: nothing left to sweep
	}
}

func me(t *testing.T) app.User {
	t.Helper()
	home := t.TempDir()
	return app.User{Name: "ada", UID: os.Getuid(), GID: os.Getgid(), Home: home}
}

func TestLaunchStartsADesktopWhoseVNCServerAnswers(t *testing.T) {
	l := launcher(t)
	d, err := l.Launch(context.Background(), me(t))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer d.Stop()
	c, err := d.Dial(context.Background())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || line != "RFB 003.008\n" {
		t.Fatalf("banner %q, %v", line, err)
	}
}

func TestTheDesktopGetsAPrivateRuntimeFolderAndACleanEnvironment(t *testing.T) {
	t.Setenv("AGENTSONE_DAEMON_ONLY", "daemon-only")
	l := launcher(t)
	u := me(t)
	d, err := l.Launch(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	rt := filepath.Join(l.RuntimeRoot, fmt.Sprint(u.UID))
	st, err := os.Stat(rt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Errorf("runtime folder mode %v, want 0700", st.Mode().Perm())
	}
	raw, err := os.ReadFile(filepath.Join(rt, "env")) // #nosec G304 -- the test's own temp folder
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Split(string(raw), "\n")
	for _, want := range []string{
		"HOME=" + u.Home, "USER=ada", "LOGNAME=ada", "XDG_RUNTIME_DIR=" + rt,
		"WLR_BACKENDS=headless", "WLR_RENDERER=pixman", "WLR_LIBINPUT_NO_DEVICES=1", "WLR_HEADLESS_OUTPUTS=1",
		"AGENTSONE_DESKTOP=" + rt,
	} {
		if !slices.Contains(env, want) {
			t.Errorf("environment lacks %s", want)
		}
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "AGENTSONE_DAEMON_ONLY=") {
			t.Error("the daemon's environment leaked into the desktop")
		}
	}
	for _, f := range []string{"labwc/menu.xml", "labwc/rc.xml"} {
		if _, err := os.Stat(filepath.Join(rt, f)); err != nil {
			t.Errorf("config %s: %v", f, err)
		}
	}
}

func TestACompositorThatDiesAtStartIsAnErrorWithItsLastWords(t *testing.T) {
	l := launcher(t)
	u := me(t)
	if err := os.WriteFile(filepath.Join(u.Home, "FAKE_CRASH"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := l.Launch(context.Background(), u)
	if err == nil {
		t.Fatal("Launch succeeded")
	}
	if !strings.Contains(err.Error(), "failed to create backend") {
		t.Errorf("error %q does not say why", err)
	}
	if _, err := os.Stat(filepath.Join(l.RuntimeRoot, fmt.Sprint(u.UID))); !os.IsNotExist(err) {
		t.Errorf("runtime folder left behind: %v", err)
	}
}

func TestStopEndsTheDesktopAndRemovesItsRuntimeFolder(t *testing.T) {
	l := launcher(t)
	u := me(t)
	d, err := l.Launch(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	d.Stop()
	d.Stop() // twice is fine
	select {
	case <-d.Done():
	default:
		t.Fatal("Done is open after Stop")
	}
	if _, err := os.Stat(filepath.Join(l.RuntimeRoot, fmt.Sprint(u.UID))); !os.IsNotExist(err) {
		t.Errorf("runtime folder left behind: %v", err)
	}
	if _, err := d.Dial(context.Background()); err == nil {
		t.Error("Dial after Stop succeeded")
	}
}

func TestAProgramEndingOnItsOwnEndsTheDesktop(t *testing.T) {
	l := launcher(t)
	u := me(t)
	d, err := l.Launch(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	pid, err := os.ReadFile(filepath.Join(l.RuntimeRoot, fmt.Sprint(u.UID), "compositor.pid"))
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if _, err := fmt.Sscan(string(pid), &n); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(n, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-d.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("Done stayed open after the compositor ended")
	}
}
