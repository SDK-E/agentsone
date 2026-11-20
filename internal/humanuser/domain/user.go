// SPDX-License-Identifier: MIT

// Package domain holds the rules of a human's Linux user: its name, uid,
// group and home. Humans get Linux users so what they run on the machine
// (the editor, the desktop) runs as them, not as root or a shared user.
package domain

import (
	"fmt"

	"github.com/SDK-E/agentsone/internal/core"
)

// Prefix starts every human's Linux user and group name. Agent user names
// are agent names, which never contain an underscore, so a human's user can
// never be an agent's user, whatever the two are called.
const Prefix = "h_"

// MaxHumanLen is the longest human name that gets a Linux user: with the
// prefix it fills the 32 bytes a Linux user name may hold.
const MaxHumanLen = 32 - len(Prefix)

// The uid range of human users: Debian's range for ordinary users. Agent
// users start at 200000, so the ranges never meet.
const (
	UIDMin = 1000
	UIDMax = 59999
)

// HomeRoot holds the humans' homes, apart from the agents' homes in
// /home/agents and the shared folder /home/shared.
const HomeRoot = "/home/humans"

// Shell is the login shell of human users. They never log in through a
// password (useradd leaves it locked); the daemon starts their programs.
const Shell = "/bin/bash"

// User is a human's Linux user.
type User struct {
	// Human is the human's login name in Agents One.
	Human string
	// Name is the Linux user name, and the name of the user's own group.
	Name string
	// UID is the user's uid; GID equals it.
	UID, GID int
	// Home is the user's private home folder.
	Home  string
	Shell string
}

// UserFor returns the Linux user of human, whose account number is uid. A
// name that is not a contract HumanName, or too long for a Linux user name,
// and a uid outside UIDMin-UIDMax are ErrInvalid: the name ends up in user
// names and paths, so nothing else gets through.
func UserFor(human string, uid int) (User, error) {
	if err := validName(human); err != nil {
		return User{}, err
	}
	if uid < UIDMin || uid > UIDMax {
		return User{}, fmt.Errorf("human %s: uid %d outside %d-%d: %w", human, uid, UIDMin, UIDMax, core.ErrInvalid)
	}
	return User{
		Human: human, Name: Prefix + human, UID: uid, GID: uid,
		Home: HomeRoot + "/" + human, Shell: Shell,
	}, nil
}

func validName(name string) error {
	bad := fmt.Errorf("human name %q must be a lowercase letter, then up to %d lowercase letters, digits or hyphens: %w",
		name, MaxHumanLen-1, core.ErrInvalid)
	if name == "" || len(name) > MaxHumanLen || name[0] < 'a' || name[0] > 'z' {
		return bad
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return bad
		}
	}
	return nil
}
