// SPDX-License-Identifier: MIT

package accounts_test

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/accounts"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
)

// system is a fake user database that the fake commands change.
type system struct {
	users  map[string]*user.User
	groups map[string]string // name -> gid
	cmds   []string
	owner  map[string]int
}

func newSystem() *system {
	return &system{users: map[string]*user.User{}, groups: map[string]string{}, owner: map[string]int{}}
}

func arg(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 {
		return args[i+1]
	}
	return ""
}

func (s *system) accounts(root string) *accounts.Accounts {
	return &accounts.Accounts{
		Root: root,
		Run: func(_ context.Context, name string, args ...string) error {
			s.cmds = append(s.cmds, name+" "+strings.Join(args, " "))
			last := args[len(args)-1]
			switch name {
			case "groupadd":
				s.groups[last] = arg(args, "--gid")
			case "useradd":
				s.users[last] = &user.User{Username: last, Uid: arg(args, "--uid"), Gid: arg(args, "--gid")}
			}
			return nil
		},
		LookupUser: func(n string) (*user.User, error) {
			if u, ok := s.users[n]; ok {
				return u, nil
			}
			return nil, user.UnknownUserError(n)
		},
		LookupGroup: func(n string) (*user.Group, error) {
			if gid, ok := s.groups[n]; ok {
				return &user.Group{Name: n, Gid: gid}, nil
			}
			return nil, user.UnknownGroupError(n)
		},
		Chown: func(p string, uid, _ int) error { s.owner[p] = uid; return nil },
	}
}

var mira = domain.AccountFor("mira", false).WithUID(200001)

func TestEnsure_CreatesGroupAndUserWithTheStoredIDAndPrivateHome(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sys := newSystem()

	uid, err := sys.accounts(root).Ensure(t.Context(), mira)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"groupadd --gid 200001 mira",
		"useradd --uid 200001 --gid 200001 --home-dir /home/agents/mira --shell /usr/sbin/nologin --no-create-home --no-user-group mira",
	}
	if diff := cmp.Diff(want, sys.cmds); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
	home := filepath.Join(root, "home/agents/mira")
	info, err := os.Stat(home)
	if err != nil || info.Mode().Perm() != 0o700 || sys.owner[home] != 200001 || uid != 200001 {
		t.Fatalf("home mode=%v owner=%d uid=%d err=%v; want 0700 owned by the agent", info.Mode().Perm(), sys.owner[home], uid, err)
	}
}

// After a reset of /etc the user comes back with its stored uid; a second call changes nothing.
func TestEnsure_IsIdempotent(t *testing.T) {
	t.Parallel()
	sys := newSystem()
	a := sys.accounts(t.TempDir())
	if _, err := a.Ensure(t.Context(), mira); err != nil {
		t.Fatal(err)
	}
	if uid, err := a.Ensure(t.Context(), mira); err != nil || uid != 200001 {
		t.Fatalf("second Ensure = %d, %v", uid, err)
	}
	if len(sys.cmds) != 2 {
		t.Fatalf("commands = %v, want only the first call's two", sys.cmds)
	}
}

func TestEnsure_NeverAllocatesAndStaysInTheAgentRange(t *testing.T) {
	t.Parallel()
	for _, uid := range []int{0, -1, 1000, 199999, 300000} {
		a := newSystem().accounts(t.TempDir())
		acc := domain.AccountFor("mira", false)
		acc.UID = uid
		if _, err := a.Ensure(t.Context(), acc); !errors.Is(err, accounts.ErrUIDRange) {
			t.Errorf("uid %d: error = %v, want ErrUIDRange", uid, err)
		}
	}
}

func TestEnsure_RefusesExistingUserOrGroupWithOtherIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*system)
	}{
		{"user with another uid", func(s *system) {
			s.groups["mira"] = "200001"
			s.users["mira"] = &user.User{Username: "mira", Uid: "3000", Gid: "200001"}
		}},
		{"user that is root", func(s *system) {
			s.groups["mira"] = "200001"
			s.users["mira"] = &user.User{Username: "mira", Uid: "0", Gid: "0"}
		}},
		{"group with another gid", func(s *system) { s.groups["mira"] = "4000" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sys := newSystem()
			tt.setup(sys)
			if _, err := sys.accounts(t.TempDir()).Ensure(t.Context(), mira); !errors.Is(err, accounts.ErrUIDTaken) {
				t.Fatalf("Ensure error = %v, want ErrUIDTaken", err)
			}
		})
	}
}

func TestEnsure_ReportsFailingCommands(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		failOn string
		group  bool
	}{
		{"groupadd fails", "groupadd", false},
		{"useradd fails", "useradd", true},
		{"user missing after useradd", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sys := newSystem()
			if tt.group {
				sys.groups["mira"] = "200001"
			}
			a := sys.accounts(t.TempDir())
			a.Run = func(_ context.Context, name string, _ ...string) error {
				if name == tt.failOn {
					return errors.New(name + " failed")
				}
				return nil // useradd "succeeds" without creating the user
			}
			if _, err := a.Ensure(t.Context(), mira); err == nil {
				t.Fatal("Ensure succeeded")
			}
		})
	}
}

func TestEnsure_ReportsBadHome(t *testing.T) {
	t.Parallel()
	a := newSystem().accounts(t.TempDir())
	a.Chown = func(string, int, int) error { return errors.New("chown refused") }
	if _, err := a.Ensure(t.Context(), mira); err == nil {
		t.Fatal("Ensure succeeded although the home could not be owned by the agent")
	}
}

func TestEnsureAndLock_RefuseRootAccounts(t *testing.T) {
	t.Parallel()
	a := newSystem().accounts(t.TempDir())
	if _, err := a.Ensure(t.Context(), domain.AccountFor("founder", true)); err == nil {
		t.Error("Ensure accepted the root account")
	}
	if err := a.Lock(t.Context(), domain.AccountFor("founder", true)); err == nil {
		t.Error("Lock accepted the root account")
	}
}

func TestLock_LocksAndExpiresExistingUserOnly(t *testing.T) {
	t.Parallel()
	sys := newSystem()
	a := sys.accounts(t.TempDir())
	if err := a.Lock(t.Context(), domain.AccountFor("ghost", false)); err != nil || len(sys.cmds) != 0 {
		t.Fatalf("Lock of a missing user: err=%v cmds=%v", err, sys.cmds)
	}
	sys.users["mira"] = &user.User{Username: "mira", Uid: "200001", Gid: "200001"}
	if err := a.Lock(t.Context(), mira); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"usermod --lock --expiredate 1 mira"}, sys.cmds); diff != "" {
		t.Errorf("commands (-want +got):\n%s", diff)
	}
}

func TestLock_ReportsALookupFailureInsteadOfSkippingTheLock(t *testing.T) {
	t.Parallel()
	sys := newSystem()
	a := sys.accounts(t.TempDir())
	broken := errors.New("passwd database unreadable")
	a.LookupUser = func(string) (*user.User, error) { return nil, broken }
	if err := a.Lock(t.Context(), mira); !errors.Is(err, broken) {
		t.Fatalf("Lock with a broken lookup: err=%v, want %v", err, broken)
	}
	if len(sys.cmds) != 0 {
		t.Errorf("commands ran after a failed lookup: %v", sys.cmds)
	}
}
