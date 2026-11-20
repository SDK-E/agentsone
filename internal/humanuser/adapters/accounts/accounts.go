// SPDX-License-Identifier: MIT

// Package accounts creates the Linux users of humans with the shadow-utils
// commands (groupadd, useradd), passing arguments directly, never through a
// shell. It is idempotent, because the container's /etc is reset by a
// rescue and users are made again from the human store at every start.
package accounts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/SDK-E/agentsone/internal/humanuser/domain"
)

// ErrIDTaken is a user or group that exists with other ids than the human's.
var ErrIDTaken = errors.New("user or group exists with a different id")

// Accounts makes human users. The zero value works on the real system.
type Accounts struct {
	// Root is prepended to home paths for file operations; empty in
	// production, a temp dir in tests.
	Root string
	// Run runs a command; nil runs it for real.
	Run func(ctx context.Context, name string, args ...string) error
	// LookupUser and LookupGroup default to os/user.
	LookupUser  func(name string) (*user.User, error)
	LookupGroup func(name string) (*user.Group, error)
	// Chown defaults to os.Chown.
	Chown func(path string, uid, gid int) error
}

func (a *Accounts) run(ctx context.Context, name string, args ...string) error {
	if a.Run != nil {
		return a.Run(ctx, name, args...)
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() // #nosec G204 -- fixed tools; the user name passed domain.UserFor
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return nil
}

func (a *Accounts) lookupUser(name string) (*user.User, error) {
	if a.LookupUser != nil {
		return a.LookupUser(name)
	}
	return user.Lookup(name) //nolint:wrapcheck // the caller wraps it with the user name
}

func (a *Accounts) lookupGroup(name string) (*user.Group, error) {
	if a.LookupGroup != nil {
		return a.LookupGroup(name)
	}
	return user.LookupGroup(name) //nolint:wrapcheck // the caller wraps it with the group name
}

// Ensure makes u's group (gid == uid), its user with exactly u.UID and its
// private home (0700, owned by the user), as needed. A user that another
// caller made at the same moment counts as made. It only accepts a user
// built by domain.UserFor.
func (a *Accounts) Ensure(ctx context.Context, u domain.User) error {
	if want, err := domain.UserFor(u.Human, u.UID); err != nil || want != u {
		return fmt.Errorf("ensure user %q: not a human user", u.Name)
	}
	id := strconv.Itoa(u.UID)
	if g, err := a.lookupGroup(u.Name); err != nil {
		if err := a.run(ctx, "groupadd", "--gid", id, u.Name); err != nil {
			if g, again := a.lookupGroup(u.Name); again != nil || g.Gid != id {
				return err
			}
		}
	} else if g.Gid != id {
		return fmt.Errorf("group %s has gid %s, want %s: %w", u.Name, g.Gid, id, ErrIDTaken)
	}
	have, err := a.lookupUser(u.Name)
	if err != nil {
		args := []string{"--uid", id, "--gid", id, "--home-dir", u.Home, "--shell", u.Shell, "--no-create-home", "--no-user-group", u.Name}
		runErr := a.run(ctx, "useradd", args...)
		if have, err = a.lookupUser(u.Name); err != nil {
			if runErr != nil {
				return runErr
			}
			return fmt.Errorf("look up new user %s: %w", u.Name, err)
		}
	}
	if have.Uid != id || have.Gid != id {
		return fmt.Errorf("user %s has uid %s gid %s, want %s: %w", u.Name, have.Uid, have.Gid, id, ErrIDTaken)
	}
	return a.home(filepath.Join(a.Root, u.Home), u.UID, u.GID)
}

// home makes the home private to its user: mode 0700 and owned by them,
// also when it survived a rescue on the home volume.
func (a *Accounts) home(path string, uid, gid int) error {
	homes := filepath.Dir(path)
	if err := os.MkdirAll(homes, 0o755); err != nil { // #nosec G301 -- /home/humans only lists homes; each home is 0700
		return fmt.Errorf("homes root %s: %w", homes, err)
	}
	if err := os.Chmod(homes, 0o755); err != nil { // #nosec G302 -- a directory needs execute bits to be traversed
		return fmt.Errorf("homes root %s: %w", homes, err)
	}
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("home %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o700); err != nil { // #nosec G302 -- a directory needs the owner's execute bit; 0700 is private
		return fmt.Errorf("home %s: %w", path, err)
	}
	chown := a.Chown
	if chown == nil {
		chown = os.Chown
	}
	if err := chown(path, uid, gid); err != nil {
		return fmt.Errorf("home %s: %w", path, err)
	}
	return nil
}
