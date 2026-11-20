// SPDX-License-Identifier: MIT

// Command fakeprovider is an OpenAI-compatible model provider for the e2e scenario only. It is
// never part of a release image: e2e/compose.e2e.yaml builds it and puts it on the outer network,
// where the edge can reach it and the machine cannot.
//
// It answers every chat completion with a fixed reply and fixed token usage, so the cost the
// gateway books is known in advance. A routing prompt from the router gets a verdict that picks
// the first candidate with a fixed reason, so the e2e can tell a model pick from the router's
// text-match fallback. Each call leaves a note: the path, the model, a SHA-256 of the
// Authorization header, the header names, and whether an Agents One gateway token ("ao1.") was in
// any header or in the body. GET /seen returns the notes. The fake never keeps or returns a key
// itself, so the e2e can show what the provider saw without printing a secret.
//
// Usage:
//
//	fakeprovider serve [addr]   serve on addr (default :8080) until SIGTERM or SIGINT
//	fakeprovider probe <url>    exit 0 when GET url answers 200 (the compose health check)
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// What every answer holds. The e2e checks these exact values.
const (
	// RouteReason is the reason given for the candidate the fake picks.
	RouteReason = "Picked by the e2e fake provider."
	// OtherReason is the reason given for every other candidate.
	OtherReason = "Not picked by the e2e fake provider."
	// ChatReply is the answer to any call that is not a routing prompt.
	ChatReply = "Hello from the e2e fake provider."
	// PromptTokens and CompletionTokens are the usage every answer reports.
	PromptTokens     = 1000
	CompletionTokens = 50
)

// Kinds of call the fake notes.
const (
	KindRoute   = "route"   // the router's rating prompt
	KindChat    = "chat"    // any other chat completion
	KindRefused = "refused" // no bearer token, or a body that is not JSON
)

const (
	maxBody = 1 << 20
	// routerPrompt is how the router's system prompt starts (internal/router/app/prompt.go).
	routerPrompt = "You are a router."
	// tokenMark starts every Agents One gateway token (ao1.<claims>.<mac>).
	tokenMark = "ao1."
)

// Call is the note of one call. It holds no key and no request text.
type Call struct {
	Path   string `json:"path"`
	Model  string `json:"model"`
	Kind   string `json:"kind"`
	Stream bool   `json:"stream"`
	// AuthSHA256 is the hex SHA-256 of the whole Authorization header value, empty when none.
	AuthSHA256 string `json:"auth_sha256"`
	// Headers are the request header names, canonical and sorted.
	Headers []string `json:"headers"`
	// GatewayToken is true when an ao1. token was in any header value or in the body.
	GatewayToken bool `json:"gateway_token"`
}

// Seen is the answer of GET /seen.
type Seen struct {
	Calls []Call `json:"calls"`
}

// Provider is the fake. The zero value is ready.
type Provider struct {
	mu    sync.Mutex
	calls []Call
}

// Handler serves the provider routes.
func (p *Provider) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", p.complete)
	mux.HandleFunc("GET /seen", p.seen)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n") //nolint:errcheck // the status is already sent; a gone client has nothing to be told
	})
	return mux
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type request struct {
	Model    string    `json:"model"`
	Stream   bool      `json:"stream"`
	Messages []message `json:"messages"`
}

func (p *Provider) complete(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	call := Call{Path: r.URL.Path, Headers: headerNames(r.Header), GatewayToken: hasToken(r.Header, body)}
	if a := r.Header.Get("Authorization"); a != "" {
		sum := sha256.Sum256([]byte(a))
		call.AuthSHA256 = hex.EncodeToString(sum[:])
	}
	var req request
	if err == nil {
		err = json.Unmarshal(body, &req)
	}
	call.Model, call.Stream = req.Model, req.Stream
	switch {
	case err != nil:
		call.Kind = KindRefused
		p.note(call)
		http.Error(w, "the request body is not a JSON chat completion", http.StatusBadRequest)
		return
	case !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
		call.Kind = KindRefused
		p.note(call)
		http.Error(w, "send the provider key as a bearer token", http.StatusUnauthorized)
		return
	}
	content, routed := routeVerdict(req)
	call.Kind = KindChat
	if routed {
		call.Kind = KindRoute
	} else {
		content = ChatReply
	}
	p.note(call)
	if req.Stream {
		writeStream(w, req.Model, content)
		return
	}
	writeJSON(w, completion(req.Model, content))
}

func (p *Provider) note(c Call) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, c)
}

func (p *Provider) seen(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	out := Seen{Calls: append([]Call{}, p.calls...)}
	p.mu.Unlock()
	writeJSON(w, out)
}

func headerNames(h http.Header) []string {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, http.CanonicalHeaderKey(k))
	}
	slices.Sort(names)
	return names
}

func hasToken(h http.Header, body []byte) bool {
	for _, vs := range h {
		for _, v := range vs {
			if strings.Contains(v, tokenMark) {
				return true
			}
		}
	}
	return bytes.Contains(body, []byte(tokenMark))
}

type rating struct {
	Agent  string  `json:"agent"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

type verdict struct {
	Ratings []rating  `json:"ratings"`
	Handoff *struct{} `json:"handoff"`
}

// routeVerdict answers the router's rating prompt: the first candidate scores 1, the others 0.
// It returns false for any other call.
func routeVerdict(req request) (string, bool) {
	if len(req.Messages) < 2 || req.Messages[0].Role != "system" {
		return "", false
	}
	var system, user string
	if json.Unmarshal(req.Messages[0].Content, &system) != nil || !strings.HasPrefix(system, routerPrompt) {
		return "", false
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" || json.Unmarshal(last.Content, &user) != nil {
		return "", false
	}
	var prompt struct {
		Candidates []struct {
			Agent string `json:"agent"`
		} `json:"candidates"`
	}
	if json.Unmarshal([]byte(user), &prompt) != nil {
		return "", false
	}
	v := verdict{Ratings: []rating{}}
	for i, c := range prompt.Candidates {
		r := rating{Agent: c.Agent, Reason: OtherReason}
		if i == 0 {
			r.Score, r.Reason = 1, RouteReason
		}
		v.Ratings = append(v.Ratings, r)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

var fixedUsage = usage{PromptTokens: PromptTokens, CompletionTokens: CompletionTokens, TotalTokens: PromptTokens + CompletionTokens}

func completion(model, content string) map[string]any {
	return map[string]any{
		"id": "chatcmpl-e2e", "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{
			"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop",
		}},
		"usage": fixedUsage,
	}
}

// writeStream answers as an OpenAI event stream: the content, the stop, the usage chunk and
// [DONE].
func writeStream(w http.ResponseWriter, model, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	chunk := func(choices []any, extra map[string]any) map[string]any {
		c := map[string]any{"id": "chatcmpl-e2e", "object": "chat.completion.chunk", "created": time.Now().Unix(),
			"model": model, "choices": choices}
		for k, v := range extra {
			c[k] = v
		}
		return c
	}
	events := []map[string]any{
		chunk([]any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": content}, "finish_reason": nil}}, nil),
		chunk([]any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, nil),
		chunk([]any{}, map[string]any{"usage": fixedUsage}),
	}
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n") //nolint:errcheck // the stream is already sent; a gone client has nothing to be told
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck // the status is already sent; a gone client has nothing to be told
}

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func run(args []string, stderr io.Writer) int {
	switch {
	case len(args) >= 1 && len(args) <= 2 && args[0] == "serve":
		addr := ":8080"
		if len(args) == 2 {
			addr = args[1]
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		defer stop()
		return serve(ctx, addr, stderr)
	case len(args) == 2 && args[0] == "probe":
		return probe(args[1], stderr)
	}
	_, _ = fmt.Fprintln(stderr, "usage: fakeprovider serve [addr] | fakeprovider probe <url>") //nolint:errcheck // stderr is the last place to report; the exit code still tells the failure
	return 2
}

func serve(ctx context.Context, addr string, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, nil))
	p := &Provider{}
	srv := &http.Server{Addr: addr, Handler: p.Handler(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	log.Info("fake provider listening", "addr", addr)
	select {
	case err := <-done:
		log.Error("fake provider stopped", "error", err)
		return 1
	case <-ctx.Done():
	}
	grace, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(grace); err != nil {
		log.Error("fake provider shutdown", "error", err)
		return 1
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		log.Error("fake provider stopped", "error", err)
		return 1
	}
	log.Info("fake provider stopped")
	return 0
}

func probe(url string, stderr io.Writer) int {
	c := &http.Client{Timeout: 3 * time.Second}
	res, err := c.Get(url) //nolint:gosec // G107: the URL is the health check's own, from compose.
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "probe:", err) //nolint:errcheck // stderr is the last place to report; the exit code still tells the failure
		return 1
	}
	defer func() { _ = res.Body.Close() }() //nolint:errcheck // a read-only body has nothing to flush
	if res.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintln(stderr, "probe: status", res.StatusCode) //nolint:errcheck // stderr is the last place to report; the exit code still tells the failure
		return 1
	}
	return 0
}
