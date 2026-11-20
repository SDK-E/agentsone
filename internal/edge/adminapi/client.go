// SPDX-License-Identifier: MIT

package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// DefaultTimeout bounds one admin call.
const DefaultTimeout = 30 * time.Second

// Client calls the edge admin API.
type Client struct {
	http *http.Client
	base string
}

// NewClient returns a client for the admin socket at socketPath.
func NewClient(socketPath string) *Client {
	return &Client{
		base: "http://edge",
		http: &http.Client{Timeout: DefaultTimeout, Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		}},
	}
}

// NewHTTPClient returns a client that calls base (for example an httptest server) with hc.
func NewHTTPClient(base string, hc *http.Client) *Client { return &Client{http: hc, base: base} }

// Providers lists the providers the gateway knows.
func (c *Client) Providers(ctx context.Context) ([]Provider, error) {
	var out ProviderList
	err := c.do(ctx, http.MethodGet, "/v1/gateway/providers", nil, http.StatusOK, &out)
	return out.Providers, err
}

// AddProvider enables a provider known to the gateway.
func (c *Client) AddProvider(ctx context.Context, id string) (Provider, error) {
	var out Provider
	err := c.do(ctx, http.MethodPut, "/v1/gateway/providers/"+url.PathEscape(id), nil, http.StatusOK, &out)
	return out, err
}

// RemoveProvider disables a provider. Its key stays stored.
func (c *Client) RemoveProvider(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/gateway/providers/"+url.PathEscape(id), nil, http.StatusNoContent, nil)
}

// SetKey stores a provider key. The answer says it is set and gives its fingerprint.
func (c *Client) SetKey(ctx context.Context, provider, key string) (KeyState, error) {
	var out KeyState
	err := c.do(ctx, http.MethodPut, "/v1/gateway/providers/"+url.PathEscape(provider)+"/key",
		SetKeyRequest{Key: key}, http.StatusOK, &out)
	return out, err
}

// ClearKey removes a provider key.
func (c *Client) ClearKey(ctx context.Context, provider string) error {
	return c.do(ctx, http.MethodDelete, "/v1/gateway/providers/"+url.PathEscape(provider)+"/key", nil, http.StatusNoContent, nil)
}

// Budget returns the budget.
func (c *Client) Budget(ctx context.Context) (Budget, error) {
	var out Budget
	err := c.do(ctx, http.MethodGet, "/v1/gateway/budget", nil, http.StatusOK, &out)
	return out, err
}

// SetBudget changes the total or the period.
func (c *Client) SetBudget(ctx context.Context, r SetBudgetRequest) (Budget, error) {
	var out Budget
	err := c.do(ctx, http.MethodPatch, "/v1/gateway/budget", r, http.StatusOK, &out)
	return out, err
}

// SetDefence changes the defence slice.
func (c *Client) SetDefence(ctx context.Context, r SetDefenceRequest) (Budget, error) {
	var out Budget
	err := c.do(ctx, http.MethodPatch, "/v1/gateway/budget/defence", r, http.StatusOK, &out)
	return out, err
}

// AllowList lists the allow list.
func (c *Client) AllowList(ctx context.Context) ([]AllowEntry, error) {
	var out AllowList
	err := c.do(ctx, http.MethodGet, "/v1/net/allow-list", nil, http.StatusOK, &out)
	return out.Entries, err
}

// AddAllowed adds an allow list entry.
func (c *Client) AddAllowed(ctx context.Context, pattern, why string) (AllowEntry, error) {
	var out AllowEntry
	err := c.do(ctx, http.MethodPost, "/v1/net/allow-list", AllowEntry{Pattern: pattern, Why: why}, http.StatusCreated, &out)
	return out, err
}

// RemoveAllowed removes an allow list entry.
func (c *Client) RemoveAllowed(ctx context.Context, pattern string) error {
	return c.do(ctx, http.MethodDelete, "/v1/net/allow-list/"+url.PathEscape(pattern), nil, http.StatusNoContent, nil)
}

// EgressFrozen reports whether all egress is frozen.
func (c *Client) EgressFrozen(ctx context.Context) (bool, error) {
	var out EgressFreeze
	err := c.do(ctx, http.MethodGet, "/v1/net/freeze", nil, http.StatusOK, &out)
	return out.Frozen, err
}

func (c *Client) do(ctx context.Context, method, path string, in any, want int, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("adminapi: %s %s: %w", method, path, err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("adminapi: %s %s: %w", method, path, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("adminapi: %s %s: is the edge running? %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // read-only body; the reads below report read failures
	if resp.StatusCode != want {
		return problemOf(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
		return fmt.Errorf("adminapi: %s %s: read answer: %w", method, path, err)
	}
	return nil
}

func problemOf(resp *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return &Problem{Type: "about:blank", Title: http.StatusText(resp.StatusCode), Status: resp.StatusCode,
			Detail: "the problem details could not be read: " + err.Error()}
	}
	var p Problem
	if err := json.Unmarshal(data, &p); err != nil || p.Type == "" {
		return &Problem{Type: "about:blank", Title: http.StatusText(resp.StatusCode), Status: resp.StatusCode,
			Detail: string(bytes.TrimSpace(data))}
	}
	return &p
}

var _ error = (*Problem)(nil)
