// SPDX-License-Identifier: MIT

package accounts_test

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SDK-E/agentsone/internal/humanuser/adapters/accounts"
	"github.com/SDK-E/agentsone/internal/humanuser/domain"
)

// fakeSystem is /etc/passwd and /etc/group in memory, changed only by the
// commands the adapter runs.
type fakeSystem struct {
	users  map[string]*user.User
	groups map[string]*user.Group
	runs   [][]string
	chowns []string
	// failUseradd makes useradd fail; addAnyway adds the user first, the way
	// a second daemon module racing on the same user would.
	failUseradd, addAnyway bool
}

func newFake() *fakeSystem {
	return &fakeSystem{users: map[string]*user.User{}, groups: map[string]*user.Group{}}
}

func (f *fakeSystem) accounts(root string) *accounts.Accounts {
	return &accounts.Accounts{
		Root: root,
		Run: func(_ context.Context, name string, args ...string) error {
			f.runs = append(f.runs, append([]string{name}, args...))
			switch name {
			case "groupadd":
				f.groups[args[len(args)-1]] = &user.Group{Gid: args[1], Name: args[len(args)-1]}
			case "useradd":
				n := args[len(args)-1]
				if f.addAnyway {
					f.users[n] = &user.User{Uid: args[1], Gid: args[3], Username: n}
				}
				if f.failUseradd {
					return errors.New("useradd: user already exists")
				}
				f.users[n] = &user.User{Uid: args[1], Gid: args[3], Username: n}
			}
			return nil
		},
		LookupUser: func(n string) (*user.User, error) {
			if u, ok := f.users[n]; ok {
				return u, nil
			}
			return nil, user.UnknownUserError(n)
		},
		LookupGroup: func(n string) (*user.Group, error) {
			if g, ok := f.groups[n]; ok {
				return g, nil
			}
			return nil, user.UnknownGroupError(n)
		},
		Chown: func(path string, _, _ int) error {
			f.chowns = append(f.chowns, filepath.Base(path))
			return nil
		},
	}
}

func ada(t *testing.T) domain.User {
	t.Helper()
	u, err := domain.UserFor("ada", 1000)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestEnsureCreatesGroupUserAndPrivateHome(t *testing.T) {
	root := t.TempDir()
	f := newFake()
	if err := f.accounts(root).Ensure(context.Background(), ada(t)); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"groupadd", "--gid", "1000", "h_ada"},
		{"useradd", "--uid", "1000", "--gid", "1000", "--home-dir", "/home/humans/ada", "--shell", "/bin/bash",
			"--no-create-home", "--no-user-group", "h_ada"},
	}
	if !reflect.DeepEqual(f.runs, want) {
		t.Fatalf("commands %q, want %q", f.runs, want)
	}
	st, err := os.Stat(filepath.Join(root, "home/humans/ada"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("home mode %v, want 0700", st.Mode().Perm())
	}
	rootSt, err := os.Stat(filepath.Join(root, "home/humans"))
	if err != nil {
		t.Fatal(err)
	}
	if rootSt.Mode().Perm() != 0o755 {
		t.Fatalf("homes root mode %v, want 0755", rootSt.Mode().Perm())
	}
	if !reflect.DeepEqual(f.chowns, []string{"ada"}) {
		t.Fatalf("chowns %q", f.chowns)
	}
}

func TestEnsureTwiceRunsNoCommandTheSecondTime(t *testing.T) {
	root := t.TempDir()
	f := newFake()
	a := f.accounts(root)
	ctx := context.Background()
	if err := a.Ensure(ctx, ada(t)); err != nil {
		t.Fatal(err)
	}
	f.runs = nil
	if err := os.Chmod(filepath.Join(root, "home/humans/ada"), 0o755); err != nil { // #nosec G302 -- test widens it to see Ensure close it again
		t.Fatal(err)
	}
	if err := a.Ensure(ctx, ada(t)); err != nil {
		t.Fatal(err)
	}
	if len(f.runs) != 0 {
		t.Fatalf("second ensure ran %q", f.runs)
	}
	st, err := os.Stat(filepath.Join(root, "home/humans/ada"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("home mode %v, want 0700 again", st.Mode().Perm())
	}
}

func TestEnsureAcceptsAUserAnotherCallerJustMade(t *testing.T) {
	f := newFake()
	f.failUseradd, f.addAnyway = true, true
	if err := f.accounts(t.TempDir()).Ensure(context.Background(), ada(t)); err != nil {
		t.Fatalf("racing useradd: %v", err)
	}
}

func TestEnsureReportsAFailedUseradd(t *testing.T) {
	f := newFake()
	f.failUseradd = true
	err := f.accounts(t.TempDir()).Ensure(context.Background(), ada(t))
	if err == nil || !strings.Contains(err.Error(), "useradd") {
		t.Fatalf("error %v, want the useradd failure", err)
	}
}

func TestEnsureRefusesAUserOrGroupWithOtherIDs(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	f.groups["h_ada"] = &user.Group{Gid: "1001", Name: "h_ada"}
	if err := f.accounts(t.TempDir()).Ensure(ctx, ada(t)); !errors.Is(err, accounts.ErrIDTaken) {
		t.Fatalf("group with other gid: %v", err)
	}
	f = newFake()
	f.groups["h_ada"] = &user.Group{Gid: "1000", Name: "h_ada"}
	f.users["h_ada"] = &user.User{Uid: "1001", Gid: "1000", Username: "h_ada"}
	if err := f.accounts(t.TempDir()).Ensure(ctx, ada(t)); !errors.Is(err, accounts.ErrIDTaken) {
		t.Fatalf("user with other uid: %v", err)
	}
}

func TestEnsureRefusesAUserThatIsNotAHumanUser(t *testing.T) {
	f := newFake()
	bad := domain.User{Human: "root", Name: "root", UID: 0, GID: 0, Home: "/root", Shell: "/bin/bash"}
	if err := f.accounts(t.TempDir()).Ensure(context.Background(), bad); err == nil {
		t.Fatal("ensured root as a human user")
	}
	if len(f.runs) != 0 {
		t.Fatalf("ran %q for a bad user", f.runs)
	}
}

func TestEnsureRunsTheRealToolsWhenNoRunnerIsSet(t *testing.T) {
	a := &accounts.Accounts{Root: t.TempDir(),
		LookupUser:  func(n string) (*user.User, error) { return nil, user.UnknownUserError(n) },
		LookupGroup: func(n string) (*user.Group, error) { return nil, user.UnknownGroupError(n) },
	}
	t.Setenv("PATH", t.TempDir()) // no groupadd here: the real command fails to start
	if err := a.Ensure(context.Background(), ada(t)); err == nil || !strings.Contains(err.Error(), "groupadd") {
		t.Fatalf("error %v, want the groupadd failure", err)
	}
}
