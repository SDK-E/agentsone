// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSystemdNotifySendsState(t *testing.T) {
	dir, err := os.MkdirTemp("", "sd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "n.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	sd := systemd{socket: sock}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		sd.keepAlive(ctx, time.Millisecond, func(err error) { t.Error(err) })
	}()
	// Cleanups run last in, first out: stop the watchdog goroutine and wait for
	// it to return before the socket is closed and before the test ends, so it
	// can neither dial a closed socket nor report to a finished test.
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	if err := sd.notify("READY=1"); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	buf := make([]byte, 64)
	for !got["READY=1"] || !got["WATCHDOG=1"] {
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read: %v (got %v)", err, got)
		}
		got[string(buf[:n])] = true
	}
}

func TestSystemdNotifyIsSilentOutsideSystemd(t *testing.T) {
	if err := (systemd{}).notify("READY=1"); err != nil {
		t.Fatal(err)
	}
}

func TestWatchdogInterval(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "30000000")
	if got := watchdogInterval(); got != 15*time.Second {
		t.Fatalf("got %v", got)
	}
	t.Setenv("WATCHDOG_USEC", "")
	if got := watchdogInterval(); got != 0 {
		t.Fatalf("got %v", got)
	}
}
