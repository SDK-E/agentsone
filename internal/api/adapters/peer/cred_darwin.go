// SPDX-License-Identifier: MIT

//go:build darwin

package peer

import (
	"fmt"
	"io/fs"
	"net"

	"golang.org/x/sys/unix"

	"github.com/SDK-E/agentsone/internal/api/domain"
)

// Credentials reads LOCAL_PEERCRED and LOCAL_PEERPID, so the daemon runs on
// a developer's Mac. There are no cgroups there: a peer is the daemon
// itself, a known agent or human by uid, or refused.
func Credentials(c net.Conn) (domain.Peer, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return domain.Peer{}, fmt.Errorf("%T is not a Unix connection: %w", c, ErrUnsupported)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return domain.Peer{}, fmt.Errorf("raw conn: %w", err)
	}
	var p domain.Peer
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		var x *unix.Xucred
		x, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED) // #nosec G115 -- fd is a live socket descriptor
		if credErr != nil {
			return
		}
		p.UID = int(x.Uid)
		p.PID, credErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) // #nosec G115 -- fd is a live socket descriptor
	}); err != nil {
		return domain.Peer{}, fmt.Errorf("control: %w", err)
	}
	if credErr != nil {
		return domain.Peer{}, fmt.Errorf("LOCAL_PEERCRED: %w", credErr)
	}
	return p, nil
}

// ProcFS is nil: macOS has no /proc.
func ProcFS() fs.FS { return nil }
