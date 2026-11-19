// SPDX-License-Identifier: MIT

// Package gateway is the router's client for the edge model gateway, which
// speaks OpenAI chat completions. It makes the one quick model call that
// rates a shortlist.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/router/app"
)

const (
	model        = "autoagents/one"
	weightHeader = "X-Agentsone-Weight"
	quick        = "quick"
	maxTokens    = 600
	maxBody      = 1 << 20
)

// Client calls the gateway.
type Client struct {
	base string
	http *http.Client
}

var _ app.Gateway = (*Client)(nil)

// New returns a client for the gateway at baseURL, for example
// http://edge:4000. timeout bounds each attempt.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: timeout}}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
}

type response struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

// retryable marks a failure worth one more attempt: the network or the gateway
// had a bad moment.
type retryable struct{ error }

func (r retryable) Unwrap() error { return r.error }

// Complete sends the prompt with the router's token and returns the model's
// text. A 402 from the gateway is core.ErrBudget. A network failure or a 5xx
// is retried once; nothing else is.
func (c *Client) Complete(ctx context.Context, token string, p app.Prompt) (string, error) {
	body, err := json.Marshal(request{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  []message{{Role: "system", Content: p.System}, {Role: "user", Content: p.User}},
	})
	if err != nil {
		return "", fmt.Errorf("encode model request: %w", err)
	}
	text, err := c.once(ctx, token, body)
	var again retryable
	if errors.As(err, &again) && ctx.Err() == nil {
		text, err = c.once(ctx, token, body)
	}
	return text, err
}

func (c *Client) once(ctx context.Context, token string, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(weightHeader, quick)
	res, err := c.http.Do(req)
	if err != nil {
		return "", retryable{fmt.Errorf("call gateway: %w", err)}
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return "", retryable{fmt.Errorf("read gateway answer: %w", err)}
	}
	switch {
	case res.StatusCode == http.StatusPaymentRequired:
		return "", fmt.Errorf("gateway refused, budget used up: %w", core.ErrBudget)
	case res.StatusCode >= http.StatusInternalServerError:
		return "", retryable{fmt.Errorf("gateway answered %d", res.StatusCode)}
	case res.StatusCode != http.StatusOK:
		return "", fmt.Errorf("gateway answered %d", res.StatusCode)
	}
	return firstChoice(raw)
}

func firstChoice(raw []byte) (string, error) {
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode gateway answer: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("gateway answer has no text")
	}
	return out.Choices[0].Message.Content, nil
}
