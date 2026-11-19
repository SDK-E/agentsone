// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/agentsapi"
)

// This is a minimal Server-Sent Events writer. It moves to the shared
// internal/api/sse helper once that lands on main.

// StreamChat streams new chat messages and reply deltas of one agent.
func (h *Handler) StreamChat(ctx context.Context, req agentsapi.StreamChatRequestObject) (agentsapi.StreamChatResponseObject, error) {
	if _, err := h.s.Get(ctx, req.AgentName); err != nil {
		return nil, err
	}
	frames := h.s.Hub().Subscribe(ctx, req.AgentName, app.StreamChat)
	return agentsapi.StreamChat200TexteventStreamResponse{Body: h.serve(ctx, frames, chatFrame)}, nil
}

// StreamAgentOutput streams runner output and state changes of one agent.
func (h *Handler) StreamAgentOutput(ctx context.Context, req agentsapi.StreamAgentOutputRequestObject) (agentsapi.StreamAgentOutputResponseObject, error) {
	if _, err := h.s.Get(ctx, req.AgentName); err != nil {
		return nil, err
	}
	frames := h.s.Hub().Subscribe(ctx, req.AgentName, app.StreamOutput)
	return agentsapi.StreamAgentOutput200TexteventStreamResponse{Body: h.serve(ctx, frames, agentOutputFrame)}, nil
}

// chatFrame maps a hub frame to a ChatStreamFrame member, or nil to skip it.
func chatFrame(f app.Frame) any {
	switch {
	case f.Message != nil:
		return api.ChatMessageFrame{Kind: api.ChatMessageFrameKindMessage, Message: toChatMessage(f.Message.Message)}
	case f.Delta != nil:
		return api.ChatDeltaFrame{Kind: api.ChatDeltaFrameKindDelta, MessageId: f.Delta.MessageID, Text: f.Delta.Text}
	}
	return nil
}

// agentOutputFrame maps a hub frame to an AgentOutputFrame member, or nil.
func agentOutputFrame(f app.Frame) any {
	switch {
	case f.Output != nil:
		return outputFrame(*f.Output)
	case f.State != nil:
		return stateFrame(*f.State)
	}
	return nil
}

// serve writes frames as SSE events, with a heartbeat, until ctx ends or
// the subscription closes; then the body ends cleanly.
func (h *Handler) serve(ctx context.Context, frames <-chan app.Frame, mapFrame func(app.Frame) any) io.Reader {
	pr, pw := io.Pipe()
	ticks, stop := h.tickers()
	go func() {
		defer stop()
		w := sseWriter{w: pw}
		err := w.loop(ctx, frames, ticks, mapFrame)
		_ = pw.CloseWithError(err) // nil closes with io.EOF: a clean end
	}()
	return pr
}

type sseWriter struct {
	w  io.Writer
	id uint64
}

func (s *sseWriter) loop(ctx context.Context, frames <-chan app.Frame, ticks <-chan time.Time, mapFrame func(app.Frame) any) error {
	// An SSE comment first, so the status and headers reach the client
	// before the first frame.
	if _, err := io.WriteString(s.w, ": open\n\n"); err != nil {
		return nil //nolint:nilerr // the client left; end the stream cleanly
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case t := <-ticks:
			if err := s.send(heartbeat(t)); err != nil {
				return nil //nolint:nilerr // the client left; end the stream cleanly
			}
		case f, ok := <-frames:
			if !ok {
				return nil
			}
			v := mapFrame(f)
			if v == nil {
				continue
			}
			if err := s.send(v); err != nil {
				return nil //nolint:nilerr // the client left; end the stream cleanly
			}
		}
	}
}

func (s *sseWriter) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode stream frame: %w", err)
	}
	s.id++
	_, err = fmt.Fprintf(s.w, "id: %d\ndata: %s\n\n", s.id, data)
	return err
}
