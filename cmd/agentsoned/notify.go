// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

// systemd tells the service manager about readiness and liveness through
// $NOTIFY_SOCKET (Type=notify, WatchdogSec=). Outside systemd it does nothing.
type systemd struct{ socket string }

func systemdFromEnv() systemd { return systemd{socket: os.Getenv("NOTIFY_SOCKET")} }

func (s systemd) notify(state string) error {
	if s.socket == "" {
		return nil
	}
	conn, err := net.Dial("unixgram", s.socket)
	if err != nil {
		return fmt.Errorf("dial notify socket: %w", err)
	}
	defer func() { _ = conn.Close() }() // datagram already sent or failed
	if _, err := conn.Write([]byte(state)); err != nil {
		return fmt.Errorf("notify %q: %w", state, err)
	}
	return nil
}

// watchdogInterval is half of WATCHDOG_USEC, or zero when no watchdog is set.
func watchdogInterval() time.Duration {
	usec, err := strconv.ParseInt(os.Getenv("WATCHDOG_USEC"), 10, 64)
	if err != nil || usec <= 0 {
		return 0
	}
	return time.Duration(usec) * time.Microsecond / 2
}

// keepAlive pings the watchdog until ctx ends.
func (s systemd) keepAlive(ctx context.Context, every time.Duration, onErr func(error)) {
	if every == 0 {
		return
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := s.notify("WATCHDOG=1"); err != nil {
				onErr(err)
			}
		}
	}
}
