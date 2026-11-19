// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
)

// stream is an open SSE connection to the bus stream.
type stream struct {
	t     *testing.T
	resp  *http.Response
	lines *bufio.Scanner
}

// frame is one SSE frame: its id line and its data line.
type frame struct {
	ID   string
	Data string
}

// openStream connects as the caller. The response headers arrive once the
// daemon has subscribed, so messages sent after it returns are delivered.
func openStream(t *testing.T, e *env, c core.Caller, query, lastEventID string) *stream {
	t.Helper()
	srv := httptest.NewServer(e.as(c))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/bus/stream"+query, nil)
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
	return &stream{t: t, resp: resp, lines: bufio.NewScanner(resp.Body)}
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

func (s *stream) message() (api.BusMessageFrame, string) {
	s.t.Helper()
	f := s.next()
	var m api.BusMessageFrame
	if err := json.Unmarshal([]byte(f.Data), &m); err != nil || m.Kind != api.BusMessageFrameKindMessage {
		s.t.Fatalf("frame %q is not a message frame: %v", f.Data, err)
	}
	return m, f.ID
}

func TestStreamBus_DeliversLiveMessagesWithASequenceId(t *testing.T) {
	e := newEnv(t)
	createChannel(t, e, builder, "general", "public")
	s := openStream(t, e, human, "", "")
	if got := s.resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q", got)
	}

	sent := send(t, e, builder, obj{"channel": "general", "text": "Live."})

	got, id := s.message()
	if got.Message.Id != sent.Id || got.Message.Text != "Live." {
		t.Fatalf("frame message = %+v, want %s", got.Message, sent.Id)
	}
	if n, err := strconv.ParseUint(id, 10, 64); err != nil || n == 0 {
		t.Fatalf("frame id = %q, want a sequence", id)
	}
}

func TestStreamBus_NarrowsToAChannelAndHidesWhatTheCallerMayNotSee(t *testing.T) {
	e := newEnv(t)
	createChannel(t, e, builder, "general", "public")
	createChannel(t, e, builder, "random", "public")
	s := openStream(t, e, intern, "?channel=general", "")

	send(t, e, builder, obj{"channel": "random", "text": "Elsewhere."})
	send(t, e, builder, obj{"to": "founder", "text": "Not for intern."})
	want := send(t, e, builder, obj{"channel": "general", "text": "Here."})

	if got, _ := s.message(); got.Message.Id != want.Id {
		t.Fatalf("first frame = %+v, want %s", got.Message, want.Id)
	}
}

func TestStreamBus_SendsAHeartbeatOnEveryTick(t *testing.T) {
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

func TestStreamBus_ResumesAfterLastEventID(t *testing.T) {
	e := newEnv(t)
	createChannel(t, e, builder, "general", "public")
	send(t, e, builder, obj{"channel": "general", "text": "one"})
	first := send(t, e, builder, obj{"channel": "general", "text": "two"})
	third := send(t, e, builder, obj{"channel": "general", "text": "three"})
	// The id of "one" is the sequence of the first stored message.
	resume := openStream(t, e, human, "", "1")

	if got, _ := resume.message(); got.Message.Id != first.Id {
		t.Fatalf("first replayed = %s, want %s", got.Message.Id, first.Id)
	}
	if got, _ := resume.message(); got.Message.Id != third.Id {
		t.Fatalf("second replayed = %s, want %s", got.Message.Id, third.Id)
	}
	live := send(t, e, builder, obj{"channel": "general", "text": "four"})
	if got, _ := resume.message(); got.Message.Id != live.Id {
		t.Fatalf("live after the replay = %s, want %s", got.Message.Id, live.Id)
	}
}

func TestStreamBus_RefusesBadParametersWithProblems(t *testing.T) {
	e := newEnv(t)
	for _, tt := range []struct {
		name, target, lastEventID string
	}{
		{"bad Last-Event-ID", "/v1/bus/stream", "abc"},
		{"zero Last-Event-ID", "/v1/bus/stream", "0"},
		{"bad channel", "/v1/bus/stream?channel=Not+Valid", ""},
	} {
		req := httptest.NewRequest(http.MethodGet, tt.target, nil)
		if tt.lastEventID != "" {
			req.Header.Set("Last-Event-ID", tt.lastEventID)
		}
		rec := httptest.NewRecorder()
		e.as(human).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), problemBase+"validation") {
			t.Errorf("%s: status = %d, body = %s", tt.name, rec.Code, rec.Body)
		}
	}
}

func TestStreamBus_EndsWhenTheClientLeaves(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(e.as(human))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/bus/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = bufio.NewReader(resp.Body).ReadString(0)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the body stayed open after the client left")
	}
}
