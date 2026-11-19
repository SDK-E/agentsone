// SPDX-License-Identifier: MIT

//go:build linux

package peer

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"

	"github.com/SDK-E/agentsone/internal/api/domain"
)

// Credentials reads SO_PEERCRED from a Unix connection.
func Credentials(c net.Conn) (domain.Peer, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return domain.Peer{}, fmt.Errorf("%T is not a Unix connection: %w", c, ErrUnsupported)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return domain.Peer{}, fmt.Errorf("raw conn: %w", err)
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) // #nosec G115 -- fd is a live socket descriptor
	}); err != nil {
		return domain.Peer{}, fmt.Errorf("control: %w", err)
	}
	if credErr != nil {
		return domain.Peer{}, fmt.Errorf("SO_PEERCRED: %w", credErr)
	}
	return domain.Peer{PID: int(cred.Pid), UID: int(cred.Uid)}, nil
}
