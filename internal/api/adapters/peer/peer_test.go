// SPDX-License-Identifier: MIT

package peer_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/fstest"

	"github.com/SDK-E/agentsone/internal/api/adapters/peer"
	"github.com/SDK-E/agentsone/internal/api/domain"
	"github.com/SDK-E/agentsone/internal/core"
)

func fixed(p domain.Peer, err error) func(net.Conn) (domain.Peer, error) {
	return func(net.Conn) (domain.Peer, error) { return p, err }
}

var proc = fstest.MapFS{
	"10/cgroup": {Data: []byte("0::/agents/founder\n")},
	"11/cgroup": {Data: []byte("0::/agents/cto\n")},
}

func TestResolver_NamesEachUID0AgentByItsUnit(t *testing.T) {
	t.Parallel()
	for pid, want := range map[int]string{10: "founder", 11: "cto"} {
		r := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{PID: pid, UID: 0}, nil), AgentByUID: peer.NoAgents}
		c, err := r.Caller(nil)
		if err != nil || c.Kind != core.CallerAgent || c.Name != want || !c.IsRoot {
			t.Errorf("pid %d: caller %+v err %v", pid, c, err)
		}
	}
}

func TestResolver_WithoutProcEveryPeerIsSystem(t *testing.T) {
	t.Parallel()
	r := peer.Resolver{Credentials: fixed(domain.Peer{PID: 10, UID: 501}, nil), AgentByUID: peer.NoAgents}
	c, err := r.Caller(nil)
	if err != nil || c.Kind != core.CallerSystem || c.Name != "uid:501" {
		t.Fatalf("caller %+v err %v", c, err)
	}
}

func TestResolver_FailsWithoutCredentials(t *testing.T) {
	t.Parallel()
	r := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{}, peer.ErrUnsupported), AgentByUID: peer.NoAgents}
	if _, err := r.Caller(nil); !errors.Is(err, peer.ErrUnsupported) {
		t.Fatalf("err %v", err)
	}
}

func TestResolver_FailsWhenThePeerIsGone(t *testing.T) {
	t.Parallel()
	r := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{PID: 99}, nil), AgentByUID: peer.NoAgents}
	if _, err := r.Caller(nil); err == nil {
		t.Fatal("no error for a missing /proc entry")
	}
}

type key struct{}

func TestConnContext_StoresTheCallerOrLeavesTheContextBare(t *testing.T) {
	t.Parallel()
	store := func(ctx context.Context, c core.Caller) context.Context { return context.WithValue(ctx, key{}, c) }
	good := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{PID: 10}, nil), AgentByUID: peer.NoAgents}
	if c, ok := good.ConnContext(store)(context.Background(), nil).Value(key{}).(core.Caller); !ok || c.Name != "founder" {
		t.Fatalf("caller %+v", c)
	}
	bad := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{}, peer.ErrUnsupported), AgentByUID: peer.NoAgents}
	if v := bad.ConnContext(store)(context.Background(), nil).Value(key{}); v != nil {
		t.Fatalf("stored %v", v)
	}
}
