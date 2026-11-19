// SPDX-License-Identifier: MIT

package component_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SDK-E/agentsone/internal/edge/component"
)

// journal records what fake components do, in order.
type journal struct {
	mu    sync.Mutex
	lines []string
}

func (j *journal) add(s string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lines = append(j.lines, s)
}

func (j *journal) get() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.lines...)
}

type fake struct {
	name     string
	j        *journal
	startErr error
	stopErr  error
	started  chan struct{}
	onStart  func(*component.Env)
}

func (f *fake) Name() string { return f.name }

func (f *fake) Start(_ context.Context, env *component.Env) (component.Stopper, error) {
	if f.onStart != nil {
		f.onStart(env)
	}
	if f.startErr != nil {
		f.j.add("fail " + f.name)
		return nil, f.startErr
	}
	f.j.add("start " + f.name)
	if f.started != nil {
		close(f.started)
	}
	return stopFunc(func() error {
		f.j.add("stop " + f.name)
		return f.stopErr
	}), nil
}

type stopFunc func() error

func (s stopFunc) Stop() error { return s() }

func env() *component.Env {
	return &component.Env{
		InnerAddr: netip.MustParseAddr("10.77.42.2"),
		DataDir:   "/data",
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestRun_StartsInOrderAndStopsInReverseWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	j := &journal{}
	last := make(chan struct{})
	comps := []component.Component{&fake{name: "a", j: j}, &fake{name: "b", j: j, started: last}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- component.Run(ctx, env(), comps, listen(t)) }()
	<-last
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if diff := cmp.Diff([]string{"start a", "start b", "stop b", "stop a"}, j.get()); diff != "" {
		t.Errorf("journal (-want +got):\n%s", diff)
	}
}

func TestRun_StopsWhatStartedWhenAComponentFails(t *testing.T) {
	t.Parallel()
	j := &journal{}
	boom := errors.New("boom")
	comps := []component.Component{&fake{name: "a", j: j}, &fake{name: "b", j: j, startErr: boom}, &fake{name: "c", j: j}}

	err := component.Run(t.Context(), env(), comps, listen(t))

	if !errors.Is(err, boom) {
		t.Fatalf("Run() error = %v, want boom", err)
	}
	if diff := cmp.Diff([]string{"start a", "fail b", "stop a"}, j.get()); diff != "" {
		t.Errorf("journal (-want +got):\n%s", diff)
	}
}

func TestRun_ReportsStopErrors(t *testing.T) {
	t.Parallel()
	j := &journal{}
	boom := errors.New("stuck")
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() {
		done <- component.Run(ctx, env(), []component.Component{&fake{name: "a", j: j, stopErr: boom, started: ready}}, listen(t))
	}()
	<-ready
	cancel()

	if err := <-done; !errors.Is(err, boom) {
		t.Fatalf("Run() error = %v, want stuck", err)
	}
}

func TestRun_LaterComponentsSeeWhatEarlierOnesShare(t *testing.T) {
	t.Parallel()
	j := &journal{}
	var seen component.Freezer
	ready := make(chan struct{})
	comps := []component.Component{
		&fake{name: "net", j: j, onStart: func(e *component.Env) { e.Freezer = fakeFreezer{} }},
		&fake{name: "door", j: j, started: ready, onStart: func(e *component.Env) { seen = e.Freezer }},
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- component.Run(ctx, env(), comps, listen(t)) }()
	<-ready
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if seen == nil {
		t.Fatal("door did not see the freezer the net component shared")
	}
}

func TestRun_RefusesAnEnvWithoutInnerAddress(t *testing.T) {
	t.Parallel()
	e := env()
	e.InnerAddr = netip.Addr{}
	ln := listen(t)
	if err := component.Run(t.Context(), e, nil, ln); err == nil {
		t.Fatal("Run() without an inner address succeeded")
	}
	if _, err := ln.Accept(); err == nil {
		t.Error("Run() left the admin socket open")
	}
}

func TestRun_ServesTheRoutesComponentsAdd(t *testing.T) {
	t.Parallel()
	j := &journal{}
	ln := listen(t)
	ready := make(chan struct{})
	comps := []component.Component{&fake{name: "a", j: j, started: ready, onStart: func(e *component.Env) {
		e.Admin.HandleFunc("GET /v1/a/ping", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "pong") })
	}}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- component.Run(ctx, env(), comps, ln) }()
	<-ready

	resp, err := http.Get("http://" + ln.Addr().String() + "/v1/a/ping") // #nosec G107 -- test listener
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if string(body) != "pong" {
		t.Fatalf("body = %q, want pong", body)
	}
	if late, err := http.Get("http://" + ln.Addr().String() + "/v1/a/ping"); err == nil { // #nosec G107 -- test listener
		_ = late.Body.Close()
		t.Error("admin socket still serves after Run returned")
	}
}

func TestRun_StopsWhenTheAdminSocketFails(t *testing.T) {
	t.Parallel()
	j := &journal{}
	ln := listen(t)
	comps := []component.Component{&fake{name: "a", j: j, onStart: func(*component.Env) { _ = ln.Close() }}}

	if err := component.Run(t.Context(), env(), comps, ln); err == nil {
		t.Fatal("Run() with a broken admin socket returned no error")
	}
	if diff := cmp.Diff([]string{"start a", "stop a"}, j.get()); diff != "" {
		t.Errorf("journal (-want +got):\n%s", diff)
	}
}

// listen is a stand-in for the admin socket.
func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

type fakeFreezer struct{}

func (fakeFreezer) Freeze(context.Context) error         { return nil }
func (fakeFreezer) Release(context.Context) error        { return nil }
func (fakeFreezer) Frozen(context.Context) (bool, error) { return false, nil }
