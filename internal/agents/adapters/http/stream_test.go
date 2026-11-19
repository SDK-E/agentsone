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

	"github.com/SDK-E/agentsone/internal/agents/app"
	"github.com/SDK-E/agentsone/internal/core"
)

type frame struct {
	Kind        string  `json:"kind"`
	MessageID   string  `json:"message_id"`
	Text        string  `json:"text"`
	State       string  `json:"state"`
	StateReason *string `json:"state_reason"`
	Message     struct {
		Text string `json:"text"`
	} `json:"message"`
}

// openStream starts a stream over a real HTTP server and returns a func
// that reads the next frame. Closing happens through t.Cleanup.
func (s *server) openStream(path string) func() frame {
	s.t.Helper()
	srv := httptest.NewServer(s.h)
	ctx, cancel := context.WithCancel(s.t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set(callerHeader, "human:ada")
	resp, err := srv.Client().Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
		srv.Close()
	})
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		s.t.Fatalf("stream status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				select {
				case lines <- data:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return func() frame {
		s.t.Helper()
		select {
		case data, ok := <-lines:
			if !ok {
				s.t.Fatal("stream ended")
			}
			var f frame
			if err := json.Unmarshal([]byte(data), &f); err != nil {
				s.t.Fatalf("frame %s: %v", data, err)
			}
			return f
		case <-time.After(5 * time.Second):
			s.t.Fatal("no frame within 5s")
			return frame{}
		}
	}
}

// waitSubscribed sends heartbeats until one comes back, which proves the
// stream is subscribed before the test publishes.
func (s *server) waitSubscribed(next func() frame) {
	s.t.Helper()
	s.ticks <- time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if f := next(); f.Kind != "heartbeat" {
		s.t.Fatalf("first frame kind %q, want heartbeat", f.Kind)
	}
}

func TestStreamChat_StreamsNewMessagesAndReplyDeltas(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	next := s.openStream("/v1/agents/dev/chat/stream")
	s.waitSubscribed(next)

	if _, err := s.society.SendChat(ctxAs(core.CallerHuman, "ada"), "dev", "hello"); err != nil {
		t.Fatal(err)
	}
	if f := next(); f.Kind != "message" || f.Message.Text != "hello" {
		t.Fatalf("frame %+v, want the human message", f)
	}

	if err := s.society.Output(context.Background(), "dev", app.OutputChunk{TurnID: "t1", ChatID: "c1", Text: "Hi"}); err != nil {
		t.Fatal(err)
	}
	if f := next(); f.Kind != "delta" || f.Text != "Hi" || f.MessageID == "" {
		t.Fatalf("frame %+v, want a delta", f)
	}
}

func TestStreamAgentOutput_StreamsOutputAndStateChanges(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	next := s.openStream("/v1/agents/dev/output")
	s.waitSubscribed(next)

	if err := s.society.Output(context.Background(), "dev", app.OutputChunk{TurnID: "t1", Text: "running tests"}); err != nil {
		t.Fatal(err)
	}
	if f := next(); f.Kind != "output" || f.Text != "running tests" {
		t.Fatalf("frame %+v, want output", f)
	}
	host := ctxAs(core.CallerHost, "agentsone")
	if err := s.society.Freezer().Freeze(host, core.FreezeScope{Kind: core.FreezeAgent, Target: "dev"}); err != nil {
		t.Fatal(err)
	}
	if f := next(); f.Kind != "state" || f.State != "frozen" {
		t.Fatalf("frame %+v, want state frozen", f)
	}
}

func TestStreamAgentOutput_StateFrameSaysWhyTheAgentFailed(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	next := s.openStream("/v1/agents/dev/output")
	s.waitSubscribed(next)

	if err := s.society.RunnerFailed(context.Background(), "dev", "The model gateway is not reachable."); err != nil {
		t.Fatal(err)
	}
	if f := next(); f.Kind != "state" || f.State != "failed" || f.StateReason == nil || *f.StateReason != "The model gateway is not reachable." {
		t.Fatalf("frame %+v, want state failed with the reason", f)
	}
	if err := s.society.RunnerRunning(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	if f := next(); f.Kind != "state" || f.State != "awake" || f.StateReason != nil {
		t.Fatalf("frame %+v, want state awake without a reason", f)
	}
}

func TestStreams_ReturnNotFoundForAnUnknownAgent(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	wantProblem(t, s.do("human:ada", http.MethodGet, "/v1/agents/ghost/chat/stream", ""), http.StatusNotFound, "not-found")
	wantProblem(t, s.do("human:ada", http.MethodGet, "/v1/agents/ghost/output", ""), http.StatusNotFound, "not-found")
}
