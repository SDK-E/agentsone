// SPDX-License-Identifier: MIT

package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/gateway/adapters/providers"
	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

// seen is what a fake provider received.
type seen struct {
	path   string
	header http.Header
	body   map[string]any
}

func fakeProvider(t *testing.T, status int, contentType, answer string) (*httptest.Server, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.body)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		// Write in small pieces so the stream parser sees split lines.
		for i := 0; i < len(answer); i += 7 {
			end := min(i+7, len(answer))
			_, _ = io.WriteString(w, answer[i:end])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func client(cfgs ...providers.Config) *providers.Client {
	return providers.New(http.DefaultClient, cfgs, nil)
}

func readAll(t *testing.T, resp *gatewayapp.UpstreamResponse) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return string(b)
}

func TestOpenAINonStreaming(t *testing.T) {
	answer := `{"id":"c1","object":"chat.completion","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150}}`
	srv, got := fakeProvider(t, 200, "application/json", answer)
	c := client(providers.Config{ID: "openai", OpenAIBaseURL: srv.URL + "/v1", MaxTokensField: "max_completion_tokens", KeyRequired: true})

	resp, err := c.Call(context.Background(), gatewayapp.UpstreamCall{
		Provider: "openai", API: routing.APIOpenAI, Model: "gpt-mid", Key: "test-key",
		Body:            []byte(`{"model":"autoagents/one","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`),
		MaxOutputTokens: 50,
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if body := readAll(t, resp); body != answer || resp.Status != 200 {
		t.Fatalf("status %d body %q", resp.Status, body)
	}
	u, ok := resp.Usage()
	if !ok || u.Input != 120 || u.Output != 30 {
		t.Fatalf("usage = %+v, %v", u, ok)
	}
	if got.path != "/v1/chat/completions" || got.header.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("request path %q auth %q", got.path, got.header.Get("Authorization"))
	}
	if got.body["model"] != "gpt-mid" || got.body["max_completion_tokens"] != float64(50) {
		t.Fatalf("rewritten body = %v", got.body)
	}
	if _, has := got.body["max_tokens"]; has {
		t.Fatalf("max_tokens left in body for a max_completion_tokens provider: %v", got.body)
	}
}

func TestOpenAIStreamingAsksForUsageAndCapturesIt(t *testing.T) {
	answer := "data: {\"choices\":[{\"delta\":{\"content\":\"Good \"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"day\"}}],\"usage\":null}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":2}}\n\n" +
		"data: [DONE]\n\n"
	srv, got := fakeProvider(t, 200, "text/event-stream", answer)
	c := client(providers.Config{ID: "groq", OpenAIBaseURL: srv.URL + "/openai/v1", KeyRequired: true})

	resp, err := c.Call(context.Background(), gatewayapp.UpstreamCall{
		Provider: "groq", API: routing.APIOpenAI, Model: "llama", Key: "k", Stream: true,
		Body:            []byte(`{"model":"autoagents/one","stream":true,"messages":[]}`),
		MaxOutputTokens: 4096,
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if body := readAll(t, resp); body != answer {
		t.Fatalf("stream not passed through unchanged: %q", body)
	}
	u, ok := resp.Usage()
	if !ok || u.Input != 11 || u.Output != 2 {
		t.Fatalf("usage = %+v, %v", u, ok)
	}
	opts, _ := got.body["stream_options"].(map[string]any)
	if opts["include_usage"] != true || got.body["max_tokens"] != float64(4096) {
		t.Fatalf("rewritten body = %v", got.body)
	}
}

func TestOpenAIStreamingAsksForUsageWhenStreamOptionsIsNull(t *testing.T) {
	srv, got := fakeProvider(t, 200, "text/event-stream", "data: [DONE]\n\n")
	c := client(providers.Config{ID: "groq", OpenAIBaseURL: srv.URL + "/openai/v1", KeyRequired: true})

	resp, err := c.Call(context.Background(), gatewayapp.UpstreamCall{
		Provider: "groq", API: routing.APIOpenAI, Model: "llama", Key: "k", Stream: true,
		Body:            []byte(`{"model":"autoagents/one","stream":true,"stream_options":null,"messages":[]}`),
		MaxOutputTokens: 4096,
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	readAll(t, resp)
	opts, _ := got.body["stream_options"].(map[string]any)
	if opts["include_usage"] != true {
		t.Fatalf("rewritten body = %v, want stream_options.include_usage true", got.body)
	}
}

func TestAnthropicNonStreaming(t *testing.T) {
	answer := `{"id":"msg_1","type":"message","content":[],"usage":{"input_tokens":200,"output_tokens":40,"cache_creation_input_tokens":10,"cache_read_input_tokens":5}}`
	srv, got := fakeProvider(t, 200, "application/json", answer)
	c := client(providers.Config{ID: "anthropic", AnthropicBaseURL: srv.URL + "/v1", KeyRequired: true})

	resp, err := c.Call(context.Background(), gatewayapp.UpstreamCall{
		Provider: "anthropic", API: routing.APIAnthropic, Model: "claude-sonnet-5-5", Key: "test-key",
		Body:            []byte(`{"model":"autoagents/one","max_tokens":100,"messages":[]}`),
		MaxOutputTokens: 100,
		Header:          http.Header{"Anthropic-Beta": {"some-beta"}, "Authorization": {"Bearer agent-token"}},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = readAll(t, resp)
	u, ok := resp.Usage()
	if !ok || u.Input != 200 || u.Output != 40 || u.CacheWrite != 10 || u.CacheRead != 5 {
		t.Fatalf("usage = %+v, %v", u, ok)
	}
	if got.path != "/v1/messages" || got.header.Get("X-Api-Key") != "test-key" ||
		got.header.Get("Anthropic-Version") != providers.DefaultAnthropicVersion ||
		got.header.Get("Anthropic-Beta") != "some-beta" || got.header.Get("Authorization") != "" {
		t.Fatalf("request path %q headers %v", got.path, got.header)
	}
	if got.body["model"] != "claude-sonnet-5-5" || got.body["max_tokens"] != float64(100) {
		t.Fatalf("rewritten body = %v", got.body)
	}
}

func TestAnthropicStreamingCapturesStartAndDeltaUsage(t *testing.T) {
	answer := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":25,\"output_tokens\":1,\"cache_read_input_tokens\":3}}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":15}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"
	srv, got := fakeProvider(t, 200, "text/event-stream; charset=utf-8", answer)
	c := client(providers.Config{ID: "anthropic", AnthropicBaseURL: srv.URL + "/v1", KeyRequired: true})

	resp, err := c.Call(context.Background(), gatewayapp.UpstreamCall{
		Provider: "anthropic", API: routing.APIAnthropic, Model: "claude-haiku-5-5", Key: "k", Stream: true,
		Body:            []byte(`{"model":"autoagents/one:quick","stream":true,"messages":[]}`),
		MaxOutputTokens: 4096,
		Header:          http.Header{"Anthropic-Version": {"2026-10-21"}},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if body := readAll(t, resp); body != answer {
		t.Fatalf("stream changed: %q", body)
	}
	u, ok := resp.Usage()
	if !ok || u.Input != 25 || u.Output != 15 || u.CacheRead != 3 {
		t.Fatalf("usage = %+v, %v", u, ok)
	}
	if got.header.Get("Anthropic-Version") != "2026-10-21" || got.body["max_tokens"] != float64(4096) {
		t.Fatalf("headers %v body %v", got.header, got.body)
	}
	if _, has := got.body["stream_options"]; has {
		t.Fatal("stream_options sent to the Anthropic Messages API")
	}
}

func TestAnthropicCountTokens(t *testing.T) {
	srv, got := fakeProvider(t, 200, "application/json", `{"input_tokens":1234}`)
	c := client(providers.Config{ID: "anthropic", AnthropicBaseURL: srv.URL + "/v1", KeyRequired: true})
	resp, err := c.Call(context.Background(), gatewayapp.UpstreamCall{
		Op: gatewayapp.OpCountTokens, Provider: "anthropic", API: routing.APIAnthropic, Model: "claude-sonnet-5-5", Key: "k",
		Body:   []byte(`{"model":"autoagents/one","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`),
		Header: http.Header{"Anthropic-Beta": {"token-counting"}},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if body := readAll(t, resp); body != `{"input_tokens":1234}` {
		t.Fatalf("body = %q", body)
	}
	if got.path != "/v1/messages/count_tokens" || got.header.Get("X-Api-Key") != "k" || got.header.Get("Anthropic-Beta") != "token-counting" {
		t.Fatalf("path %q headers %v", got.path, got.header)
	}
	if got.body["model"] != "claude-sonnet-5-5" {
		t.Fatalf("model not rewritten: %v", got.body)
	}
	for _, field := range []string{"max_tokens", "stream", "stream_options"} {
		if _, has := got.body[field]; has {
			t.Fatalf("%s sent to count_tokens: %v", field, got.body)
		}
	}
	if _, ok := resp.Usage(); ok {
		t.Fatal("a count answer reported usage")
	}
}

func TestCountTokensNeedsTheMessagesAPI(t *testing.T) {
	c := client(providers.Config{ID: "openai-only", OpenAIBaseURL: "http://127.0.0.1:1"})
	_, err := c.Call(context.Background(), gatewayapp.UpstreamCall{
		Op: gatewayapp.OpCountTokens, Provider: "openai-only", API: routing.APIOpenAI, Body: []byte(`{}`),
	})
	if !errors.Is(err, providers.ErrAPINotOffered) {
		t.Fatalf("error = %v, want ErrAPINotOffered", err)
	}
}

func TestUsageUnknownWhenStreamCutShort(t *testing.T) {
	answer := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	srv, _ := fakeProvider(t, 200, "text/event-stream", answer)
	c := client(providers.Config{ID: "x", OpenAIBaseURL: srv.URL})
	resp, err := c.Call(context.Background(), gatewayapp.UpstreamCall{
		Provider: "x", API: routing.APIOpenAI, Model: "m", Stream: true, Body: []byte(`{}`), MaxOutputTokens: 10,
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_ = readAll(t, resp)
	if _, ok := resp.Usage(); ok {
		t.Fatal("usage reported for a stream without a usage chunk")
	}
}

func TestErrorStatusIsReturnedWithBody(t *testing.T) {
	srv, _ := fakeProvider(t, 429, "application/json", `{"error":{"message":"slow down"}}`)
	c := client(providers.Config{ID: "x", OpenAIBaseURL: srv.URL})
	resp, err := c.Call(context.Background(), gatewayapp.UpstreamCall{
		Provider: "x", API: routing.APIOpenAI, Model: "m", Body: []byte(`{}`), MaxOutputTokens: 10,
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Status != 429 || !strings.Contains(readAll(t, resp), "slow down") {
		t.Fatalf("status = %d", resp.Status)
	}
	if _, ok := resp.Usage(); ok {
		t.Fatal("usage reported for an error answer")
	}
}

func TestCallRefusals(t *testing.T) {
	c := client(
		providers.Config{ID: "needs-key", OpenAIBaseURL: "http://127.0.0.1:1", KeyRequired: true},
		providers.Config{ID: "openai-only", OpenAIBaseURL: "http://127.0.0.1:1"},
	)
	ctx := context.Background()
	tests := []struct {
		name string
		call gatewayapp.UpstreamCall
		want error
	}{
		{"no key", gatewayapp.UpstreamCall{Provider: "needs-key", API: routing.APIOpenAI, Body: []byte(`{}`)}, gatewayapp.ErrKeyRequired},
		{"unknown provider", gatewayapp.UpstreamCall{Provider: "nope", API: routing.APIOpenAI, Body: []byte(`{}`)}, providers.ErrUnknownProvider},
		{"API not offered", gatewayapp.UpstreamCall{Provider: "openai-only", API: routing.APIAnthropic, Body: []byte(`{}`)}, providers.ErrAPINotOffered},
		{"body not JSON", gatewayapp.UpstreamCall{Provider: "openai-only", API: routing.APIOpenAI, Body: []byte(`[1`)}, providers.ErrBadBody},
		{"stream options not an object", gatewayapp.UpstreamCall{
			Provider: "openai-only", API: routing.APIOpenAI, Stream: true, Body: []byte(`{"stream":true,"stream_options":true}`),
		}, providers.ErrBadBody},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Call(ctx, tc.call); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestFieldsTheModelRefusesAreDroppedForThatModelOnly(t *testing.T) {
	srv, got := fakeProvider(t, 200, "application/json", `{"choices":[]}`)
	var logs strings.Builder
	c := providers.New(http.DefaultClient, []providers.Config{{
		ID: "anthropic", OpenAIBaseURL: srv.URL + "/v1", AnthropicBaseURL: srv.URL + "/v1",
		DropParams: map[string][]string{"claude-quick": {"temperature", "top_p", "top_k"}},
	}}, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	ctx := context.Background()
	body := []byte(`{"model":"autoagents/one","messages":[],"temperature":0,"top_p":0.5,"seed":7}`)

	for _, api := range []routing.API{routing.APIOpenAI, routing.APIAnthropic} {
		resp, err := c.Call(ctx, gatewayapp.UpstreamCall{Provider: "anthropic", API: api, Model: "claude-quick", Body: body, MaxOutputTokens: 600})
		if err != nil {
			t.Fatalf("%s Call: %v", api, err)
		}
		readAll(t, resp)
		for _, field := range []string{"temperature", "top_p"} {
			if _, has := got.body[field]; has {
				t.Errorf("%s: %s forwarded to a model that refuses it: %v", api, field, got.body)
			}
		}
		if got.body["seed"] != float64(7) || got.body["max_tokens"] != float64(600) {
			t.Errorf("%s: other fields changed: %v", api, got.body)
		}
	}
	// A model without the list gets the fields.
	resp, err := c.Call(ctx, gatewayapp.UpstreamCall{Provider: "anthropic", API: routing.APIOpenAI, Model: "other", Body: body})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	readAll(t, resp)
	if got.body["temperature"] != float64(0) || got.body["top_p"] != 0.5 {
		t.Errorf("fields dropped for a model that takes them: %v", got.body)
	}
	if n := strings.Count(logs.String(), `msg="dropped a request field the model refuses"`); n != 4 {
		t.Errorf("drop lines = %d, want 4 (two fields, two calls):\n%s", n, logs.String())
	}
	if strings.Contains(logs.String(), "0.5") {
		t.Errorf("a dropped field's value is logged:\n%s", logs.String())
	}
}

func TestARefusalKeepsItsBodyAndGivesTheProvidersMessage(t *testing.T) {
	tests := []struct{ name, answer, want string }{
		{"openai shape", `{"error":{"message":"Unsupported value: 'temperature'","type":"invalid_request_error"}}`, "Unsupported value: 'temperature'"},
		{"anthropic shape", `{"type":"error","error":{"type":"invalid_request_error","message":"temperature: bad"}}`, "temperature: bad"},
		{"problem", `{"type":"urn:x","detail":"no such model"}`, "no such model"},
		{"plain text", "upstream\n  exploded   badly", "upstream exploded badly"},
		{"key echoed", `{"error":{"message":"Incorrect API key provided: test-key"}}`, "Incorrect API key provided: [provider key]"},
		{"long", `{"error":{"message":"` + strings.Repeat("x", 400) + `"}}`, strings.Repeat("x", 300) + "…"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeProvider(t, http.StatusBadRequest, "application/json", tc.answer)
			var logs strings.Builder
			c := providers.New(http.DefaultClient, []providers.Config{{ID: "p", OpenAIBaseURL: srv.URL}},
				slog.New(slog.NewTextHandler(&logs, nil)))
			resp, err := c.Call(context.Background(), gatewayapp.UpstreamCall{
				Provider: "p", API: routing.APIOpenAI, Model: "m", Key: "test-key", Body: []byte(`{}`),
			})
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if body := readAll(t, resp); body != tc.answer {
				t.Errorf("body = %q, want the provider's answer unchanged", body)
			}
			if resp.Refusal != tc.want {
				t.Errorf("Refusal = %q, want %q", resp.Refusal, tc.want)
			}
			line := logs.String()
			if strings.Count(line, "\n") != 1 || !strings.Contains(line, `level=WARN msg="provider refused the call" status=400 provider=p model=m`) {
				t.Errorf("log = %q, want one warn line", line)
			}
			if strings.Contains(line, "test-key") {
				t.Errorf("the provider key is logged: %q", line)
			}
		})
	}
}

func TestAnAnswerIsNotARefusal(t *testing.T) {
	srv, _ := fakeProvider(t, http.StatusOK, "application/json", `{"error":{"message":"not an error"}}`)
	var logs strings.Builder
	c := providers.New(http.DefaultClient, []providers.Config{{ID: "p", OpenAIBaseURL: srv.URL}},
		slog.New(slog.NewTextHandler(&logs, nil)))
	resp, err := c.Call(context.Background(), gatewayapp.UpstreamCall{Provider: "p", API: routing.APIOpenAI, Model: "m", Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	readAll(t, resp)
	if resp.Refusal != "" || logs.Len() != 0 {
		t.Errorf("a 200 answer was treated as a refusal: %q %q", resp.Refusal, logs.String())
	}
}

func TestTransportFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := client(providers.Config{ID: "x", OpenAIBaseURL: url})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, gatewayapp.UpstreamCall{Provider: "x", API: routing.APIOpenAI, Body: []byte(`{}`)}); err == nil {
		t.Fatal("Call to a closed server succeeded")
	}
}
