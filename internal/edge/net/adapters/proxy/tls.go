// SPDX-License-Identifier: MIT

package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

var errHelloRead = errors.New("client hello read")

// handleTLS is the transparent TLS proxy: route by the server name in the ClientHello.
func (s *Server) handleTLS(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(s.handshakeTimeout)) //nolint:errcheck // only a closed connection fails this, and then the read below fails too
	rec := &recorder{r: conn}
	name, err := serverName(s.ctx, rec)
	if err != nil {
		return
	}
	route, err := s.route(conn, name, 443, domain.ViaTLS)
	if err != nil {
		return // a TLS client cannot read a text answer; closing is the refusal
	}
	upstream, err := s.connect(route)
	if err != nil {
		s.log.Info("upstream unreachable", "name", route.Name, "error", err)
		return
	}
	s.splice(conn, conn, upstream, rec.buf)
}

// serverName reads a TLS ClientHello from r with the standard library's parser and returns its
// server name, without answering: the handshake is stopped as soon as the hello is parsed.
func serverName(ctx context.Context, r io.Reader) (string, error) {
	var name string
	var seen bool
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			name, seen = h.ServerName, true
			return nil, errHelloRead
		},
	}
	err := tls.Server(readOnlyConn{r: r}, cfg).HandshakeContext(ctx)
	if !seen {
		return "", errors.Join(errors.New("no TLS client hello"), err)
	}
	if name == "" {
		return "", errors.New("TLS client hello has no server name")
	}
	return name, nil
}

// readOnlyConn feeds the TLS parser. Writes (the parser's alert) are dropped: nothing is sent to
// the client before the decision.
type readOnlyConn struct{ r io.Reader }

func (c readOnlyConn) Read(p []byte) (int, error)     { return c.r.Read(p) }
func (readOnlyConn) Write(p []byte) (int, error)      { return len(p), nil }
func (readOnlyConn) Close() error                     { return nil }
func (readOnlyConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (readOnlyConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (readOnlyConn) SetDeadline(time.Time) error      { return nil }
func (readOnlyConn) SetReadDeadline(time.Time) error  { return nil }
func (readOnlyConn) SetWriteDeadline(time.Time) error { return nil }
