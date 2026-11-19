// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	httpadapter "github.com/SDK-E/agentsone/internal/mcp/adapters/http"
)

// bearerTransport adds the bearer token to every request.
type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// MCPTransport is a Streamable HTTP MCP client transport to the daemon's
// /v1/mcp, over the same socket or address and token as the commands.
func (c *Client) MCPTransport() *mcp.StreamableClientTransport {
	hc := c.http
	if c.token != "" {
		hc = &http.Client{Transport: bearerTransport{base: c.http.Transport, token: c.token}}
	}
	return &mcp.StreamableClientTransport{
		Endpoint:             c.ep.Base + httpadapter.Path,
		HTTPClient:           hc,
		DisableStandaloneSSE: true,
	}
}

// Bridge copies MCP messages both ways between two transports until one
// side ends. A clean end (the local side hanging up) returns nil.
func Bridge(ctx context.Context, local, remote mcp.Transport) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lc, err := local.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = lc.Close() }()
	rc, err := remote.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	b := &bridge{pending: map[any]bool{}, errc: make(chan error, 8)}
	var wg sync.WaitGroup
	// Each message goes on its own, so a slow tool call never holds up a
	// cancellation sent behind it.
	send := func(to mcp.Connection, msg jsonrpc.Message) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := to.Write(ctx, msg); err != nil && ctx.Err() == nil {
				b.fail(err)
			}
		}()
	}
	go func() { // local to remote
		// Until the client says it is initialized, messages go one at a
		// time in order. After that they go side by side.
		initialized := false
		for {
			msg, err := lc.Read(ctx)
			if err != nil {
				b.localEnded(err)
				return
			}
			req, isRequest := msg.(*jsonrpc.Request)
			if isRequest && req.ID.IsValid() {
				b.expect(req.ID.Raw())
			}
			if initialized {
				send(rc, msg)
				continue
			}
			if err := rc.Write(ctx, msg); err != nil {
				b.fail(err)
				return
			}
			initialized = isRequest && req.Method == "notifications/initialized"
		}
	}()
	go func() { // remote to local
		for {
			msg, err := rc.Read(ctx)
			if err != nil {
				b.fail(err)
				return
			}
			send(lc, msg)
			if resp, ok := msg.(*jsonrpc.Response); ok {
				b.answered(resp.ID.Raw())
			}
		}
	}()

	err = <-b.errc
	if errors.Is(err, io.EOF) {
		wg.Wait() // let the last answers reach the local side
	}
	cancel()
	_ = lc.Close()
	_ = rc.Close()
	wg.Wait()
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, mcp.ErrConnectionClosed) {
		return nil
	}
	return err
}

// bridge tracks the calls in flight. When the local side hangs up, the
// bridge still waits for the answers to calls already sent.
type bridge struct {
	mu      sync.Mutex
	pending map[any]bool
	hungUp  bool
	errc    chan error
}

func (b *bridge) expect(id any) {
	b.mu.Lock()
	b.pending[id] = true
	b.mu.Unlock()
}

func (b *bridge) answered(id any) {
	b.mu.Lock()
	delete(b.pending, id)
	b.finishLocked()
	b.mu.Unlock()
}

func (b *bridge) localEnded(err error) {
	if !errors.Is(err, io.EOF) {
		b.fail(err)
		return
	}
	b.mu.Lock()
	b.hungUp = true
	b.finishLocked()
	b.mu.Unlock()
}

func (b *bridge) finishLocked() {
	if b.hungUp && len(b.pending) == 0 {
		b.fail(io.EOF)
	}
}

func (b *bridge) fail(err error) {
	select {
	case b.errc <- err:
	default:
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// mcpCommand is `agentctl mcp`: a stdio MCP server that bridges to the
// daemon's /v1/mcp, so an MCP client that only speaks stdio can use the tools.
func mcpCommand(g *globals, io IO) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the daemon's machine tools as an MCP server on stdin and stdout",
		Long: "Serve the daemon's machine tools as an MCP server on stdin and stdout.\n" +
			"Every tool is one API operation and runs with the permissions of the caller\n" +
			"the daemon sees on the connection.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := ReadToken(g.token, g.tokenFile)
			if err != nil {
				return usageError{err}
			}
			ep, err := ParseEndpoint(g.url)
			if err != nil {
				return usageError{err}
			}
			local := &mcp.IOTransport{Reader: readCloser{io.In}, Writer: nopWriteCloser{io.Out}}
			if err := Bridge(cmd.Context(), local, NewClient(ep, token).MCPTransport()); err != nil {
				return runtimeError{err}
			}
			return nil
		},
	}
}

type readCloser struct{ io.Reader }

func (readCloser) Close() error { return nil }
