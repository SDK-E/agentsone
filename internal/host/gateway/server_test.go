// SPDX-License-Identifier: MIT

package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/gatewaytoken"
	"github.com/SDK-E/agentsone/internal/host/budget/budgetapp"
	"github.com/SDK-E/agentsone/internal/host/budget/budgetstore"
	"github.com/SDK-E/agentsone/internal/host/budget/ledger"
	"github.com/SDK-E/agentsone/internal/host/gateway"
	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayapp"
	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayconfig"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
)

type memKeys map[routing.ProviderID]string

func (m memKeys) Get(_ context.Context, p routing.ProviderID) (string, error) {
	if k, ok := m[p]; ok {
		return k, nil
	}
	return "", gatewayapp.ErrKeyNotFound
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// setup writes a config whose only provider is a fake OpenAI-compatible
// server, sets a budget, and opens the gateway.
func setup(t *testing.T, total ledger.Micros) (*gateway.Server, string, *time.Time) {
	t.Helper()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-provider-key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":1000000,"completion_tokens":0}}`)
	}))
	t.Cleanup(fake.Close)

	dir := t.TempDir()
	cfg := gatewayconfig.File{
		Limits: gatewayconfig.Default().Limits,
		Providers: []gatewayconfig.Provider{{
			ID: "fake", Enabled: true, OpenAIBaseURL: fake.URL + "/v1", KeyRequired: true,
			Models: []gatewayconfig.Model{{ID: "fake-mid", Tier: "normal", Family: "fake", Input: "1.00", Output: "2.00"}},
		}},
	}
	if err := gatewayconfig.Save(filepath.Join(dir, gatewayconfig.FileName), cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	ctx := context.Background()
	st, err := budgetstore.Open(ctx, filepath.Join(dir, gateway.BudgetFileName))
	if err != nil {
		t.Fatalf("budget store: %v", err)
	}
	if err := st.SaveSettings(ctx, budgetapp.Settings{Total: total}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	srv, err := gateway.Open(ctx, gateway.Options{
		DataDir:    dir,
		Keys:       memKeys{"fake": "test-provider-key"},
		Clock:      fixedClock{now},
		HTTPClient: fake.Client(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return srv, dir, &now
}

func serve(t *testing.T, srv *gateway.Server) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ticks := make(chan time.Time)
	go func() { done <- srv.Run(ctx, ticks, ln) }()
	stop := func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	}
	return "http://" + ln.Addr().String(), stop
}

func agentToken(t *testing.T, srv *gateway.Server, now time.Time) string {
	t.Helper()
	tok, err := gatewaytoken.Sign(srv.TokenKey(), gatewaytoken.Claims{
		Agent: "worker", Kind: gatewaytoken.KindChat, Team: "t",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

func post(t *testing.T, url, tok, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestCallIsRoutedChargedAndFlushedOnStop(t *testing.T) {
	srv, dir, now := setup(t, 10*ledger.MicrosPerUSD)
	base, stop := serve(t, srv)
	tok := agentToken(t, srv, *now)

	status, body := post(t, base+"/v1/chat/completions", tok, `{"model":"autoagents/one","messages":[]}`)
	if status != 200 || !strings.Contains(body, "choices") {
		t.Fatalf("status %d body %s", status, body)
	}
	stop()

	// 1M input tokens at $1.00.
	st, err := budgetstore.Open(context.Background(), filepath.Join(dir, gateway.BudgetFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	sp, err := st.LoadSpend(context.Background())
	if err != nil || sp.General != ledger.MicrosPerUSD || sp.Agents["worker"] != ledger.MicrosPerUSD {
		t.Fatalf("stored spend = %+v, %v", sp, err)
	}
}

func TestBudgetWallAndAllocations(t *testing.T) {
	srv, _, now := setup(t, 0)
	base, stop := serve(t, srv)
	defer stop()
	tok := agentToken(t, srv, *now)

	status, body := post(t, base+"/v1/chat/completions", tok, `{"model":"autoagents/one"}`)
	if status != http.StatusPaymentRequired || !strings.Contains(body, "budget-exhausted") {
		t.Fatalf("status %d body %s", status, body)
	}
}

func TestSoftAllocationsFromTheDaemon(t *testing.T) {
	srv, _, now := setup(t, 10*ledger.MicrosPerUSD)
	srv.SetAllocations(map[string]ledger.Micros{"worker": 1})
	base, stop := serve(t, srv)
	defer stop()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/v1/budget", nil)
	req.Header.Set("Authorization", "Bearer "+agentToken(t, srv, *now))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var v map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	if v["remaining_micro_usd"] != float64(1) {
		t.Fatalf("budget = %v", v)
	}
}

func TestTokenKeyIsPerBootAndCopied(t *testing.T) {
	a, _, _ := setup(t, 1)
	b, _, _ := setup(t, 1)
	ka, kb := a.TokenKey(), b.TokenKey()
	if len(ka) != 32 || string(ka) == string(kb) {
		t.Fatalf("token keys not random per boot: %d bytes", len(ka))
	}
	ka[0] ^= 0xff
	if a.TokenKey()[0] == ka[0] {
		t.Fatal("TokenKey returned the internal slice")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReloadPicksUpConfigChanges(t *testing.T) {
	srv, dir, now := setup(t, 10*ledger.MicrosPerUSD)
	path := filepath.Join(dir, gatewayconfig.FileName)
	f, err := gatewayconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SetEnabled("fake", false); err != nil {
		t.Fatal(err)
	}
	if err := gatewayconfig.Save(path, f); err != nil {
		t.Fatal(err)
	}
	if err := srv.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	base, stop := serve(t, srv)
	defer stop()
	status, _ := post(t, base+"/v1/chat/completions", agentToken(t, srv, *now), `{"model":"autoagents/one"}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 after the only provider was disabled", status)
	}
}
