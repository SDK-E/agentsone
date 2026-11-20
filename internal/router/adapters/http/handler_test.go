// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/api/routerapi"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	httpadapter "github.com/SDK-E/agentsone/internal/router/adapters/http"
	"github.com/SDK-E/agentsone/internal/router/adapters/memory"
	"github.com/SDK-E/agentsone/internal/router/app"
	"github.com/SDK-E/agentsone/internal/router/routertest"
)

type rig struct {
	mux       *http.ServeMux
	live      *app.LiveList
	profiles  *routertest.Profiles
	chats     *routertest.Chats
	clock     *coretest.Clock
	beats     chan time.Time
	reconcile chan time.Time
}

func newRig(t *testing.T, replies ...routertest.Reply) *rig {
	t.Helper()
	clock := coretest.NewClock(coretest.Epoch)
	profiles := routertest.NewProfiles(
		routertest.Router("router"),
		routertest.Agent("builder", "Builder"),
		routertest.Agent("scribe", "Docs writer"),
	)
	live := app.NewLiveList(memory.NewLines(), profiles, clock, nil)
	chats := routertest.NewChats()
	routing := app.NewRouter(app.RouterDeps{
		Live: live, Chats: chats, Credentials: routertest.Credentials{}, Gateway: routertest.NewGateway(replies...),
		Assignments: memory.NewAssignments(), Clock: clock, IDs: coretest.NewIDs(), Log: slog.New(slog.DiscardHandler),
	})
	r := &rig{live: live, profiles: profiles, chats: chats, clock: clock,
		beats: make(chan time.Time), reconcile: make(chan time.Time)}
	ticker := func(every time.Duration) (<-chan time.Time, func()) {
		if every >= 10*time.Second {
			return r.beats, func() {}
		}
		return r.reconcile, func() {}
	}
	r.mux = http.NewServeMux()
	routerapi.Group(httpadapter.New(live, routing, clock, httpadapter.WithTicker(ticker))).Register(r.mux, core.APIOptions{
		RequestError: func(w http.ResponseWriter, r *http.Request, err error) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "validation", err.Error()))
		},
		ResponseError: core.WriteError,
	})
	return r
}

func (r *rig) do(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(core.WithCaller(req.Context(), core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}))
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	contracttest.CheckResponse(t, req, rec)
	return rec
}

func (r *rig) say(t *testing.T, agent, text string) {
	t.Helper()
	if err := r.live.UpdateLine(context.Background(), agent, text); err != nil {
		t.Fatal(err)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
	return v
}

func fixture(t *testing.T, operation, variant string) contracttest.APIFixture {
	t.Helper()
	all, err := contracttest.LoadAPIFixtures(filepath.Join("..", "..", "..", "..", "contract", "fixtures", "api"))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := all[operation][variant]
	if !ok {
		t.Fatalf("no fixture %s.%s", operation, variant)
	}
	return f
}

func good(agent string) routertest.Reply {
	return routertest.Reply{Text: fmt.Sprintf(`{"ratings":[{"agent":%q,"score":0.91,"reason":"Owns it."}]}`, agent)}
}

func TestListLiveList_MatchesTheContractAndTheFixtureShape(t *testing.T) {
	r := newRig(t)
	r.clock.Set(time.Date(2026, 11, 19, 18, 38, 55, 0, time.UTC))
	r.say(t, "builder", "Goal: ship the docs site. Now: writing the page layout and asking scribe for copy.")

	rec := r.do(t, http.MethodGet, "/v1/router/live", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := decode[api.LiveList](t, rec)
	byAgent := map[string]api.LiveEntry{}
	for _, e := range got.Items {
		byAgent[e.Agent] = e
	}
	b := byAgent["builder"]
	if b.OneLine == nil || b.OneLine.Text != "Goal: ship the docs site. Now: writing the page layout and asking scribe for copy." {
		t.Fatalf("builder = %+v", b)
	}
	if byAgent["scribe"].OneLine != nil {
		t.Fatal("scribe has no line yet and must show null")
	}
	// The fixture's entry for builder has the same keys, so the shape matches.
	var fx struct{ Items []map[string]any }
	if err := json.Unmarshal(fixture(t, "listLiveList", "").Body, &fx); err != nil {
		t.Fatal(err)
	}
	var have []map[string]any
	var raw struct{ Items []map[string]any }
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	have = raw.Items
	if !reflect.DeepEqual(keys(have[0]), keys(fx.Items[0])) {
		t.Fatalf("keys %v, fixture %v", keys(have[0]), keys(fx.Items[0]))
	}
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestListLiveList_PagesWithACursor(t *testing.T) {
	r := newRig(t)
	first := decode[api.LiveList](t, r.do(t, http.MethodGet, "/v1/router/live?limit=2", ""))
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}
	second := decode[api.LiveList](t, r.do(t, http.MethodGet, "/v1/router/live?limit=2&cursor="+*first.NextCursor, ""))
	if len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatalf("second page = %+v", second)
	}
}

func TestListLiveList_RejectsBadParameters(t *testing.T) {
	r := newRig(t)
	for _, target := range []string{"/v1/router/live?limit=0", "/v1/router/live?limit=500", "/v1/router/live?cursor=nonsense"} {
		if rec := r.do(t, http.MethodGet, target, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", target, rec.Code)
		}
	}
}

func TestRouteRequest_AssignsAndMatchesTheContract(t *testing.T) {
	r := newRig(t, good("builder"))

	rec := r.do(t, http.MethodPost, "/v1/router/route", `{"text":"Can you add a page about the budget walls to the docs site?"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	a := decode[api.Assignment](t, rec)
	if a.Chosen != "builder" || a.Router != "router" || a.ChatPath != "/v1/agents/builder/chat" || a.CreatedBy != "human:ada" {
		t.Fatalf("assignment = %+v", a)
	}
	if len(a.Shortlist) == 0 || a.Shortlist[0].Agent != "builder" || a.Shortlist[0].Score != 0.91 || a.Hops == nil {
		t.Fatalf("shortlist = %+v", a.Shortlist)
	}
	fx := fixture(t, "routeRequest", "")
	if rec.Code != fx.Status {
		t.Fatalf("status %d, fixture %d", rec.Code, fx.Status)
	}
	again := r.do(t, http.MethodGet, "/v1/router/assignments/"+a.Id, "")
	if again.Code != http.StatusOK || decode[api.Assignment](t, again).Id != a.Id {
		t.Fatalf("get = %d %s", again.Code, again.Body)
	}
}

func TestRouteRequest_ProblemsMatchTheFixtures(t *testing.T) {
	tests := []struct {
		name    string
		variant string
		body    string
		setup   func(*rig)
	}{
		{"empty text", "validation", `{"text":"  "}`, nil},
		{"no router running", "error", `{"text":"hello"}`, func(r *rig) {
			r.profiles.Set(routertest.Agent("builder", "Builder"))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, good("builder"))
			if tc.setup != nil {
				tc.setup(r)
			}
			fx := fixture(t, "routeRequest", tc.variant)

			rec := r.do(t, http.MethodPost, "/v1/router/route", tc.body)

			if rec.Code != fx.Status {
				t.Fatalf("status %d, fixture %d: %s", rec.Code, fx.Status, rec.Body)
			}
			var want, got struct{ Type string }
			_ = json.Unmarshal(fx.Body, &want)
			_ = json.Unmarshal(rec.Body.Bytes(), &got)
			if got.Type != want.Type {
				t.Fatalf("problem type %q, fixture %q", got.Type, want.Type)
			}
		})
	}
}

func TestRouteRequest_UnknownRouterIs404(t *testing.T) {
	r := newRig(t, good("builder"))
	if rec := r.do(t, http.MethodPost, "/v1/router/route", `{"text":"hello","router":"ghost"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestGetAssignment_UnknownIs404(t *testing.T) {
	r := newRig(t)
	if rec := r.do(t, http.MethodGet, "/v1/router/assignments/asg_01JD7ZK3M00000000000000009", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}

// sse is a client of the live stream.
type sse struct {
	t      *testing.T
	frames chan string
}

func openStream(t *testing.T, r *rig) (*sse, context.CancelFunc) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mux.ServeHTTP(w, req.WithContext(core.WithCaller(req.Context(), core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1})))
	}))
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/router/live/stream", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	s := &sse{t: t, frames: make(chan string, 64)}
	go func() {
		defer close(s.frames)
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				s.frames <- data
			}
		}
	}()
	t.Cleanup(cancel)
	return s, cancel
}

func (s *sse) next() map[string]any {
	s.t.Helper()
	select {
	case f, ok := <-s.frames:
		if !ok {
			s.t.Fatal("stream ended")
		}
		validator, err := contracttest.NewValidator(api.Spec)
		if err != nil {
			s.t.Fatal(err)
		}
		if err := validator.ValidateStreamFrame("streamLiveList", []byte(f)); err != nil {
			s.t.Fatalf("frame %s: %v", f, err)
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(f), &m)
		return m
	case <-time.After(5 * time.Second):
		s.t.Fatal("no frame within 5s")
		return nil
	}
}

func TestStreamLiveList_SendsTheListThenChangesAndHeartbeats(t *testing.T) {
	r := newRig(t)
	r.say(t, "builder", "Goal: build. Now: building.")
	s, cancel := openStream(t, r)

	seen := map[string]bool{}
	for range 3 {
		f := s.next()
		if f["kind"] != "entry" {
			t.Fatalf("frame = %v", f)
		}
		seen[f["entry"].(map[string]any)["agent"].(string)] = true
	}
	if !seen["builder"] || !seen["scribe"] || !seen["router"] {
		t.Fatalf("snapshot = %v", seen)
	}

	r.say(t, "scribe", "Goal: write. Now: drafting.")
	f := s.next()
	if e := f["entry"].(map[string]any); e["agent"] != "scribe" || e["one_line"].(map[string]any)["text"] != "Goal: write. Now: drafting." {
		t.Fatalf("frame = %v", f)
	}

	r.beats <- time.Time{}
	if f := s.next(); f["kind"] != "heartbeat" {
		t.Fatalf("frame = %v", f)
	}

	r.profiles.Set(routertest.Router("router"), routertest.Agent("builder", "Builder"))
	r.reconcile <- time.Time{}
	if f := s.next(); f["kind"] != "removed" || f["agent"] != "scribe" {
		t.Fatalf("frame = %v", f)
	}

	cancel()
	for range s.frames { // the stream ends once the client leaves
	}
}

func TestStreamLiveList_FrameBytesAreOneSSEEvent(t *testing.T) {
	r := newRig(t)
	s, cancel := openStream(t, r)
	defer cancel()
	f := s.next()
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(f)
	if strings.Contains(strings.TrimSpace(buf.String()), "\n") {
		t.Fatal("frame is not one line")
	}
}
