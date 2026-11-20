// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/desktop/app"
)

// Pattern is the bridge's route. It sits outside /v1 like the IDE server
// (/ide/<human>/): the stream is raw VNC, not part of the API contract.
const Pattern = "GET /desktop/{human}/vnc"

// Subprotocol is the WebSocket subprotocol the browser must offer. Binary
// frames carry the VNC (RFB) stream both ways.
const Subprotocol = "agentsone.vnc.v1"

const (
	pingEvery  = 15 * time.Second
	retryAfter = 60 // seconds, at the desktop cap
)

// Connector opens a VNC connection to a human's desktop (app.Desktops).
type Connector interface {
	Connect(ctx context.Context, human string) (net.Conn, error)
}

// Bridge serves Pattern: a signed-in human's browser to the VNC server of
// that human's own desktop. Every check runs before the desktop is
// touched, so a refused request never starts one.
func Bridge(desktops Connector) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		human := r.PathValue("human")
		if err := allowed(r, human); err != nil {
			core.WriteError(w, r, err)
			return
		}
		vnc, err := desktops.Connect(r.Context(), human)
		if err != nil {
			writeDesktopError(w, r, err)
			return
		}
		defer func() { _ = vnc.Close() }()
		// The library's own origin check stays on as the final word.
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
		if err != nil {
			return // Accept already wrote the refusal
		}
		pump(r.Context(), ws, vnc)
	})
}

// allowed holds the bridge to its one user: the signed-in human whose
// desktop it is, from a same-site page, asking for the VNC subprotocol.
func allowed(r *http.Request, human string) error {
	c, ok := core.CallerFrom(r.Context())
	if !ok || c.Name == "" {
		return fmt.Errorf("sign in to open the desktop: %w", core.ErrUnauthorized)
	}
	if c.Kind != core.CallerHuman {
		return fmt.Errorf("the desktop is for humans: %w", core.ErrForbidden)
	}
	if c.Name != human {
		return fmt.Errorf("this is not your desktop: %w", core.ErrForbidden)
	}
	// GET passes the CSRF check, so the origin check is the wall against
	// cross-site WebSocket use.
	if !sameSite(r) {
		return fmt.Errorf("cross-site desktop connections are refused: %w", core.ErrForbidden)
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !offers(r, Subprotocol) {
		return fmt.Errorf("open a WebSocket with subprotocol %s: %w", Subprotocol, core.ErrInvalid)
	}
	return nil
}

func writeDesktopError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, app.ErrTooMany) {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		core.WriteProblem(w, r, core.NewProblem(http.StatusTooManyRequests, "rate-limited", err.Error()))
		return
	}
	core.WriteError(w, r, err)
}

// sameSite is the WebSocket library's origin rule, asked early: no Origin
// header (a program, not a browser) or an Origin whose host is the host the
// request was sent to (the API layer sets it from the web door or the
// configured public URL).
func sameSite(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func offers(r *http.Request, protocol string) bool {
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for p := range strings.SplitSeq(v, ",") {
			if strings.TrimSpace(p) == protocol {
				return true
			}
		}
	}
	return false
}

// pump copies bytes both ways until either side ends, pinging the browser
// every 15 seconds so proxies keep the idle connection open.
func pump(ctx context.Context, ws *websocket.Conn, vnc net.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	browser := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	go keepAlive(ctx, ws)
	toVNC := make(chan struct{})
	go func() {
		defer close(toVNC)
		_, _ = io.Copy(vnc, browser) // ends when the browser leaves or vnc fails
		_ = vnc.Close()
	}()
	_, _ = io.Copy(browser, vnc) // ends when the desktop ends or the browser leaves
	// Close the WebSocket itself, not the net.Conn view: that one cancels
	// the pending read first, which drops the connection without a close frame.
	_ = ws.Close(websocket.StatusNormalClosure, "desktop connection ended")
	<-toVNC
}

func keepAlive(ctx context.Context, ws *websocket.Conn) {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if ws.Ping(ctx) != nil {
				return
			}
		}
	}
}
