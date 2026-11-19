// SPDX-License-Identifier: MIT

package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// heartbeat is how often an idle stream sends a heartbeat frame, as the
// contract says (x-stream heartbeat_seconds).
const heartbeat = 15 * time.Second

// opening is an SSE comment. It makes the server send the response headers
// at once, so a client knows the stream is open before the first event.
const opening = ": stream open\n\n"

// frameReader turns events into server-sent event frames. The generated
// handler copies it to the response and flushes after every read, so a read
// blocks until the next frame is ready. The stream ends when the request
// context ends or the event channel closes.
type frameReader struct {
	ctx    context.Context
	cancel context.CancelFunc
	events <-chan core.Event
	ticks  <-chan time.Time
	stop   func()
	clock  core.Clock

	buf  bytes.Buffer
	done bool
}

func newFrameReader(ctx context.Context, cancel context.CancelFunc, events <-chan core.Event,
	ticks <-chan time.Time, stop func(), clock core.Clock) *frameReader {
	r := &frameReader{ctx: ctx, cancel: cancel, events: events, ticks: ticks, stop: stop, clock: clock}
	r.buf.WriteString(opening)
	return r
}

// Read blocks until a frame is ready, then returns its bytes.
func (r *frameReader) Read(p []byte) (int, error) {
	for r.buf.Len() == 0 {
		if r.done {
			return 0, io.EOF
		}
		select {
		case <-r.ctx.Done():
			r.done = true
		case e, ok := <-r.events:
			if !ok {
				r.done = true
				continue
			}
			r.writeEvent(e)
		case <-r.ticks:
			r.writeHeartbeat()
		}
	}
	return r.buf.Read(p)
}

// Close stops the heartbeat and ends the event stream behind the reader.
func (r *frameReader) Close() error {
	r.stop()
	r.cancel()
	return nil
}

func (r *frameReader) writeEvent(e core.Event) {
	raw, err := json.Marshal(api.EventFrame{Kind: api.EventFrameKind("event"), Event: toEvent(e)})
	if err != nil {
		return // a frame that cannot be encoded is skipped; the stream goes on
	}
	r.buf.WriteString("id: " + e.Sequence + "\ndata: ")
	r.buf.Write(raw)
	r.buf.WriteString("\n\n")
}

func (r *frameReader) writeHeartbeat() {
	raw, err := json.Marshal(api.Heartbeat{Kind: api.HeartbeatKind("heartbeat"), Time: stamp(r.clock.Now())})
	if err != nil {
		return
	}
	r.buf.WriteString("data: ")
	r.buf.Write(raw)
	r.buf.WriteString("\n\n")
}
