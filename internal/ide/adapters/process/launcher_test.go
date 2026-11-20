// SPDX-License-Identifier: MIT

package process_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	humandomain "github.com/SDK-E/agentsone/internal/humanuser/domain"
	"github.com/SDK-E/agentsone/internal/ide/adapters/process"
	"github.com/SDK-E/agentsone/internal/ide/app"
)

// TestMain turns this test binary into a fake editor server when it is
// started with --socket-path, the way the launcher starts the real one.
func TestMain(m *testing.M) {
	if i := slices.Index(os.Args, "--socket-path"); i > 0 {
		os.Exit(fakeServer(os.Args[i+1], slices.Contains(os.Args, "--fail-now")))
	}
	os.Exit(m.Run())
}

func fakeServer(socket string, fail bool) int {
	if fail {
		_, _ = os.Stderr.WriteString("fake server: refusing to start\n")
		return 3
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return 4
	}
	_, _ = os.Stdout.WriteString("fake server: listening as " + strconv.Itoa(os.Getuid()) + "\n")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	<-stop
	_ = ln.Close()
	return 0
}

// spec is a server for the current user in a short folder under /tmp:
// Unix socket paths may hold about 100 bytes.
func spec(t *testing.T, args ...string) app.Spec {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "ide")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	uid := os.Getuid()
	dir := filepath.Join(root, "ada")
	socket := filepath.Join(dir, "server.sock")
	home := t.TempDir()
	return app.Spec{
		User:   humandomain.User{Human: "ada", Name: "h_ada", UID: uid, GID: os.Getgid(), Home: home, Shell: "/bin/sh"},
		RunDir: dir, Socket: socket, LogFile: filepath.Join(dir, "server.log"),
		Args: append([]string{"--socket-path", socket}, args...),
	}
}

func launcher(t *testing.T) process.Launcher {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return process.Launcher{Bin: bin, Grace: 5 * time.Second}
}

func TestLaunchStartsTheServerInAPrivateFolderAndStopsIt(t *testing.T) {
	s := spec(t)
	if err := os.MkdirAll(s.RunDir, 0o755); err != nil { // #nosec G301 -- test leaves it open to see Launch close it
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Socket, nil, 0o600); err != nil { // a stale socket from before a restart
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := launcher(t).Launch(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.Ready(ready); err != nil {
		t.Fatalf("ready: %v", err)
	}
	st, err := os.Stat(s.RunDir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("run folder %v %v, want 0700", st.Mode().Perm(), err)
	}
	p.Stop()
	select {
	case <-p.Done():
	default:
		t.Fatal("stop returned before the process ended")
	}
	log, err := os.ReadFile(s.LogFile)
	if err != nil || !strings.Contains(string(log), "listening as "+strconv.Itoa(os.Getuid())) {
		t.Fatalf("log %q %v", log, err)
	}
	if st, err := os.Stat(s.LogFile); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("log mode %v %v", st.Mode().Perm(), err)
	}
}

func TestReadyReportsAServerThatExits(t *testing.T) {
	ctx := context.Background()
	p, err := launcher(t).Launch(ctx, spec(t, "--fail-now"))
	if err != nil {
		t.Fatal(err)
	}
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.Ready(ready); err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("ready of an exiting server: %v", err)
	}
	<-p.Done()
	if p.Err() == nil {
		t.Fatal("no exit error")
	}
	p.Stop() // stopping an ended process is fine
}

func TestReadyGivesUpWhenTheContextEnds(t *testing.T) {
	s := spec(t)
	s.Args = []string{"--socket-path", filepath.Join(s.RunDir, "elsewhere.sock")}
	p, err := launcher(t).Launch(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := p.Ready(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ready: %v, want the deadline", err)
	}
}

func TestLaunchReportsAMissingBinary(t *testing.T) {
	l := process.Launcher{Bin: filepath.Join(t.TempDir(), "no-such-server")}
	if _, err := l.Launch(context.Background(), spec(t)); err == nil {
		t.Fatal("launched a missing binary")
	}
}

func TestLaunchRefusesASocketOutsideItsFolder(t *testing.T) {
	s := spec(t)
	s.Socket = "/tmp/elsewhere.sock"
	if _, err := launcher(t).Launch(context.Background(), s); err == nil {
		t.Fatal("launched with a socket outside the run folder")
	}
}
