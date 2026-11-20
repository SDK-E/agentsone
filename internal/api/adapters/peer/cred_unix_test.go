// SPDX-License-Identifier: MIT

//go:build linux || darwin

package peer_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/SDK-E/agentsone/internal/api/adapters/peer"
)

// System test (TESTING.md; SO_PEERCRED and /proc on Linux CI): the kernel
// reports our own pid and uid for a connection we make to ourselves.
func TestCredentials_ReadsTheRealPeer(t *testing.T) {
	t.Parallel()
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
		close(accepted)
	}()
	client, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	server := <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	defer func() { _ = server.Close() }()
	p, err := peer.Credentials(server)
	if err != nil {
		t.Fatal(err)
	}
	if p.PID != os.Getpid() || p.UID != os.Getuid() {
		t.Fatalf("peer %+v, want pid %d uid %d", p, os.Getpid(), os.Getuid())
	}
	// We are the daemon here: the peer is our own process.
	r := peer.Resolver{Proc: peer.ProcFS(), Credentials: peer.Credentials, Directory: none, DaemonPID: os.Getpid()}
	if c, err := r.Caller(server); err != nil || c.Name != "daemon" {
		t.Fatalf("resolve real peer: %+v %v", c, err)
	}
}

func TestCredentials_RejectsANonUnixConnection(t *testing.T) {
	t.Parallel()
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	if _, err := peer.Credentials(a); err == nil {
		t.Fatal("no error for a pipe")
	}
}
