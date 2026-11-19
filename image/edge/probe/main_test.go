// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAcceptsListeningTCPAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go acceptAll(ln)

	var stderr bytes.Buffer
	if got := run([]string{"tcp", ln.Addr().String()}, &stderr); got != 0 {
		t.Fatalf("run = %d, want 0; stderr: %s", got, stderr.String())
	}
}

func TestRunFailsOnClosedTCPPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var stderr bytes.Buffer
	if got := run([]string{"tcp", addr}, &stderr); got != 1 {
		t.Fatalf("run = %d, want 1", got)
	}
	if !strings.Contains(stderr.String(), addr) {
		t.Fatalf("stderr %q does not name the address %s", stderr.String(), addr)
	}
}

func TestRunAcceptsListeningUnixSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "admin.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go acceptAll(ln)

	var stderr bytes.Buffer
	if got := run([]string{"unix", path}, &stderr); got != 0 {
		t.Fatalf("run = %d, want 0; stderr: %s", got, stderr.String())
	}
}

func TestRunFailsOnMissingUnixSocket(t *testing.T) {
	var stderr bytes.Buffer
	if got := run([]string{"unix", filepath.Join(t.TempDir(), "none.sock")}, &stderr); got != 1 {
		t.Fatalf("run = %d, want 1", got)
	}
}

func TestRunRejectsBadUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"tcp"}, {"udp", "127.0.0.1:53"}, {"tcp", "a", "b"}} {
		var stderr bytes.Buffer
		if got := run(args, &stderr); got != 2 {
			t.Errorf("run(%q) = %d, want 2", args, got)
		}
		if !strings.HasPrefix(stderr.String(), "usage:") {
			t.Errorf("run(%q) stderr %q, want usage line", args, stderr.String())
		}
	}
}

// shortTempDir makes a temporary folder with a short path. t.TempDir paths on macOS are longer
// than the 104 bytes a unix socket path may have.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "probe")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// acceptAll accepts and closes connections until the listener closes.
func acceptAll(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}
