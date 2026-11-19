// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// stream is an open SSE connection to the events stream.
type stream struct {
	t      *testing.T
	resp   *http.Response
	lines  *bufio.Scanner
	cancel context.CancelFunc
}

// frame is one SSE frame: its id line and its data line.
type frame struct {
	ID   string
	Data string
}

// openStream connects as the caller. The response headers arrive once the
// daemon has subscribed, so events published after it returns are delivered.
func openStream(t *testing.T, e *env, c core.Caller, query, lastEventID string) *stream {
	t.Helper()
	srv := httptest.NewServer(e.as(c))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events/stream"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return &stream{t: t, resp: resp, lines: bufio.NewScanner(resp.Body), cancel: cancel}
}

// next reads the next frame, skipping comment lines.
func (s *stream) next() frame {
	s.t.Helper()
	var f frame
	for s.lines.Scan() {
		line := s.lines.Text()
		switch {
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "id: "):
			f.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			f.Data = strings.TrimPrefix(line, "data: ")
		case line == "" && f != frame{}:
			return f
		}
	}
	s.t.Fatalf("stream ended: %v", s.lines.Err())
	return f
}

func (s *stream) event() api.EventFrame {
	s.t.Helper()
	f := s.next()
	var ev api.EventFrame
	if err := json.Unmarshal([]byte(f.Data), &ev); err != nil || ev.Kind != "event" {
		s.t.Fatalf("frame %q is not an event frame: %v", f.Data, err)
	}
	if f.ID != ev.Event.Sequence {
		s.t.Fatalf("frame id = %q, want the sequence %q", f.ID, ev.Event.Sequence)
	}
	return ev
}

func TestStreamEvents_DeliversLiveEventsWithTheSequenceAsId(t *testing.T) {
	e := newEnv(t)
	s := openStream(t, e, human, "", "")
	if got := s.resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q", got)
	}

	published := publish(t, e, "agentsone.custom.a", obj{"n": 1})

	got := s.event()
	if got.Event.Id != published.Id || got.Event.Data["n"] != float64(1) {
		t.Fatalf("frame event = %+v, want %s", got.Event, published.Id)
	}
}

func TestStreamEvents_SendsAHeartbeatOnEveryTick(t *testing.T) {
	e := newEnv(t)
	s := openStream(t, e, human, "", "")

	e.ticks <- time.Time{}

	f := s.next()
	var hb api.Heartbeat
	if err := json.Unmarshal([]byte(f.Data), &hb); err != nil || hb.Kind != "heartbeat" || f.ID != "" {
		t.Fatalf("heartbeat frame = %+v, %v", f, err)
	}
	if !time.Time(hb.Time).Equal(e.clock.Now()) {
		t.Fatalf("heartbeat time = %v, want the clock time %v", hb.Time, e.clock.Now())
	}
}

func TestStreamEvents_ResumesAfterLastEventID(t *testing.T) {
	e := newEnv(t)
	first := publish(t, e, "agentsone.custom.a", obj{})
	second := publish(t, e, "agentsone.custom.a", obj{})
	third := publish(t, e, "agentsone.custom.b", obj{})

	s := openStream(t, e, human, "", first.Sequence)

	if got := s.event().Event.Id; got != second.Id {
		t.Fatalf("first replayed event = %s, want %s", got, second.Id)
	}
	if got := s.event().Event.Id; got != third.Id {
		t.Fatalf("second replayed event = %s, want %s", got, third.Id)
	}
	live := publish(t, e, "agentsone.custom.a", obj{})
	if got := s.event().Event.Id; got != live.Id {
		t.Fatalf("live event after the replay = %s, want %s", got, live.Id)
	}
}

func TestStreamEvents_FiltersByRepeatedTypeParameters(t *testing.T) {
	e := newEnv(t)
	s := openStream(t, e, human, "?type=agentsone.custom.a&type=agentsone.other.*", "")

	publish(t, e, "agentsone.custom.b", obj{})
	want := publish(t, e, "agentsone.other.thing", obj{})

	if got := s.event().Event.Id; got != want.Id {
		t.Fatalf("delivered event = %s, want %s: the custom.b event must be filtered out", got, want.Id)
	}
}

func TestStreamEvents_RefusesALastEventIDThatIsNotASequence(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/events/stream", nil)
	req.Header.Set("Last-Event-ID", "not-a-number")
	rec := httptest.NewRecorder()

	e.as(human).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestStreamEvents_EndsWhenTheClientGoesAway(t *testing.T) {
	e := newEnv(t)
	s := openStream(t, e, human, "", "")
	s.cancel()

	for s.lines.Scan() {
		_ = s.lines.Text() // drain until the server closes the stream
	}

	if err := s.lines.Err(); err == nil {
		t.Log("stream closed cleanly")
	}
}
