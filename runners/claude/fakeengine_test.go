// SPDX-License-Identifier: MIT

package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The fake engine is this test binary started again by the runner. The
// runner gives the engine only its allow-listed environment, so the fake is
// recognised by its first argument and CLAUDE_CONFIG_DIR, and it reads its
// behaviour from a mode file in that folder:
//
//   - root: refuse to start, the way Claude Code refuses bypass as root
//   - crash: exit on the first user message
//   - ignore-interrupt: never answer an interrupt
//   - linger: keep running when stdin closes
//   - child: start a child process in the same process group
//
// It records its arguments and environment to record-<pid>.json, speaks
// stream-json, and calls the model through $ANTHROPIC_BASE_URL with
// $ANTHROPIC_AUTH_TOKEN, as Claude Code does.

const (
	fakeModeFile  = "fake-mode"
	fakeChildArg  = "fake-engine-child"
	fakeChildFile = "child-pid"
)

// isFakeEngine says whether this process was started as the fake engine.
func isFakeEngine() bool {
	return len(os.Args) > 1 && os.Args[1] == "-p" && os.Getenv("CLAUDE_CONFIG_DIR") != ""
}

// isFakeChild says whether this process is the fake engine's child.
func isFakeChild() bool { return len(os.Args) > 1 && os.Args[1] == fakeChildArg }

// engineRecord is what the fake engine saw at start.
type engineRecord struct {
	Pid  int      `json:"pid"`
	Args []string `json:"args"`
	Env  []string `json:"env"`
}

type fakeEngine struct {
	dir    string
	args   []string
	mode   string
	sid    string
	model  string
	system string
	mcpCfg string

	outMu sync.Mutex
	out   *json.Encoder

	mu      sync.Mutex
	history []map[string]string
	cancel  context.CancelFunc
	done    chan struct{}
}

func runFakeEngine() int {
	f := &fakeEngine{dir: os.Getenv("CLAUDE_CONFIG_DIR"), args: os.Args[1:], out: json.NewEncoder(os.Stdout)}
	mode, _ := os.ReadFile(filepath.Join(f.dir, fakeModeFile)) //nolint:gosec // G703: the folder the runner gave its engine
	f.mode = string(mode)
	rec, _ := json.Marshal(engineRecord{Pid: os.Getpid(), Args: f.args, Env: os.Environ()})
	// Numbered in start order: the runner starts one engine at a time.
	earlier, _ := filepath.Glob(filepath.Join(f.dir, "record-*.json"))
	_ = os.WriteFile(filepath.Join(f.dir, fmt.Sprintf("record-%03d.json", len(earlier))), rec, 0o600) //nolint:gosec // G703: as above
	// A careless engine prints its credentials; the runner must redact them.
	fmt.Fprintf(os.Stderr, "fake engine up with auth %s\n", os.Getenv("ANTHROPIC_AUTH_TOKEN"))
	if f.has("root") {
		fmt.Fprintln(os.Stderr, "--dangerously-skip-permissions cannot be used with root/sudo privileges for security reasons")
		return 1
	}
	if f.has("child") {
		child := exec.Command(os.Args[0], fakeChildArg) //nolint:gosec // G204, G702: the test binary itself
		if err := child.Start(); err == nil {
			_ = os.WriteFile(filepath.Join(f.dir, fakeChildFile), []byte(strconv.Itoa(child.Process.Pid)), 0o600) //nolint:gosec // G703: the engine's folder
		}
	}
	f.sid = f.flag("--session-id")
	if f.sid == "" {
		f.sid = f.flag("--resume")
	}
	f.model = f.flag("--model")
	sys, _ := os.ReadFile(f.flag("--append-system-prompt-file"))
	f.system = string(sys)
	f.mcpCfg = f.flag("--mcp-config")
	f.emit(map[string]any{"type": "system", "subtype": "init", "session_id": f.sid, "model": f.model,
		"mcp_servers": []any{map[string]any{"name": "machine", "status": "connected"}}, "capabilities": []string{"interrupt_receipt_v1"}})
	f.emit(map[string]any{"type": "system", "subtype": "api_retry", "attempt": 1, "error_status": 529, "error": "overloaded"})
	f.emit(map[string]any{"type": "keep_alive"})
	_, _ = os.Stdout.WriteString("not json\n")
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		f.handle(sc.Bytes())
	}
	if f.has("linger") {
		<-time.After(time.Hour)
	}
	f.stopTurn()
	return 0
}

func (f *fakeEngine) has(mode string) bool { return strings.Contains(f.mode, mode) }

func (f *fakeEngine) flag(name string) string {
	i := slices.Index(f.args, name)
	if i < 0 || i+1 >= len(f.args) {
		return ""
	}
	return f.args[i+1]
}

func (f *fakeEngine) emit(v any) {
	f.outMu.Lock()
	defer f.outMu.Unlock()
	_ = f.out.Encode(v)
}

func (f *fakeEngine) handle(line []byte) {
	var in struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &in) != nil {
		return
	}
	switch in.Type {
	case "user":
		if f.has("crash") {
			fmt.Fprintln(os.Stderr, "fake engine crashed")
			os.Exit(3)
		}
		f.stopTurn()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		f.mu.Lock()
		f.cancel, f.done = cancel, done
		f.mu.Unlock()
		go func() {
			defer close(done)
			f.turn(ctx, in.Message.Content)
		}()
	case "control_request":
		if in.Request.Subtype != "interrupt" || f.has("ignore-interrupt") {
			return
		}
		f.stopTurn()
		f.emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": in.RequestID}})
	}
}

// stopTurn aborts the running turn and waits for its result line.
func (f *fakeEngine) stopTurn() {
	f.mu.Lock()
	cancel, done := f.cancel, f.done
	f.cancel, f.done = nil, nil
	f.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (f *fakeEngine) turn(ctx context.Context, text string) {
	if strings.Contains(text, "TOOL") {
		f.emit(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
			"content": []any{map[string]any{"type": "tool_use", "id": "toolu_1", "name": "mcp__machine__add", "input": map[string]int{"a": 2, "b": 3}}}}})
		out, err := f.callTool(ctx)
		if err != nil {
			out = "tool error: " + err.Error()
		}
		f.emit(map[string]any{"type": "user", "message": map[string]any{"role": "user",
			"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": out}}}})
		text += "\ntool result: " + out
	}
	f.mu.Lock()
	f.history = append(f.history, map[string]string{"role": "user", "content": text})
	msgs := slices.Clone(f.history)
	f.mu.Unlock()
	status, full, err := f.callModel(ctx, msgs)
	switch {
	case ctx.Err() != nil:
		f.emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "session_id": f.sid})
	case err != nil:
		f.emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": err.Error(), "session_id": f.sid})
	case status != http.StatusOK:
		category := "api_error"
		if status == http.StatusPaymentRequired {
			category = "billing_error"
		}
		f.emit(map[string]any{"type": "assistant", "error": category, "message": map[string]any{"role": "assistant", "content": []any{}}})
		f.emit(map[string]any{"type": "result", "subtype": "success", "is_error": true, "api_error_status": status,
			"result": fmt.Sprintf("API Error: %d", status), "session_id": f.sid})
	default:
		f.mu.Lock()
		f.history = append(f.history, map[string]string{"role": "assistant", "content": full})
		f.mu.Unlock()
		f.emit(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
			"content": []any{map[string]any{"type": "text", "text": full}}}})
		f.emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": full, "session_id": f.sid,
			"usage":      map[string]int{"input_tokens": 11, "output_tokens": 7, "cache_read_input_tokens": 3},
			"modelUsage": map[string]any{"autoagents/one-actual": map[string]int{"inputTokens": 11}}})
	}
}

// client honours HTTP(S)_PROXY and ALL_PROXY for every host, loopback too,
// the way a Node client does, so a proxy variable that reached the engine
// would send its calls to the trap.
func client() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
		for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
			if v := os.Getenv(k); v != "" {
				return url.Parse(v)
			}
		}
		return nil, nil
	}}}
}

func (f *fakeEngine) callModel(ctx context.Context, msgs []map[string]string) (status int, text string, err error) {
	body, _ := json.Marshal(map[string]any{"model": f.model, "stream": true, "max_tokens": 1024, "system": f.system, "messages": msgs})
	// The engine calls whatever base URL the runner set: the token proxy.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, os.Getenv("ANTHROPIC_BASE_URL")+"/v1/messages?beta=true", strings.NewReader(string(body))) //nolint:gosec // G704: see above
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("ANTHROPIC_AUTH_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("X-Api-Key", "engine-own-key")
	resp, err := client().Do(req) //nolint:gosec // G704: the token proxy, as above
	if err != nil {
		return 0, "", err
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, "", nil
	}
	var full strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" {
			full.WriteString(ev.Delta.Text)
		}
		f.emit(map[string]any{"type": "stream_event", "event": json.RawMessage(data), "session_id": f.sid})
	}
	if err := sc.Err(); err != nil {
		return 0, "", err
	}
	return http.StatusOK, full.String(), nil
}

// callTool calls add(2, 3) on the machine server of the --mcp-config file.
func (f *fakeEngine) callTool(ctx context.Context) (result string, err error) {
	raw, err := os.ReadFile(f.mcpCfg)
	if err != nil {
		return "", err
	}
	var cfg struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	srv, ok := cfg.MCPServers["machine"]
	if !ok {
		return "", errors.New("no machine server")
	}
	hc := client()
	hc.Transport = headers{h: srv.Headers, next: hc.Transport}
	c := mcp.NewClient(&mcp.Implementation{Name: "fake-engine", Version: "0"}, nil)
	session, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: hc, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "add", Arguments: map[string]int{"a": 2, "b": 3}})
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(res.StructuredContent)
	return string(out), nil
}

type headers struct {
	h    map[string]string
	next http.RoundTripper
}

func (h headers) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.h {
		r.Header.Set(k, v)
	}
	return h.next.RoundTrip(r)
}
