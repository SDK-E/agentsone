// SPDX-License-Identifier: MIT

package freeze_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"

	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/freeze"
)

type filter struct {
	mu     sync.Mutex
	frozen bool
}

func (f *filter) Freeze(context.Context) error  { f.set(true); return nil }
func (f *filter) Release(context.Context) error { f.set(false); return nil }
func (f *filter) Frozen(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frozen, nil
}

func (f *filter) set(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frozen = v
}

type proofs struct {
	mu  sync.Mutex
	got []component.Proof
}

func (p *proofs) RecordProof(_ context.Context, pr component.Proof) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, pr)
	return nil
}

func newEnv(t *testing.T, dataDir string, f *filter, p *proofs) *component.Env {
	t.Helper()
	env := &component.Env{
		InnerAddr: netip.MustParseAddr("10.77.42.2"), DataDir: dataDir,
		Log: slog.New(slog.DiscardHandler), Admin: http.NewServeMux(), Freezer: f,
	}
	if p != nil {
		env.Proof = p
	}
	return env
}

func start(t *testing.T, env *component.Env) {
	t.Helper()
	stop, err := freeze.Component{}.Start(t.Context(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := stop.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
}

func call(t *testing.T, h http.Handler, method, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, nil))
	return rec.Code
}

func TestComponent_NeedsTheFilterFirst(t *testing.T) {
	t.Parallel()
	env := newEnv(t, t.TempDir(), nil, nil)
	env.Freezer = nil
	if _, err := (freeze.Component{}).Start(t.Context(), env); !errors.Is(err, freeze.ErrNoFilter) {
		t.Fatalf("Start without filter = %v, want ErrNoFilter", err)
	}
}

func TestComponent_FreezeAllCutsEgressGatesModelsAndRecordsProof(t *testing.T) {
	t.Parallel()
	f, p := &filter{}, &proofs{}
	env := newEnv(t, t.TempDir(), f, p)
	start(t, env)
	gateway := freeze.GateModelCalls(env.Freezer, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	if got := call(t, env.Admin, http.MethodPost, "/v1/freeze"); got != http.StatusOK {
		t.Fatalf("POST /v1/freeze = %d", got)
	}
	if on, _ := f.Frozen(t.Context()); !on {
		t.Fatal("egress not cut")
	}
	if got := call(t, gateway, http.MethodPost, "/v1/chat/completions"); got != http.StatusLocked {
		t.Fatalf("model call while frozen = %d, want 423", got)
	}
	if len(p.got) != 1 || p.got[0].Action != "freeze.on" || p.got[0].Target != "machine:all" {
		t.Fatalf("proofs = %+v, want one freeze.on on machine:all", p.got)
	}

	if got := call(t, env.Admin, http.MethodDelete, "/v1/freeze"); got != http.StatusOK {
		t.Fatalf("DELETE /v1/freeze = %d", got)
	}
	if got := call(t, gateway, http.MethodPost, "/v1/chat/completions"); got != http.StatusOK {
		t.Fatalf("model call after release = %d, want 200", got)
	}
}

func TestComponent_StaysFrozenAcrossRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := newEnv(t, dir, &filter{}, nil)
	start(t, first)
	if got := call(t, first.Admin, http.MethodPost, "/v1/freeze"); got != http.StatusOK {
		t.Fatalf("POST /v1/freeze = %d", got)
	}

	// A restarted edge: a fresh filter that knows nothing, the same data folder.
	f := &filter{}
	second := newEnv(t, dir, f, nil)
	start(t, second)
	if on, _ := f.Frozen(t.Context()); !on {
		t.Fatal("egress open after restart")
	}
	if on, _ := second.Freezer.Frozen(t.Context()); !on {
		t.Fatal("switch open after restart")
	}
}
