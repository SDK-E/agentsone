// SPDX-License-Identifier: MIT

package domain_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/ide/domain"
)

func TestOnlyTheSignedInHumanControlsTheirEditor(t *testing.T) {
	cases := []struct {
		name   string
		caller core.Caller
		want   error
	}{
		{"owner", core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}, nil},
		{"other human", core.Caller{Kind: core.CallerHuman, Name: "bob", UID: -1}, core.ErrForbidden},
		{"root human acting as root", core.Caller{Kind: core.CallerHuman, Name: "bob", IsRoot: true}, core.ErrForbidden},
		{"root agent", core.Caller{Kind: core.CallerAgent, Name: "ada", UID: 0, IsRoot: true}, core.ErrForbidden},
		{"system", core.Caller{Kind: core.CallerSystem, Name: "ada"}, core.ErrForbidden},
		{"not signed in", core.Anonymous, core.ErrUnauthorized},
	}
	for _, c := range cases {
		err := domain.CanControl(c.caller, "ada")
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func TestPathsOfAHumansEditor(t *testing.T) {
	if got := domain.BasePath("ada"); got != "/ide/ada" {
		t.Fatalf("base path %q", got)
	}
	if got := domain.URL("ada"); got != "/ide/ada/" {
		t.Fatalf("url %q", got)
	}
	if got := domain.SocketPath("/run/agentsone/ide", "ada"); got != "/run/agentsone/ide/ada/server.sock" {
		t.Fatalf("socket %q", got)
	}
}

func TestServerShowsItsURLOnlyWhileRunning(t *testing.T) {
	s := domain.Server{Human: "ada", State: domain.Running}
	if u, ok := s.URL(); !ok || u != "/ide/ada/" {
		t.Fatalf("running url %q %v", u, ok)
	}
	for _, st := range []domain.State{domain.Stopped, domain.Starting, domain.Failed} {
		if _, ok := (domain.Server{Human: "ada", State: st}).URL(); ok {
			t.Errorf("%s has a url", st)
		}
	}
}

func TestArgsServeOnTheSocketUnderTheBasePathWithoutTheEditorsOwnToken(t *testing.T) {
	got := domain.Args("/run/agentsone/ide/ada/server.sock", "ada", "/home/humans/ada")
	want := []string{
		"--socket-path", "/run/agentsone/ide/ada/server.sock",
		"--server-base-path", "/ide/ada",
		"--without-connection-token",
		"--default-folder", "/home/humans/ada",
		"--disable-telemetry",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("args %q\nwant %q", got, want)
	}
}
