// SPDX-License-Identifier: MIT

package rpc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/adapters/rpc"
	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/internal/contract/common"
	"github.com/SDK-E/agentsone/internal/contract/contracttest"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

const hello = `{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocolVersions":["1.0"],"runner":{"name":"echo","version":"1.0.0"},"capabilities":{"streaming":true,"cancel":true,"compaction":true,"resume":true}}}`

const waitFor = 5 * time.Second

var cancelParams = protocol.CancelParams{TurnID: "trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3J", Reason: "human"}

var (
	schemaOnce sync.Once
	schemas    *contracttest.SchemaSet
	schemaErr  error
)

func schemaSet(t *testing.T) *contracttest.SchemaSet {
	t.Helper()
	schemaOnce.Do(func() {
		schemas, schemaErr = contracttest.NewSchemaSet(filepath.Join("..", "..", "..", "..", "..", "contract", "schemas"))
	})
	if schemaErr != nil {
		t.Fatal(schemaErr)
	}
	return schemas
}

// fakeHandler records what the daemon side received.
type fakeHandler struct {
	mu       sync.Mutex
	statuses []protocol.StatusParams
	outputs  []protocol.OutputParams
	profiles []protocol.ProfileUpdateParams
	usages   []protocol.UsageParams
	logs     []protocol.LogParams
	errs     []protocol.ErrorParams
	heard    int
	busErr   error
	closed   chan error
}

func newFakeHandler() *fakeHandler { return &fakeHandler{closed: make(chan error, 1)} }

func (h *fakeHandler) Hello(_ context.Context, version string, _ protocol.HelloParams) (protocol.HelloResult, error) {
	summary := "Earlier work."
	return protocol.HelloResult{
		ProtocolVersion: protocol.ProtocolVersion(version),
		Agent: protocol.AgentIdentity{Name: "founder", Kind: "always-on", IsRoot: true, Team: "founder",
			OneLine: "Goal: test. Now: starting.", Charter: "c", Goals: []common.OneLine{"g"}},
		Mcp: protocol.McpEndpointHttp{Transport: "streamable-http", Url: "http://127.0.0.1:7700/mcp",
			Token: "test-mcp-token", TokenExpiresAt: common.Timestamp(time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC))}, // #nosec G101 -- test value
		Model: protocol.ModelGateway{BaseUrl: "http://gateway.agentsone.internal:4000/v1", Name: "autoagents/one",
			Token: "test-gw", Weights: []protocol.Weight{"normal"}, DefaultWeight: "normal"},
		Session: protocol.SessionState{Summary: &summary},
		Config:  protocol.RunnerConfig{HeartbeatIntervalMs: 15000, HeartbeatTimeoutMs: 45000, MaxLineBytes: 1048576, Runner: protocol.RunnerConfigRunner{}},
	}, nil
}
func (h *fakeHandler) Heard() { h.mu.Lock(); h.heard++; h.mu.Unlock() }
func (h *fakeHandler) Status(_ context.Context, p protocol.StatusParams) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statuses = append(h.statuses, p)
	return nil
}
func (h *fakeHandler) Output(_ context.Context, p protocol.OutputParams) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.outputs = append(h.outputs, p)
	return nil
}
func (h *fakeHandler) Usage(_ context.Context, p protocol.UsageParams) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.usages = append(h.usages, p)
	return nil
}
func (h *fakeHandler) ProfileUpdate(_ context.Context, p protocol.ProfileUpdateParams) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.profiles = append(h.profiles, p)
	return nil
}
func (h *fakeHandler) Log(_ context.Context, p protocol.LogParams) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, p)
}
func (h *fakeHandler) Error(_ context.Context, p protocol.ErrorParams) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.errs = append(h.errs, p)
}
func (h *fakeHandler) BusSend(context.Context, protocol.BusSendParams) (protocol.BusSendResult, error) {
	if h.busErr != nil {
		return protocol.BusSendResult{}, h.busErr
	}
	return protocol.BusSendResult{MessageID: "msg_01JD8X3Q5Y6Z7A8B9C0D1E2F43"}, nil
}
func (h *fakeHandler) TaskDone(context.Context, protocol.TaskDoneParams) error { return nil }
func (h *fakeHandler) Closed(err error)                                        { h.closed <- err }

// peer is the fake runner end of a real Unix socket.
type peer struct {
	t     *testing.T
	nc    net.Conn
	lines chan string
	conn  *rpc.Conn
}

func newPeer(t *testing.T, h rpc.Handler, opts rpc.Options) *peer {
	t.Helper()
	dir, err := os.MkdirTemp("", "ao") // B5c: short socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "r.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	nc, err := net.Dial("unix", filepath.Join(dir, "r.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	if opts.Versions == nil {
		opts.Versions = domain.DaemonVersions
	}
	p := &peer{t: t, nc: nc, lines: make(chan string, 64)}
	p.conn = rpc.Serve(t.Context(), server, h, opts)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(p.lines)
		sc := bufio.NewScanner(nc)
		sc.Buffer(make([]byte, 0, 1<<16), 4<<20)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
	}()
	t.Cleanup(func() {
		_ = nc.Close()
		_ = p.conn.Close()
		<-p.conn.Done()
		<-readerDone
	})
	return p
}

func (p *peer) send(line string) {
	p.t.Helper()
	if _, err := p.nc.Write([]byte(line + "\n")); err != nil {
		p.t.Fatal(err)
	}
}

// next returns the next line from the daemon and checks rule F1 on it.
func (p *peer) next() map[string]any {
	p.t.Helper()
	select {
	case line, ok := <-p.lines:
		if !ok {
			p.t.Fatal("daemon closed the connection, want a line")
		}
		if err := schemaSet(p.t).Validate("messages", []byte(line)); err != nil {
			p.t.Fatalf("F1: daemon line fails messages.schema.json: %v\n%s", err, line)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			p.t.Fatal(err)
		}
		return m
	case <-time.After(waitFor):
		p.t.Fatal("no line from the daemon within 5s")
		return nil
	}
}

func (p *peer) expectClosed() {
	p.t.Helper()
	select {
	case _, ok := <-p.lines:
		if ok {
			p.t.Fatal("got a line, want the socket closed")
		}
	case <-time.After(waitFor):
		p.t.Fatal("socket still open after 5s")
	}
}

func errCode(t *testing.T, m map[string]any) (code int, id any) {
	t.Helper()
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("line %v is not an error response", m)
	}
	return int(e["code"].(float64)), m["id"]
}

func handshake(t *testing.T, h rpc.Handler) *peer {
	t.Helper()
	p := newPeer(t, h, rpc.Options{})
	p.send(hello)
	if m := p.next(); m["result"] == nil {
		t.Fatalf("hello answer = %v, want welcome", m)
	}
	p.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}`)
	// Lines are handled in order: once this request is answered, the status was handled.
	p.send(`{"jsonrpc":"2.0","id":1000,"method":"ping","params":{}}`)
	if code, _ := errCode(t, p.next()); code != rpc.CodeMethodNotFound {
		t.Fatalf("sync request code = %d", code)
	}
	return p
}

// H3, H4: the first daemon line is the welcome, with the negotiated version.
func TestConn_AnswersHelloWithWelcome(t *testing.T) {
	t.Parallel()
	p := newPeer(t, newFakeHandler(), rpc.Options{})
	p.send(`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocolVersions":["2.0","1.0"],"runner":{"name":"echo","version":"1.0.0"},"capabilities":{"streaming":true,"cancel":true,"compaction":true,"resume":true}}}`)
	m := p.next()
	res, ok := m["result"].(map[string]any)
	if !ok || m["id"] != float64(1) {
		t.Fatalf("first line = %v, want hello response with id 1", m)
	}
	if res["protocolVersion"] != "1.0" {
		t.Fatalf("protocolVersion = %v, want 1.0 (H4)", res["protocolVersion"])
	}
}

// H5: no common version answers -32002 with data.supported and closes.
func TestConn_RefusesRunnerWithNoCommonVersion(t *testing.T) {
	t.Parallel()
	p := newPeer(t, newFakeHandler(), rpc.Options{})
	p.send(strings.Replace(hello, `["1.0"]`, `["9.0"]`, 1))
	m := p.next()
	code, _ := errCode(t, m)
	data := m["error"].(map[string]any)["data"].(map[string]any)
	// The daemon names the versions it speaks: 1.1 and 1.0.
	if code != rpc.CodeUnsupportedVersion || len(data["supported"].([]any)) != 2 {
		t.Fatalf("answer = %v, want -32002 with supported", m)
	}
	p.expectClosed()
}

// H6: any message before the welcome, and a second hello, get -32000.
func TestConn_RejectsMessagesOutOfOrder(t *testing.T) {
	t.Parallel()
	t.Run("request before hello", func(t *testing.T) {
		t.Parallel()
		p := newPeer(t, newFakeHandler(), rpc.Options{})
		p.send(`{"jsonrpc":"2.0","id":1,"method":"task.done","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3H","taskId":"tsk_01JD8X3Q5Y6Z7A8B9C0D1E2F50","outcome":"done","summary":"x"}}`)
		if code, id := errCode(t, p.next()); code != rpc.CodeOutOfOrder || id != float64(1) {
			t.Fatalf("code=%d id=%v, want -32000 id 1", code, id)
		}
	})
	t.Run("notification before hello", func(t *testing.T) {
		t.Parallel()
		p := newPeer(t, newFakeHandler(), rpc.Options{})
		p.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}`)
		if code, id := errCode(t, p.next()); code != rpc.CodeOutOfOrder || id != nil {
			t.Fatalf("code=%d id=%v, want -32000 id null", code, id)
		}
	})
	t.Run("second hello", func(t *testing.T) {
		t.Parallel()
		p := handshake(t, newFakeHandler())
		p.send(strings.Replace(hello, `"id":1`, `"id":2`, 1))
		if code, id := errCode(t, p.next()); code != rpc.CodeOutOfOrder || id != float64(2) {
			t.Fatalf("code=%d id=%v, want -32000 id 2", code, id)
		}
	})
}

// H1: a runner that does not say hello in time is disconnected.
func TestConn_ClosesWhenHelloDeadlinePasses(t *testing.T) {
	t.Parallel()
	deadline := make(chan struct{})
	p := newPeer(t, newFakeHandler(), rpc.Options{HelloDeadline: deadline})
	close(deadline)
	p.expectClosed()
}

func TestConn_KeepsConnectionWhenHelloArrivesBeforeDeadline(t *testing.T) {
	t.Parallel()
	deadline := make(chan struct{})
	h := newFakeHandler()
	p := newPeer(t, h, rpc.Options{HelloDeadline: deadline})
	p.send(hello)
	p.next()
	close(deadline)
	p.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}`)
	p.send(`{"jsonrpc":"2.0","id":2,"method":"wake","params":{}}`)
	if code, _ := errCode(t, p.next()); code != rpc.CodeMethodNotFound {
		t.Fatalf("code = %d, want connection open and -32601", code)
	}
}

// F3: an overlong line gets -32001 with id null and the connection stays open.
func TestConn_AnswersOverlongLineAndStaysOpen(t *testing.T) {
	t.Parallel()
	p := newPeer(t, newFakeHandler(), rpc.Options{MaxLineBytes: 2048})
	p.send(`{"jsonrpc":"2.0","method":"log","params":{"level":"info","message":"` + strings.Repeat("x", 100_000) + `"}}`)
	if code, id := errCode(t, p.next()); code != rpc.CodeLineTooLarge || id != nil {
		t.Fatalf("code=%d id=%v, want -32001 id null", code, id)
	}
	p.send(hello)
	if m := p.next(); m["result"] == nil {
		t.Fatalf("after overlong line: %v, want welcome", m)
	}
}

// F4: not JSON gets -32700; a batch gets -32600; both with id null.
func TestConn_AnswersUnreadableLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		code int
	}{
		{"not json", `{"jsonrpc":`, rpc.CodeParseError},
		{"batch", `[` + hello + `]`, rpc.CodeInvalidRequest},
		{"wrong jsonrpc version", `{"jsonrpc":"1.0","id":1,"method":"hello"}`, rpc.CodeInvalidRequest},
		{"no method and no id", `{"jsonrpc":"2.0"}`, rpc.CodeInvalidRequest},
		{"string id", `{"jsonrpc":"2.0","id":"a","method":"hello","params":{}}`, rpc.CodeInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newPeer(t, newFakeHandler(), rpc.Options{})
			p.send(tt.line)
			if code, id := errCode(t, p.next()); code != tt.code || id != nil {
				t.Fatalf("code=%d id=%v, want %d id null", code, id, tt.code)
			}
		})
	}
}

// F7: an unknown method, or a daemon-to-runner method sent by the runner, gets -32601.
func TestConn_AnswersWrongDirectionWithMethodNotFound(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"wake", "deliver", "cancel", "sleep", "shutdown", "dance"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			p := handshake(t, newFakeHandler())
			p.send(`{"jsonrpc":"2.0","id":2,"method":"` + method + `","params":{}}`)
			if code, id := errCode(t, p.next()); code != rpc.CodeMethodNotFound || id != float64(2) {
				t.Fatalf("code=%d id=%v, want -32601 id 2", code, id)
			}
		})
	}
}

// F8: params that fail the schema get -32602.
func TestConn_AnswersBadParamsWithInvalidParams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
	}{
		{"missing field", `{"jsonrpc":"2.0","id":2,"method":"bus.send","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","to":"watcher","subject":"s"}}`},
		{"unknown field", `{"jsonrpc":"2.0","id":2,"method":"task.done","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","taskId":"tsk_01JD8X3Q5Y6Z7A8B9C0D1E2F50","outcome":"done","summary":"s","extra":1}}`},
		{"params not an object", `{"jsonrpc":"2.0","id":2,"method":"bus.send","params":[1]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := handshake(t, newFakeHandler())
			p.send(tt.line)
			if code, id := errCode(t, p.next()); code != rpc.CodeInvalidParams || id != float64(2) {
				t.Fatalf("code=%d id=%v, want -32602 id 2", code, id)
			}
		})
	}
	t.Run("hello without capabilities", func(t *testing.T) {
		t.Parallel()
		p := newPeer(t, newFakeHandler(), rpc.Options{})
		p.send(`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocolVersions":["1.0"],"runner":{"name":"echo","version":"1.0.0"}}}`)
		if code, _ := errCode(t, p.next()); code != rpc.CodeInvalidParams {
			t.Fatalf("code=%d, want -32602", code)
		}
	})
}

// F6: requests get exactly one response; notifications get none.
func TestConn_AnswersRequestsButNotNotifications(t *testing.T) {
	t.Parallel()
	h := newFakeHandler()
	p := handshake(t, h)
	p.send(`{"jsonrpc":"2.0","method":"heartbeat","params":{"uptimeMs":15002}}`)
	p.send(`{"jsonrpc":"2.0","method":"log","params":{"level":"info","message":"m"}}`)
	p.send(`{"jsonrpc":"2.0","method":"error","params":{"code":1010,"message":"Budget exhausted","fatal":false}}`)
	p.send(`{"jsonrpc":"2.0","method":"usage","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","model":"echo","weight":"quick","tokensIn":1,"tokensOut":1,"costMicroUsd":0}}`)
	p.send(`{"jsonrpc":"2.0","method":"profile.update","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","line":"Goal: g. Now: n."}}`)
	p.send(`{"jsonrpc":"2.0","method":"output","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","chunk":0,"text":"hi","final":true}}`)
	p.send(`{"jsonrpc":"2.0","id":2,"method":"bus.send","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","to":"watcher","subject":"s","body":"b"}}`)
	m := p.next()
	if m["id"] != float64(2) || m["result"].(map[string]any)["messageId"] != "msg_01JD8X3Q5Y6Z7A8B9C0D1E2F43" {
		t.Fatalf("first line after notifications = %v, want bus.send result id 2", m)
	}
	p.send(`{"jsonrpc":"2.0","id":3,"method":"task.done","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","taskId":"tsk_01JD8X3Q5Y6Z7A8B9C0D1E2F50","outcome":"done","summary":"s"}}`)
	if m := p.next(); m["id"] != float64(3) || m["result"] == nil {
		t.Fatalf("task.done answer = %v", m)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.logs) != 1 || len(h.errs) != 1 || len(h.usages) != 1 || len(h.profiles) != 1 || len(h.outputs) != 1 || len(h.statuses) != 1 {
		t.Fatalf("handler saw logs=%d errs=%d usages=%d profiles=%d outputs=%d statuses=%d",
			len(h.logs), len(h.errs), len(h.usages), len(h.profiles), len(h.outputs), len(h.statuses))
	}
	if h.heard < 9 {
		t.Fatalf("Heard called %d times, want once per line", h.heard)
	}
}

func TestConn_MapsHandlerErrorsToProtocolCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		code int
	}{
		{"unknown recipient", domain.ErrUnknownRecipient, rpc.CodeUnknownRecipient},
		{"rate limited", domain.ErrRateLimited, rpc.CodeRateLimited},
		{"internal error hides detail", errors.New("disk on fire at /var/lib"), rpc.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newFakeHandler()
			h.busErr = tt.err
			p := handshake(t, h)
			p.send(`{"jsonrpc":"2.0","id":2,"method":"bus.send","params":{"turnId":"trn_01JD8X3Q5Y6Z7A8B9C0D1E2F3G","to":"ghost","subject":"s","body":"b"}}`)
			m := p.next()
			if code, _ := errCode(t, m); code != tt.code {
				t.Fatalf("code = %d, want %d", code, tt.code)
			}
			if msg := m["error"].(map[string]any)["message"].(string); strings.Contains(msg, "/var/lib") {
				t.Fatalf("error message leaks detail: %q", msg)
			}
		})
	}
}

// H8: no daemon request before the runner's first status.
func TestConn_RefusesCallBeforeFirstStatus(t *testing.T) {
	t.Parallel()
	p := newPeer(t, newFakeHandler(), rpc.Options{})
	p.send(hello)
	p.next()
	err := p.conn.Call(t.Context(), protocol.MethodSleep, protocol.SleepParams{Reason: "idle", MaxSummaryChars: 4096}, nil)
	if !errors.Is(err, rpc.ErrNotReady) {
		t.Fatalf("Call before status error = %v, want ErrNotReady", err)
	}
}

// H8: WaitReady holds a daemon request until the first status is accepted,
// and gives up when the connection ends first.
func TestConn_WaitReadyReturnsOnceTheFirstStatusIsAccepted(t *testing.T) {
	t.Parallel()
	p := newPeer(t, newFakeHandler(), rpc.Options{})
	p.send(hello)
	p.next()
	stopped, stop := context.WithCancel(t.Context())
	stop()
	if err := p.conn.WaitReady(stopped); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitReady before status error = %v, want the context's error", err)
	}
	p.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}`)
	if err := p.conn.WaitReady(t.Context()); err != nil {
		t.Fatalf("WaitReady after status error = %v", err)
	}
	called := make(chan error, 1)
	go func() { called <- p.conn.Call(t.Context(), protocol.MethodCancel, cancelParams, nil) }()
	if m := p.next(); m["method"] != "cancel" {
		t.Fatalf("request after WaitReady = %v, want cancel", m)
	}
	p.send(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	if err := <-called; err != nil {
		t.Fatalf("Call after WaitReady error = %v", err)
	}

	closed := newPeer(t, newFakeHandler(), rpc.Options{})
	closed.send(hello)
	closed.next()
	_ = closed.nc.Close()
	if err := closed.conn.WaitReady(t.Context()); !errors.Is(err, rpc.ErrClosed) {
		t.Fatalf("WaitReady on an ended connection error = %v, want ErrClosed", err)
	}
}

// F5 and F6: daemon request ids start at 1 and rise by 1; each response reaches its caller.
func TestConn_NumbersDaemonRequestsFromOne(t *testing.T) {
	t.Parallel()
	p := handshake(t, newFakeHandler())
	type result struct {
		res protocol.SleepResult
		err error
	}
	for want := 1; want <= 3; want++ {
		done := make(chan result, 1)
		go func() {
			var r protocol.SleepResult
			err := p.conn.Call(context.Background(), protocol.MethodSleep, protocol.SleepParams{Reason: "idle", MaxSummaryChars: 4096}, &r)
			done <- result{r, err}
		}()
		m := p.next()
		if m["id"] != float64(want) || m["method"] != "sleep" {
			t.Fatalf("request = %v, want sleep with id %d", m, want)
		}
		p.send(`{"jsonrpc":"2.0","id":` + string(rune('0'+want)) + `,"result":{"summary":"s"}}`)
		select {
		case r := <-done:
			if r.err != nil || r.res.Summary != "s" {
				t.Fatalf("Call = %+v, %v", r.res, r.err)
			}
		case <-time.After(waitFor):
			t.Fatal("Call did not return within 5s")
		}
	}
}

func TestConn_ReturnsRunnerErrorFromCall(t *testing.T) {
	t.Parallel()
	p := handshake(t, newFakeHandler())
	done := make(chan error, 1)
	go func() {
		done <- p.conn.Call(context.Background(), protocol.MethodCancel, cancelParams, nil)
	}()
	p.next()
	p.send(`{"jsonrpc":"2.0","id":1,"error":{"code":1011,"message":"Resume unavailable"}}`)
	select {
	case err := <-done:
		if !errors.Is(err, domain.ErrResumeUnavailable) {
			t.Fatalf("Call error = %v, want ErrResumeUnavailable", err)
		}
	case <-time.After(waitFor):
		t.Fatal("Call did not return within 5s")
	}
}

func TestConn_FailsPendingCallsWhenRunnerDisconnects(t *testing.T) {
	t.Parallel()
	h := newFakeHandler()
	p := handshake(t, h)
	done := make(chan error, 1)
	go func() { done <- p.conn.Call(context.Background(), protocol.MethodCancel, cancelParams, nil) }()
	p.next()
	_ = p.nc.Close()
	select {
	case err := <-done:
		if !errors.Is(err, rpc.ErrClosed) {
			t.Fatalf("Call error = %v, want ErrClosed", err)
		}
	case <-time.After(waitFor):
		t.Fatal("Call did not return within 5s")
	}
	if err := <-h.closed; err != nil {
		t.Fatalf("Closed(%v), want nil for a clean EOF", err)
	}
	if err := p.conn.Call(context.Background(), protocol.MethodCancel, nil, nil); !errors.Is(err, rpc.ErrClosed) {
		t.Fatalf("Call after close error = %v, want ErrClosed", err)
	}
}

func TestConn_CallStopsWhenContextEnds(t *testing.T) {
	t.Parallel()
	p := handshake(t, newFakeHandler())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- p.conn.Call(ctx, protocol.MethodCancel, cancelParams, nil) }()
	p.next()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Call error = %v, want context.Canceled", err)
	}
}

// F2: the daemon never sends a line over the limit.
// H8 and F6: a notification goes out only after the runner's first status,
// as one line without an id.
func TestConn_NotifiesOnlyAfterTheFirstStatus(t *testing.T) {
	t.Parallel()
	p := newPeer(t, newFakeHandler(), rpc.Options{})
	p.send(hello)
	p.next()
	at := common.Timestamp(time.Date(2026, 11, 19, 19, 30, 0, 0, time.UTC))
	params := protocol.CredentialsUpdateParams{
		GatewayToken: "test-gateway-token-founder-0002", GatewayTokenExpiresAt: at, // #nosec G101 -- test value
		McpToken: "test-mcp-token-founder-0002", McpTokenExpiresAt: at,
	}
	if err := p.conn.Notify(t.Context(), protocol.MethodCredentialsUpdate, params); !errors.Is(err, rpc.ErrNotReady) {
		t.Fatalf("Notify before status = %v, want ErrNotReady", err)
	}
	p.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}`)
	p.send(`{"jsonrpc":"2.0","id":2,"method":"dance","params":{}}`)
	p.next()

	if err := p.conn.Notify(t.Context(), protocol.MethodCredentialsUpdate, params); err != nil {
		t.Fatal(err)
	}
	m := p.next()
	if _, hasID := m["id"]; hasID || m["method"] != protocol.MethodCredentialsUpdate || m["jsonrpc"] != "2.0" {
		t.Fatalf("notification = %v, want credentials.update without an id", m)
	}
	if err := p.conn.Notify(t.Context(), protocol.MethodCredentialsUpdate, map[string]any{"x": strings.Repeat("x", rpc.DefaultMaxLineBytes)}); !errors.Is(err, rpc.ErrLineTooLarge) {
		t.Fatalf("overlong Notify = %v, want ErrLineTooLarge", err)
	}
	_ = p.conn.Close()
	<-p.conn.Done()
	if err := p.conn.Notify(t.Context(), protocol.MethodCredentialsUpdate, params); !errors.Is(err, rpc.ErrClosed) {
		t.Fatalf("Notify after close = %v, want ErrClosed", err)
	}
}

func TestConn_RefusesToSendOverlongLine(t *testing.T) {
	t.Parallel()
	p := newPeer(t, newFakeHandler(), rpc.Options{MaxLineBytes: 4096})
	p.send(hello)
	p.next()
	p.send(`{"jsonrpc":"2.0","method":"status","params":{"state":"idle"}}`)
	// Wait for the status to be handled: a request answered after it proves it.
	p.send(`{"jsonrpc":"2.0","id":2,"method":"dance","params":{}}`)
	p.next()
	err := p.conn.Call(t.Context(), protocol.MethodDeliver, map[string]any{"text": strings.Repeat("x", 5000)}, nil)
	if !errors.Is(err, rpc.ErrLineTooLarge) {
		t.Fatalf("Call error = %v, want ErrLineTooLarge", err)
	}
}

// A failed step of the daemon's work tells the runner which step failed,
// never the cause, which stays in the daemon log. A step that failed with a
// protocol error keeps that error's code.
func TestFromError_NamesTheFailedStepButNotItsCause(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		err     error
		code    int
		message string
	}{
		{"internal cause", fmt.Errorf("hello: %w", &domain.StepError{Step: "issue the MCP token",
			Err: errors.New(`token scope "mcp" refused by /var/lib/agentsone/core.db`)}),
			rpc.CodeInternal, "Internal error: could not issue the MCP token"},
		{"protocol cause", &domain.StepError{Step: "read the agent's identity", Err: domain.ErrNotPermitted},
			rpc.CodeNotPermitted, "Not permitted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := rpc.FromError(tt.err)
			if got.Code != tt.code || got.Message != tt.message || got.Data != nil {
				t.Fatalf("FromError = %+v, want code %d and message %q only", got, tt.code, tt.message)
			}
		})
	}
}

func TestRemoteError_FormatsCodeAndMessage(t *testing.T) {
	t.Parallel()
	e := &rpc.RemoteError{Code: 1001, Message: "Unknown turn"}
	if e.Error() != "runner error 1001: Unknown turn" || !errors.Is(e, domain.ErrUnknownTurn) || errors.Is(e, domain.ErrUnknownTask) {
		t.Fatalf("RemoteError %v", e)
	}
	if got := rpc.FromError(e); got != e {
		t.Fatalf("FromError(remote) = %v, want the same error", got)
	}
}
