// SPDX-License-Identifier: MIT

package sse_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/api/sse"
	"github.com/SDK-E/agentsone/internal/core"
)

// fakeTicker returns a ticker factory driven by the test and a flag that
// records whether the stop function ran.
func fakeTicker() (ticks chan time.Time, factory func(time.Duration) (<-chan time.Time, func()), stopped *atomic.Bool, got *atomic.Int64) {
	ticks = make(chan time.Time)
	stopped = new(atomic.Bool)
	got = new(atomic.Int64)
	factory = func(d time.Duration) (<-chan time.Time, func()) {
		got.Store(int64(d))
		return ticks, func() { stopped.Store(true) }
	}
	return ticks, factory, stopped, got
}

// serveAll runs Serve against a recorder with a source that is already
// filled and closed.
func serveAll(t *testing.T, events ...sse.Event) (*httptest.ResponseRecorder, error) {
	t.Helper()
	src := make(chan sse.Event, len(events))
	for _, e := range events {
		src <- e
	}
	close(src)
	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	rec := httptest.NewRecorder()
	_, factory, _, _ := fakeTicker()
	err := sse.Serve(rec, req, src, sse.Options{NewTicker: factory})
	return rec, err
}

func TestServe_EncodesFrames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   sse.Event
		want string
	}{
		{"all fields", sse.Event{ID: "7", Name: "agent.hired", Data: map[string]string{"id": "agt_a"}},
			"id: 7\nevent: agent.hired\ndata: {\"id\":\"agt_a\"}\n\n"},
		{"no id", sse.Event{Name: "x", Data: 1}, "event: x\ndata: 1\n\n"},
		{"no name", sse.Event{ID: "9", Data: "hi"}, "id: 9\ndata: \"hi\"\n\n"},
		{"neither", sse.Event{Data: []int{1, 2}}, "data: [1,2]\n\n"},
		{"nil data", sse.Event{Name: "ping"}, "event: ping\ndata: null\n\n"},
		{"newline in data is escaped", sse.Event{Data: "a\nb"}, "data: \"a\\nb\"\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec, err := serveAll(t, tt.in)
			if err != nil {
				t.Fatalf("Serve() error = %v", err)
			}
			if got := rec.Body.String(); got != tt.want {
				t.Fatalf("body = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServe_SetsStreamingHeaders(t *testing.T) {
	t.Parallel()
	rec, err := serveAll(t)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	want := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"X-Accel-Buffering": "no",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !rec.Flushed {
		t.Error("response was not flushed")
	}
}

func TestServe_RejectsNewlineInIDBeforeWriting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   sse.Event
	}{
		{"newline in id", sse.Event{ID: "1\nevent: evil", Data: 1}},
		{"carriage return in id", sse.Event{ID: "1\rx", Data: 1}},
		{"newline in name", sse.Event{Name: "a\nb", Data: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec, err := serveAll(t, tt.in)
			if !errors.Is(err, sse.ErrInvalidField) {
				t.Fatalf("Serve() error = %v, want %v", err, sse.ErrInvalidField)
			}
			if rec.Body.Len() != 0 {
				t.Fatalf("body = %q, want nothing written for the bad event", rec.Body.String())
			}
		})
	}
}

func TestServe_ReturnsErrorWhenDataCannotBeEncoded(t *testing.T) {
	t.Parallel()
	rec, err := serveAll(t, sse.Event{Data: make(chan int)})
	if err == nil {
		t.Fatal("Serve() error = nil, want an encoding error")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want nothing written", rec.Body.String())
	}
}

func TestServe_WritesPingCommentOnHeartbeatTick(t *testing.T) {
	t.Parallel()
	ticks, factory, stopped, interval := fakeTicker()
	src := make(chan sse.Event)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	done := make(chan error, 1)
	go func() {
		done <- sse.Serve(rec, req, src, sse.Options{Heartbeat: 3 * time.Second, NewTicker: factory})
	}()

	ticks <- time.Time{} // returns once Serve has taken the tick
	close(src)           // Serve handles the tick before it can see the close

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the source closed")
	}
	if got, want := rec.Body.String(), ": ping\n\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if got := time.Duration(interval.Load()); got != 3*time.Second {
		t.Errorf("ticker interval = %v, want 3s", got)
	}
	if !stopped.Load() {
		t.Error("ticker was not stopped")
	}
}

func TestServe_UsesFifteenSecondHeartbeatByDefault(t *testing.T) {
	t.Parallel()
	_, factory, _, interval := fakeTicker()
	src := make(chan sse.Event)
	close(src)
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if err := sse.Serve(httptest.NewRecorder(), req, src, sse.Options{NewTicker: factory}); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	if got := time.Duration(interval.Load()); got != 15*time.Second {
		t.Fatalf("default heartbeat = %v, want 15s", got)
	}
}

func TestServe_UsesRealTickerWhenNoneIsInjected(t *testing.T) {
	t.Parallel()
	src := make(chan sse.Event, 1)
	src <- sse.Event{Data: 1}
	close(src)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	if err := sse.Serve(rec, req, src, sse.Options{}); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	if got, want := rec.Body.String(), "data: 1\n\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestServe_StopsWhenRequestContextEnds(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	src := make(chan sse.Event) // never sends, never closes
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	_, factory, stopped, _ := fakeTicker()
	done := make(chan error, 1)
	go func() {
		done <- sse.Serve(httptest.NewRecorder(), req, src, sse.Options{NewTicker: factory})
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v, want nil on client disconnect", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop after the context was cancelled")
	}
	if !stopped.Load() {
		t.Error("ticker was not stopped")
	}
}

func TestServe_StopsWhenSourceCloses(t *testing.T) {
	t.Parallel()
	rec, err := serveAll(t, sse.Event{Data: 1}, sse.Event{Data: 2})
	if err != nil {
		t.Fatalf("Serve() error = %v, want nil when the source closes", err)
	}
	if got, want := rec.Body.String(), "data: 1\n\ndata: 2\n\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestServe_StreamsEachEventToTheClientBeforeTheNextArrives(t *testing.T) {
	t.Parallel()
	src := make(chan sse.Event)
	_, factory, _, _ := fakeTicker()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = sse.Serve(w, r, src, sse.Options{NewTicker: factory})
	}))
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	br := bufio.NewReader(resp.Body)

	for _, id := range []string{"1", "2"} {
		src <- sse.Event{ID: id, Data: id}
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read frame %s: %v", id, err)
		}
		if want := "id: " + id + "\n"; line != want {
			t.Fatalf("line = %q, want %q", line, want)
		}
		for range 2 { // data line and blank line
			if _, err := br.ReadString('\n'); err != nil {
				t.Fatalf("read rest of frame %s: %v", id, err)
			}
		}
	}
	close(src)
	if rest, _ := br.ReadString('\n'); rest != "" {
		t.Fatalf("unexpected bytes after close: %q", rest)
	}
}

func TestLastEventID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		target string
		header string
		want   string
	}{
		{"none", "/v1/events", "", ""},
		{"header", "/v1/events", "42", "42"},
		{"query", "/v1/events?last_event_id=17", "", "17"},
		{"header beats query", "/v1/events?last_event_id=17", "42", "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			if tt.header != "" {
				req.Header.Set("Last-Event-ID", tt.header)
			}
			if got := sse.LastEventID(req); got != tt.want {
				t.Fatalf("LastEventID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// plainWriter is a ResponseWriter that cannot flush.
type plainWriter struct {
	h    http.Header
	code int
	body bytes.Buffer
}

func (p *plainWriter) Header() http.Header {
	if p.h == nil {
		p.h = http.Header{}
	}
	return p.h
}
func (p *plainWriter) Write(b []byte) (int, error) { return p.body.Write(b) }
func (p *plainWriter) WriteHeader(code int)        { p.code = code }

func TestServe_WritesProblemWhenWriterCannotFlush(t *testing.T) {
	t.Parallel()
	w := &plainWriter{}
	src := make(chan sse.Event)
	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)

	err := sse.Serve(w, req, src, sse.Options{})

	if !errors.Is(err, sse.ErrNotFlushable) {
		t.Fatalf("Serve() error = %v, want %v", err, sse.ErrNotFlushable)
	}
	if w.code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("Cache-Control = %q, want it cleared on the problem response", cc)
	}
	var p core.Problem
	if err := json.Unmarshal(w.body.Bytes(), &p); err != nil {
		t.Fatalf("body is not a problem: %v", err)
	}
	if p.Status != http.StatusInternalServerError || p.Instance != "/v1/events" {
		t.Errorf("problem = %+v, want status 500 and instance /v1/events", p)
	}
}

// errWriter is a flushable writer whose behaviour the test controls.
type errWriter struct {
	*httptest.ResponseRecorder
	flushErr    error
	writeErr    error
	deadlineErr error
	deadlines   []time.Time
}

func (e *errWriter) Write(b []byte) (int, error) {
	if e.writeErr != nil {
		return 0, e.writeErr
	}
	return e.ResponseRecorder.Write(b)
}

func (e *errWriter) FlushError() error {
	if e.flushErr != nil {
		return e.flushErr
	}
	e.Flush()
	return nil
}

func (e *errWriter) SetWriteDeadline(t time.Time) error {
	e.deadlines = append(e.deadlines, t)
	return e.deadlineErr
}

func TestServe_ReturnsErrorWhenFlushFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("flush broke")
	w := &errWriter{ResponseRecorder: httptest.NewRecorder(), flushErr: boom}
	src := make(chan sse.Event)
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	err := sse.Serve(w, req, src, sse.Options{})

	if !errors.Is(err, boom) {
		t.Fatalf("Serve() error = %v, want %v", err, boom)
	}
}

func TestServe_ReturnsErrorWhenWriteFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("client gone")
	w := &errWriter{ResponseRecorder: httptest.NewRecorder(), writeErr: boom}
	src := make(chan sse.Event, 1)
	src <- sse.Event{Data: 1}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	err := sse.Serve(w, req, src, sse.Options{})

	if !errors.Is(err, boom) {
		t.Fatalf("Serve() error = %v, want %v", err, boom)
	}
}

func TestServe_ReturnsErrorWhenHeartbeatWriteFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("client gone")
	w := &errWriter{ResponseRecorder: httptest.NewRecorder(), writeErr: boom}
	ticks, factory, _, _ := fakeTicker()
	src := make(chan sse.Event)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	done := make(chan error, 1)
	go func() { done <- sse.Serve(w, req, src, sse.Options{NewTicker: factory}) }()

	ticks <- time.Time{}

	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("Serve() error = %v, want %v", err, boom)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the heartbeat write failed")
	}
}

func TestServe_SetsWriteDeadlineBeforeEachWrite(t *testing.T) {
	t.Parallel()
	w := &errWriter{ResponseRecorder: httptest.NewRecorder()}
	src := make(chan sse.Event, 2)
	src <- sse.Event{Data: 1}
	src <- sse.Event{Data: 2}
	close(src)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	before := time.Now()

	if err := sse.Serve(w, req, src, sse.Options{WriteTimeout: time.Hour}); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	if len(w.deadlines) != 2 {
		t.Fatalf("deadlines set = %d, want 2 (one per frame)", len(w.deadlines))
	}
	for _, d := range w.deadlines {
		if d.Before(before.Add(time.Hour)) {
			t.Errorf("deadline %v is earlier than now + 1h", d)
		}
	}
}

func TestServe_UsesTenSecondWriteTimeoutByDefault(t *testing.T) {
	t.Parallel()
	w := &errWriter{ResponseRecorder: httptest.NewRecorder()}
	src := make(chan sse.Event, 1)
	src <- sse.Event{Data: 1}
	close(src)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	before := time.Now()

	if err := sse.Serve(w, req, src, sse.Options{}); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	d := w.deadlines[0]
	if d.Before(before.Add(10*time.Second)) || d.After(time.Now().Add(10*time.Second)) {
		t.Fatalf("deadline = %v, want about now + 10s", d)
	}
}

func TestServe_ReturnsErrorWhenWriteDeadlineFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("deadline broke")
	w := &errWriter{ResponseRecorder: httptest.NewRecorder(), deadlineErr: boom}
	src := make(chan sse.Event, 1)
	src <- sse.Event{Data: 1}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	err := sse.Serve(w, req, src, sse.Options{})

	if !errors.Is(err, boom) {
		t.Fatalf("Serve() error = %v, want %v", err, boom)
	}
}

func TestSend_ReportsWhetherTheEventWasQueued(t *testing.T) {
	t.Parallel()
	ch := make(chan sse.Event, 1)

	if !sse.Send(ch, sse.Event{ID: "1"}) {
		t.Fatal("Send() = false on a channel with room, want true")
	}
	if sse.Send(ch, sse.Event{ID: "2"}) {
		t.Fatal("Send() = true on a full channel, want false")
	}
	if got := (<-ch).ID; got != "1" {
		t.Fatalf("queued event ID = %q, want 1 (the dropped one must not replace it)", got)
	}
}

func TestOptions_NewChannelUsesBufferOrDefault(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"default", 0, 64},
		{"negative falls back", -3, 64},
		{"explicit", 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ch := sse.Options{Buffer: tt.in}.NewChannel()
			if got := cap(ch); got != tt.want {
				t.Fatalf("cap = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestServe_DoesNotMentionServerInternalsInProblem(t *testing.T) {
	t.Parallel()
	w := &plainWriter{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	_ = sse.Serve(w, req, make(chan sse.Event), sse.Options{})

	if strings.Contains(w.body.String(), "ResponseWriter") {
		t.Fatalf("problem leaks internals: %s", w.body.String())
	}
}
