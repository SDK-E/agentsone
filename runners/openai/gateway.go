// SPDX-License-Identifier: MIT

package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/SDK-E/agentsone/runners/sdk"
)

// WeightHeader tells the gateway which model weight a call needs (quick,
// normal or deep).
const WeightHeader = "X-Agentsone-Weight"

// maxStreamLine bounds one line of the event stream.
const maxStreamLine = 4 << 20

// Message is one message of the chat-completions API.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a call of a function the model asks for.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall names the function and holds its arguments as JSON text.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef describes a function the model may call.
type ToolDef struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef is the function part of a ToolDef.
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ChatRequest is one call to the gateway.
type ChatRequest struct {
	Weight   string
	Messages []Message
	Tools    []ToolDef
}

// TokenUsage is the usage block the gateway ends a stream with.
type TokenUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// Completion is the model's answer, assembled from the stream.
type Completion struct {
	Content   string
	ToolCalls []ToolCall
	Usage     TokenUsage
	Model     string
}

// Gateway is a client of an OpenAI-compatible chat-completions endpoint.
type Gateway struct {
	BaseURL string
	Token   sdk.Secret
	Model   string
	HTTP    *http.Client
}

type streamChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *TokenUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends the request and reads the event stream. onDelta is called for
// each piece of answer text as it arrives. A 402 answer is
// sdk.ErrBudgetExhausted; 429 and every other fault is an *sdk.CodedError
// with the protocol's code for it.
func (g *Gateway) Complete(ctx context.Context, req ChatRequest, onDelta func(string) error) (Completion, error) {
	body := map[string]any{
		"model":          g.Model,
		"messages":       req.Messages,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Completion{}, fmt.Errorf("encode model request: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(g.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return Completion{}, fmt.Errorf("build model request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	hreq.Header.Set("Authorization", "Bearer "+g.Token.Reveal())
	if req.Weight != "" {
		hreq.Header.Set(WeightHeader, req.Weight)
	}
	resp, err := g.HTTP.Do(hreq)
	if err != nil {
		if ctx.Err() != nil {
			return Completion{}, ctx.Err()
		}
		return Completion{}, modelError("model gateway unreachable: " + err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusError(resp); err != nil {
		return Completion{}, err
	}
	return readStream(ctx, resp.Body, g.Model, onDelta)
}

func modelError(msg string) error {
	return &sdk.CodedError{Code: sdk.CodeModelUnavailable, Message: msg}
}

func statusError(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusPaymentRequired:
		return sdk.ErrBudgetExhausted
	case resp.StatusCode == http.StatusTooManyRequests:
		return &sdk.CodedError{Code: sdk.CodeRateLimited, Message: "model gateway rate limit"}
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return modelError(fmt.Sprintf("model gateway answered %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet))))
}

func readStream(ctx context.Context, r io.Reader, model string, onDelta func(string) error) (Completion, error) {
	comp := Completion{Model: model}
	var content strings.Builder
	calls := map[int]*ToolCall{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxStreamLine)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return comp, modelError("model gateway sent an unreadable stream: " + err.Error())
		}
		if chunk.Error != nil {
			return comp, modelError("model gateway error: " + chunk.Error.Message)
		}
		if chunk.Model != "" {
			comp.Model = chunk.Model
		}
		if chunk.Usage != nil {
			comp.Usage = *chunk.Usage
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				content.WriteString(c.Delta.Content)
				if err := onDelta(c.Delta.Content); err != nil {
					return comp, err
				}
			}
			for _, tc := range c.Delta.ToolCalls {
				call := calls[tc.Index]
				if call == nil {
					call = &ToolCall{Type: "function"}
					calls[tc.Index] = call
				}
				if tc.ID != "" {
					call.ID = tc.ID
				}
				call.Function.Name += tc.Function.Name
				call.Function.Arguments += tc.Function.Arguments
			}
		}
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return comp, ctx.Err()
		}
		if errors.Is(err, bufio.ErrTooLong) {
			return comp, modelError("model gateway sent a stream line that is too long")
		}
		return comp, modelError("model gateway stream broke: " + err.Error())
	}
	comp.Content = content.String()
	for i := 0; i < len(calls); i++ {
		if call, ok := calls[i]; ok {
			comp.ToolCalls = append(comp.ToolCalls, *call)
		}
	}
	return comp, nil
}
