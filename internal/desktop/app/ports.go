// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net"
)

// User is the Linux account a human's desktop runs as.
type User struct {
	Name string
	UID  int
	GID  int
	Home string
}

// Accounts finds a human's Linux user, creating it when the container's
// /etc was reset.
type Accounts interface {
	HumanUser(ctx context.Context, human string) (User, error)
}

// Display is one running desktop: a compositor and its VNC server.
type Display interface {
	// Dial opens a new connection to the VNC server.
	Dial(ctx context.Context) (net.Conn, error)
	// Done is closed when the desktop ended on its own or was stopped.
	Done() <-chan struct{}
	// Stop ends every program of the desktop and waits for them. It may be
	// called more than once.
	Stop()
}

// Launcher starts a desktop as a user.
type Launcher interface {
	Launch(ctx context.Context, u User) (Display, error)
}
