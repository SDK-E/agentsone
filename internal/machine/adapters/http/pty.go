// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/machineapi"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/machine/app"
	"github.com/SDK-E/agentsone/internal/machine/domain"
)

const (
	ptySubprotocol = "agentsone.pty.v1"
	pingEvery      = 15 * time.Second
	retryAfter     = 30 // seconds, when a terminal cap is reached
)

// WithTerminals serves the terminal operations from terms.
func (h *Handler) WithTerminals(terms *app.Terminals) *Handler {
	h.terms = terms
	return h
}

// ListPtySessions lists the terminals the caller may see.
func (h *Handler) ListPtySessions(ctx context.Context, req machineapi.ListPtySessionsRequestObject) (machineapi.ListPtySessionsResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	items, next, err := h.terms.List(ctx, a, valueOr(p.Cursor, ""), valueOr(p.Limit, defaultLimit))
	if err != nil {
		return nil, err
	}
	return machineapi.ListPtySessions200JSONResponse{Items: mapAll(items, toPty), NextCursor: cursor(next)}, nil
}

// CreatePtySession opens a terminal as the caller's Linux user.
func (h *Handler) CreatePtySession(ctx context.Context, req machineapi.CreatePtySessionRequestObject) (machineapi.CreatePtySessionResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	s, err := h.terms.Open(ctx, a, valueOr(req.Body.Command, nil), req.Body.Cols, req.Body.Rows)
	if errors.Is(err, domain.ErrTooMany) {
		limit := retryAfter
		p := core.NewProblem(http.StatusTooManyRequests, "rate-limited", err.Error())
		return machineapi.CreatePtySession429ApplicationProblemPlusJSONResponse{
			TooManyRequestsApplicationProblemPlusJSONResponse: machineapi.TooManyRequestsApplicationProblemPlusJSONResponse{
				Body:    api.Problem{Type: p.Type, Title: p.Title, Status: p.Status, Detail: &p.Detail},
				Headers: machineapi.TooManyRequestsResponseHeaders{RetryAfter: &limit},
			},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return machineapi.CreatePtySession201JSONResponse(toPty(s)), nil
}

// GetPtySession returns one terminal.
func (h *Handler) GetPtySession(ctx context.Context, req machineapi.GetPtySessionRequestObject) (machineapi.GetPtySessionResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	s, err := h.terms.Get(ctx, a, req.PtyId)
	if err != nil {
		return nil, err
	}
	return machineapi.GetPtySession200JSONResponse(toPty(s)), nil
}

// ClosePtySession hangs up a terminal.
func (h *Handler) ClosePtySession(ctx context.Context, req machineapi.ClosePtySessionRequestObject) (machineapi.ClosePtySessionResponseObject, error) {
	a, err := h.actor(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.terms.Close(ctx, a, req.PtyId); err != nil {
		return nil, err
	}
	return machineapi.ClosePtySession204Response{}, nil
}

// Terminal is the strict middleware that serves connectPty: the generated
// handler cannot take over the connection, so the upgrade happens here, and
// a nil response tells the generated code to write nothing.
func (h *Handler) Terminal(next machineapi.StrictHandlerFunc, operationID string) machineapi.StrictHandlerFunc {
	if operationID != "ConnectPty" {
		return next
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		a, err := h.actor(ctx)
		if err != nil {
			return nil, err
		}
		if !offers(r, ptySubprotocol) {
			return nil, fmt.Errorf("send subprotocol %s: %w", ptySubprotocol, core.ErrInvalid)
		}
		// GET passes the CSRF check, so the origin check is the wall against
		// cross-site WebSocket use. It runs before the terminal is touched:
		// a cross-site page must not learn whether the terminal exists or
		// has a client, nor restart its idle timer.
		if !sameSite(r) {
			return nil, fmt.Errorf("cross-site terminal connections are refused: %w", core.ErrForbidden)
		}
		id := req.(machineapi.ConnectPtyRequestObject).PtyId
		conn, err := h.terms.Attach(ctx, a, id)
		if err != nil {
			return nil, err
		}
		defer conn.Release()
		// The library's own origin check stays on as the final word.
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{ptySubprotocol}})
		if err != nil {
			return nil, nil // Accept already wrote the refusal
		}
		pump(ctx, ws, conn)
		return nil, nil
	}
}

// sameSite is the WebSocket library's origin rule, asked early: no Origin
// header (a program, not a browser) or an Origin whose host is the host the
// request was sent to.
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

// pump copies terminal output to binary frames and binary frames to
// terminal input, applies resize messages, pings every 15 seconds, and sends
// the exit message when the program ends.
func pump(ctx context.Context, ws *websocket.Conn, conn *app.PtyConn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	output := make(chan struct{})
	go func() {
		defer close(output)
		buf := make([]byte, 32<<10)
		for {
			n, err := conn.Read(buf)
			if n > 0 && ws.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
				return
			}
			if err != nil {
				return
			}
		}
	}()
	go keepAlive(ctx, ws)
	go readInput(ctx, ws, conn, cancel)
	select {
	case <-ctx.Done():
		_ = ws.Close(websocket.StatusNormalClosure, "client left")
	case <-conn.Done():
		<-output // flush what the program wrote last
		_ = ws.Write(ctx, websocket.MessageText, domain.PtyExitMessage(conn.ExitCode()))
		_ = ws.Close(websocket.StatusNormalClosure, "program ended")
	}
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

func readInput(ctx context.Context, ws *websocket.Conn, conn *app.PtyConn, stop context.CancelFunc) {
	defer stop()
	for {
		typ, msg, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if _, err := conn.Write(msg); err != nil {
				return
			}
			continue
		}
		if size, err := domain.ParsePtyControl(msg); err == nil {
			_ = conn.Resize(size.Cols, size.Rows) // a failed resize leaves the old size; the session goes on
		}
	}
}

func toPty(s domain.PtySession) api.PtySession {
	return api.PtySession{
		Id: s.ID, User: s.User, Command: slices.Clone(s.Spec.Command), Cols: s.Spec.Cols, Rows: s.Spec.Rows,
		SocketPath: "/v1/machine/pty/" + s.ID + "/socket", CreatedAt: api.Timestamp(s.Created),
		Exited: s.Exited, ExitCode: s.ExitCode,
	}
}
