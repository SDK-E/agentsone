// SPDX-License-Identifier: MIT

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SDK-E/agentsone/internal/mcp/adapters/cli"
	"github.com/SDK-E/agentsone/internal/mcp/app"
)

const cliSpec = `{
  "servers": [{"url": "http://127.0.0.1:7700/v1"}],
  "paths": {
    "/agents": {
      "get": {
        "operationId": "listAgents", "tags": ["agents"], "summary": "List agents",
        "parameters": [
          {"name": "limit", "in": "query", "schema": {"type": "integer"}},
          {"name": "cursor", "in": "query", "schema": {"type": "string"}},
          {"name": "tag", "in": "query", "schema": {"type": "array", "items": {"type": "string"}}}
        ],
        "responses": {"200": {"description": "ok"}}
      },
      "post": {
        "operationId": "hireAgent", "tags": ["agents"], "summary": "Hire an agent",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {
          "type": "object", "required": ["name", "kind"],
          "properties": {
            "name": {"type": "string"},
            "kind": {"type": "string", "enum": ["chat", "worker"]},
            "max_turns": {"type": "integer"},
            "dry_run": {"type": "boolean"},
            "limits": {"type": "object", "properties": {"cpu": {"type": "integer"}}}
          }}}}},
        "responses": {"201": {"description": "created"}}
      }
    },
    "/agents/{agent_name}": {
      "get": {
        "operationId": "getAgent", "tags": ["agents"], "summary": "Get an agent",
        "parameters": [{"name": "agent_name", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "ok"}}
      },
      "delete": {
        "operationId": "retireAgent", "tags": ["agents"], "summary": "Retire an agent",
        "parameters": [{"name": "agent_name", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"204": {"description": "gone"}}
      }
    },
    "/events/stream": {
      "get": {
        "operationId": "streamEvents", "tags": ["events"], "summary": "Follow events", "x-stream": {"protocol": "sse"},
        "responses": {"200": {"description": "s", "content": {"text/event-stream": {"schema": {"type": "object"}}}}}
      }
    },
    "/terminal": {
      "get": {"operationId": "openTerminal", "tags": ["agents"], "x-websocket": true, "responses": {"101": {"description": "up"}}}
    }
  }
}`

// seen is one request the fake daemon got.
type seen struct {
	Method, Path, Query, Auth, Body string
}

type fakeDaemon struct {
	mu   sync.Mutex
	reqs []seen
}

func (f *fakeDaemon) last() seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqs[len(f.reqs)-1]
}

func startDaemon(t *testing.T) (*fakeDaemon, string) {
	t.Helper()
	f := &fakeDaemon{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), string(b)})
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/v1/agents/ghost":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"urn:agentsone:problem:not-found","title":"Not found","status":404,"detail":"no agent ghost"}`))
		case r.URL.Path == "/v1/agents/sick":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"urn:agentsone:problem:internal","title":"Internal error","status":500}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v1/events/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"n\":1}\n\ndata: {\"n\":2}\n\n"))
		case r.URL.Path == "/v1/agents" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"items":[{"name":"bo","kind":"chat"}],"next_cursor":null}`))
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"bo","kind":"chat"}`))
		}
	}))
	t.Cleanup(ts.Close)
	return f, ts.URL
}

func newCatalog(t *testing.T) *app.Catalog {
	t.Helper()
	cat, err := app.NewCatalog([]byte(cliSpec))
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	return cat
}

// run executes agentctl with args and returns the exit code and both outputs.
func run(t *testing.T, url string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	full := append([]string{"--url", url}, args...)
	code = cli.Run(context.Background(), newCatalog(t), "test", full, cli.IO{In: strings.NewReader(""), Out: &out, Err: &errOut})
	return code, out.String(), errOut.String()
}

func TestRun_HireSendsTheFlagsAsTheBody(t *testing.T) {
	t.Parallel()
	d, url := startDaemon(t)

	code, out, errOut := run(t, url, "agents", "hire", "--name", "bo", "--kind", "chat", "--max-turns", "5", "--dry-run", "--limits", `{"cpu":2}`)

	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	got := d.last()
	if got.Method != "POST" || got.Path != "/v1/agents" {
		t.Fatalf("request = %+v", got)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.Body), &body); err != nil {
		t.Fatal(err)
	}
	limits, _ := body["limits"].(map[string]any)
	if body["name"] != "bo" || body["kind"] != "chat" || body["max_turns"] != float64(5) || body["dry_run"] != true || limits["cpu"] != float64(2) {
		t.Fatalf("body = %v", body)
	}
	if !strings.Contains(out, "bo") {
		t.Errorf("stdout = %q", out)
	}
}

func TestRun_PositionalArgumentIsThePathParameter(t *testing.T) {
	t.Parallel()
	d, url := startDaemon(t)

	code, _, errOut := run(t, url, "agents", "get", "a b")

	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if got := d.last(); got.Method != "GET" || got.Path != "/v1/agents/a b" {
		t.Fatalf("request = %+v", got)
	}
}

func TestRun_QueryFlagsAndRepeatedList(t *testing.T) {
	t.Parallel()
	d, url := startDaemon(t)

	code, out, errOut := run(t, url, "agents", "list", "--limit", "10", "--cursor", "c1", "--tag", "x", "--tag", "y")

	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if got := d.last().Query; got != "cursor=c1&limit=10&tag=x&tag=y" {
		t.Errorf("query = %q", got)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "bo") {
		t.Errorf("stdout = %q, want a table", out)
	}
}

func TestRun_JSONFlagPrintsTheRawBody(t *testing.T) {
	t.Parallel()
	_, url := startDaemon(t)

	code, out, _ := run(t, url, "--json", "agents", "list")

	if code != 0 || strings.TrimSpace(out) != `{"items":[{"name":"bo","kind":"chat"}],"next_cursor":null}` {
		t.Fatalf("exit %d stdout %q", code, out)
	}
}

func TestRun_BodyJSONSuppliesTheWholeBodyAndFlagsWin(t *testing.T) {
	t.Parallel()
	d, url := startDaemon(t)

	code, _, errOut := run(t, url, "agents", "hire", "--body-json", `{"name":"from-json","kind":"worker"}`, "--name", "from-flag")

	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(d.last().Body), &body)
	if body["name"] != "from-flag" || body["kind"] != "worker" {
		t.Fatalf("body = %v, want the flag to win and the JSON to fill the rest", body)
	}
}

func TestRun_BodyJSONFromStdin(t *testing.T) {
	t.Parallel()
	d, url := startDaemon(t)
	var out, errOut bytes.Buffer

	code := cli.Run(context.Background(), newCatalog(t), "test",
		[]string{"--url", url, "agents", "hire", "--body-json", "-"},
		cli.IO{In: strings.NewReader(`{"name":"piped","kind":"chat"}`), Out: &out, Err: &errOut})

	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if !strings.Contains(d.last().Body, `"piped"`) {
		t.Fatalf("body = %q", d.last().Body)
	}
}

func TestRun_TokenFlagSendsABearerToken(t *testing.T) {
	t.Parallel()
	d, url := startDaemon(t)

	code, _, _ := run(t, url, "--token", "s3cret", "agents", "get", "bo")

	if code != 0 || d.last().Auth != "Bearer s3cret" {
		t.Fatalf("exit %d auth %q", code, d.last().Auth)
	}
}

func TestRun_EmptySuccessPrintsOK(t *testing.T) {
	t.Parallel()
	_, url := startDaemon(t)

	code, out, _ := run(t, url, "agents", "retire", "bo")

	if code != 0 || out != "ok\n" {
		t.Fatalf("exit %d stdout %q", code, out)
	}
}

func TestRun_ProblemShowsPlainlyWithExitCode3(t *testing.T) {
	t.Parallel()
	_, url := startDaemon(t)

	code, out, errOut := run(t, url, "agents", "get", "ghost")

	if code != 3 {
		t.Fatalf("exit %d, want 3 for a 4xx problem", code)
	}
	if out != "" || !strings.Contains(errOut, "no agent ghost") || !strings.Contains(errOut, "urn:agentsone:problem:not-found") {
		t.Fatalf("stdout %q stderr %q", out, errOut)
	}
}

func TestRun_ServerProblemHasExitCode4(t *testing.T) {
	t.Parallel()
	_, url := startDaemon(t)

	code, _, errOut := run(t, url, "agents", "get", "sick")

	if code != 4 || !strings.Contains(errOut, "Internal error") {
		t.Fatalf("exit %d stderr %q", code, errOut)
	}
}

func TestRun_JSONModeKeepsTheProblemBodyOnStderr(t *testing.T) {
	t.Parallel()
	_, url := startDaemon(t)

	code, out, errOut := run(t, url, "--json", "agents", "get", "ghost")

	if code != 3 || out != "" || !strings.Contains(errOut, `"status":404`) {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

func TestRun_UsageMistakesExitWith2AndTeach(t *testing.T) {
	t.Parallel()
	_, url := startDaemon(t)
	tests := []struct {
		name     string
		args     []string
		contains string
	}{
		{"missing required flag", []string{"agents", "hire", "--name", "bo"}, "--kind"},
		{"bad enum", []string{"agents", "hire", "--name", "bo", "--kind", "robot"}, "chat"},
		{"missing positional", []string{"agents", "get"}, "agent_name"},
		{"unknown command", []string{"agents", "fire"}, "fire"},
		{"unknown flag", []string{"agents", "list", "--nope"}, "nope"},
		{"bad json", []string{"agents", "hire", "--body-json", "{oops"}, "JSON"},
		{"bad limits json", []string{"agents", "hire", "--name", "bo", "--kind", "chat", "--limits", "{"}, "limits"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, out, errOut := run(t, url, tt.args...)
			if code != 2 {
				t.Fatalf("exit %d, want 2; stdout %q stderr %q", code, out, errOut)
			}
			if !strings.Contains(errOut, tt.contains) {
				t.Errorf("stderr %q misses %q", errOut, tt.contains)
			}
		})
	}
}

func TestRun_UnreachableDaemonExitsWith1AndSaysWhere(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer

	code := cli.Run(context.Background(), newCatalog(t), "test",
		[]string{"--url", "/nonexistent/api.sock", "agents", "get", "bo"}, cli.IO{In: strings.NewReader(""), Out: &out, Err: &errOut})

	if code != 1 || !strings.Contains(errOut.String(), "/nonexistent/api.sock") {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
}

func TestRun_StreamCommandFollowsEventsOneLineEach(t *testing.T) {
	t.Parallel()
	_, url := startDaemon(t)

	code, out, errOut := run(t, url, "events", "stream")

	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if out != "{\"n\":1}\n{\"n\":2}\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestRun_VersionAndHelp(t *testing.T) {
	t.Parallel()
	_, url := startDaemon(t)

	code, out, _ := run(t, url, "version")
	if code != 0 || strings.TrimSpace(out) != "test" {
		t.Errorf("version: exit %d stdout %q", code, out)
	}
	code, out, _ = run(t, url, "agents", "--help")
	if code != 0 || !strings.Contains(out, "hire") || !strings.Contains(out, "Hire an agent") {
		t.Errorf("help: exit %d stdout %q", code, out)
	}
	code, out, _ = run(t, url, "agents", "hire", "--help")
	if code != 0 || !strings.Contains(out, "--name") || !strings.Contains(out, "required") || !strings.Contains(out, "worker") {
		t.Errorf("hire help: exit %d stdout %q", code, out)
	}
}

func TestRun_HasNoCommandForWebSocketOperations(t *testing.T) {
	t.Parallel()
	_, url := startDaemon(t)

	code, _, _ := run(t, url, "agents", "open-terminal")

	if code != 2 {
		t.Fatalf("exit %d, want 2 (no such command)", code)
	}
}
