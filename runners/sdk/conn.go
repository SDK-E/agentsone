// SPDX-License-Identifier: MIT

package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// DefaultMaxLineBytes is the line limit before the welcome says otherwise.
const DefaultMaxLineBytes = 1 << 20

var errLineTooLong = errors.New("sdk: line too long")

// lineReader reads newline-terminated lines of at most max bytes, newline
// included. A longer line is discarded up to its newline and reported as
// errLineTooLong, so the connection stays usable (rule F3).
type lineReader struct {
	r   *bufio.Reader
	max int
}

func newLineReader(r io.Reader, max int) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, 64*1024), max: max}
}

func (l *lineReader) setMax(max int) { l.max = max }

// next returns the next line without its newline. A partial line at end of
// input is dropped and the read error is returned.
func (l *lineReader) next() ([]byte, error) {
	var buf []byte
	tooLong := false
	for {
		chunk, err := l.r.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > l.max {
				tooLong = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case err == nil:
			if tooLong {
				return nil, errLineTooLong
			}
			return bytes.TrimSuffix(buf, []byte("\n")), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return nil, err
		}
	}
}

// Wire shapes. Field order is the order of the transcript in PROTOCOL.md, so
// the bytes on the wire match it exactly.
type (
	outRequest struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}
	outNotification struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}
	outResult struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  any    `json:"result"`
	}
	outError struct {
		JSONRPC string   `json:"jsonrpc"`
		ID      *int     `json:"id"`
		Error   rpcError `json:"error"`
	}
	rpcError struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data,omitempty"`
	}
)

type empty struct{}

// inbound is one classified incoming line.
type inbound struct {
	kind   inboundKind
	id     int
	method string
	params json.RawMessage
	result json.RawMessage
	err    *RPCError
}

type inboundKind int

const (
	kindRequest inboundKind = iota + 1
	kindNotification
	kindResponse
)

// problem is a protocol fault in an incoming line, answered with an error.
type problem struct {
	id      *int
	code    int
	message string
}

// classify turns one line into an inbound message or a problem to answer.
func classify(line []byte) (inbound, *problem) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && trimmed[0] == '[' && json.Valid(trimmed) {
		return inbound{}, &problem{code: CodeInvalidRequest, message: "batches are not used"}
	}
	if !json.Valid(trimmed) {
		return inbound{}, &problem{code: CodeParseError, message: "parse error"}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return inbound{}, &problem{code: CodeInvalidRequest, message: "message is not an object"}
	}
	id, hasID, idOK := parseID(obj["id"])
	var idPtr *int
	if hasID && idOK {
		idPtr = &id
	}
	if string(obj["jsonrpc"]) != `"2.0"` {
		return inbound{}, &problem{id: idPtr, code: CodeInvalidRequest, message: `jsonrpc must be "2.0"`}
	}
	if hasID && !idOK {
		return inbound{}, &problem{code: CodeInvalidRequest, message: "id must be an integer"}
	}
	if rawMethod, ok := obj["method"]; ok {
		var method string
		if err := json.Unmarshal(rawMethod, &method); err != nil || method == "" {
			return inbound{}, &problem{id: idPtr, code: CodeInvalidRequest, message: "method must be a string"}
		}
		in := inbound{method: method, params: obj["params"], id: id, kind: kindNotification}
		if hasID {
			in.kind = kindRequest
		}
		return in, nil
	}
	if !hasID {
		return inbound{}, &problem{code: CodeInvalidRequest, message: "not a request, notification or response"}
	}
	result, hasResult := obj["result"]
	rawErr, hasErr := obj["error"]
	if hasResult == hasErr {
		return inbound{}, &problem{id: idPtr, code: CodeInvalidRequest, message: "a response has exactly one of result and error"}
	}
	in := inbound{kind: kindResponse, id: id, result: result}
	if hasErr {
		var e struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rawErr, &e); err != nil {
			return inbound{}, &problem{id: idPtr, code: CodeInvalidRequest, message: "malformed error object"}
		}
		in.err = &RPCError{Code: e.Code, Message: e.Message, Data: e.Data}
	}
	return in, nil
}

func parseID(raw json.RawMessage) (id int, present, ok bool) {
	if raw == nil {
		return 0, false, false
	}
	if string(raw) == "null" {
		return 0, true, false
	}
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, true, false
	}
	return id, true, true
}

type response struct {
	result json.RawMessage
	err    *RPCError
}

// conn is one framed JSON-RPC connection. It numbers its own requests from 1
// (rule F5), matches responses to them (F6) and serialises writes.
type conn struct {
	rwc     io.ReadWriteCloser
	lines   *lineReader
	wmu     sync.Mutex
	nextID  int
	maxLine int
	pmu     sync.Mutex
	pending map[int]chan response
	done    chan struct{}
	once    sync.Once
}

func newConn(rwc io.ReadWriteCloser, maxLine int) *conn {
	return &conn{
		rwc:     rwc,
		lines:   newLineReader(rwc, maxLine),
		maxLine: maxLine,
		pending: map[int]chan response{},
		done:    make(chan struct{}),
	}
}

func (c *conn) setMaxLine(max int) {
	c.wmu.Lock()
	c.maxLine = max
	c.wmu.Unlock()
	c.lines.setMax(max)
}

// close closes the socket and fails every waiting call.
func (c *conn) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.rwc.Close()
	})
}

// writeLocked encodes v as one line and writes it. The caller holds wmu.
func (c *conn) writeLocked(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	if buf.Len() > c.maxLine {
		return ErrLineTooLarge
	}
	select {
	case <-c.done:
		return ErrDaemonClosed
	default:
	}
	if _, err := c.rwc.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	return nil
}

func (c *conn) write(v any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.writeLocked(v)
}

func (c *conn) notify(method string, params any) error {
	return c.write(outNotification{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *conn) respond(id int, result any) error {
	return c.write(outResult{JSONRPC: "2.0", ID: id, Result: result})
}

func (c *conn) respondError(id *int, code int, message string, data any) error {
	return c.write(outError{JSONRPC: "2.0", ID: id, Error: rpcError{Code: code, Message: message, Data: data}})
}

// call sends a request and waits for its response. An error response comes
// back as *RPCError.
func (c *conn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch := make(chan response, 1)
	c.wmu.Lock()
	c.nextID++
	id := c.nextID
	c.pmu.Lock()
	c.pending[id] = ch
	c.pmu.Unlock()
	err := c.writeLocked(outRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	c.wmu.Unlock()
	if err != nil {
		c.forget(id)
		return nil, err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return r.result, nil
	case <-ctx.Done():
		c.forget(id)
		return nil, fmt.Errorf("wait for %s response: %w", method, ctx.Err())
	case <-c.done:
		c.forget(id)
		return nil, ErrDaemonClosed
	}
}

func (c *conn) forget(id int) {
	c.pmu.Lock()
	delete(c.pending, id)
	c.pmu.Unlock()
}

// deliverResponse hands a response to the call that waits for it. It reports
// false when no call waits for that id.
func (c *conn) deliverResponse(id int, r response) bool {
	c.pmu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.pmu.Unlock()
	if ok {
		ch <- r
	}
	return ok
}
