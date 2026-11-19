// SPDX-License-Identifier: MIT

package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/adminapi"
	budgetstore "github.com/SDK-E/agentsone/internal/edge/budget/adapters/sqlite"
	budgetapp "github.com/SDK-E/agentsone/internal/edge/budget/app"
	ledger "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	"github.com/SDK-E/agentsone/internal/edge/gateway"
	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

type memKeys map[routing.ProviderID]string

func (m memKeys) Get(_ context.Context, p routing.ProviderID) (string, error) {
	if k, ok := m[p]; ok {
		return k, nil
	}
	return "", gatewayapp.ErrKeyNotFound
}

func (m memKeys) Has(_ context.Context, p routing.ProviderID) (bool, error) {
	_, ok := m[p]
	return ok, nil
}

func (m memKeys) Set(_ context.Context, p routing.ProviderID, k string) error {
	m[p] = k
	return nil
}

func (m memKeys) Delete(_ context.Context, p routing.ProviderID) error {
	delete(m, p)
	return nil
}

func notFrozen(context.Context) (bool, error) { return false, nil }

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
		Frozen:     notFrozen,
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
		Agent: "worker", Kind: gatewaytoken.KindChat, Chart: "founder/worker",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(10 * time.Minute).Unix(),
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

func TestAnthropicStreamIsChargedFromItsUsageEvents(t *testing.T) {
	stream := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":500000,\"output_tokens\":1}}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":100000}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "test-claude-key" {
			http.Error(w, "wrong call", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer fake.Close()

	dir := t.TempDir()
	cfg := gatewayconfig.File{
		Limits: gatewayconfig.Default().Limits,
		Providers: []gatewayconfig.Provider{
			{
				ID: "anthropic", Enabled: true, AnthropicBaseURL: fake.URL + "/v1", KeyRequired: true,
				Models: []gatewayconfig.Model{{ID: "claude-sonnet-5-5", Tier: "normal", Family: "claude", Input: "2", Output: "10"}},
			},
			{
				// Cheaper, but not Claude: the Anthropic endpoint must not use it.
				ID: "cheap", Enabled: true, AnthropicBaseURL: fake.URL + "/v1", KeyRequired: false,
				Models: []gatewayconfig.Model{{ID: "cheap-model", Tier: "normal", Family: "other", Input: "0", Output: "0"}},
			},
		},
	}
	if err := gatewayconfig.Save(filepath.Join(dir, gatewayconfig.FileName), cfg); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := budgetstore.Open(ctx, filepath.Join(dir, gateway.BudgetFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSettings(ctx, budgetapp.Settings{Total: 10 * ledger.MicrosPerUSD}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	srv, err := gateway.Open(ctx, gateway.Options{
		DataDir: dir, Frozen: notFrozen, Keys: memKeys{"anthropic": "test-claude-key"}, Clock: fixedClock{now}, HTTPClient: fake.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	base, stop := serve(t, srv)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/anthropic/v1/messages",
		strings.NewReader(`{"model":"autoagents/one","max_tokens":200000,"stream":true,"messages":[]}`))
	req.Header.Set("X-Api-Key", agentToken(t, srv, now))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != stream || resp.Header.Get("X-Agentsone-Model") != "anthropic/claude-sonnet-5-5" {
		t.Fatalf("status %d model %q body %q", resp.StatusCode, resp.Header.Get("X-Agentsone-Model"), body)
	}
	stop()

	st, err = budgetstore.Open(ctx, filepath.Join(dir, gateway.BudgetFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	sp, _ := st.LoadSpend(ctx)
	// 500k input at $2 = $1.00, 100k output at $10 = $1.00.
	if sp.General != 2*ledger.MicrosPerUSD {
		t.Fatalf("spent = %v, want $2.00", sp.General)
	}
}

func TestCountTokensWorksWithNoBudget(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" || r.Header.Get("X-Api-Key") != "test-claude-key" {
			http.Error(w, "wrong call", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"input_tokens":321}`)
	}))
	defer fake.Close()
	dir := t.TempDir()
	cfg := gatewayconfig.File{
		Limits: gatewayconfig.Default().Limits,
		Providers: []gatewayconfig.Provider{{
			ID: "anthropic", Enabled: true, AnthropicBaseURL: fake.URL + "/v1", KeyRequired: true,
			Models: []gatewayconfig.Model{{ID: "claude-haiku-5-5", Tier: "quick", Family: "claude", Input: "0.10", Output: "0.50"}},
		}},
	}
	if err := gatewayconfig.Save(filepath.Join(dir, gatewayconfig.FileName), cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	srv, err := gateway.Open(context.Background(), gateway.Options{
		DataDir: dir, Frozen: notFrozen, Keys: memKeys{"anthropic": "test-claude-key"}, Clock: fixedClock{now}, HTTPClient: fake.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	base, stop := serve(t, srv)
	defer stop()
	status, body := post(t, base+"/anthropic/v1/messages/count_tokens", agentToken(t, srv, now),
		`{"model":"autoagents/one","messages":[{"role":"user","content":"hi"}]}`)
	if status != 200 || body != `{"input_tokens":321}` {
		t.Fatalf("status %d body %q", status, body)
	}
}

func TestKeySetThroughTheAdminRoutesIsUsedByCalls(t *testing.T) {
	srv, _, now := setup(t, 10*ledger.MicrosPerUSD)
	mux := http.NewServeMux()
	srv.RegisterAdmin(mux)
	adminSrv := httptest.NewServer(mux)
	defer adminSrv.Close()
	c := adminapi.NewHTTPClient(adminSrv.URL, adminSrv.Client())
	base, stop := serve(t, srv)
	defer stop()

	// Replace the key with a wrong one: the provider refuses it.
	if _, err := c.SetKey(t.Context(), "fake", "wrong-key"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	if status, _ := post(t, base+"/v1/chat/completions", agentToken(t, srv, *now),
		`{"model":"autoagents/one","messages":[]}`); status == 200 {
		t.Fatal("call with a wrong key succeeded")
	}
	if _, err := c.SetKey(t.Context(), "nope", "k"); !adminapi.IsProblem(err, adminapi.SlugNotFound) {
		t.Fatalf("unknown provider error = %v", err)
	}
}

func TestRemovingTheProviderThroughTheAdminRoutesAppliesAtOnce(t *testing.T) {
	srv, _, now := setup(t, 10*ledger.MicrosPerUSD)
	mux := http.NewServeMux()
	srv.RegisterAdmin(mux)
	adminSrv := httptest.NewServer(mux)
	defer adminSrv.Close()
	base, stop := serve(t, srv)
	defer stop()

	if err := adminapi.NewHTTPClient(adminSrv.URL, adminSrv.Client()).RemoveProvider(t.Context(), "fake"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}

	if status, _ := post(t, base+"/v1/chat/completions", agentToken(t, srv, *now), `{"model":"autoagents/one"}`); status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 at once after the only provider was removed", status)
	}
}

func TestBudgetThroughTheAdminRoutes(t *testing.T) {
	srv, _, _ := setup(t, 10*ledger.MicrosPerUSD)
	defer func() { _ = srv.Close() }()
	mux := http.NewServeMux()
	srv.RegisterAdmin(mux)
	adminSrv := httptest.NewServer(mux)
	defer adminSrv.Close()
	c := adminapi.NewHTTPClient(adminSrv.URL, adminSrv.Client())
	total := int64(3_000_000)

	got, err := c.SetBudget(t.Context(), adminapi.SetBudgetRequest{TotalMicros: &total})

	if err != nil || got.TotalMicros != total || got.Period != "month" {
		t.Fatalf("SetBudget = %+v, %v", got, err)
	}
}

func TestDefaultKeyStoreIsTheKeysFolder(t *testing.T) {
	dir := t.TempDir()
	srv, err := gateway.Open(context.Background(), gateway.Options{DataDir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = srv.Close() }()
	info, err := os.Stat(filepath.Join(dir, "keys"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("keys folder = %v, %v", info, err)
	}
}

func TestModelCallsAreRefusedWithoutAFreezeSwitch(t *testing.T) {
	dir := t.TempDir()
	srv, err := gateway.Open(context.Background(), gateway.Options{DataDir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	base, stop := serve(t, srv)
	defer stop()

	status, body := post(t, base+"/v1/chat/completions", agentToken(t, srv, time.Now()), `{"model":"autoagents/one"}`)

	if status != http.StatusLocked || !strings.Contains(body, "urn:agentsone:problem:frozen") {
		t.Fatalf("status %d body %s, want 423 frozen", status, body)
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

func TestTokenKeyIsKeptAcrossStartsAndCopied(t *testing.T) {
	a, dir, _ := setup(t, 1)
	other, _, _ := setup(t, 1)
	ka := a.TokenKey()
	if len(ka) != 32 || string(ka) == string(other.TokenKey()) {
		t.Fatalf("token keys not random per install: %d bytes", len(ka))
	}
	ka[0] ^= 0xff
	if a.TokenKey()[0] == ka[0] {
		t.Fatal("TokenKey returned the internal slice")
	}
	ka[0] ^= 0xff
	for _, s := range []*gateway.Server{a, other} {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(filepath.Join(dir, "gateway-key", "key"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("gateway-key/key = %v, %v; want a 0600 file", info, err)
	}

	again, err := gateway.Open(context.Background(), gateway.Options{DataDir: dir, Keys: memKeys{}})
	if err != nil {
		t.Fatalf("Open again: %v", err)
	}
	defer func() { _ = again.Close() }()
	if string(again.TokenKey()) != string(ka) {
		t.Fatal("a restart made a new token key; tokens the daemon issued would stop working")
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
