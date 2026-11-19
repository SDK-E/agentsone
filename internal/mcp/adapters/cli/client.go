// SPDX-License-Identifier: MIT

package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

// DefaultURL is where agentctl looks for the daemon: the Unix socket, where
// the daemon identifies callers by socket peer credentials.
const DefaultURL = "unix:///run/agentsone/api.sock"

// unixHost is the placeholder host in requests sent over a Unix socket.
const unixHost = "agentsone"

const dialTimeout = 5 * time.Second

// maxFrame bounds one server-sent event, so a broken stream cannot fill memory.
const maxFrame = 8 << 20

// Endpoint says how to reach the daemon.
type Endpoint struct {
	// Socket is the Unix socket path, empty for TCP.
	Socket string
	// Base is the URL requests are built on, without a trailing slash.
	Base string
}

// ParseEndpoint reads --url: "unix:///path", a bare absolute path, an
// http(s) URL, or host:port. Empty means DefaultURL.
func ParseEndpoint(raw string) (Endpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultURL
	}
	switch {
	case strings.HasPrefix(raw, "unix://"):
		path := strings.TrimPrefix(raw, "unix://")
		if path == "" {
			return Endpoint{}, fmt.Errorf("--url %q has no socket path", raw)
		}
		return Endpoint{Socket: path, Base: "http://" + unixHost}, nil
	case strings.HasPrefix(raw, "/"):
		return Endpoint{Socket: raw, Base: "http://" + unixHost}, nil
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return Endpoint{}, fmt.Errorf("--url %q is not a valid URL", raw)
		}
		return Endpoint{Base: strings.TrimRight(raw, "/")}, nil
	case strings.Contains(raw, ":"):
		return Endpoint{Base: "http://" + raw}, nil
	}
	return Endpoint{}, fmt.Errorf("--url %q is not a socket path, URL or host:port", raw)
}

// ReadToken picks the bearer token: --token wins over --token-file, and
// neither means no token (the socket identifies the caller by itself).
func ReadToken(token, file string) (string, error) {
	if token != "" || file == "" {
		return token, nil
	}
	b, err := os.ReadFile(file) //nolint:gosec // the path is the user's own --token-file flag
	if err != nil {
		return "", fmt.Errorf("read --token-file: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Client sends API requests to the daemon.
type Client struct {
	ep    Endpoint
	token string
	http  *http.Client
}

// NewClient builds a client. The token, when set, is sent as a bearer token.
func NewClient(ep Endpoint, token string) *Client {
	tr := &http.Transport{}
	if ep.Socket != "" {
		socket := ep.Socket
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: dialTimeout}
			return d.DialContext(ctx, "unix", socket)
		}
	}
	return &Client{ep: ep, token: token, http: &http.Client{Transport: tr}}
}

// Base returns the URL requests are built on.
func (c *Client) Base() string { return c.ep.Base }

// HTTPClient returns the underlying client, for callers that need raw access.
func (c *Client) HTTPClient() *http.Client { return c.http }

// Authorize sets the bearer token on h when there is one.
func (c *Client) Authorize(h http.Header) {
	if c.token != "" {
		h.Set("Authorization", "Bearer "+c.token)
	}
}

func (c *Client) newRequest(ctx context.Context, r domain.Request, accept string) (*http.Request, error) {
	target := c.ep.Base + r.Path
	if len(r.Query) > 0 {
		target += "?" + r.Query.Encode()
	}
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.Authorize(req.Header)
	return req, nil
}

// Do sends r and returns the status and the whole response body.
func (c *Client) Do(ctx context.Context, r domain.Request) (int, []byte, error) {
	req, err := c.newRequest(ctx, r, "application/json")
	if err != nil {
		return 0, nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, c.unreachable(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// Stream sends r and calls fn with the data of each server-sent event until
// the stream ends or ctx is cancelled. A problem answer is returned as a *domain.Problem.
func (c *Client) Stream(ctx context.Context, r domain.Request, fn func(data []byte) error) error {
	req, err := c.newRequest(ctx, r, "text/event-stream")
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return c.unreachable(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		p := domain.ParseProblem(resp.StatusCode, b)
		return &p
	}
	err = ReadEvents(resp.Body, fn)
	if ctx.Err() != nil {
		return nil // the user stopped following
	}
	return err
}

// ReadEvents splits a server-sent event stream and calls fn with each event's
// data. Comment lines (heartbeats) and events without data are skipped.
func ReadEvents(r io.Reader, fn func(data []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrame)
	var data [][]byte
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		frame := bytes.Join(data, []byte("\n"))
		data = nil
		return fn(frame)
	}
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case len(line) == 0:
			if err := flush(); err != nil {
				return err
			}
		case line[0] == ':':
		case bytes.HasPrefix(line, []byte("data:")):
			data = append(data, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" ")))
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	return flush()
}

func (c *Client) unreachable(err error) error {
	where := c.ep.Base
	if c.ep.Socket != "" {
		where = c.ep.Socket
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("cannot reach the daemon at %s: %w", where, err)
}
