// SPDX-License-Identifier: MIT

// Package sse streams server-sent events over HTTP.
//
// # Resume contract
//
// Every event a module streams carries a stable, ordered ID. When a client
// reconnects, the browser (or any EventSource client) sends the ID of the last
// event it saw in the Last-Event-ID header. Handlers read it with
// [LastEventID]. A module then replays the stored events that come after that
// ID, and only then switches to live events on the same channel it hands to
// [Serve]. A client that sends no ID gets live events only.
//
// # Backpressure
//
// [Serve] reads the source channel itself and writes each frame to the client,
// so a slow client slows the loop. A write that fails, or that takes longer
// than [Options.WriteTimeout], ends the stream with an error. Producers must
// never block on a subscriber: they use [Send], and when it reports a full
// channel they drop the subscriber. The client reconnects with Last-Event-ID
// and the module replays what it missed.
package sse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
)

const (
	defaultHeartbeat    = 15 * time.Second
	defaultBuffer       = 64
	defaultWriteTimeout = 10 * time.Second
)

// ping is the comment frame sent on every heartbeat tick. Clients ignore it,
// proxies see traffic.
var ping = []byte(": ping\n\n")

// ErrNotFlushable means the response writer cannot flush, so it cannot stream.
var ErrNotFlushable = errors.New("sse: response writer cannot flush")

// ErrInvalidField means an event ID or name holds a line break. The event is
// not written.
var ErrInvalidField = errors.New("sse: event id or name contains a line break")

// Event is one SSE frame. ID becomes `id:`, Name `event:`, Data is JSON-encoded onto `data:` lines.
type Event struct {
	// ID is the resume point a client sends back as Last-Event-ID. Empty omits the line.
	ID string
	// Name is the event type. Empty omits the line.
	Name string
	// Data is encoded as JSON on a single data line.
	Data any
}

// Options tune a stream. The zero value picks every default.
type Options struct {
	// Heartbeat is the gap between `: ping` comments. Default 15s.
	Heartbeat time.Duration
	// Buffer is how many events are queued before the backpressure policy
	// applies: it is the capacity [Options.NewChannel] gives the source
	// channel. Default 64.
	Buffer int
	// WriteTimeout is how long one write may take before Serve gives up on
	// the client. Default 10s. It needs a server that supports write
	// deadlines; others ignore it.
	WriteTimeout time.Duration
	// NewTicker makes the heartbeat ticker and its stop function. Tests
	// inject a fake. Default wraps time.NewTicker.
	NewTicker func(d time.Duration) (<-chan time.Time, func())
}

// NewChannel makes a source channel with the capacity set by Options.Buffer
// (64 when unset). Producers feed it with [Send]; the stream handler passes it
// to [Serve].
func (o Options) NewChannel() chan Event {
	n := o.Buffer
	if n <= 0 {
		n = defaultBuffer
	}
	return make(chan Event, n)
}

// LastEventID returns the resume point from the Last-Event-ID header, or the
// `last_event_id` query parameter, or "". The header wins.
func LastEventID(r *http.Request) string {
	if id := r.Header.Get("Last-Event-ID"); id != "" {
		return id
	}
	return r.URL.Query().Get("last_event_id")
}

// Send tries a non-blocking send. On a full channel it returns false so the
// caller can drop the subscriber; the client then resumes with Last-Event-ID.
func Send(ch chan<- Event, e Event) bool {
	select {
	case ch <- e:
		return true
	default:
		return false
	}
}

// Serve streams events from src to w until src closes or the request context
// ends. It sets Content-Type text/event-stream, Cache-Control no-cache and
// X-Accel-Buffering no, flushes after each frame, and writes a `: ping`
// comment on every heartbeat tick. It returns nil when the client disconnects
// or src closes.
//
// If w cannot flush, Serve writes a 500 problem with [core.WriteProblem] and
// returns [ErrNotFlushable].
//
// Backpressure: Serve reads src itself, so a slow client blocks the write. If
// a write fails, or takes longer than [Options.WriteTimeout], Serve returns
// the error. An event whose ID or Name holds a line break, or whose Data
// cannot be encoded, ends the stream with an error before anything of it is
// written.
func Serve(w http.ResponseWriter, r *http.Request, src <-chan Event, o Options) error {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	if err := rc.Flush(); err != nil {
		if errors.Is(err, http.ErrNotSupported) {
			h.Del("Cache-Control")
			h.Del("X-Accel-Buffering")
			core.WriteProblem(w, r, core.NewProblem(http.StatusInternalServerError, "internal", ""))
			return ErrNotFlushable
		}
		return fmt.Errorf("sse: flush headers: %w", err)
	}

	beat := o.Heartbeat
	if beat <= 0 {
		beat = defaultHeartbeat
	}
	timeout := o.WriteTimeout
	if timeout <= 0 {
		timeout = defaultWriteTimeout
	}
	newTicker := o.NewTicker
	if newTicker == nil {
		newTicker = realTicker
	}
	ticks, stop := newTicker(beat)
	defer stop()

	write := func(frame []byte) error {
		// A write deadline is real I/O time, so it uses the wall clock.
		err := rc.SetWriteDeadline(time.Now().Add(timeout))
		if err != nil && !errors.Is(err, http.ErrNotSupported) {
			return fmt.Errorf("sse: set write deadline: %w", err)
		}
		if _, err := w.Write(frame); err != nil {
			return fmt.Errorf("sse: write: %w", err)
		}
		if err := rc.Flush(); err != nil {
			return fmt.Errorf("sse: flush: %w", err)
		}
		return nil
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
			if err := write(ping); err != nil {
				return err
			}
		case e, ok := <-src:
			if !ok {
				return nil
			}
			frame, err := encode(e)
			if err != nil {
				return err
			}
			if err := write(frame); err != nil {
				return err
			}
		}
	}
}

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// encode renders one event as a frame. It checks the fields first so a bad
// event never reaches the wire.
func encode(e Event) ([]byte, error) {
	if strings.ContainsAny(e.ID, "\r\n") || strings.ContainsAny(e.Name, "\r\n") {
		return nil, ErrInvalidField
	}
	data, err := json.Marshal(e.Data)
	if err != nil {
		return nil, fmt.Errorf("sse: encode data: %w", err)
	}
	var b bytes.Buffer
	if e.ID != "" {
		b.WriteString("id: " + e.ID + "\n")
	}
	if e.Name != "" {
		b.WriteString("event: " + e.Name + "\n")
	}
	b.WriteString("data: ")
	b.Write(data)
	b.WriteString("\n\n")
	return b.Bytes(), nil
}
