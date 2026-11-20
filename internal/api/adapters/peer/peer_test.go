// SPDX-License-Identifier: MIT

package peer_test

import (
	"context"
	"errors"
	"fmt"
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
	"12/cgroup": {Data: []byte("0::/daemon\n")},
}

func nobody(_ context.Context, uid int) (string, error) {
	return "", fmt.Errorf("uid %d: %w", uid, core.ErrNotFound)
}

var none = domain.Directory{AgentByUID: nobody, HumanByUID: nobody}

func TestResolver_NamesEachUID0AgentByItsUnit(t *testing.T) {
	t.Parallel()
	for pid, want := range map[int]string{10: "founder", 11: "cto"} {
		r := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{PID: pid, UID: 0}, nil), Directory: none, DaemonPID: 1}
		c, err := r.Caller(nil)
		if err != nil || c.Kind != core.CallerAgent || c.Name != want || !c.IsRoot {
			t.Errorf("pid %d: caller %+v err %v", pid, c, err)
		}
	}
}

func TestResolver_WithoutProcAnUnknownPeerIsRefused(t *testing.T) {
	t.Parallel()
	r := peer.Resolver{Credentials: fixed(domain.Peer{PID: 10, UID: 501}, nil), Directory: none, DaemonPID: 1}
	_, err := r.Caller(nil)
	var refused *domain.Refusal
	if !errors.As(err, &refused) || refused.Reason != domain.ReasonNobody || !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("err %v, want a refusal of uid 501", err)
	}
}

func TestResolver_WithoutProcTheDaemonIsItself(t *testing.T) {
	t.Parallel()
	r := peer.Resolver{Credentials: fixed(domain.Peer{PID: 4242, UID: 501}, nil), Directory: none, DaemonPID: 4242}
	c, err := r.Caller(nil)
	if err != nil || c.Kind != core.CallerSystem || c.Name != "daemon" {
		t.Fatalf("caller %+v err %v", c, err)
	}
}

func TestResolver_RootInTheDaemonCgroupIsNotTheDaemon(t *testing.T) {
	t.Parallel()
	r := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{PID: 12, UID: 0}, nil), Directory: none, DaemonPID: 1}
	_, err := r.Caller(nil)
	var refused *domain.Refusal
	if !errors.As(err, &refused) || refused.Reason != domain.ReasonRootOutsideAgents {
		t.Fatalf("err %v, want uid 0 refused", err)
	}
}

func TestResolver_RefusesWithoutCredentials(t *testing.T) {
	t.Parallel()
	r := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{}, peer.ErrUnsupported), Directory: none, DaemonPID: 1}
	_, err := r.Caller(nil)
	var refused *domain.Refusal
	if !errors.As(err, &refused) || refused.Reason != domain.ReasonNoCredentials || !errors.Is(err, peer.ErrUnsupported) {
		t.Fatalf("err %v", err)
	}
	if refused.Peer != (domain.Peer{PID: -1, UID: -1}) {
		t.Errorf("peer %+v, want unknown pid and uid", refused.Peer)
	}
}

func TestResolver_RefusesWhenThePeerIsGone(t *testing.T) {
	t.Parallel()
	r := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{PID: 99, UID: 1000}, nil), Directory: none, DaemonPID: 1}
	_, err := r.Caller(nil)
	var refused *domain.Refusal
	if !errors.As(err, &refused) || refused.Reason != domain.ReasonNoCgroup || refused.Peer.PID != 99 {
		t.Fatalf("err %v, want the missing /proc entry refused", err)
	}
}

type key struct{}

type peerResult struct {
	c   core.Caller
	err error
}

func TestConnContext_StoresTheCallerOrTheRefusal(t *testing.T) {
	t.Parallel()
	store := func(ctx context.Context, c core.Caller, err error) context.Context {
		return context.WithValue(ctx, key{}, peerResult{c, err})
	}
	good := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{PID: 10}, nil), Directory: none, DaemonPID: 1}
	if got, ok := good.ConnContext(store)(context.Background(), nil).Value(key{}).(peerResult); !ok || got.err != nil || got.c.Name != "founder" {
		t.Fatalf("stored %+v", got)
	}
	bad := peer.Resolver{Proc: proc, Credentials: fixed(domain.Peer{}, peer.ErrUnsupported), Directory: none, DaemonPID: 1}
	got, ok := bad.ConnContext(store)(context.Background(), nil).Value(key{}).(peerResult)
	if !ok || !errors.Is(got.err, core.ErrForbidden) || got.c.Name != "" {
		t.Fatalf("stored %+v", got)
	}
}
