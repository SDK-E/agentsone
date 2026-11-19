// SPDX-License-Identifier: MIT

// Package rpc is the daemon side of the runner protocol on one connection:
// line framing, JSON-RPC 2.0 requests and responses in both directions, and
// the handshake. It knows nothing about agents; it hands typed messages to a
// Handler and lets the supervisor call the runner through Conn.Call.
package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/SDK-E/agentsone/internal/agents/supervisor/domain"
	"github.com/SDK-E/agentsone/runners/sdk/protocol"
)

// DefaultMaxLineBytes is the line limit, newline included (PROTOCOL.md framing).
const DefaultMaxLineBytes = 1 << 20

// Errors of a connection.
var (
	// ErrClosed is returned by Call when the connection ended.
	ErrClosed = errors.New("runner connection closed")
	// ErrNotReady is returned by Call before the runner's first status (rule H8).
	ErrNotReady = errors.New("runner has not reported its first status")
	// ErrLineTooLarge is a message the daemon will not send because it is over the line limit (rule F2).
	ErrLineTooLarge = errors.New("message is over the line limit")
)

// Handler receives what the runner sends. Methods run on the connection's
// read loop, one at a time, so a slow handler slows that runner only.
type Handler interface {
	// Hello builds the welcome for the negotiated version.
	Hello(ctx context.Context, version string, p protocol.HelloParams) (protocol.HelloResult, error)
	// Heard is called for every line the runner sends, before it is handled.
	Heard()
	Status(ctx context.Context, p protocol.StatusParams) error
	Output(ctx context.Context, p protocol.OutputParams) error
	Usage(ctx context.Context, p protocol.UsageParams) error
	ProfileUpdate(ctx context.Context, p protocol.ProfileUpdateParams) error
	Log(ctx context.Context, p protocol.LogParams)
	Error(ctx context.Context, p protocol.ErrorParams)
	BusSend(ctx context.Context, p protocol.BusSendParams) (protocol.BusSendResult, error)
	TaskDone(ctx context.Context, p protocol.TaskDoneParams) error
	// Closed is called once when the connection ends.
	Closed(err error)
}

// Options tune a connection.
type Options struct {
	// Versions are the daemon's protocol versions, highest first.
	Versions []string
	// MaxLineBytes is the line limit; zero means DefaultMaxLineBytes.
	MaxLineBytes int
	// HelloDeadline fires when the runner has had its time to send hello
	// (10 seconds in production). Nil means no deadline.
	HelloDeadline <-chan struct{}
}

// Conn is one runner connection.
type Conn struct {
	nc   net.Conn
	h    Handler
	opts Options

	wmu sync.Mutex // serialises writes

	mu       sync.Mutex
	nextID   int
	pending  map[int]chan response
	welcomed bool
	ready    bool
	readyCh  chan struct{} // closed when ready becomes true
	closed   bool
	done     chan struct{}
	closeErr error
}

type response struct {
	result json.RawMessage
	err    *RemoteError
}

// Serve starts the read loop on nc and returns at once.
func Serve(ctx context.Context, nc net.Conn, h Handler, opts Options) *Conn {
	if opts.MaxLineBytes <= 0 {
		opts.MaxLineBytes = DefaultMaxLineBytes
	}
	c := &Conn{nc: nc, h: h, opts: opts, pending: map[int]chan response{}, readyCh: make(chan struct{}), done: make(chan struct{})}
	go c.readLoop(ctx)
	if opts.HelloDeadline != nil {
		go c.helloWatch(opts.HelloDeadline)
	}
	return c
}

// Done is closed when the connection has ended and Handler.Closed returned.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Close ends the connection.
func (c *Conn) Close() error { return c.nc.Close() }

// WaitReady returns nil once the runner's first status has been accepted,
// so Call no longer refuses with ErrNotReady. It returns ErrClosed when the
// connection ends first, or ctx's error.
func (c *Conn) WaitReady(ctx context.Context) error {
	select {
	case <-c.readyCh:
		return nil
	case <-c.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Conn) helloWatch(deadline <-chan struct{}) {
	select {
	case <-deadline:
		c.mu.Lock()
		w := c.welcomed
		c.mu.Unlock()
		if !w {
			_ = c.nc.Close() // rule H1: no hello in time, close the socket
		}
	case <-c.done:
	}
}

// Call sends a request to the runner and waits for its response. The result
// is decoded into result when it is not nil. A runner error comes back as a
// *RemoteError.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	switch {
	case c.closed:
		c.mu.Unlock()
		return ErrClosed
	case !c.ready:
		c.mu.Unlock()
		return ErrNotReady
	}
	c.nextID++ // rule F5: ids start at 1 and rise by 1
	id := c.nextID
	ch := make(chan response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.drop(id)
		return err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if r.err != nil {
			return r.err
		}
		if result != nil {
			if err := json.Unmarshal(r.result, result); err != nil {
				return fmt.Errorf("decode %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.drop(id)
		return ctx.Err()
	}
}

// Notify sends a notification to the runner. Like Call, it is refused
// before the runner's first status (rule H8), so it never precedes the
// welcome.
func (c *Conn) Notify(_ context.Context, method string, params any) error {
	c.mu.Lock()
	switch {
	case c.closed:
		c.mu.Unlock()
		return ErrClosed
	case !c.ready:
		c.mu.Unlock()
		return ErrNotReady
	}
	c.mu.Unlock()
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Conn) drop(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Conn) send(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	if len(b)+1 > c.opts.MaxLineBytes {
		return ErrLineTooLarge
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.nc.Write(b); err != nil {
		return fmt.Errorf("write to runner: %w", err)
	}
	return nil
}

func (c *Conn) reply(id any, result any) {
	_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *Conn) replyErr(id any, e *RemoteError) {
	_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
}

func (c *Conn) readLoop(ctx context.Context) {
	r := bufio.NewReaderSize(c.nc, 64<<10)
	var err error
	for {
		var line []byte
		var tooLong bool
		line, tooLong, err = readLine(r, c.opts.MaxLineBytes)
		if err != nil {
			break
		}
		c.h.Heard()
		if tooLong {
			c.replyErr(nil, &RemoteError{Code: CodeLineTooLarge, Message: "Line too large"}) // rule F3
			continue
		}
		if !c.handleLine(ctx, line) {
			break
		}
	}
	c.finish(err)
}

func (c *Conn) finish(err error) {
	_ = c.nc.Close()
	c.mu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		err = nil
	}
	c.closeErr = err
	c.mu.Unlock()
	c.h.Closed(err)
	close(c.done)
}

// readLine reads one line without its newline. A line over max bytes,
// newline included, is discarded up to the next newline and reported as
// tooLong.
func readLine(r *bufio.Reader, maxBytes int) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		frag, err := r.ReadSlice('\n')
		if len(buf)+len(frag) > maxBytes {
			tooLong = true
		}
		if !tooLong {
			buf = append(buf, frag...)
		}
		switch {
		case err == nil:
			if tooLong {
				return nil, true, nil
			}
			return buf[:len(buf)-1], false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return nil, false, err
		}
	}
}

type envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  *string         `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *RemoteError    `json:"error"`
}

// handleLine handles one message. It returns false when the connection must close.
func (c *Conn) handleLine(ctx context.Context, line []byte) bool {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if json.Valid(trimmed) {
			c.replyErr(nil, &RemoteError{Code: CodeInvalidRequest, Message: "Invalid request"}) // rule F4: batches
			return true
		}
	}
	if !json.Valid(trimmed) {
		c.replyErr(nil, &RemoteError{Code: CodeParseError, Message: "Parse error"}) // rule F4
		return true
	}
	var env envelope
	if err := json.Unmarshal(trimmed, &env); err != nil || env.JSONRPC != "2.0" {
		c.replyErr(nil, &RemoteError{Code: CodeInvalidRequest, Message: "Invalid request"})
		return true
	}
	hasID := len(env.ID) > 0 && string(env.ID) != "null"
	switch {
	case env.Method != nil && hasID:
		var id int
		if err := json.Unmarshal(env.ID, &id); err != nil {
			c.replyErr(nil, &RemoteError{Code: CodeInvalidRequest, Message: "Invalid request"})
			return true
		}
		return c.request(ctx, id, *env.Method, env.Params)
	case env.Method != nil:
		c.notification(ctx, *env.Method, env.Params)
		return true
	case hasID && (env.Result != nil || env.Error != nil):
		c.response(env)
		return true
	default:
		c.replyErr(nil, &RemoteError{Code: CodeInvalidRequest, Message: "Invalid request"})
		return true
	}
}

func (c *Conn) response(env envelope) {
	var id int
	if err := json.Unmarshal(env.ID, &id); err != nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ok {
		ch <- response{result: env.Result, err: env.Error}
	}
}

func (c *Conn) isWelcomed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.welcomed
}

func (c *Conn) request(ctx context.Context, id int, method string, params json.RawMessage) bool {
	if method == protocol.MethodHello {
		if c.isWelcomed() {
			c.replyErr(id, &RemoteError{Code: CodeOutOfOrder, Message: "Out of order"}) // rule H6: second hello
			return true
		}
		return c.hello(ctx, id, params)
	}
	if !c.isWelcomed() {
		c.replyErr(id, &RemoteError{Code: CodeOutOfOrder, Message: "Out of order"}) // rule H6
		return true
	}
	switch method {
	case protocol.MethodBusSend:
		var p protocol.BusSendParams
		if e := decode(params, &p, "turnId", "to", "subject", "body"); e != nil {
			c.replyErr(id, e)
			return true
		}
		res, err := c.h.BusSend(ctx, p)
		c.answer(id, res, err)
	case protocol.MethodTaskDone:
		var p protocol.TaskDoneParams
		if e := decode(params, &p, "turnId", "taskId", "outcome", "summary"); e != nil {
			c.replyErr(id, e)
			return true
		}
		c.answer(id, protocol.Ack{}, c.h.TaskDone(ctx, p))
	default:
		// Rule F7: unknown method, or a daemon-to-runner method sent the wrong way.
		c.replyErr(id, &RemoteError{Code: CodeMethodNotFound, Message: "Method not found"})
	}
	return true
}

func (c *Conn) answer(id int, result any, err error) {
	if err != nil {
		c.replyErr(id, FromError(err))
		return
	}
	c.reply(id, result)
}

func (c *Conn) hello(ctx context.Context, id int, params json.RawMessage) bool {
	var p protocol.HelloParams
	if e := decode(params, &p, "protocolVersions", "runner", "capabilities"); e != nil {
		c.replyErr(id, e)
		return true
	}
	runnerVersions := make([]string, len(p.ProtocolVersions))
	for i, v := range p.ProtocolVersions {
		runnerVersions[i] = string(v)
	}
	version, err := domain.Negotiate(runnerVersions, c.opts.Versions)
	if err != nil {
		// Rule H5: -32002 with the daemon's versions, then close.
		c.replyErr(id, &RemoteError{Code: CodeUnsupportedVersion, Message: "Unsupported protocol version",
			Data: map[string]any{"supported": c.opts.Versions}})
		return false
	}
	welcome, err := c.h.Hello(ctx, version, p)
	if err != nil {
		c.replyErr(id, FromError(err))
		return false
	}
	c.mu.Lock()
	c.welcomed = true
	c.mu.Unlock()
	c.reply(id, welcome)
	return true
}

func (c *Conn) notification(ctx context.Context, method string, params json.RawMessage) {
	if !c.isWelcomed() {
		c.replyErr(nil, &RemoteError{Code: CodeOutOfOrder, Message: "Out of order"}) // rule H6
		return
	}
	// Notifications never get a response (rule F6); invalid ones are dropped.
	switch method {
	case protocol.MethodStatus:
		var p protocol.StatusParams
		if decode(params, &p, "state") == nil && c.h.Status(ctx, p) == nil {
			c.mu.Lock()
			if !c.ready {
				c.ready = true
				close(c.readyCh)
			}
			c.mu.Unlock()
		}
	case protocol.MethodOutput:
		var p protocol.OutputParams
		if decode(params, &p, "turnId", "chunk", "text", "final") == nil {
			_ = c.h.Output(ctx, p)
		}
	case protocol.MethodUsage:
		var p protocol.UsageParams
		if decode(params, &p, "turnId", "model", "weight", "tokensIn", "tokensOut", "costMicroUsd") == nil {
			_ = c.h.Usage(ctx, p)
		}
	case protocol.MethodProfileUpdate:
		var p protocol.ProfileUpdateParams
		if decode(params, &p, "turnId", "line") == nil {
			_ = c.h.ProfileUpdate(ctx, p)
		}
	case protocol.MethodLog:
		var p protocol.LogParams
		if decode(params, &p, "level", "message") == nil {
			c.h.Log(ctx, p)
		}
	case protocol.MethodError:
		var p protocol.ErrorParams
		if decode(params, &p, "code", "message", "fatal") == nil {
			c.h.Error(ctx, p)
		}
	case protocol.MethodHeartbeat:
		// Heard already moved the deadline; the uptime is not needed.
	}
}

// decode reads params strictly: an object, no unknown fields, every
// required field present (rule F8 answers -32602 otherwise).
func decode(params json.RawMessage, into any, required ...string) *RemoteError {
	bad := &RemoteError{Code: CodeInvalidParams, Message: "Invalid params"}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil || fields == nil {
		return bad
	}
	for _, f := range required {
		if v, ok := fields[f]; !ok || string(v) == "null" {
			bad.Data = map[string]any{"missing": f}
			return bad
		}
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return bad
	}
	return nil
}
