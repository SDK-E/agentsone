// SPDX-License-Identifier: MIT

// Package adminsock opens the edge admin socket. The control container reaches the edge only
// through it: the socket sits in a volume shared by the two containers, and its mode 0660 lets in
// the edge user and its group (65532 in the compose stack) and nobody else. The machine never
// mounts that volume.
//
// The socket takes the edge process's own group; the compose stack runs the edge as
// 65532:65532 and adds the control container to group 65532.
//
// Owned by work package 15 (edge: network, freeze).
package adminsock

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
)

// DefaultPath is the admin socket in the volume the edge shares with the control container.
const DefaultPath = "/run/agentsone-edge-admin/admin.sock"

// Listen opens the admin socket at path with mode 0660, creating its folder if needed. A socket
// left by an earlier run is replaced; anything else at path is refused. Closing the listener
// removes the socket.
func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("admin socket folder: %w", err)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&fs.ModeSocket == 0 {
			return nil, fmt.Errorf("admin socket %s: something that is not a socket is in the way", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove old admin socket: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("admin socket %s: %w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("admin socket: %w", err)
	}
	// #nosec G302 -- the group (the control container) must reach the socket.
	if err := os.Chmod(path, 0o660); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("admin socket mode: %w", err)
	}
	return ln, nil
}
