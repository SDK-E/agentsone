// SPDX-License-Identifier: MIT

// Package providers is the gateway's adapter to model providers. It speaks
// two wire protocols: the OpenAI chat completions API (OpenAI, OpenRouter,
// Gemini's OpenAI-compatible endpoint, Mistral, Groq, Anthropic's
// OpenAI-compatible endpoint, local servers such as Ollama) and the Anthropic
// Messages API. It rewrites the caller's body for the chosen model, sends it
// with the provider key, and passes the answer back byte for byte while it
// reads the token usage out of it, streamed or not.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	gatewayapp "github.com/SDK-E/agentsone/internal/edge/gateway/app"
	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

// DefaultAnthropicVersion is sent when the caller sends no anthropic-version.
const DefaultAnthropicVersion = "2023-06-01"

// Errors returned by Call.
var (
	ErrUnknownProvider = errors.New("providers: unknown provider")
	ErrAPINotOffered   = errors.New("providers: provider does not offer this API")
	ErrBadBody         = errors.New("providers: request body is not a JSON object")
)

// Config is how to reach one provider.
type Config struct {
	ID routing.ProviderID
	// OpenAIBaseURL is the base of the OpenAI-compatible API, ending before
	// /chat/completions, such as https://api.openai.com/v1. Empty when the
	// provider does not offer it.
	OpenAIBaseURL string
	// AnthropicBaseURL is the base of the Messages API, ending before
	// /messages, such as https://api.anthropic.com/v1. Empty when not offered.
	AnthropicBaseURL string
	// MaxTokensField is the output limit field for the OpenAI API:
	// "max_tokens" (the default) or "max_completion_tokens".
	MaxTokensField string
	// KeyRequired refuses calls when no key is stored.
	KeyRequired bool
}

// Client implements gatewayapp.Upstream.
type Client struct {
	http *http.Client

	mu   sync.RWMutex
	cfgs map[routing.ProviderID]Config
}

var _ gatewayapp.Upstream = (*Client)(nil)

// New builds a client for these providers.
func New(hc *http.Client, cfgs []Config) *Client {
	c := &Client{http: hc}
	c.SetConfigs(cfgs)
	return c
}

// SetConfigs replaces the provider configs, for a reloaded config file.
func (c *Client) SetConfigs(cfgs []Config) {
	m := make(map[routing.ProviderID]Config, len(cfgs))
	for _, cfg := range cfgs {
		m[cfg.ID] = cfg
	}
	c.mu.Lock()
	c.cfgs = m
	c.mu.Unlock()
}

// NewHTTPClient returns an HTTP client for providers. headerTimeout bounds the
// wait for the answer's headers; streamed bodies may then run as long as they
// need, bounded by the caller's context.
func NewHTTPClient(headerTimeout time.Duration) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: headerTimeout,
	}}
}

// Call sends one call to its provider.
func (c *Client) Call(ctx context.Context, call gatewayapp.UpstreamCall) (*gatewayapp.UpstreamResponse, error) {
	c.mu.RLock()
	cfg, ok := c.cfgs[call.Provider]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, call.Provider)
	}
	if cfg.KeyRequired && call.Key == "" {
		return nil, fmt.Errorf("%w: %s", gatewayapp.ErrKeyRequired, call.Provider)
	}
	if call.Op == gatewayapp.OpCountTokens && call.API != routing.APIAnthropic {
		return nil, fmt.Errorf("%w: token counting needs the Messages API", ErrAPINotOffered)
	}
	var url string
	switch call.API {
	case routing.APIAnthropic:
		url = cfg.AnthropicBaseURL
	case routing.APIOpenAI:
		url = cfg.OpenAIBaseURL
	}
	if url == "" {
		return nil, fmt.Errorf("%w: %s has no %s API", ErrAPINotOffered, call.Provider, call.API)
	}
	body, err := rewrite(call, cfg)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildURL(url, call.API, call.Op), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("providers: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if call.API == routing.APIAnthropic {
		req.Header.Set("Anthropic-Version", DefaultAnthropicVersion)
		if v := call.Header.Get("Anthropic-Version"); v != "" {
			req.Header.Set("Anthropic-Version", v)
		}
		if v := call.Header.Get("Anthropic-Beta"); v != "" {
			req.Header.Set("Anthropic-Beta", v)
		}
		if call.Key != "" {
			req.Header.Set("X-Api-Key", call.Key)
		}
	} else if call.Key != "" {
		req.Header.Set("Authorization", "Bearer "+call.Key)
	}
	if call.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	// The body is handed to the caller inside the tap, which closes it.
	resp, err := c.http.Do(req) //nolint:bodyclose // closed by the caller through the returned Body
	if err != nil {
		return nil, fmt.Errorf("providers: call %s: %w", call.Provider, err)
	}
	// Count answers carry no usage; errors neither.
	tapOn := resp.StatusCode < 300 && call.Op == gatewayapp.OpGenerate
	t := newTap(resp.Body, isEventStream(resp.Header.Get("Content-Type")), tapOn)
	return &gatewayapp.UpstreamResponse{
		Status: resp.StatusCode,
		Header: resp.Header,
		Body:   t,
		Usage:  t.Usage,
	}, nil
}

func buildURL(base string, api routing.API, op gatewayapp.Op) string {
	base = strings.TrimRight(base, "/")
	if op == gatewayapp.OpCountTokens {
		return base + "/messages/count_tokens"
	}
	if api == routing.APIAnthropic {
		return base + "/messages"
	}
	return base + "/chat/completions"
}

func isEventStream(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mt == "text/event-stream"
}

// rewrite sets the real model, the output limit and, for streamed OpenAI
// calls, the request for a final usage chunk. Other fields pass unchanged.
func rewrite(call gatewayapp.UpstreamCall, cfg Config) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(call.Body, &m); err != nil || m == nil {
		return nil, ErrBadBody
	}
	m["model"] = mustJSON(call.Model)
	if call.Op == gatewayapp.OpCountTokens {
		// count_tokens takes the request without generation settings.
		delete(m, "max_tokens")
		delete(m, "stream")
		delete(m, "stream_options")
		return json.Marshal(m)
	}
	limit := mustJSON(call.MaxOutputTokens)
	if call.API == routing.APIAnthropic {
		m["max_tokens"] = limit
		return json.Marshal(m)
	}
	field := cfg.MaxTokensField
	if field == "" {
		field = "max_tokens"
	}
	delete(m, "max_tokens")
	delete(m, "max_completion_tokens")
	m[field] = limit
	if call.Stream {
		opts := map[string]json.RawMessage{}
		if raw, ok := m["stream_options"]; ok {
			if err := json.Unmarshal(raw, &opts); err != nil {
				return nil, ErrBadBody
			}
			if opts == nil { // "stream_options": null
				opts = map[string]json.RawMessage{}
			}
		}
		opts["include_usage"] = mustJSON(true)
		m["stream_options"] = mustJSON(opts)
	}
	return json.Marshal(m)
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("providers: marshal %T: %v", v, err))
	}
	return b
}
