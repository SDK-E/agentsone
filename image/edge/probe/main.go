// SPDX-License-Identifier: MIT

// Command probe is the health check of the edge image. The image has no shell, so a Docker
// health check cannot run nc or curl; it runs this program instead. probe opens one connection
// to the address it is given and exits 0 when the connection is accepted.
//
//	probe tcp 10.77.42.2:53
//	probe unix /run/agentsone-edge-admin/admin.sock
//
// Exit status: 0 the address accepted a connection, 1 it did not, 2 bad usage.
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// timeout keeps a hung listener from stalling Docker's health check, which has its own timeout.
const timeout = 3 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	if len(args) != 2 || (args[0] != "tcp" && args[0] != "unix") {
		_, _ = fmt.Fprintln(stderr, "usage: probe tcp HOST:PORT | probe unix PATH") //nolint:errcheck // stderr is the last place to report; the exit code still says failure
		return 2
	}
	// The address comes from the image's own health check, not from a request.
	conn, err := net.DialTimeout(args[0], args[1], timeout) //nolint:gosec // G704: dialing the given address is the program's job

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "probe: %s %s: %v\n", args[0], args[1], err) //nolint:errcheck // stderr is the last place to report; the exit code still says failure
		return 1
	}
	if err := conn.Close(); err != nil {
		_, _ = fmt.Fprintf(stderr, "probe: close %s %s: %v\n", args[0], args[1], err) //nolint:errcheck // stderr is the last place to report; the exit code still says failure
		return 1
	}
	return 0
}
