// SPDX-License-Identifier: MIT

package providers

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"

	"github.com/SDK-E/agentsone/internal/host/gateway/routing"
)

// Limits on what the tap keeps in memory. Past them usage is unknown and the
// gateway charges the call's full estimate.
const (
	maxJSONBody = 32 << 20
	maxSSELine  = 4 << 20
)

// usageFields covers both APIs' usage objects.
type usageFields struct {
	// OpenAI chat completions.
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
	// Anthropic Messages.
	InputTokens   *int64 `json:"input_tokens"`
	OutputTokens  *int64 `json:"output_tokens"`
	CacheCreation *int64 `json:"cache_creation_input_tokens"`
	CacheRead     *int64 `json:"cache_read_input_tokens"`
}

// envelope is a whole answer, an OpenAI chunk, or an Anthropic stream event.
// Usage sits at the top level everywhere except Anthropic's message_start,
// where it is inside message.
type envelope struct {
	Usage   *usageFields `json:"usage"`
	Message *struct {
		Usage *usageFields `json:"usage"`
	} `json:"message"`
}

// tap passes a body through and reads token usage out of it.
type tap struct {
	rc  io.ReadCloser
	sse bool
	// on is false for error answers, which carry no usage.
	on bool

	mu       sync.Mutex
	body     bytes.Buffer
	overflow bool
	line     []byte
	usage    routing.Usage
	seen     bool
	parsed   bool
}

func newTap(rc io.ReadCloser, sse, on bool) *tap { return &tap{rc: rc, sse: sse, on: on} }

func (t *tap) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 && t.on {
		t.mu.Lock()
		t.feed(p[:n])
		t.mu.Unlock()
	}
	return n, err
}

func (t *tap) Close() error { return t.rc.Close() }

func (t *tap) feed(b []byte) {
	if !t.sse {
		if t.overflow || t.body.Len()+len(b) > maxJSONBody {
			t.overflow = true
			t.body.Reset()
			return
		}
		t.body.Write(b)
		return
	}
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			if len(t.line)+len(b) > maxSSELine {
				t.line = t.line[:0]
				t.overflow = true
				return
			}
			t.line = append(t.line, b...)
			return
		}
		t.line = append(t.line, b[:i]...)
		t.sseLine(bytes.TrimRight(t.line, "\r"))
		t.line = t.line[:0]
		b = b[i+1:]
	}
}

func (t *tap) sseLine(line []byte) {
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return // "[DONE]" and other markers
	}
	t.apply(data)
}

func (t *tap) apply(doc []byte) {
	var e envelope
	if json.Unmarshal(doc, &e) != nil {
		return
	}
	if e.Message != nil {
		t.merge(e.Message.Usage)
	}
	t.merge(e.Usage)
}

// merge takes the fields present; later events replace earlier counts, as
// Anthropic's message_delta carries the running output total.
func (t *tap) merge(u *usageFields) {
	if u == nil {
		return
	}
	set := func(dst *int64, src *int64) {
		if src != nil {
			*dst = *src
			t.seen = true
		}
	}
	set(&t.usage.Input, u.PromptTokens)
	set(&t.usage.Output, u.CompletionTokens)
	set(&t.usage.Input, u.InputTokens)
	set(&t.usage.Output, u.OutputTokens)
	set(&t.usage.CacheWrite, u.CacheCreation)
	set(&t.usage.CacheRead, u.CacheRead)
}

// Usage reports the usage read so far. Call it after the body was read.
func (t *tap) Usage() (routing.Usage, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.on {
		return routing.Usage{}, false
	}
	if !t.sse && !t.parsed {
		t.parsed = true
		if !t.overflow {
			t.apply(t.body.Bytes())
		}
		t.body.Reset()
	}
	return t.usage, t.seen
}
