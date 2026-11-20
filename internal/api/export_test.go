// SPDX-License-Identifier: MIT

package api

import (
	"io/fs"
	"net"

	"github.com/SDK-E/agentsone/internal/api/domain"
)

// SetPeerSource replaces what the socket listener reads about a peer: its
// credentials and its /proc. Call it before Listen. Only tests can, since
// the kernel cannot give a test process another uid.
func SetPeerSource(s *Server, credentials func(net.Conn) (domain.Peer, error), proc fs.FS) {
	s.resolver.Credentials = credentials
	s.resolver.Proc = proc
}
