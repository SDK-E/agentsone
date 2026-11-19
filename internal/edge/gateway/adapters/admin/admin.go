// SPDX-License-Identifier: MIT

// Package admin serves the edge's admin socket: a unix socket, mode 0600,
// that only the control container mounts. It is the only way to write
// provider keys.
//
//	GET    /v1/keys             which providers have a key (never the keys)
//	PUT    /v1/keys/{provider}  store a key, sent as the request body
//	DELETE /v1/keys/{provider}  remove a key
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"

	routing "github.com/SDK-E/agentsone/internal/edge/gateway/domain"
)

// maxKeyBytes bounds a key request body.
const maxKeyBytes = 8 << 10

// ErrUnknownProvider is returned for a provider not in the gateway config.
var ErrUnknownProvider = errors.New("admin: unknown provider")

// Keys is the key store the socket writes to.
type Keys interface {
	Has(ctx context.Context, p routing.ProviderID) (bool, error)
	Set(ctx context.Context, p routing.ProviderID, key string) error
	Delete(ctx context.Context, p routing.ProviderID) error
}

// Options configure the handler.
type Options struct {
	Keys Keys
	// Providers lists the provider ids in the gateway config.
	Providers func() []string
}

// New returns the admin handler.
func New(o Options) http.Handler {
	known := func(id string) bool { return slices.Contains(o.Providers(), id) }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/keys", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]bool{}
		for _, id := range o.Providers() {
			has, err := o.Keys.Has(r.Context(), routing.ProviderID(id))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			out[id] = has
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": out})
	})
	mux.HandleFunc("PUT /v1/keys/{provider}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("provider")
		if !known(id) {
			http.Error(w, "unknown provider", http.StatusNotFound)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxKeyBytes))
		if err != nil {
			http.Error(w, "key too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err := o.Keys.Set(r.Context(), routing.ProviderID(id), strings.TrimSpace(string(raw))); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /v1/keys/{provider}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("provider")
		if !known(id) {
			http.Error(w, "unknown provider", http.StatusNotFound)
			return
		}
		if err := o.Keys.Delete(r.Context(), routing.ProviderID(id)); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// Listen opens the admin socket at path with mode 0600, replacing a stale
// socket file left by an earlier run.
func Listen(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("admin: remove stale socket: %w", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		return nil, fmt.Errorf("admin: listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("admin: socket mode: %w", err), ln.Close())
	}
	return ln, nil
}

// Client talks to the admin socket, for the control container's commands.
type Client struct {
	http *http.Client
}

// NewClient returns a client for the socket at path.
func NewClient(path string) *Client {
	return &Client{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://edge"+path, body)
	if err != nil {
		return nil, fmt.Errorf("admin: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("admin: %s %s: %w", method, path, err)
	}
	return resp, nil
}

func answerError(resp *http.Response) error {
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", ErrUnknownProvider, strings.TrimSpace(string(msg)))
	}
	return fmt.Errorf("admin: %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
}

// SetKey stores a provider key.
func (c *Client) SetKey(ctx context.Context, provider, key string) error {
	resp, err := c.do(ctx, http.MethodPut, "/v1/keys/"+provider, strings.NewReader(key))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return answerError(resp)
	}
	return nil
}

// DeleteKey removes a provider key.
func (c *Client) DeleteKey(ctx context.Context, provider string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/v1/keys/"+provider, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return answerError(resp)
	}
	return nil
}

// Keys reports which providers have a key.
func (c *Client) Keys(ctx context.Context) (map[string]bool, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/keys", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, answerError(resp)
	}
	var out struct {
		Keys map[string]bool `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("admin: read key list: %w", err)
	}
	return out.Keys, nil
}
