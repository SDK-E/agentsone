// SPDX-License-Identifier: MIT

package proxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/edge/net/app"
	"github.com/SDK-E/agentsone/internal/edge/net/domain"
)

// handleHTTP is the transparent HTTP proxy: route by the Host header of the first request.
func (s *Server) handleHTTP(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(s.handshakeTimeout)) //nolint:errcheck // only a closed connection fails this, and then the read below fails too
	rec := &recorder{r: conn}
	req, err := http.ReadRequest(bufio.NewReader(rec))
	if err != nil {
		return
	}
	host, port, err := splitHost(req.Host, 80)
	if err != nil {
		writeStatus(conn, http.StatusBadRequest, "The request has no valid Host header.")
		return
	}
	route, err := s.route(conn, host, port, domain.ViaHTTP)
	if err != nil {
		writeRefusal(conn, err)
		return
	}
	upstream, err := s.connect(route)
	if err != nil {
		s.log.Info("upstream unreachable", "name", route.Name, "error", err)
		writeStatus(conn, http.StatusBadGateway, "The site could not be reached.")
		return
	}
	// Replay everything read so far (the head and anything buffered after it), then copy the rest.
	s.splice(conn, conn, upstream, rec.buf)
}

// handleProxy is the proxy for proxy-aware tools: CONNECT tunnels and absolute-form HTTP.
func (s *Server) handleProxy(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(s.handshakeTimeout)) //nolint:errcheck // only a closed connection fails this, and then the read below fails too
	limit := &headLimit{r: conn}
	br := bufio.NewReader(limit)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	limit.lift()
	if req.Method == http.MethodConnect {
		s.connectTunnel(conn, br, req)
		return
	}
	s.forwardPlain(conn, req) // req.Body reads on through br
}

func (s *Server) connectTunnel(conn net.Conn, br *bufio.Reader, req *http.Request) {
	host, port, err := splitHost(req.Host, 0)
	if err != nil || port == 0 {
		writeStatus(conn, http.StatusBadRequest, "CONNECT needs host:port.")
		return
	}
	route, err := s.route(conn, host, port, domain.ViaConnect)
	if err != nil {
		writeRefusal(conn, err)
		return
	}
	upstream, err := s.connect(route)
	if err != nil {
		s.log.Info("upstream unreachable", "name", route.Name, "error", err)
		writeStatus(conn, http.StatusBadGateway, "The site could not be reached.")
		return
	}
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = upstream.Close() //nolint:errcheck // the client is gone, so the unused upstream connection is dropped
		return
	}
	// The client may already have sent its TLS hello; it sits in br's buffer.
	early, _ := br.Peek(br.Buffered()) //nolint:errcheck // peeking at bytes already buffered cannot fail
	s.splice(conn, conn, upstream, append([]byte(nil), early...))
}

func (s *Server) forwardPlain(conn net.Conn, req *http.Request) {
	if req.URL == nil || req.URL.Host == "" || req.URL.Scheme != "http" {
		writeStatus(conn, http.StatusBadRequest, "Send an absolute http:// URL, or CONNECT host:port for HTTPS.")
		return
	}
	host, port, err := splitHost(req.URL.Host, 80)
	if err != nil {
		writeStatus(conn, http.StatusBadRequest, "The URL has no valid host.")
		return
	}
	route, err := s.route(conn, host, port, domain.ViaConnect)
	if err != nil {
		writeRefusal(conn, err)
		return
	}
	upstream, err := s.connect(route)
	if err != nil {
		s.log.Info("upstream unreachable", "name", route.Name, "error", err)
		writeStatus(conn, http.StatusBadGateway, "The site could not be reached.")
		return
	}
	both := closePair{conn, upstream}
	release, ok := s.router.Track(both)
	if !ok {
		return
	}
	defer release()
	defer func() { _ = both.Close() }() //nolint:errcheck // the exchange is over; closing both ends leaves nothing to report
	_ = conn.SetDeadline(time.Time{})   //nolint:errcheck // only a closed connection fails this, and then the copy below ends at once

	// One request per connection: a later request on the same connection could name another
	// host, and it must not reach this one's address.
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	req.Close = true
	if err := req.Write(upstream); err != nil {
		return
	}
	_, _ = io.Copy(conn, upstream) //nolint:errcheck // the copy ends when either side closes; that is the exchange's normal end
}

// splitHost splits "name[:port]". A missing port takes def; a bracketed IPv6 literal is kept
// as the host so the policy refuses it as not a name.
func splitHost(hostport string, def uint16) (string, uint16, error) {
	if hostport == "" {
		return "", 0, errors.New("empty host")
	}
	host, portText, err := net.SplitHostPort(hostport)
	if err != nil {
		if strings.Contains(err.Error(), "missing port") {
			return strings.Trim(hostport, "[]"), def, nil
		}
		return "", 0, err
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("bad port %q", portText)
	}
	return host, uint16(port), nil
}

func writeRefusal(conn net.Conn, err error) {
	var refusal *app.Refusal
	if errors.As(err, &refusal) {
		writeStatus(conn, http.StatusForbidden, "Blocked by the Agents One allow list: "+string(refusal.Reason)+".")
		return
	}
	writeStatus(conn, http.StatusBadGateway, "The site could not be reached.")
}

func writeStatus(conn net.Conn, code int, msg string) {
	body := msg + "\n"
	head := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
		code, http.StatusText(code), len(body))
	_, _ = io.WriteString(conn, head+body) //nolint:errcheck // the last words before the connection closes; a failed write means the machine left
}
