// SPDX-License-Identifier: MIT

package httpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/sched/app"
)

// sseReader turns task frames into Server-Sent Events. The generated strict
// response copies it to the client and flushes after every chunk. Read blocks
// until a frame, a heartbeat or the end of the request.
type sseReader struct {
	ctx    context.Context
	frames <-chan app.Frame
	beat   <-chan time.Time
	stop   func()
	clock  core.Clock
	buf    []byte
}

func newSSEReader(ctx context.Context, frames <-chan app.Frame, clock core.Clock, heartbeat time.Duration) *sseReader {
	// The opening comment makes the server send the response headers at once,
	// so the client knows the stream is open before the first change.
	r := &sseReader{ctx: ctx, frames: frames, clock: clock, stop: func() {}, buf: []byte(": stream open\n\n")}
	if heartbeat > 0 {
		t := time.NewTicker(heartbeat)
		r.beat, r.stop = t.C, t.Stop
	}
	return r
}

func (r *sseReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		next, err := r.next()
		if err != nil {
			return 0, err
		}
		r.buf = next
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// Close stops the heartbeat. The generated response calls it when the stream ends.
func (r *sseReader) Close() error {
	r.stop()
	return nil
}

func (r *sseReader) next() ([]byte, error) {
	select {
	case <-r.ctx.Done():
		return nil, io.EOF
	case f, ok := <-r.frames:
		if !ok {
			return nil, io.EOF
		}
		return encodeFrame(f.ID, api.TaskFrame{Kind: api.TaskFrameKindTask, Task: toTask(f.Task)})
	case <-r.beat:
		return encodeFrame("", api.Heartbeat{Kind: api.HeartbeatKindHeartbeat, Time: api.Timestamp(r.clock.Now().UTC())})
	}
}

// encodeFrame writes one event. A frame without an id is a heartbeat, which
// must not move the client's Last-Event-ID.
func encodeFrame(id string, v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode stream frame: %w", err)
	}
	if id == "" {
		return fmt.Appendf(nil, "data: %s\n\n", data), nil
	}
	return fmt.Appendf(nil, "id: %s\ndata: %s\n\n", id, data), nil
}
