// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api/routerapi"
	"github.com/SDK-E/agentsone/internal/router/app"
)

// This is a minimal Server-Sent Events writer, the same as the agents
// context uses. Both move to the shared internal/api/sse helper once that
// lands on main.

// StreamLiveList streams the live list: every entry first, then changes.
// A client that reconnects gets the whole list again, which also covers
// Last-Event-ID: a snapshot holds everything a replay would.
func (h *Handler) StreamLiveList(ctx context.Context, _ routerapi.StreamLiveListRequestObject) (routerapi.StreamLiveListResponseObject, error) {
	ctx, cancel := context.WithCancel(ctx)
	reconcile, stopReconcile := h.tick(reconcileEvery)
	changes := h.live.Watch(ctx, reconcile)
	beats, stopBeats := h.tick(heartbeatEvery)

	pr, pw := io.Pipe()
	go func() {
		defer stopReconcile()
		defer stopBeats()
		defer cancel()
		w := sseWriter{w: pw}
		err := w.loop(ctx, changes, beats, h.clock.Now)
		_ = pw.CloseWithError(err) // nil closes with io.EOF: a clean end
	}()
	return routerapi.StreamLiveList200TexteventStreamResponse{Body: pr}, nil
}

type sseWriter struct {
	w  io.Writer
	id uint64
}

func (s *sseWriter) loop(ctx context.Context, changes <-chan app.Change, beats <-chan time.Time, now func() time.Time) error {
	// An SSE comment first, so the status and headers reach the client
	// before the first frame.
	if _, err := io.WriteString(s.w, ": open\n\n"); err != nil {
		return nil //nolint:nilerr // the client left; end the stream cleanly
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-beats:
			if err := s.send(heartbeatFrame(now())); err != nil {
				return nil //nolint:nilerr // the client left; end the stream cleanly
			}
		case c, ok := <-changes:
			if !ok {
				return nil
			}
			if err := s.send(frameOf(c)); err != nil {
				return nil //nolint:nilerr // the client left; end the stream cleanly
			}
		}
	}
}

func frameOf(c app.Change) any {
	if c.Entry != nil {
		return entryFrame(*c.Entry)
	}
	return removedFrame(c.Removed)
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
