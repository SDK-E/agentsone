// SPDX-License-Identifier: MIT

//go:build !linux

package peer

import (
	"net"

	"github.com/SDK-E/agentsone/internal/api/domain"
)

// Credentials is unsupported off Linux: the daemon runs only on Linux, and
// development builds elsewhere refuse socket callers.
func Credentials(net.Conn) (domain.Peer, error) { return domain.Peer{}, ErrUnsupported }
