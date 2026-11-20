// SPDX-License-Identifier: MIT

package daemontest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/contract/eventdata"
	"github.com/SDK-E/agentsone/internal/core"
)

// base is the host of every request: the socket is dialled whatever the
// URL says.
const base = "http://agentsoned/v1"

// Client calls the API as one caller: a human with a session cookie on the
// daemon's TCP listener, where the edge's web door forwards the interface's
// requests, or an agent (see AsAgent).
type Client struct {
	// Name is the human's or the agent's name.
	Name string
	d    *Daemon
	c    *http.Client
}

func (d *Daemon) newClient(name string) *Client {
	d.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		d.t.Fatalf("cookie jar: %v", err)
	}
	addr := d.tcp
	return &Client{Name: name, d: d, c: &http.Client{Jar: jar, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
	}}}
}

// AsAgent is a client that acts as agent the way the agent's own process
// does on the socket: the kernel's peer credentials and the agent's cgroup
// name it, so a root agent is root. Those are Linux only (TESTING.md B5b),
// so the client hands each request to the daemon's composed handler with
// that caller set, as the socket would. Every module's own rules still
// apply. Humans only read the society; hiring, for one, is for agents such
// as the founder.
func (d *Daemon) AsAgent(name string) *Client {
	d.t.Helper()
	ctx := core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "daemontest", UID: -1, IsRoot: true})
	a, err := d.Ports.Agents.GetAgent(ctx, name)
	if err != nil {
		d.t.Fatalf("agent %s: %v", name, err)
	}
	c := d.newClient(name)
	c.c.Transport = peerTransport{door: d.door, caller: core.Caller{Kind: core.CallerAgent, Name: a.Name, UID: a.UID, IsRoot: a.IsRoot}}
	return c
}

// peerTransport serves each request in process as caller.
type peerTransport struct {
	door   *webDoor
	caller core.Caller
}

func (p peerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return p.door.serveAs(req, p.caller).Result(), nil
}

// SignInHuman creates the machine's first human and signs it in. Like on a
// fresh machine, the first human starts the society: the small seed
// provisions its founder, router and waker. SignInHuman returns once the
// founder exists.
func (d *Daemon) SignInHuman(name string) *Client {
	d.t.Helper()
	h := d.newClient(name)
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	hired, err := d.Ports.Subscribe.Subscribe(ctx, core.EventFilter{Types: []string{eventdata.TypeAgentsHired}})
	if err != nil {
		d.t.Fatalf("subscribe to hires: %v", err)
	}
	// The account lives only in this test's temporary core.db; its
	// password is no secret, just long enough for the password rule.
	pass := name + " uses the test harness"
	b, err := json.Marshal(api.FirstHumanCreate{Name: name, DisplayName: name, Password: &pass}) // #nosec G117 -- a test account in a temporary core.db; the password is no secret
	if err != nil {
		d.t.Fatalf("encode the first human: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, base+"/setup/first-human", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if rec := d.door.serveAs(req, core.Anonymous); rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		d.t.Fatalf("POST /setup/first-human through the door: status %d: %s", rec.Code, rec.Body)
	}
	h.Do(http.MethodPost, "/session", api.LoginRequest{Name: name, Password: &pass}, nil)
	for founder := false; !founder; {
		select {
		case ev, ok := <-hired:
			if !ok {
				d.t.Fatalf("the founder was not hired within %s", Timeout)
			}
			var data eventdata.AgentsHired
			founder = json.Unmarshal(ev.Data, &data) == nil && data.Agent == "founder"
		case <-ctx.Done():
			d.t.Fatalf("the founder was not hired within %s", Timeout)
		}
	}
	return h
}

// Do calls one API operation as the human: body, when not nil, is sent as
// JSON, and a 2xx answer is decoded into out, when not nil. Any other
// status fails the test with the problem the daemon answered.
func (h *Client) Do(method, path string, body, out any) {
	h.d.t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.d.t.Fatalf("%s %s: encode body: %v", method, path, err)
		}
		r = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, base+path, r)
	if err != nil {
		h.d.t.Fatalf("%s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	h.authorize(req)
	resp, err := h.c.Do(req)
	if err != nil {
		h.d.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		h.d.t.Fatalf("%s %s: read answer: %v", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		h.d.t.Fatalf("%s %s: status %d: %s", method, path, resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			h.d.t.Fatalf("%s %s: decode answer: %v: %s", method, path, err, raw)
		}
	}
}

// authorize makes req look like the interface's: a same-origin browser
// request, which the TCP listener asks of every change.
func (h *Client) authorize(req *http.Request) {
	req.Header.Set("Origin", "http://"+req.URL.Host)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
}

// HireEcho hires a chat agent named name that runs the echo runner, below
// the founder. The client must be an agent allowed to hire, such as the
// founder.
func (h *Client) HireEcho(name string) api.Agent {
	h.d.t.Helper()
	founder := "founder"
	var a api.Agent
	h.Do(http.MethodPost, "/agents", api.AgentCreate{
		Name: name, DisplayName: name, Kind: api.AgentKindChat, Role: "Echoes what it is told",
		Runner: "echo", Manager: &founder,
	}, &a)
	return a
}

// Agent reads an agent.
func (h *Client) Agent(name string) api.Agent {
	h.d.t.Helper()
	var a api.Agent
	h.Do(http.MethodGet, "/agents/"+url.PathEscape(name), nil, &a)
	return a
}

// Chat sends text to agent's chat and returns the agent's reply once the
// daemon has stored it.
func (h *Client) Chat(agent, text string) api.ChatMessage {
	h.d.t.Helper()
	s := h.Stream("/agents/" + url.PathEscape(agent) + "/chat/stream")
	defer s.Close()
	h.Do(http.MethodPost, "/agents/"+url.PathEscape(agent)+"/chat", api.ChatMessageCreate{Text: text}, nil)
	var reply api.ChatMessageFrame
	s.Wait("chat reply from "+agent, func(raw json.RawMessage) bool {
		var f api.ChatMessageFrame
		if json.Unmarshal(raw, &f) != nil || f.Kind != api.ChatMessageFrameKindMessage {
			return false
		}
		reply = f
		return f.Message.Role == api.ChatRoleAgent
	})
	return reply.Message
}

// Output opens agent's output stream: what its runner says outside a chat.
func (h *Client) Output(agent string) *Stream {
	h.d.t.Helper()
	return h.Stream("/agents/" + url.PathEscape(agent) + "/output")
}

// SendBus sends a direct bus message to agent.
func (h *Client) SendBus(to, text string) api.BusMessage {
	h.d.t.Helper()
	var m api.BusMessage
	h.Do(http.MethodPost, "/bus/messages", api.BusMessageCreate{To: &to, Text: text}, &m)
	return m
}

// CreateTask creates a task.
func (h *Client) CreateTask(in api.TaskCreate) api.Task {
	h.d.t.Helper()
	var task api.Task
	h.Do(http.MethodPost, "/tasks", in, &task)
	return task
}

// Task reads a task.
func (h *Client) Task(id string) api.Task {
	h.d.t.Helper()
	var task api.Task
	h.Do(http.MethodGet, "/tasks/"+url.PathEscape(id), nil, &task)
	return task
}

// CreateSchedule creates a schedule.
func (h *Client) CreateSchedule(in api.ScheduleCreate) api.Schedule {
	h.d.t.Helper()
	var s api.Schedule
	h.Do(http.MethodPost, "/schedules", in, &s)
	return s
}

// CreateTrigger creates a trigger.
func (h *Client) CreateTrigger(in api.TriggerCreate) api.Trigger {
	h.d.t.Helper()
	var tr api.Trigger
	h.Do(http.MethodPost, "/triggers", in, &tr)
	return tr
}

// PublishEvent publishes an event.
func (h *Client) PublishEvent(in api.EventPublish) api.Event {
	h.d.t.Helper()
	var e api.Event
	h.Do(http.MethodPost, "/events", in, &e)
	return e
}

// Audit reads the first page of the audit trail.
func (h *Client) Audit() []api.AuditRecord {
	h.d.t.Helper()
	var l api.AuditList
	h.Do(http.MethodGet, "/audit", nil, &l)
	return l.Items
}

// Stream is one server-sent event stream of the API, read frame by frame.
type Stream struct {
	d      *Daemon
	what   string
	frames chan json.RawMessage
	cancel context.CancelFunc
	body   io.Closer
	wg     sync.WaitGroup
	err    error // why the stream ended; read after frames closes
}

// Stream opens the event stream at path and returns once the daemon has
// subscribed it, so no frame sent after it returns is missed.
func (h *Client) Stream(path string) *Stream {
	h.d.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		cancel()
		h.d.t.Fatalf("GET %s: %v", path, err)
	}
	h.authorize(req)
	opened := make(chan *http.Response, 1)
	failed := make(chan error, 1)
	go func() {
		resp, err := h.c.Do(req) //nolint:bodyclose // closed by Stream.Close
		if err != nil {
			failed <- err
			return
		}
		opened <- resp
	}()
	var resp *http.Response
	select {
	case resp = <-opened:
	case err := <-failed:
		cancel()
		h.d.t.Fatalf("GET %s: %v", path, err)
	case <-time.After(Timeout):
		cancel()
		h.d.t.Fatalf("GET %s: no answer within %s", path, Timeout)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		h.d.t.Fatalf("GET %s: status %d: %s", path, resp.StatusCode, raw)
	}
	s := &Stream{d: h.d, what: path, frames: make(chan json.RawMessage, 64), cancel: cancel, body: resp.Body}
	lines := bufio.NewScanner(resp.Body)
	lines.Buffer(make([]byte, 0, 64<<10), 16<<20)
	// The daemon writes ": open" once it has subscribed the stream.
	openSeen := make(chan bool, 1)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(s.frames)
		first := true
		var data strings.Builder
		for lines.Scan() {
			line := lines.Text()
			switch {
			case first && line == ": open":
				first = false
				openSeen <- true
			case strings.HasPrefix(line, "data: "):
				data.WriteString(strings.TrimPrefix(line, "data: "))
			case line == "" && data.Len() > 0:
				// A test that stopped reading must not keep the reader
				// from ending at Close.
				select {
				case s.frames <- json.RawMessage(data.String()):
				case <-ctx.Done():
					return
				}
				data.Reset()
			}
		}
		if first {
			openSeen <- false
		}
		s.err = lines.Err()
	}()
	select {
	case ok := <-openSeen:
		if !ok {
			s.Close()
			h.d.t.Fatalf("GET %s: the stream ended before it opened: %v", path, s.err)
		}
	case <-time.After(Timeout):
		s.Close()
		h.d.t.Fatalf("GET %s: the stream did not open within %s", path, Timeout)
	}
	h.d.t.Cleanup(s.Close)
	return s
}

// Wait reads frames until match accepts one, and fails the test when the
// stream ends or Timeout passes first. what names the frame for the failure
// message.
func (s *Stream) Wait(what string, match func(json.RawMessage) bool) {
	s.d.t.Helper()
	deadline := time.After(Timeout)
	var seen []string
	for {
		select {
		case raw, ok := <-s.frames:
			if !ok {
				s.d.t.Fatalf("%s ended before %s (%v); frames: %v; runner errors: %v", s.what, what, s.err, seen, s.d.RunnerErrors())
			}
			if match(raw) {
				return
			}
			seen = append(seen, string(raw))
		case <-deadline:
			s.d.t.Fatalf("no %s within %s; frames: %v; runner errors: %v", what, Timeout, seen, s.d.RunnerErrors())
		}
	}
}

// WaitText waits for an output frame whose text holds want and returns it.
func (s *Stream) WaitText(want string) api.OutputFrame {
	s.d.t.Helper()
	var got api.OutputFrame
	s.Wait(fmt.Sprintf("output holding %q", want), func(raw json.RawMessage) bool {
		var f api.OutputFrame
		if json.Unmarshal(raw, &f) != nil || f.Kind != api.OutputFrameKindOutput {
			return false
		}
		got = f
		return strings.Contains(f.Text, want)
	})
	return got
}

// Close ends the stream. It is safe to call more than once.
func (s *Stream) Close() {
	s.cancel()
	_ = s.body.Close()
	s.wg.Wait()
}
