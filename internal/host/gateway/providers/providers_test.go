// SPDX-License-Identifier: MIT

package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/host/gateway/gatewayapp"
	"github.com/SDK-E/agentsone/internal/host/gateway/providers"
	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
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
	return providers.New(http.DefaultClient, cfgs)
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
	answer := "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}],\"usage\":null}\n\n" +
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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Call(ctx, tc.call); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
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
