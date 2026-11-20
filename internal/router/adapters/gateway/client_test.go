// SPDX-License-Identifier: MIT

package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/adapters/gateway"
	"github.com/SDK-E/agentsone/internal/router/app"
)

func answer(content string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}}})
	return string(b)
}

func TestComplete_SendsAQuickWeightedChatCompletion(t *testing.T) {
	t.Parallel()
	var gotReq struct {
		Model     string `json:"model"`
		Messages  []struct{ Role, Content string }
		MaxTokens int `json:"max_tokens"`
	}
	var path, auth, weight string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth, weight = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Agentsone-Weight")
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_, _ = w.Write([]byte(answer(`{"ratings":[]}`)))
	}))
	t.Cleanup(srv.Close)

	got, err := gateway.New(srv.URL, time.Second).Complete(context.Background(), "tok", app.Prompt{System: "sys", User: "usr"})
	if err != nil {
		t.Fatal(err)
	}

	if got != `{"ratings":[]}` || path != "/v1/chat/completions" || auth != "Bearer tok" || weight != "quick" {
		t.Fatalf("got %q path %q auth %q weight %q", got, path, auth, weight)
	}
	if gotReq.Model != "autoagents/one" || len(gotReq.Messages) != 2 || gotReq.Messages[0].Role != "system" || gotReq.Messages[1].Content != "usr" {
		t.Fatalf("request = %+v", gotReq)
	}
	// Quick Claude 5 and GPT-5 models think by default, and the limit covers thinking and answer.
	if gotReq.MaxTokens != 4096 {
		t.Fatalf("max_tokens = %d, want 4096: room to think before the rating", gotReq.MaxTokens)
	}
}

func TestComplete_BudgetExhaustedIsErrBudget(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "budget", http.StatusPaymentRequired)
	}))
	t.Cleanup(srv.Close)

	_, err := gateway.New(srv.URL, time.Second).Complete(context.Background(), "tok", app.Prompt{})
	if !errors.Is(err, core.ErrBudget) {
		t.Fatalf("error = %v, want ErrBudget", err)
	}
}

func TestComplete_RetriesOnceOnAServerError(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(answer("ok")))
	}))
	t.Cleanup(srv.Close)

	got, err := gateway.New(srv.URL, time.Second).Complete(context.Background(), "tok", app.Prompt{})
	if err != nil || got != "ok" || calls.Load() != 2 {
		t.Fatalf("got %q, %v after %d calls", got, err, calls.Load())
	}
}

func TestComplete_GivesUpAfterOneRetry(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	if _, err := gateway.New(srv.URL, time.Second).Complete(context.Background(), "tok", app.Prompt{}); err == nil {
		t.Fatal("no error")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestComplete_DoesNotRetryAClientError(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	if _, err := gateway.New(srv.URL, time.Second).Complete(context.Background(), "tok", app.Prompt{}); err == nil {
		t.Fatal("no error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

// The router logs the error, so it carries the status and the message of the refusal: the
// provider's own (passed back by the gateway) or the gateway's problem detail, cut short and
// without the router's token.
func TestComplete_ARefusalCarriesItsStatusAndMessage(t *testing.T) {
	t.Parallel()
	const tok = "ao1.router-token"
	tests := []struct {
		name, contentType, body, want string
		status                        int
	}{
		{"provider", "application/json", `{"error":{"message":"temperature: only the default value 1 is supported for this model.","type":"invalid_request_error"}}`,
			"gateway answered 400: temperature: only the default value 1 is supported for this model.", http.StatusBadRequest},
		{"gateway problem", "application/problem+json", `{"type":"urn:agentsone:problem:upstream-failed","detail":"gateway: every provider failed, last status 429: slow down"}`,
			"gateway answered 502: gateway: every provider failed, last status 429: slow down", http.StatusBadGateway},
		{"token echoed", "application/json", `{"error":{"message":"bad token ` + tok + `"}}`, "gateway answered 401: bad token [token]", http.StatusUnauthorized},
		{"no message", "text/plain", "nope", "gateway answered 404", http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			_, err := gateway.New(srv.URL, time.Second).Complete(context.Background(), tok, app.Prompt{})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestComplete_TimesOut(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs before srv.Close, so the stuck handler can end

	_, err := gateway.New(srv.URL, 50*time.Millisecond).Complete(context.Background(), "tok", app.Prompt{})
	if err == nil {
		t.Fatal("no error")
	}
}

func TestComplete_RejectsUnusableBodies(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"not json":   "<html>",
		"no choices": `{"choices":[]}`,
		"empty text": answer(""),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			t.Cleanup(srv.Close)
			if _, err := gateway.New(srv.URL, time.Second).Complete(context.Background(), "tok", app.Prompt{}); err == nil {
				t.Fatal("no error")
			}
		})
	}
}

func TestComplete_UnreachableGatewayFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := gateway.New(url, time.Second).Complete(context.Background(), "tok", app.Prompt{}); err == nil {
		t.Fatal("no error")
	}
}

func TestComplete_BadBaseURLFails(t *testing.T) {
	t.Parallel()
	if _, err := gateway.New("://bad", time.Second).Complete(context.Background(), "tok", app.Prompt{}); err == nil {
		t.Fatal("no error")
	}
}
