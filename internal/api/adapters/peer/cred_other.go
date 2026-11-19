// SPDX-License-Identifier: MIT

//go:build !linux && !darwin

package peer

import (
	"io/fs"
	"net"

	"github.com/SDK-E/agentsone/internal/api/domain"
)

// Credentials is unsupported here: the daemon runs on Linux, and
// development builds elsewhere refuse socket callers.
func Credentials(net.Conn) (domain.Peer, error) { return domain.Peer{}, ErrUnsupported }

// ProcFS is the process file system holding <pid>/cgroup, or nil.
func ProcFS() fs.FS { return nil }
