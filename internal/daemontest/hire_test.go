// SPDX-License-Identifier: MIT

package daemontest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SDK-E/agentsone/internal/contract/api"
	"github.com/SDK-E/agentsone/internal/core"
	"github.com/SDK-E/agentsone/internal/daemontest"
)

// Integration tests: an agent hires the way its runner does, with the tool
// token the daemon issues it, over MCP on the API's TCP listener. A token
// caller is never root, whoever holds it, so these hires go through the
// same rules a runner meets in production.

// mcpScope is the scope of the tool token the supervisor gives a runner.
const mcpScope = "mcp:write"

func TestHire_TheFounderHiresThroughItsTokenOverTheToolServer(t *testing.T) {
	t.Parallel()
	d := daemontest.Start(t)
	alice := d.SignInHuman("alice")
	founder := toolSession(t, d, "founder")

	res := callTool(t, founder, "hire_agent", hireBody("scribe", nil, false))

	if res.IsError {
		t.Fatalf("hire_agent = error %q, want the hired agent", text(res))
	}
	if a := alice.Agent("scribe"); a.Manager == nil || *a.Manager != "founder" || a.IsRoot {
		t.Fatalf("the society has scribe as %+v, want a custom agent below the founder", a)
	}
	ev := hiredEvents(alice, "scribe")
	if len(ev) != 1 || ev[0].Actor != "agent:founder" || ev[0].Source != "/agents/founder" {
		t.Fatalf("hired events = %+v, want one from the founder", ev)
	}
	if !audited(alice, "agent.hire", "agent:founder", "agent:scribe") {
		t.Fatal("the hire is not in the audit log as the founder's")
	}
	if !provisioned(alice, "agent:scribe") {
		t.Fatal("scribe was not provisioned")
	}
}

// An agent without the right to a hire is refused through its token as it
// is on the socket: nothing is stored, audited or announced.
func TestHire_AnAgentWithoutTheRightIsRefusedThroughItsToken(t *testing.T) {
	t.Parallel()
	founder := "founder"
	tests := []struct {
		name string
		body map[string]any
	}{
		{"a custom agent hires a root agent", hireBody("guard", nil, true)},
		{"a custom agent hires above itself", hireBody("guard", &founder, false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := daemontest.Start(t)
			alice := d.SignInHuman("alice")
			d.AsAgent("founder").HireEcho("echo")
			echo := toolSession(t, d, "echo")

			res := callTool(t, echo, "hire_agent", tt.body)

			if !res.IsError || !strings.Contains(text(res), "status 403") {
				t.Fatalf("hire_agent = %v %q, want a 403 refusal", res.IsError, text(res))
			}
			if _, err := d.Ports.Agents.GetAgent(asDaemon(), "guard"); !errors.Is(err, core.ErrNotFound) {
				t.Fatalf("GetAgent(guard) error = %v, want not found", err)
			}
			if ev := hiredEvents(alice, "guard"); len(ev) != 0 {
				t.Fatalf("hired events = %+v, want none", ev)
			}
			if audited(alice, "agent.hire", "agent:echo", "agent:guard") {
				t.Fatal("the refused hire is in the audit log")
			}
			// The supervisor audits every provisioning: a Linux user, its home
			// and its unit. None was made for the refused agent.
			if provisioned(alice, "agent:guard") {
				t.Fatal("the refused agent was provisioned")
			}
		})
	}
}

// hireBody is the hire_agent arguments: the body fields, as a runner sends them.
func hireBody(name string, manager *string, root bool) map[string]any {
	in := api.AgentCreate{
		Name: name, DisplayName: name, Kind: api.AgentKindChat, Role: "Writes things down",
		Runner: "echo", Manager: manager, IsRoot: &root,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		panic(err) // a fixed literal always encodes
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		panic(err)
	}
	return args
}

func asDaemon() context.Context {
	return core.WithCaller(context.Background(), core.Caller{Kind: core.CallerSystem, Name: "daemontest", UID: -1, IsRoot: true})
}

// toolSession opens an MCP session as agent the way its runner does: with
// the tool token the supervisor issues at hello, over the TCP listener.
func toolSession(t *testing.T, d *daemontest.Daemon, agent string) *mcp.ClientSession {
	t.Helper()
	a, err := d.Ports.Agents.GetAgent(asDaemon(), agent)
	if err != nil {
		t.Fatalf("agent %s: %v", agent, err)
	}
	subject := core.Caller{Kind: core.CallerAgent, Name: a.Name, UID: a.UID, IsRoot: a.IsRoot, Scopes: []string{mcpScope}}
	tok, err := d.Ports.Tokens.Issue(asDaemon(), subject, []string{mcpScope}, time.Hour)
	if err != nil {
		t.Fatalf("issue %s's tool token: %v", agent, err)
	}
	httpc := &http.Client{Transport: bearer{token: tok.Value, next: http.DefaultTransport}}
	client := mcp.NewClient(&mcp.Implementation{Name: "daemontest-runner", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: "http://" + d.TCPAddr() + "/v1/mcp", HTTPClient: httpc, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect to the tool server as %s: %v", agent, err)
	}
	// Runs before the daemon stops: cleanups run last in, first out.
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close the tool session of %s: %v", agent, err)
		}
	})
	return session
}

// bearer sends the token on every request, as a runner does.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(req)
}

func callTool(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), daemontest.Timeout)
	defer cancel()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

type storedEvent struct {
	Actor  string `json:"actor"`
	Source string `json:"source"`
	Data   struct {
		Agent string `json:"agent"`
	} `json:"data"`
}

// hiredEvents are the stored agentsone.agents.hired events about agent.
func hiredEvents(h *daemontest.Client, agent string) []storedEvent {
	var page struct {
		Items []storedEvent `json:"items"`
	}
	h.Do(http.MethodGet, "/events?type=agentsone.agents.hired&limit=100", nil, &page)
	var out []storedEvent
	for _, e := range page.Items {
		if e.Data.Agent == agent {
			out = append(out, e)
		}
	}
	return out
}

// provisioned reports whether the supervisor provisioned target, by anyone.
func provisioned(h *daemontest.Client, target string) bool {
	for _, r := range h.Audit() {
		if r.Action == "agent.runner.provision" && string(r.Target.Kind)+":"+r.Target.Id == target {
			return true
		}
	}
	return false
}

// audited reports whether the audit trail holds action by actor on target,
// each as "<kind>:<name>".
func audited(h *daemontest.Client, action, actor, target string) bool {
	for _, r := range h.Audit() {
		by := string(r.Actor.Kind) + ":" + r.Actor.Name
		on := string(r.Target.Kind) + ":" + r.Target.Id
		if r.Action == action && by == actor && on == target {
			return true
		}
	}
	return false
}
