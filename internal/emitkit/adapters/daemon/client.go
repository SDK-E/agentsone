// SPDX-License-Identifier: MIT

// Package daemon publishes events to agentsoned over its Unix socket. The
// daemon knows the caller by the socket peer credentials, so a source sends
// no token.
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/SDK-E/agentsone/internal/emitkit/app"
	"github.com/SDK-E/agentsone/internal/emitkit/domain"
)

// DefaultSocket is where agentsoned listens for the machine's own units.
const DefaultSocket = "/run/agentsone/api.sock"

const (
	requestTimeout = 10 * time.Second
	maxReason      = 4 << 10
)

// Client is an app.Publisher for one emitter.
type Client struct {
	http   *http.Client
	source string
}

var _ app.Publisher = (*Client)(nil)

// New returns a client that publishes as the emitter named emitter, over the
// socket at path.
func New(socket, emitter string) *Client {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}
	return &Client{
		http:   &http.Client{Timeout: requestTimeout, Transport: &http.Transport{DialContext: dial}},
		source: "/emitters/" + emitter,
	}
}

type body struct {
	Type    string          `json:"type"`
	Source  string          `json:"source"`
	Subject string          `json:"subject,omitempty"`
	Data    json.RawMessage `json:"data"`
}

// Publish sends d. A 4xx answer other than 429 wraps app.ErrRejected; every
// other failure is transient.
func (c *Client) Publish(ctx context.Context, d domain.Draft) error {
	payload, err := json.Marshal(body{Type: d.Type, Source: c.source, Subject: d.Subject, Data: d.Data})
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agentsone/v1/events", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("publish %s: %w", d.Type, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxReason))
		return nil
	}
	reason := reasonOf(resp.Body)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		return fmt.Errorf("%w: %s: status %d: %s", app.ErrRejected, d.Type, resp.StatusCode, reason)
	}
	return fmt.Errorf("publish %s: status %d: %s", d.Type, resp.StatusCode, reason)
}

// reasonOf reads the daemon's problem detail, or the raw start of the answer.
func reasonOf(r io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(r, maxReason))
	var p struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(raw, &p) == nil && p.Detail != "" {
		return p.Detail
	}
	return string(raw)
}
