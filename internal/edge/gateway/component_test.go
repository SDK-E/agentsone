// SPDX-License-Identifier: MIT

package gateway_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/adminapi"
	budgetstore "github.com/SDK-E/agentsone/internal/edge/budget/adapters/sqlite"
	budgetapp "github.com/SDK-E/agentsone/internal/edge/budget/app"
	ledger "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	"github.com/SDK-E/agentsone/internal/edge/component"
	"github.com/SDK-E/agentsone/internal/edge/gateway"
	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
)

type switchFreezer struct {
	mu     sync.Mutex
	frozen bool
}

func (f *switchFreezer) Freeze(context.Context) error { f.set(true); return nil }
func (f *switchFreezer) Release(context.Context) error {
	f.set(false)
	return nil
}
func (f *switchFreezer) Frozen(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frozen, nil
}
func (f *switchFreezer) set(v bool) {
	f.mu.Lock()
	f.frozen = v
	f.mu.Unlock()
}

var loopback = netip.MustParseAddr("127.0.0.1")

// startComponent starts the gateway component on loopback with a fake provider and a budget.
func startComponent(t *testing.T, fr component.Freezer) (*gateway.Server, netip.AddrPort, *component.Env, component.Stopper) {
	t.Helper()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":0}}`)
	}))
	t.Cleanup(fake.Close)
	dir := t.TempDir()
	cfg := gatewayconfig.File{Limits: gatewayconfig.Default().Limits, Providers: []gatewayconfig.Provider{{
		ID: "fake", Enabled: true, OpenAIBaseURL: fake.URL + "/v1",
		Models: []gatewayconfig.Model{{ID: "fake-mid", Tier: "normal", Family: "fake", Input: "1.00", Output: "2.00"}},
	}}}
	if err := gatewayconfig.Save(filepath.Join(dir, gatewayconfig.FileName), cfg); err != nil {
		t.Fatal(err)
	}
	st, err := budgetstore.Open(t.Context(), filepath.Join(dir, gateway.BudgetFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSettings(t.Context(), budgetapp.Settings{Total: 10 * ledger.MicrosPerUSD}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	var srv *gateway.Server
	var addr netip.AddrPort
	zero := uint16(0)
	c := gateway.Component{Port: &zero, HTTPClient: fake.Client(), Keys: memKeys{},
		Started: func(s *gateway.Server, a netip.AddrPort) { srv, addr = s, a }}
	env := &component.Env{InnerAddr: loopback, DataDir: dir, Log: slog.New(slog.DiscardHandler), Admin: http.NewServeMux(), Freezer: fr}
	stopper, err := c.Start(t.Context(), env)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := stopper.Stop(); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	return srv, addr, env, stopper
}

func TestComponent_ServesModelCallsOnTheInnerAddressOnly(t *testing.T) {
	srv, addr, _, _ := startComponent(t, &switchFreezer{})

	if addr.Addr() != loopback || addr.Port() == 0 {
		t.Fatalf("gateway listens on %v, want %v", addr, loopback)
	}
	status, body := post(t, "http://"+addr.String()+"/v1/chat/completions", agentToken(t, srv, time.Now()), `{"model":"autoagents/one"}`)
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
}

// The machine reaches the inner listener, so it must serve model calls and the two reads only:
// no admin route and no write, even with a valid agent token.
func TestComponent_InnerListenerServesNoAdminRouteAndNoWrite(t *testing.T) {
	srv, addr, _, _ := startComponent(t, &switchFreezer{})
	tok := agentToken(t, srv, time.Now())
	params := strings.NewReplacer("{id}", "fake", "{pattern}", "example.com")
	type call struct{ method, path string }
	var calls []call
	for _, r := range adminapi.Routes() {
		method, path, _ := strings.Cut(r, " ")
		calls = append(calls, call{method, params.Replace(path)})
	}
	for _, path := range []string{"/v1/models", "/v1/budget"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			calls = append(calls, call{method, path})
		}
	}
	for _, path := range []string{"/v1/allocations", "/v1/budget/allocations", "/v1/budget/defence", "/v1/freeze"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
			calls = append(calls, call{method, path})
		}
	}

	for _, c := range calls {
		req, err := http.NewRequestWithContext(t.Context(), c.method, "http://"+addr.String()+c.path, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s on the inner listener = %d, want 404 or 405", c.method, c.path, resp.StatusCode)
		}
	}
}

func TestComponent_StopsModelCallsWhileFrozen(t *testing.T) {
	fr := &switchFreezer{}
	srv, addr, _, _ := startComponent(t, fr)
	url := "http://" + addr.String() + "/v1/chat/completions"

	fr.set(true)
	status, body := post(t, url, agentToken(t, srv, time.Now()), `{"model":"autoagents/one"}`)
	if status != http.StatusLocked || !strings.Contains(body, "frozen") {
		t.Fatalf("frozen: status %d body %s", status, body)
	}
	fr.set(false)
	if status, _ := post(t, url, agentToken(t, srv, time.Now()), `{"model":"autoagents/one"}`); status != http.StatusOK {
		t.Fatalf("released: status %d", status)
	}
}

func TestComponent_ServesItsAdminRoutesOnTheSharedMux(t *testing.T) {
	_, _, env, _ := startComponent(t, &switchFreezer{})
	adminSrv := httptest.NewServer(env.Admin)
	defer adminSrv.Close()

	b, err := adminapi.NewHTTPClient(adminSrv.URL, adminSrv.Client()).Budget(t.Context())

	if err != nil || b.TotalMicros != 10_000_000 {
		t.Fatalf("Budget = %+v, %v", b, err)
	}
}

func TestComponent_StopIsSafeTwiceAndClosesTheListener(t *testing.T) {
	_, addr, _, stopper := startComponent(t, &switchFreezer{})

	if err := stopper.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := stopper.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if c, err := net.DialTimeout("tcp", addr.String(), time.Second); err == nil {
		_ = c.Close()
		t.Fatal("the gateway still listens after Stop")
	}
}

func TestComponent_RefusesToStartWithoutTheFreezeSwitch(t *testing.T) {
	env := &component.Env{InnerAddr: loopback, DataDir: t.TempDir()}
	if _, err := (gateway.Component{}).Start(t.Context(), env); err == nil || !strings.Contains(err.Error(), "freeze") {
		t.Fatalf("Start without a freezer error = %v", err)
	}
}

func TestComponent_ReportsAPortItCannotTake(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	busy := netip.MustParseAddrPort(ln.Addr().String()).Port()
	env := &component.Env{InnerAddr: loopback, DataDir: t.TempDir(), Freezer: &switchFreezer{}}

	if _, err := (gateway.Component{Port: &busy, Keys: memKeys{}}).Start(t.Context(), env); err == nil {
		t.Fatal("Start on a busy port succeeded")
	}
	if (gateway.Component{}).Name() == "" {
		t.Error("Name() is empty")
	}
}
