// SPDX-License-Identifier: MIT

// Package domain holds the rules of the built-in editor: an open-source
// build of VS Code (the VSCodium server) baked into the image, one server
// per human, running as that human's Linux user. Each server listens on a
// Unix socket in a folder only its human's user (and root) may enter; the
// daemon proxies the human's browser to it under /ide/<human>/, only for
// that signed-in human. The editor's own connection token is off: the
// socket folder and the daemon's sign-in are the walls, so there is no
// editor secret a browser could ever see.
package domain

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

// State is where a human's editor server stands (contract IdeServerState).
type State string

// Server states.
const (
	Stopped  State = "stopped"
	Starting State = "starting"
	Running  State = "running"
	Failed   State = "failed"
)

// Server is one human's editor server.
type Server struct {
	Human     string
	State     State
	StartedAt time.Time // zero unless starting or running
	Error     string    // why it failed; empty otherwise
}

// URL is the path the human's browser opens, served by the daemon proxy,
// while the server runs.
func (s Server) URL() (string, bool) {
	if s.State != Running {
		return "", false
	}
	return URL(s.Human), true
}

// CanControl allows only the signed-in human themselves to start, stop and
// open their editor. It runs as their Linux user and opens their files, so
// nobody else may: not another human, not an agent, not root.
func CanControl(c core.Caller, human string) error {
	if c.Kind == core.CallerHuman && c.Name == "" {
		return fmt.Errorf("sign in to open your editor: %w", core.ErrUnauthorized)
	}
	if c.Kind != core.CallerHuman || c.Name != human {
		return fmt.Errorf("only %s may use their own editor: %w", human, core.ErrForbidden)
	}
	return nil
}

// BasePath is where the human's server serves the editor: the daemon proxy
// forwards paths unchanged, so the server must know its prefix.
func BasePath(human string) string { return "/ide/" + human }

// URL is the editor's address for the human's browser.
func URL(human string) string { return BasePath(human) + "/" }

// SocketPath is the server's Unix socket under root, in the human's own
// folder (mode 0700, owned by their user).
func SocketPath(root, human string) string { return filepath.Join(root, human, "server.sock") }

// Args are the server's command-line arguments: listen on socket only (no
// TCP port), serve under the human's base path, open folder first, and
// send no telemetry. Without a connection token the socket folder is the
// wall; see the package doc.
func Args(socket, human, folder string) []string {
	return []string{
		"--socket-path", socket,
		"--server-base-path", BasePath(human),
		"--without-connection-token",
		"--default-folder", folder,
		"--disable-telemetry",
	}
}
