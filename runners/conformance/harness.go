// SPDX-License-Identifier: MIT

package conformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/SDK-E/agentsone/internal/contract/contracttest"
)

const contractDirEnv = "AGENTSONE_CONTRACT_DIR"

var (
	schemaOnce sync.Once
	schemaSet  *contracttest.SchemaSet
	schemaErr  error
)

// schemas loads the contract schemas once. The directory is
// $AGENTSONE_CONTRACT_DIR/schemas, or contract/schemas of this repository.
func schemas() (*contracttest.SchemaSet, error) {
	schemaOnce.Do(func() {
		dir := os.Getenv(contractDirEnv)
		if dir == "" {
			_, file, _, ok := runtime.Caller(0)
			if !ok {
				schemaErr = errors.New("cannot locate the conformance package source")
				return
			}
			dir = filepath.Join(filepath.Dir(file), "..", "..", "contract")
		}
		schemaSet, schemaErr = contracttest.NewSchemaSet(filepath.Join(dir, "schemas"))
	})
	return schemaSet, schemaErr
}

type rawLine struct {
	b   []byte
	err error
}

// H is the fake daemon for one rule: a Unix socket, the runner process, and
// the checks that hold in every rule (F1, F2, F5, F6).
type H struct {
	T       T
	Sub     Subject
	Welcome WelcomeSpec

	socket string
	ln     net.Listener
	conn   net.Conn
	lines  chan rawLine
	proc   Process
	set    *contracttest.SchemaSet

	nextReq     int            // last request id the daemon used
	open        map[int]string // daemon requests waiting for the runner's response
	runnerReqID int            // last request id the runner used
	maxLine     int            // maxLineBytes of the welcome
	turnSeq     int
	msgSeq      int
	exitDone    chan struct{} // closed when the runner process has exited
	exitCode    int
	// Heartbeats counts heartbeat notifications skipped by Next.
	Heartbeats int
	// Log is every line received from the runner, in order.
	Log []Msg
}

// Start creates the daemon side and starts the runner. It does not handshake.
func Start(t T, sub Subject, mutate func(*WelcomeSpec)) *H {
	t.Helper()
	set, err := schemas()
	if err != nil {
		t.Fatalf("load the contract schemas: %v", err)
	}
	dir, err := os.MkdirTemp("", "ao") // short path: unix socket paths are limited to about 100 bytes
	if err != nil {
		t.Fatalf("make socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	h := &H{T: t, Sub: sub, Welcome: DefaultWelcome(), socket: filepath.Join(dir, "r.sock"), set: set,
		lines: make(chan rawLine, 1024), open: map[int]string{}}
	if sub.Welcome != nil {
		sub.Welcome(&h.Welcome)
	}
	if mutate != nil {
		mutate(&h.Welcome)
	}
	h.ln, err = net.Listen("unix", h.socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", h.socket, err)
	}
	t.Cleanup(func() { _ = h.ln.Close() })
	h.exitDone = make(chan struct{})
	h.proc = sub.Start(t, h.socket)
	t.Cleanup(h.proc.Kill)
	go func() {
		h.exitCode = <-h.proc.Exited()
		close(h.exitDone)
	}()
	return h
}

// Accept waits for the runner to connect.
func (h *H) Accept() {
	h.T.Helper()
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := h.ln.Accept()
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			h.T.Fatalf("accept runner: %v", r.err)
		}
		h.conn = r.c
		h.T.Cleanup(func() { _ = r.c.Close() })
	case <-h.exitDone:
		h.T.Fatalf("runner exited with status %d before it connected", h.exitCode)
	case <-time.After(guard):
		h.T.Fatalf("runner did not connect within %s", guard)
	}
	h.maxLine = h.Welcome.MaxLineBytes
	go func() {
		br := bufio.NewReaderSize(h.conn, 64*1024)
		for {
			b, err := br.ReadBytes('\n')
			h.lines <- rawLine{b: b, err: err}
			if err != nil {
				return
			}
		}
	}()
}

// Raw returns the next line from the runner without any check, or the read
// error (for example io.EOF when the runner closed the socket).
func (h *H) Raw() ([]byte, error) {
	h.T.Helper()
	select {
	case l := <-h.lines:
		return l.b, l.err
	case <-h.exitDone:
		// The socket closes when the runner exits, so the reader delivers the
		// lines the runner wrote before exiting, then the read error.
		select {
		case l := <-h.lines:
			return l.b, l.err
		case <-time.After(guard):
			h.T.Fatalf("runner exited with status %d while the suite waited for a message", h.exitCode)
		}
	case <-time.After(guard):
		h.T.Fatalf("no message from the runner within %s", guard)
	}
	return nil, io.EOF
}

// RecvRaw returns the next line from the runner, parsed and checked against
// the rules that hold for every line (F1, F2, F5, F6, schema).
func (h *H) RecvRaw() Msg {
	h.T.Helper()
	b, err := h.Raw()
	if err != nil {
		h.T.Fatalf("read from the runner: %v", err)
	}
	return h.check(b)
}

// Next returns the next runner line, skipping heartbeats.
func (h *H) Next() Msg {
	h.T.Helper()
	for {
		m := h.RecvRaw()
		if m.Is("heartbeat") {
			h.Heartbeats++
			continue
		}
		return m
	}
}

func (h *H) check(b []byte) Msg {
	h.T.Helper()
	if !bytes.HasSuffix(b, []byte("\n")) || bytes.Count(b, []byte("\n")) != 1 {
		h.T.Fatalf("F1: a line must end with one newline and hold no other: %q", b)
	}
	if len(b) > h.maxLine {
		h.T.Fatalf("F2: line of %d bytes is longer than maxLineBytes %d", len(b), h.maxLine)
	}
	if err := h.set.Validate("messages", b); err != nil {
		h.T.Fatalf("F1: line breaks messages.schema.json: %v\n%s", err, b)
	}
	m, err := parseMsg(b)
	if err != nil {
		h.T.Fatalf("F1: %v: %s", err, b)
	}
	if m.IsRequest() {
		if want := h.runnerReqID + 1; *m.ID != want {
			h.T.Fatalf("F5: runner request id is %d, want %d: %s", *m.ID, want, m)
		}
		h.runnerReqID = *m.ID
	}
	if m.IsResponse() && m.ID != nil {
		if _, ok := h.open[*m.ID]; !ok {
			h.T.Fatalf("F6: response with id %d to no open request (answered twice, or never asked): %s", *m.ID, m)
		}
		delete(h.open, *m.ID)
	}
	h.Log = append(h.Log, m)
	return m
}

// SendRaw writes bytes to the runner as they are.
func (h *H) SendRaw(b []byte) {
	h.T.Helper()
	if _, err := h.conn.Write(b); err != nil {
		h.T.Fatalf("write to the runner: %v", err)
	}
}

func encode(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err) // a programming error in the suite
	}
	return buf.Bytes()
}

// Request sends a request with the next daemon id and returns the id.
func (h *H) Request(method string, params any) int {
	h.T.Helper()
	h.nextReq++
	id := h.nextReq
	h.open[id] = method
	h.SendRaw(encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}))
	return id
}

// Notify sends a notification.
func (h *H) Notify(method string, params any) {
	h.T.Helper()
	h.SendRaw(encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": params}))
}

// Respond answers a runner request with a result.
func (h *H) Respond(id int, result any) {
	h.T.Helper()
	h.SendRaw(encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}))
}

// RespondError answers a runner request with an error.
func (h *H) RespondError(id, code int, message string) {
	h.T.Helper()
	h.SendRaw(encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}))
}

// Handshake receives hello, answers with the welcome and checks that the
// runner follows with status idle (H1, H2, H7). It returns the hello.
func (h *H) Handshake() Msg {
	h.T.Helper()
	h.Accept()
	hello := h.RecvRaw()
	if !hello.IsRequest() || hello.Method != "hello" || *hello.ID != 1 {
		h.T.Fatalf("H1: the first line must be hello with id 1, got %s", hello)
	}
	h.Respond(1, h.Welcome.result())
	st := h.Next()
	if st.Method != "status" || st.Field("state") != "idle" || st.Field("turnId") != "" {
		h.T.Fatalf("H7: right after the welcome the runner must send status idle, got %s", st)
	}
	return hello
}

// Ready starts the runner and completes the handshake.
func Ready(t T, sub Subject, mutate func(*WelcomeSpec)) *H {
	t.Helper()
	h := Start(t, sub, mutate)
	h.Handshake()
	return h
}

const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func ulid(n int) string {
	return "01JD8X3Q5Y6Z7A8B9C0D1E2F"[:24] + string(ulidAlphabet[(n/32)%32]) + string(ulidAlphabet[n%32])
}

// NewTurnID returns a fresh valid turn id.
func (h *H) NewTurnID() string {
	h.turnSeq++
	return "trn_" + ulid(h.turnSeq)
}

func (h *H) newMsgID() string {
	h.msgSeq++
	return "msg_" + ulid(h.msgSeq)
}

func chatPayload(msgID, text string) map[string]any {
	return map[string]any{"id": msgID, "chatId": "cht_" + ulid(1), "from": "human:alice", "text": text, "sentAt": "2026-11-19T19:00:01Z"}
}

// WakeChat sends a chat wake and returns the turn id and the request id.
func (h *H) WakeChat(text string) (turnID string, reqID int) {
	h.T.Helper()
	turnID = h.NewTurnID()
	reqID = h.Request("wake", map[string]any{"reason": "chat", "turnId": turnID, "payload": chatPayload(h.newMsgID(), text)})
	return turnID, reqID
}

// WakeTask sends a task wake.
func (h *H) WakeTask(title, body string) (turnID string, reqID int) {
	h.T.Helper()
	turnID = h.NewTurnID()
	reqID = h.Request("wake", map[string]any{"reason": "task", "turnId": turnID, "payload": map[string]any{
		"id": "tsk_" + ulid(h.turnSeq), "title": title, "body": body, "from": "human:alice", "createdAt": "2026-11-19T19:01:00Z"}})
	return turnID, reqID
}

// WakeBudgetRestored sends the resume wake for a paused turn.
func (h *H) WakeBudgetRestored(turnID string) int {
	h.T.Helper()
	return h.Request("wake", map[string]any{"reason": "event", "turnId": turnID, "payload": map[string]any{
		"specversion": "1.0", "id": "evt_" + ulid(7), "source": "/emitters/agents", "type": "agentsone.agents.budget.restored",
		"subject": h.Welcome.AgentName, "time": "2026-11-19T19:05:00Z", "datacontenttype": "application/json",
		"data": map[string]any{"agent": h.Welcome.AgentName, "pausedTurnId": turnID}, "sequence": "00000000000000000042",
		"actor": "host:agentsone", "chartscope": "agent:" + h.Welcome.AgentName}})
}

// Deliver sends a chat message into a running turn.
func (h *H) Deliver(turnID, text string) int {
	h.T.Helper()
	return h.Request("deliver", map[string]any{"kind": "chat", "turnId": turnID, "message": chatPayload(h.newMsgID(), text)})
}

// Cancel sends cancel for a turn.
func (h *H) Cancel(turnID string) int {
	h.T.Helper()
	return h.Request("cancel", map[string]any{"turnId": turnID, "reason": "human"})
}

// ExpectResult reads the next runner line and requires it to be the success
// response with id.
func (h *H) ExpectResult(id int, what string) Msg {
	h.T.Helper()
	m := h.Next()
	if !m.IsResponse() || m.ID == nil || *m.ID != id || m.Error != nil {
		h.T.Fatalf("%s: want an empty result with id %d, got %s", what, id, m)
	}
	return m
}

// ExpectError reads the next runner line and requires an error with code and
// the given id (nil for id null).
func (h *H) ExpectError(id *int, code int, what string) Msg {
	h.T.Helper()
	m := h.Next()
	okID := (id == nil && m.HasNullID) || (id != nil && m.ID != nil && *m.ID == *id)
	if !m.IsResponse() || m.Error == nil || m.Error.Code != code || !okID {
		h.T.Fatalf("%s: want error %d, got %s", what, code, m)
	}
	return m
}

// Until collects runner lines until stop returns true for one (that line is
// the last one returned). Runner requests are answered: bus.send with a
// message id, anything else with an empty result.
func (h *H) Until(stop func(Msg) bool) []Msg {
	h.T.Helper()
	var out []Msg
	for {
		m := h.Next()
		out = append(out, m)
		if m.IsRequest() {
			if m.Method == "bus.send" {
				h.Respond(*m.ID, map[string]any{"messageId": h.newMsgID()})
			} else {
				h.Respond(*m.ID, map[string]any{})
			}
		}
		if stop(m) {
			return out
		}
	}
}

// AwaitResponse reads runner lines (answering runner requests) until the
// response with this id, and returns it.
func (h *H) AwaitResponse(id int) Msg {
	h.T.Helper()
	msgs := h.Until(func(m Msg) bool { return m.IsResponse() && m.ID != nil && *m.ID == id })
	return msgs[len(msgs)-1]
}

// TurnEnded is a stop function: the status line that ends turnID, either
// idle with that turn id or idle with it as pausedTurnId, or error.
func TurnEnded(turnID string) func(Msg) bool {
	return func(m Msg) bool {
		if m.Method != "status" {
			return false
		}
		switch m.Field("state") {
		case "idle":
			return m.Field("turnId") == turnID || m.Field("pausedTurnId") == turnID
		case "error":
			return true
		}
		return false
	}
}

// RunTurn wakes with a chat text and collects the turn to its end.
func (h *H) RunTurn(text string) (string, []Msg) {
	h.T.Helper()
	turnID, id := h.WakeChat(text)
	_ = id
	return turnID, h.Until(TurnEnded(turnID))
}

// WaitExit waits for the runner process to exit and returns its status.
func (h *H) WaitExit(within time.Duration) int {
	h.T.Helper()
	select {
	case <-h.exitDone:
		return h.exitCode
	case <-time.After(within):
		h.T.Fatalf("the runner did not exit within %s", within)
		return -1
	}
}

// CloseSocket closes the daemon end of the connection.
func (h *H) CloseSocket() {
	h.T.Helper()
	_ = h.conn.Close()
}

// ReadUntilEOF reads and checks runner lines until the socket closes.
func (h *H) ReadUntilEOF() {
	h.T.Helper()
	for {
		select {
		case l := <-h.lines:
			if l.err != nil {
				return
			}
			if len(bytes.TrimSpace(l.b)) > 0 && !strings.HasPrefix(string(l.b), "{") {
				h.T.Fatalf("F1: line is not a JSON object: %q", l.b)
			}
		case <-h.exitDone:
			return
		case <-time.After(guard):
			h.T.Fatalf("the runner did not close the socket within %s", guard)
		}
	}
}
