// SPDX-License-Identifier: MIT

// Package accounts creates and locks the Linux users of custom agents with
// the shadow-utils commands (useradd, groupadd, usermod). It is idempotent,
// because the container's /etc may be reset on every start and users are
// recreated from the agent store before any wake.
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
	"strings"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/app"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// The uid and gid range of agent users. It keeps agents apart from human
// and desktop users: RLIMIT_NPROC counts per uid across the whole host.
const (
	UIDMin = 200000
	UIDMax = 299999
)

// Account errors.
var (
	// ErrUIDTaken is a user or group that exists with another id than the agent's.
	ErrUIDTaken = errors.New("user or group exists with a different id")
	// ErrUIDRange is an agent uid outside UIDMin-UIDMax.
	ErrUIDRange = errors.New("agent uid outside the agent range")
)

// Accounts implements app.Accounts.
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

var _ app.Accounts = (*Accounts)(nil)

func (a *Accounts) run(ctx context.Context, name string, args ...string) error {
	if a.Run != nil {
		return a.Run(ctx, name, args...)
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() // #nosec G204 -- fixed tools; the user name passed ValidateName
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return nil
}

func (a *Accounts) lookupUser(name string) (*user.User, error) {
	if a.LookupUser != nil {
		return a.LookupUser(name)
	}
	return user.Lookup(name)
}

func (a *Accounts) lookupGroup(name string) (*user.Group, error) {
	if a.LookupGroup != nil {
		return a.LookupGroup(name)
	}
	return user.LookupGroup(name)
}

// Ensure creates the agent's group (gid == uid), its user with exactly
// acc.UID, and its 0700 home, as needed, and returns the uid. It never
// allocates: the society context owns the uid range and stores each uid.
func (a *Accounts) Ensure(ctx context.Context, acc domain.Account) (int, error) {
	if err := domain.ValidateName(acc.User); err != nil || acc.IsRoot {
		return 0, fmt.Errorf("ensure account %q: not a custom agent user", acc.User)
	}
	if acc.UID < UIDMin || acc.UID > UIDMax {
		return 0, fmt.Errorf("ensure account %s: uid %d outside %d-%d: %w", acc.User, acc.UID, UIDMin, UIDMax, ErrUIDRange)
	}
	id := strconv.Itoa(acc.UID)
	g, err := a.lookupGroup(acc.Group)
	if err != nil {
		if err := a.run(ctx, "groupadd", "--gid", id, acc.Group); err != nil {
			return 0, err
		}
	} else if g.Gid != id {
		return 0, fmt.Errorf("group %s has gid %s, want %s: %w", acc.Group, g.Gid, id, ErrUIDTaken)
	}
	u, err := a.lookupUser(acc.User)
	if err != nil {
		args := []string{"--uid", id, "--gid", id, "--home-dir", acc.Home, "--shell", acc.Shell, "--no-create-home", "--no-user-group", acc.User}
		if err := a.run(ctx, "useradd", args...); err != nil {
			return 0, err
		}
		if u, err = a.lookupUser(acc.User); err != nil {
			return 0, fmt.Errorf("look up new user %s: %w", acc.User, err)
		}
	}
	if u.Uid != id || u.Gid != id {
		return 0, fmt.Errorf("user %s has uid %s gid %s, want %s: %w", acc.User, u.Uid, u.Gid, id, ErrUIDTaken)
	}
	if err := a.home(filepath.Join(a.Root, acc.Home), acc.UID, acc.UID); err != nil {
		return 0, err
	}
	return acc.UID, nil
}

// EnsureRootHome makes a root agent's home: mode 0700, owned by root. Root
// agents run as root, so no user is made.
func (a *Accounts) EnsureRootHome(_ context.Context, acc domain.Account) error {
	if !acc.IsRoot || !strings.HasPrefix(acc.Home, domain.RootHomeRoot+"/") {
		return fmt.Errorf("ensure root home %q: not a root agent's home", acc.Home)
	}
	return a.home(filepath.Join(a.Root, acc.Home), 0, 0)
}

// home makes the home directory private to the agent: mode 0700 and owned
// by it, also when it survived from before (an archived or reset system).
func (a *Accounts) home(path string, uid, gid int) error {
	// The homes root must stay traversable for every agent; only each home is private.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- /home/agents is world-traversable by design
		return fmt.Errorf("homes root for %s: %w", path, err)
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

// Lock disables the user: locked password and an account that expired, so
// no login path accepts it. The user and its home stay, so the uid is never
// handed to another agent while the archived files still carry it.
func (a *Accounts) Lock(ctx context.Context, acc domain.Account) error {
	if err := domain.ValidateName(acc.User); err != nil || acc.IsRoot {
		return fmt.Errorf("lock account %q: not a custom agent user", acc.User)
	}
	if _, err := a.lookupUser(acc.User); err != nil {
		return nil // nothing to lock
	}
	return a.run(ctx, "usermod", "--lock", "--expiredate", "1", acc.User)
}
