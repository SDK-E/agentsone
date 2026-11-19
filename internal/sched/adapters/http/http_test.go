// SPDX-License-Identifier: MIT

package httpadapter_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api/schedulesapi"
	"github.com/SDK-E/agentsone/internal/contract/api/tasksapi"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/core/coretest"
	httpadapter "github.com/SDK-E/agentsone/internal/sched/adapters/http"
	"github.com/SDK-E/agentsone/internal/sched/adapters/memory"
	"github.com/SDK-E/agentsone/internal/sched/app"
)

// world is the daemon's work context on fakes, mounted on a mux.
//
//	founder (root) > builder > scribe, and keeper under founder
type world struct {
	fakes *coretest.Fakes
	mux   *http.ServeMux
}

func newWorld(t *testing.T, opts ...httpadapter.TasksOption) *world {
	t.Helper()
	fakes, ports := coretest.NewFakes()
	for _, a := range []struct {
		name, manager string
		root          bool
	}{{"founder", "", true}, {"builder", "founder", false}, {"scribe", "builder", false}, {"keeper", "founder", false}} {
		fakes.Agents.Add(core.Agent{Name: a.name, Kind: core.AgentChat, Manager: a.manager, IsRoot: a.root, Runner: "echo"})
	}
	deps := app.Deps{Clock: fakes.Clock, IDs: fakes.IDs, Ports: ports, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	tasks := app.NewTasks(deps, memory.NewTasks())
	schedules := app.NewSchedules(deps, memory.NewSchedules())
	mux := http.NewServeMux()
	o := core.APIOptions{
		RequestError: func(w http.ResponseWriter, r *http.Request, err error) {
			core.WriteProblem(w, r, core.NewProblem(http.StatusBadRequest, "invalid", err.Error()))
		},
		ResponseError: core.WriteError,
	}
	tasksapi.Group(httpadapter.NewTasks(tasks, fakes.Clock, opts...)).Register(mux, o)
	schedulesapi.Group(httpadapter.NewSchedules(schedules)).Register(mux, o)
	return &world{fakes: fakes, mux: mux}
}

var (
	human   = core.Caller{Kind: core.CallerHuman, Name: "ada", UID: -1}
	builder = core.Caller{Kind: core.CallerAgent, Name: "builder", UID: 1001}
	scribe  = core.Caller{Kind: core.CallerAgent, Name: "scribe", UID: 1002}
)

// do sends one request as caller and checks the answer against the contract.
func (w *world) do(t *testing.T, caller core.Caller, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(core.WithCaller(req.Context(), caller))
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	contracttest.CheckResponse(t, req, rec)
	return rec
}

func (w *world) ok(t *testing.T, caller core.Caller, method, target, body string, want int) map[string]any {
	t.Helper()
	rec := w.do(t, caller, method, target, body)
	if rec.Code != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, target, rec.Code, want, rec.Body)
	}
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func problemType(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem: %s", rec.Body)
	}
	return p.Type
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func TestCreateTask_AHumanCreatesAndAssigns(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	got := w.ok(t, human, http.MethodPost, "/v1/tasks", `{"title":"Write the docs","assignee":"builder","priority":"high"}`, http.StatusCreated)
	if str(got, "status") != "open" || str(got, "assignee") != "builder" || str(got, "source") != "human" ||
		str(got, "created_by") != "human:ada" || str(got, "priority") != "high" {
		t.Fatalf("task %v", got)
	}
	if n := len(w.fakes.Waker.Wakes()); n != 1 {
		t.Fatalf("wakes = %d, want 1", n)
	}
}

// The contract gives createTask no 403, so a refused assignee is a 400 there.
func TestCreateTask_AnAgentCannotAssignAboveItself(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	rec := w.do(t, scribe, http.MethodPost, "/v1/tasks", `{"title":"Do my job","assignee":"founder"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "only to itself or to agents below it") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// assignTask does declare 403.
func TestAssignTask_AnAgentCannotAssignAboveItself(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	created := w.ok(t, scribe, http.MethodPost, "/v1/tasks", `{"title":"Mine"}`, http.StatusCreated)
	rec := w.do(t, scribe, http.MethodPost, "/v1/tasks/"+str(created, "id")+"/assign", `{"assignee":"founder"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestCreateTask_AFrozenAssigneeIsLocked(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	s, err := core.ParseFreezeScope("agent:keeper")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.fakes.Freezer.Freeze(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	rec := w.do(t, human, http.MethodPost, "/v1/tasks", `{"title":"Tidy","assignee":"keeper"}`)
	if rec.Code != http.StatusLocked {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestCreateTask_ABlankTitleIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	rec := w.do(t, human, http.MethodPost, "/v1/tasks", `{"title":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestTaskLifecycle(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	created := w.ok(t, builder, http.MethodPost, "/v1/tasks", `{"title":"Draft","detail":"first"}`, http.StatusCreated)
	url := "/v1/tasks/" + str(created, "id")

	got := w.ok(t, builder, http.MethodGet, url, "", http.StatusOK)
	if str(got, "title") != "Draft" {
		t.Fatalf("get %v", got)
	}
	edited := w.ok(t, builder, http.MethodPatch, url, `{"title":"Draft v2","priority":"low","detail":"second"}`, http.StatusOK)
	if str(edited, "title") != "Draft v2" || str(edited, "priority") != "low" || str(edited, "detail") != "second" {
		t.Fatalf("edit %v", edited)
	}
	assigned := w.ok(t, builder, http.MethodPost, url+"/assign", `{"assignee":"scribe"}`, http.StatusOK)
	if str(assigned, "assignee") != "scribe" {
		t.Fatalf("assign %v", assigned)
	}
	done := w.ok(t, scribe, http.MethodPost, url+"/complete", `{"outcome":"done","result":"Written."}`, http.StatusOK)
	if str(done, "status") != "done" || str(done, "result") != "Written." {
		t.Fatalf("complete %v", done)
	}
	rec := w.do(t, builder, http.MethodPost, url+"/cancel", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("cancel a closed task = %d: %s", rec.Code, rec.Body)
	}
}

func TestCancelTask(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	created := w.ok(t, human, http.MethodPost, "/v1/tasks", `{"title":"Never mind"}`, http.StatusCreated)
	got := w.ok(t, human, http.MethodPost, "/v1/tasks/"+str(created, "id")+"/cancel", "", http.StatusOK)
	if str(got, "status") != "cancelled" {
		t.Fatalf("cancel %v", got)
	}
}

func TestGetTask_UnknownIsNotFound(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	rec := w.do(t, human, http.MethodGet, "/v1/tasks/tsk_01JD7ZK3M00000000000000099", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestCompleteTask_ARefusedOutcomeIsAProblem(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	created := w.ok(t, human, http.MethodPost, "/v1/tasks", `{"title":"Do it","assignee":"builder"}`, http.StatusCreated)
	rec := w.do(t, human, http.MethodPost, "/v1/tasks/"+str(created, "id")+"/complete", `{"outcome":"done","result":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestListTasks_PagesNewestFirstWithACursor(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, title := range []string{"one", "two", "three"} {
		w.ok(t, human, http.MethodPost, "/v1/tasks", `{"title":"`+title+`","assignee":"builder"}`, http.StatusCreated)
	}
	first := w.ok(t, human, http.MethodGet, "/v1/tasks?limit=2", "", http.StatusOK)
	items, _ := first["items"].([]any)
	cursor := str(first, "next_cursor")
	if len(items) != 2 || cursor == "" {
		t.Fatalf("first page %v", first)
	}
	if title := str(items[0].(map[string]any), "title"); title != "three" {
		t.Fatalf("newest first, got %q", title)
	}
	second := w.ok(t, human, http.MethodGet, "/v1/tasks?limit=2&cursor="+cursor, "", http.StatusOK)
	rest, _ := second["items"].([]any)
	if len(rest) != 1 || second["next_cursor"] != nil || str(rest[0].(map[string]any), "title") != "one" {
		t.Fatalf("second page %v", second)
	}
}

func TestListTasks_Filters(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ok(t, human, http.MethodPost, "/v1/tasks", `{"title":"mine","assignee":"builder"}`, http.StatusCreated)
	w.ok(t, human, http.MethodPost, "/v1/tasks", `{"title":"other","assignee":"keeper"}`, http.StatusCreated)
	got := w.ok(t, human, http.MethodGet, "/v1/tasks?assignee=builder&status=open&source=human", "", http.StatusOK)
	if items, _ := got["items"].([]any); len(items) != 1 {
		t.Fatalf("filtered %v", got)
	}
	empty := w.ok(t, human, http.MethodGet, "/v1/tasks?status=done", "", http.StatusOK)
	if items, _ := empty["items"].([]any); len(items) != 0 {
		t.Fatalf("empty %v", empty)
	}
}

func TestListTasks_ABadCursorOrLimitIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, target := range []string{"/v1/tasks?cursor=***", "/v1/tasks?limit=0", "/v1/tasks?limit=201"} {
		rec := w.do(t, human, http.MethodGet, target, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d: %s", target, rec.Code, rec.Body)
		}
	}
}

func TestSchedules_Lifecycle(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	created := w.ok(t, builder, http.MethodPost, "/v1/schedules",
		`{"name":"morning","calendar":"*-*-* 09:00:00","message":"Check the board."}`, http.StatusCreated)
	if str(created, "agent") != "builder" || created["enabled"] != true || created["next_run_at"] == nil {
		t.Fatalf("created %v", created)
	}
	url := "/v1/schedules/" + str(created, "id")
	if got := w.ok(t, builder, http.MethodGet, url, "", http.StatusOK); str(got, "name") != "morning" {
		t.Fatalf("get %v", got)
	}
	off := w.ok(t, builder, http.MethodPatch, url, `{"enabled":false,"message":"Look again."}`, http.StatusOK)
	if off["enabled"] != false || off["next_run_at"] != nil || str(off, "message") != "Look again." {
		t.Fatalf("update %v", off)
	}
	w.ok(t, builder, http.MethodDelete, url, "", http.StatusNoContent)
	if rec := w.do(t, builder, http.MethodGet, url, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("after delete = %d", rec.Code)
	}
}

func TestSchedules_AnAgentSchedulesForAnAgentBelowIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	got := w.ok(t, builder, http.MethodPost, "/v1/schedules",
		`{"agent":"scribe","name":"nightly","calendar":"daily","message":"Tidy up."}`, http.StatusCreated)
	if str(got, "agent") != "scribe" {
		t.Fatalf("created %v", got)
	}
	if rec := w.do(t, scribe, http.MethodPost, "/v1/schedules",
		`{"agent":"builder","name":"x","calendar":"daily","message":"Hi."}`); rec.Code != http.StatusForbidden {
		t.Fatalf("scheduling above = %d: %s", rec.Code, rec.Body)
	}
}

func TestSchedules_ABadCalendarIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	rec := w.do(t, builder, http.MethodPost, "/v1/schedules", `{"name":"bad","calendar":"every tuesday","message":"Hi."}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := problemType(t, rec); !strings.HasSuffix(got, ":validation") {
		t.Fatalf("problem type %q", got)
	}
}

func TestListSchedules_PagesOldestFirstAndFiltersByAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, name := range []string{"a", "b", "c"} {
		w.ok(t, builder, http.MethodPost, "/v1/schedules", `{"name":"`+name+`","calendar":"hourly","message":"Go."}`, http.StatusCreated)
	}
	w.ok(t, human, http.MethodPost, "/v1/schedules", `{"agent":"keeper","name":"k","calendar":"hourly","message":"Go."}`, http.StatusCreated)

	first := w.ok(t, human, http.MethodGet, "/v1/schedules?agent=builder&limit=2", "", http.StatusOK)
	items, _ := first["items"].([]any)
	cursor := str(first, "next_cursor")
	if len(items) != 2 || cursor == "" || str(items[0].(map[string]any), "name") != "a" {
		t.Fatalf("first page %v", first)
	}
	second := w.ok(t, human, http.MethodGet, "/v1/schedules?agent=builder&limit=2&cursor="+cursor, "", http.StatusOK)
	rest, _ := second["items"].([]any)
	if len(rest) != 1 || second["next_cursor"] != nil || str(rest[0].(map[string]any), "name") != "c" {
		t.Fatalf("second page %v", second)
	}
	if rec := w.do(t, human, http.MethodGet, "/v1/schedules?cursor=***", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor = %d", rec.Code)
	}
	if rec := w.do(t, human, http.MethodGet, "/v1/schedules?limit=0", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit = %d", rec.Code)
	}
}

// stream opens the task stream on a real server and returns a line scanner.
func stream(t *testing.T, w *world, lastEventID string) (*bufio.Scanner, context.CancelFunc) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mux.ServeHTTP(rw, r.WithContext(core.WithCaller(r.Context(), human)))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/tasks/stream", nil)
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
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	return bufio.NewScanner(resp.Body), cancel
}

// nextEvent reads one event: its id line and its data line.
func nextEvent(t *testing.T, sc *bufio.Scanner) (id string, data map[string]any) {
	t.Helper()
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				t.Fatalf("frame %q: %v", line, err)
			}
			return id, data
		}
	}
	t.Fatalf("stream ended: %v", sc.Err())
	return "", nil
}

func TestStreamTasks_ReplaysFramesAfterLastEventID(t *testing.T) {
	t.Parallel()
	w := newWorld(t, httpadapter.WithHeartbeat(0))
	w.ok(t, human, http.MethodPost, "/v1/tasks", `{"title":"before"}`, http.StatusCreated)
	sc, _ := stream(t, w, "00000000000000000000")
	id, frame := nextEvent(t, sc)
	task, _ := frame["task"].(map[string]any)
	if len(id) != 20 || frame["kind"] != "task" || str(task, "title") != "before" {
		t.Fatalf("replayed frame id=%q %v", id, frame)
	}
}

func TestStreamTasks_SendsLiveChanges(t *testing.T) {
	t.Parallel()
	w := newWorld(t, httpadapter.WithHeartbeat(0))
	sc, _ := stream(t, w, "")
	// The subscription exists once the headers are back, so this change is live.
	w.ok(t, human, http.MethodPost, "/v1/tasks", `{"title":"live"}`, http.StatusCreated)
	_, frame := nextEvent(t, sc)
	if task, _ := frame["task"].(map[string]any); str(task, "title") != "live" {
		t.Fatalf("frame %v", frame)
	}
}

func TestStreamTasks_SendsHeartbeatsWithoutAnID(t *testing.T) {
	t.Parallel()
	w := newWorld(t, httpadapter.WithHeartbeat(time.Millisecond))
	sc, _ := stream(t, w, "")
	id, frame := nextEvent(t, sc)
	if id != "" || frame["kind"] != "heartbeat" || frame["time"] == nil {
		t.Fatalf("heartbeat id=%q %v", id, frame)
	}
}

func TestStreamTasks_EndsWhenTheClientLeaves(t *testing.T) {
	t.Parallel()
	w := newWorld(t, httpadapter.WithHeartbeat(0))
	sc, cancel := stream(t, w, "")
	cancel()
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data: ") {
			t.Fatalf("unexpected frame %q", sc.Text())
		}
	}
}

func TestBodylessWrites_AreRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	created := w.ok(t, human, http.MethodPost, "/v1/tasks", `{"title":"x","assignee":"builder"}`, http.StatusCreated)
	sched := w.ok(t, builder, http.MethodPost, "/v1/schedules", `{"name":"n","calendar":"daily","message":"m"}`, http.StatusCreated)
	for _, c := range []struct{ method, target string }{
		{http.MethodPost, "/v1/tasks"},
		{http.MethodPatch, "/v1/tasks/" + str(created, "id")},
		{http.MethodPost, "/v1/tasks/" + str(created, "id") + "/assign"},
		{http.MethodPost, "/v1/tasks/" + str(created, "id") + "/complete"},
		{http.MethodPost, "/v1/schedules"},
		{http.MethodPatch, "/v1/schedules/" + str(sched, "id")},
	} {
		req := httptest.NewRequest(c.method, c.target, bytes.NewReader(nil))
		req = req.WithContext(core.WithCaller(req.Context(), human))
		rec := httptest.NewRecorder()
		w.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s = %d: %s", c.method, c.target, rec.Code, rec.Body)
		}
	}
}
