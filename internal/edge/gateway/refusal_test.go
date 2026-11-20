// SPDX-License-Identifier: MIT

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	budgetstore "github.com/SDK-E/agentsone/internal/edge/budget/adapters/sqlite"
	budgetapp "github.com/SDK-E/agentsone/internal/edge/budget/app"
	ledger "github.com/SDK-E/agentsone/internal/edge/budget/domain"
	"github.com/SDK-E/agentsone/internal/edge/gateway"
	gatewayconfig "github.com/SDK-E/agentsone/internal/edge/gateway/adapters/config"
	"github.com/SDK-E/agentsone/internal/gatewaytoken"
)

const claudeKey = "sk-ant-test-claude-key-0123456789"

// The router's rating call, as internal/router/adapters/gateway sends it.
const (
	routerPrompt = "Rate these agents for the request: plan the launch party."
	routerBody   = `{"model":"autoagents/one","messages":[` +
		`{"role":"system","content":"You route requests to agents. Answer in JSON."},` +
		`{"role":"user","content":"` + routerPrompt + `"}],` +
		`"temperature":0,"max_tokens":4096}`
)

// fakeClaude is Anthropic's OpenAI-compatible endpoint as it treats Claude 4.7 and later
// models (https://platform.claude.com/docs/en/cli-sdks-libraries/libraries/openai-sdk):
// temperature below 1 is refused, top_p other than 0.99 is refused, and so is a call that sets
// both. Errors keep the OpenAI format.
type fakeClaude struct {
	srv *httptest.Server
	// refuse, when set, is the message every call is refused with, as a 400.
	refuse string

	mu     sync.Mutex
	fields [][]string // the top-level fields of each call, sorted
	models []string
}

func newFakeClaude(t *testing.T) *fakeClaude {
	t.Helper()
	f := &fakeClaude{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeClaude) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+claudeKey {
		openAIError(w, http.StatusUnauthorized, "authentication_error", "invalid x-api-key")
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		openAIError(w, http.StatusBadRequest, "invalid_request_error", "the body is not JSON")
		return
	}
	var model string
	_ = json.Unmarshal(body["model"], &model)
	f.mu.Lock()
	f.fields = append(f.fields, slices.Sorted(func(yield func(string) bool) {
		for k := range body {
			if !yield(k) {
				return
			}
		}
	}))
	f.models = append(f.models, model)
	refuse := f.refuse
	f.mu.Unlock()
	if refuse != "" {
		openAIError(w, http.StatusBadRequest, "invalid_request_error", refuse)
		return
	}
	temp, hasTemp := number(body, "temperature")
	topP, hasTopP := number(body, "top_p")
	switch {
	case hasTemp && hasTopP:
		openAIError(w, http.StatusBadRequest, "invalid_request_error",
			"`temperature` and `top_p` cannot both be specified for this model.")
		return
	case hasTemp && temp < 1:
		openAIError(w, http.StatusBadRequest, "invalid_request_error",
			"temperature: only the default value 1 is supported for this model.")
		return
	case hasTopP && topP != 0.99:
		openAIError(w, http.StatusBadRequest, "invalid_request_error",
			"top_p: only the default value 0.99 is supported for this model.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-fake", "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": `{"ratings":[]}`}}},
		"usage": map[string]int{"prompt_tokens": 1200, "completion_tokens": 30, "total_tokens": 1230},
	})
}

func number(body map[string]json.RawMessage, key string) (float64, bool) {
	raw, ok := body[key]
	if !ok {
		return 0, false
	}
	var v float64
	_ = json.Unmarshal(raw, &v)
	return v, true
}

func openAIError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": typ, "message": msg}})
}

func (f *fakeClaude) calls() ([][]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.fields), slices.Clone(f.models)
}

// syncBuffer is a log the gateway writes from its serving goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// claudeOnly opens the gateway with the default gateway.toml, where only the Anthropic
// provider is enabled and has a key, pointed at the fake: the demo machine's setup.
func claudeOnly(t *testing.T, fake *fakeClaude, level slog.Level) (*gateway.Server, *syncBuffer, time.Time) {
	t.Helper()
	cfg := gatewayconfig.Default()
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == "anthropic" {
			cfg.Providers[i].Enabled = true
			cfg.Providers[i].OpenAIBaseURL = fake.srv.URL + "/v1"
			cfg.Providers[i].AnthropicBaseURL = fake.srv.URL + "/v1"
		}
	}
	dir := t.TempDir()
	if err := gatewayconfig.Save(filepath.Join(dir, gatewayconfig.FileName), cfg); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	ctx := context.Background()
	st, err := budgetstore.Open(ctx, filepath.Join(dir, gateway.BudgetFileName))
	if err != nil {
		t.Fatalf("budget store: %v", err)
	}
	if err := st.SaveSettings(ctx, budgetapp.Settings{Total: 10 * ledger.MicrosPerUSD}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	now := time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)
	srv, err := gateway.Open(ctx, gateway.Options{
		DataDir:    dir,
		Frozen:     notFrozen,
		Keys:       memKeys{"anthropic": claudeKey},
		Clock:      fixedClock{now},
		HTTPClient: fake.srv.Client(),
		Log:        slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: level})),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return srv, logs, now
}

func routerToken(t *testing.T, srv *gateway.Server, now time.Time) string {
	t.Helper()
	tok, err := gatewaytoken.Sign(srv.TokenKey(), gatewaytoken.Claims{
		Agent: "system:router", Kind: gatewaytoken.KindSystem, Audience: gatewaytoken.AudienceGateway,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(5 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// rate sends the router's rating call with the quick weight, as the router does.
func rate(t *testing.T, base, tok string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/v1/chat/completions",
		strings.NewReader(routerBody))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agentsone-Weight", "quick")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Errorf("read the answer: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close the answer: %v", err)
	}
	return resp.StatusCode, string(b)
}

// noSecrets fails when the log holds the prompt, the provider key or the caller's token.
func noSecrets(t *testing.T, logs, tok string) {
	t.Helper()
	for name, secret := range map[string]string{"prompt": routerPrompt, "provider key": claudeKey, "token": tok} {
		if strings.Contains(logs, secret) {
			t.Errorf("the gateway log holds the %s:\n%s", name, logs)
		}
	}
}

// The demo machine's case: the owner set only an Anthropic key, so the router's quick call goes
// to Claude Haiku 5.5 through Anthropic's OpenAI-compatible endpoint. Claude 4.7 and later
// refuse a temperature below 1, and the router sends "temperature": 0. The default
// gateway.toml says so, and the gateway drops the field instead of forwarding it.
func TestRoutersRatingCallReachesTheDefaultQuickClaudeModel(t *testing.T) {
	fake := newFakeClaude(t)
	srv, logs, now := claudeOnly(t, fake, slog.LevelInfo)
	base, stop := serve(t, srv)
	tok := routerToken(t, srv, now)

	status, body := rate(t, base, tok)
	stop()
	if status != http.StatusOK || !strings.Contains(body, "ratings") {
		t.Fatalf("rating call: status %d body %s", status, body)
	}
	fields, models := fake.calls()
	if len(models) != 1 || models[0] != "claude-haiku-5-5" {
		t.Fatalf("models called = %v, want the default quick Claude model once", models)
	}
	if want := []string{"max_tokens", "messages", "model"}; !slices.Equal(fields[0], want) {
		t.Errorf("fields sent = %v, want %v: temperature dropped", fields[0], want)
	}
	for _, want := range []string{
		`level=INFO msg="model call"`, "agent=system:router", "provider=anthropic", "model=claude-haiku-5-5",
		"tier=quick", "status=200", "tokens_in=1200", "tokens_out=30", "latency_ms=",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the per-call line misses %q:\n%s", want, logs)
		}
	}
	noSecrets(t, logs.String(), tok)
}

// A dropped field is named at debug level, never its value.
func TestADroppedFieldIsLoggedByNameAtDebug(t *testing.T) {
	fake := newFakeClaude(t)
	srv, logs, now := claudeOnly(t, fake, slog.LevelDebug)
	base, stop := serve(t, srv)
	tok := routerToken(t, srv, now)
	if status, body := rate(t, base, tok); status != http.StatusOK {
		t.Fatalf("rating call: status %d body %s", status, body)
	}
	stop()
	want := `level=DEBUG msg="dropped a request field the model refuses" provider=anthropic model=claude-haiku-5-5 field=temperature`
	if !strings.Contains(logs.String(), want) {
		t.Errorf("log misses %q:\n%s", want, logs)
	}
	noSecrets(t, logs.String(), tok)
}

// When a provider refuses a call, the gateway logs one warn line with the status, provider,
// model and the provider's own message, and the caller still gets the provider's answer.
func TestAProviderRefusalIsLoggedWithTheProvidersMessage(t *testing.T) {
	fake := newFakeClaude(t)
	// A provider may echo part of the key back; the log must not repeat it.
	fake.refuse = "messages: something the model refuses (key " + claudeKey + ")" + strings.Repeat(" more", 200)
	srv, logs, now := claudeOnly(t, fake, slog.LevelInfo)
	base, stop := serve(t, srv)
	tok := routerToken(t, srv, now)

	status, body := rate(t, base, tok)
	stop()
	if status != http.StatusBadRequest || !strings.Contains(body, "something the model refuses") {
		t.Fatalf("status %d body %s, want the provider's 400 passed back", status, body)
	}
	var refusals []string
	for line := range strings.Lines(logs.String()) {
		if strings.Contains(line, `msg="provider refused the call"`) {
			refusals = append(refusals, line)
		}
	}
	if len(refusals) != 1 {
		t.Fatalf("refusal lines = %d, want 1:\n%s", len(refusals), logs)
	}
	line := refusals[0]
	for _, want := range []string{
		"level=WARN", "status=400", "provider=anthropic", "model=claude-haiku-5-5",
		`message="messages: something the model refuses (key [provider key])`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("refusal line misses %q:\n%s", want, line)
		}
	}
	if len(line) > 700 {
		t.Errorf("refusal line is %d bytes; the provider's message must be cut short:\n%s", len(line), line)
	}
	if !strings.Contains(logs.String(), `msg="model call"`) || !strings.Contains(logs.String(), "status=400") {
		t.Errorf("the per-call line misses the refused call:\n%s", logs)
	}
	noSecrets(t, logs.String(), tok)
}
